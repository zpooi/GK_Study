package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"gongkao/study/internal/store"
)

const (
	defaultExamDate = "2026-11-29"
	examDateKey     = "exam_date"
	maxBodyBytes    = 64 << 10
)

var (
	examLoc = mustLoadLocation("Asia/Shanghai")
	idRe    = regexp.MustCompile(`data-id="([^"]+)"`)
)

// Server is the HTTP handler for the study app.
type Server struct {
	tmpl     *template.Template
	store    *store.Store
	log      *slog.Logger
	knownIDs map[string]bool
	total    int
	mux      *http.ServeMux
}

type pageData struct {
	Done      map[string]bool
	DoneCount int
	Total     int
	Percent   int
	ExamDate  string
	DaysLeft  string
	Practice  map[string]store.SectionStats
}

type sectionMeta struct {
	ID   string
	Name string
	Desc string
}

var practiceSections = []sectionMeta{
	{"politics", "一、政治理论", "马原、习近平新时代中国特色社会主义思想、2025 政府工作报告、二十届三中全会。"},
	{"common", "二、常识判断", "法律、经济、科技、人文历史、地理与云南省情。"},
	{"verbal", "三、言语理解", "逻辑填空、片段阅读、语句表达。"},
	{"math", "四、数量关系", "工程、行程、经济利润、排列组合、概率、几何。"},
	{"logic", "五、判断推理", "类比推理、定义判断、逻辑判断；图形推理以讲义为准。"},
	{"data", "六、资料分析", "增长率、比重、平均数、倍数与速算技巧。"},
	{"essay", "七、申论（行政执法）", "归纳概括、提出对策、公文、大作文提纲。"},
	{"tax", "八、税务与执法专项", "税种、增值税、所得税、金税四期、执法三项制度。"},
}

var practiceByID = func() map[string]sectionMeta {
	m := make(map[string]sectionMeta, len(practiceSections))
	for _, s := range practiceSections {
		m[s.ID] = s
	}
	return m
}()

type sectionCard struct {
	Meta     sectionMeta
	Stats    store.SectionStats
	Progress int
	Accuracy int
}

type questionView struct {
	store.Question
	Attempt  *store.Attempt
	Selected string
}

type sectionView struct {
	Meta      sectionMeta
	Questions []questionView
	Stats     store.SectionStats
	Progress  int
	Accuracy  int
}

type practicePage struct {
	IsIndex  bool
	Sections []sectionCard
	Section  *sectionView
	Overall  store.SectionStats
	Accuracy int
	DaysLeft string
}

type state struct {
	done      map[string]bool
	doneCount int
	total     int
	percent   int
}

// New loads templates and the question bank from fsys, seeds SQLite and
// returns a ready-to-serve handler.
func New(fsys fs.FS, templatePath string, st *store.Store, log *slog.Logger) (*Server, error) {
	raw, err := fs.ReadFile(fsys, templatePath)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}

	known := make(map[string]bool)
	for _, m := range idRe.FindAllSubmatch(raw, -1) {
		known[string(m[1])] = true
	}
	if len(known) == 0 {
		return nil, errors.New("template contains no data-id knowledge points")
	}

	tmpl, err := template.New("root").ParseFS(fsys, "web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	qraw, err := fs.ReadFile(fsys, "web/data/questions.json")
	if err != nil {
		return nil, fmt.Errorf("read question bank: %w", err)
	}
	var questions []store.Question
	if err := json.Unmarshal(qraw, &questions); err != nil {
		return nil, fmt.Errorf("decode question bank: %w", err)
	}
	if err := validateQuestions(questions); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := st.SeedQuestions(ctx, questions); err != nil {
		return nil, err
	}

	s := &Server{
		tmpl:     tmpl,
		store:    st,
		log:      log,
		knownIDs: known,
		total:    len(known),
	}
	s.mux = s.routes()
	return s, nil
}

func validateQuestions(qs []store.Question) error {
	seen := make(map[string]bool, len(qs))
	for _, q := range qs {
		if q.ID == "" {
			return errors.New("question with empty id")
		}
		if seen[q.ID] {
			return fmt.Errorf("duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		if _, ok := practiceByID[q.Section]; !ok {
			return fmt.Errorf("question %q has unknown section %q", q.ID, q.Section)
		}
		if q.Stem == "" || q.Answer == "" || q.Explanation == "" {
			return fmt.Errorf("question %q is missing stem/answer/explanation", q.ID)
		}
		switch q.Kind {
		case "single":
			if len(q.Options) < 2 {
				return fmt.Errorf("question %q needs at least two options", q.ID)
			}
			keys := make(map[string]bool, len(q.Options))
			ok := false
			for _, o := range q.Options {
				if o.Key == "" || keys[o.Key] {
					return fmt.Errorf("question %q has invalid option key %q", q.ID, o.Key)
				}
				keys[o.Key] = true
				if o.Key == q.Answer {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("question %q answer %q is not an option", q.ID, q.Answer)
			}
		case "essay":
			if q.Answer == "" {
				return fmt.Errorf("essay question %q needs a reference answer", q.ID)
			}
		default:
			return fmt.Errorf("question %q has unknown kind %q", q.ID, q.Kind)
		}
	}
	return nil
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /api/progress", s.handleProgress)
	mux.HandleFunc("POST /api/exam-date", s.handleExamDate)
	mux.HandleFunc("GET /practice", s.handlePracticeIndex)
	mux.HandleFunc("GET /practice/{section}", s.handlePracticeSection)
	mux.HandleFunc("POST /practice/{section}/reset", s.handlePracticeReset)
	mux.HandleFunc("POST /api/practice/answer", s.handlePracticeAnswer)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	return mux
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.recoverMW(s.logMW(s.headersMW(s.mux))).ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	st, err := s.loadState(ctx)
	if err != nil {
		s.serverError(w, "load progress", err)
		return
	}

	examDate, err := s.store.Setting(ctx, examDateKey, defaultExamDate)
	if err != nil {
		s.serverError(w, "load exam date", err)
		return
	}
	if !validDate(examDate) {
		examDate = defaultExamDate
	}

	practice, err := s.store.SectionStats(ctx)
	if err != nil {
		s.serverError(w, "load practice stats", err)
		return
	}

	data := pageData{
		Done:      st.done,
		DoneCount: st.doneCount,
		Total:     st.total,
		Percent:   st.percent,
		ExamDate:  examDate,
		DaysLeft:  daysLeft(examDate, time.Now()),
		Practice:  practice,
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		s.serverError(w, "render page", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = buf.WriteTo(w)
}

type progressReq struct {
	ID   string `json:"id"`
	Done *bool  `json:"done"`
}

type progressResp struct {
	OK        bool `json:"ok"`
	Done      bool `json:"done"`
	DoneCount int  `json:"done_count"`
	Total     int  `json:"total"`
	Percent   int  `json:"percent"`
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross-origin request rejected"})
		return
	}

	var req progressReq
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Done == nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing done field"})
		return
	}
	if !s.knownIDs[req.ID] {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown knowledge point id"})
		return
	}

	if err := s.store.SetDone(r.Context(), req.ID, *req.Done); err != nil {
		s.serverError(w, "save progress", err)
		return
	}
	st, err := s.loadState(r.Context())
	if err != nil {
		s.serverError(w, "load progress", err)
		return
	}

	s.writeJSON(w, http.StatusOK, progressResp{
		OK:        true,
		Done:      *req.Done,
		DoneCount: st.doneCount,
		Total:     st.total,
		Percent:   st.percent,
	})
}

type examDateReq struct {
	Date string `json:"date"`
}

type examDateResp struct {
	OK       bool   `json:"ok"`
	ExamDate string `json:"exam_date"`
	DaysLeft string `json:"days_left"`
}

func (s *Server) handleExamDate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross-origin request rejected"})
		return
	}

	var req examDateReq
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !validDate(req.Date) {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "date must be YYYY-MM-DD"})
		return
	}

	if err := s.store.SetSetting(r.Context(), examDateKey, req.Date); err != nil {
		s.serverError(w, "save exam date", err)
		return
	}
	s.writeJSON(w, http.StatusOK, examDateResp{
		OK:       true,
		ExamDate: req.Date,
		DaysLeft: daysLeft(req.Date, time.Now()),
	})
}

func (s *Server) handlePracticeIndex(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.SectionStats(r.Context())
	if err != nil {
		s.serverError(w, "load practice stats", err)
		return
	}

	cards := make([]sectionCard, 0, len(practiceSections))
	var overall store.SectionStats
	for _, meta := range practiceSections {
		st := stats[meta.ID]
		overall.Total += st.Total
		overall.Attempted += st.Attempted
		overall.Correct += st.Correct
		cards = append(cards, sectionCard{
			Meta:     meta,
			Stats:    st,
			Progress: pct(st.Attempted, st.Total),
			Accuracy: pct(st.Correct, st.Attempted),
		})
	}

	s.renderPractice(w, practicePage{
		IsIndex:  true,
		Sections: cards,
		Overall:  overall,
		Accuracy: pct(overall.Correct, overall.Attempted),
		DaysLeft: s.currentDaysLeft(r.Context()),
	})
}

func (s *Server) handlePracticeSection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("section")
	meta, ok := practiceByID[id]
	if !ok {
		http.NotFound(w, r)
		return
	}

	qs, err := s.store.QuestionsBySection(r.Context(), id)
	if err != nil {
		s.serverError(w, "load questions", err)
		return
	}
	attempts, err := s.store.AttemptsBySection(r.Context(), id)
	if err != nil {
		s.serverError(w, "load attempts", err)
		return
	}
	stats, err := s.store.SectionStats(r.Context())
	if err != nil {
		s.serverError(w, "load practice stats", err)
		return
	}

	views := make([]questionView, 0, len(qs))
	for _, q := range qs {
		v := questionView{Question: q}
		if a, ok := attempts[q.ID]; ok {
			av := a
			v.Attempt = &av
			v.Selected = a.Chosen
		}
		views = append(views, v)
	}
	st := stats[id]

	s.renderPractice(w, practicePage{
		Section: &sectionView{
			Meta:      meta,
			Questions: views,
			Stats:     st,
			Progress:  pct(st.Attempted, st.Total),
			Accuracy:  pct(st.Correct, st.Attempted),
		},
		DaysLeft: s.currentDaysLeft(r.Context()),
	})
}

func (s *Server) handlePracticeReset(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	id := r.PathValue("section")
	if _, ok := practiceByID[id]; !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.store.ResetSection(r.Context(), id); err != nil {
		s.serverError(w, "reset section", err)
		return
	}
	http.Redirect(w, r, "/practice/"+id, http.StatusSeeOther)
}

type answerReq struct {
	QuestionID string `json:"question_id"`
	Choice     string `json:"choice"`
}

type practiceStats struct {
	Total     int `json:"total"`
	Attempted int `json:"attempted"`
	Correct   int `json:"correct"`
	Accuracy  int `json:"accuracy"`
}

type answerResp struct {
	OK          bool          `json:"ok"`
	Correct     bool          `json:"correct"`
	Answer      string        `json:"answer"`
	Explanation string        `json:"explanation"`
	Stats       practiceStats `json:"stats"`
}

func (s *Server) handlePracticeAnswer(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "cross-origin request rejected"})
		return
	}

	var req answerReq
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	q, err := s.store.Question(r.Context(), req.QuestionID)
	if errors.Is(err, sql.ErrNoRows) {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown question"})
		return
	}
	if err != nil {
		s.serverError(w, "load question", err)
		return
	}

	choice := strings.TrimSpace(req.Choice)
	correct := false
	switch q.Kind {
	case "single":
		choice = strings.ToUpper(choice)
		valid := false
		for _, o := range q.Options {
			if o.Key == choice {
				valid = true
				break
			}
		}
		if !valid {
			s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "choice is not an option"})
			return
		}
		correct = choice == q.Answer
	case "essay":
		choice = strings.ToLower(choice)
		if choice != "mastered" && choice != "review" {
			s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "choice must be mastered or review"})
			return
		}
		correct = choice == "mastered"
	default:
		s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "bad question kind"})
		return
	}

	if err := s.store.SaveAttempt(r.Context(), q.ID, choice, correct); err != nil {
		s.serverError(w, "save attempt", err)
		return
	}
	stats, err := s.store.SectionStats(r.Context())
	if err != nil {
		s.serverError(w, "load practice stats", err)
		return
	}
	st := stats[q.Section]

	s.writeJSON(w, http.StatusOK, answerResp{
		OK:          true,
		Correct:     correct,
		Answer:      q.Answer,
		Explanation: q.Explanation,
		Stats: practiceStats{
			Total:     st.Total,
			Attempted: st.Attempted,
			Correct:   st.Correct,
			Accuracy:  pct(st.Correct, st.Attempted),
		},
	})
}

func (s *Server) renderPractice(w http.ResponseWriter, data practicePage) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "practice.html", data); err != nil {
		s.serverError(w, "render practice page", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = buf.WriteTo(w)
}

func (s *Server) currentDaysLeft(ctx context.Context) string {
	d, err := s.store.Setting(ctx, examDateKey, defaultExamDate)
	if err != nil || !validDate(d) {
		d = defaultExamDate
	}
	return daysLeft(d, time.Now())
}

func pct(n, d int) int {
	if d <= 0 {
		return 0
	}
	return int(math.Round(float64(n) * 100 / float64(d)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "database unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">` +
	`<rect width="64" height="64" rx="14" fill="#b23a2e"/>` +
	`<text x="32" y="45" font-size="34" text-anchor="middle" fill="#fffdf8" ` +
	`font-family="serif" font-weight="bold">公</text></svg>`

func (s *Server) handleFavicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = io.WriteString(w, faviconSVG)
}

func (s *Server) loadState(ctx context.Context) (state, error) {
	all, err := s.store.Done(ctx)
	if err != nil {
		return state{}, err
	}
	done := make(map[string]bool, len(all))
	n := 0
	for id := range all {
		if s.knownIDs[id] {
			done[id] = true
			n++
		}
	}
	pct := 0
	if s.total > 0 {
		pct = int(math.Round(float64(n) * 100 / float64(s.total)))
	}
	return state{done: done, doneCount: n, total: s.total, percent: pct}, nil
}

func (s *Server) serverError(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal server error"})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) headersMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (s *Server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"dur", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

func (s *Server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return errors.New("content-type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid JSON body")
	}
	if dec.More() {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client (curl, health tooling)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func validDate(s string) bool {
	_, err := time.ParseInLocation("2006-01-02", s, examLoc)
	return err == nil
}

func daysLeft(examDate string, now time.Time) string {
	t, err := time.ParseInLocation("2006-01-02", examDate, examLoc)
	if err != nil {
		return "--"
	}
	now = now.In(examLoc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, examLoc)
	d := int(t.Sub(today).Hours() / 24)
	if d < 0 {
		return "已考试"
	}
	return strconv.Itoa(d)
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}
