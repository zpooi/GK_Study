package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Option is one choice of a single-answer question.
type Option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Question is a practice question. Kind is "single" or "essay".
type Question struct {
	ID          string   `json:"id"`
	Section     string   `json:"section"`
	Topic       string   `json:"topic"`
	Kind        string   `json:"kind"`
	Stem        string   `json:"stem"`
	Options     []Option `json:"options"`
	Answer      string   `json:"answer"`
	Explanation string   `json:"explanation"`
	Source      string   `json:"source"`
	Sort        int      `json:"sort"`
}

// Attempt is the latest answer for a question.
type Attempt struct {
	QuestionID string
	Chosen     string
	Correct    bool
	AnsweredAt string
}

// SectionStats aggregates practice progress for one section.
type SectionStats struct {
	Total     int
	Attempted int
	Correct   int
}

// Store wraps the SQLite database. The app is small and single-user, so a
// single connection is enough and it removes all SQLITE_BUSY surprises.
type Store struct {
	db *sql.DB
}

// Open creates the database file (and its directory) if needed, applies the
// schema and returns a ready store.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS progress (
			kp_id      TEXT PRIMARY KEY,
			done       INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1)),
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_progress_done ON progress(done)`,
		`CREATE TABLE IF NOT EXISTS questions (
			id          TEXT PRIMARY KEY,
			section     TEXT NOT NULL,
			topic       TEXT NOT NULL DEFAULT '',
			kind        TEXT NOT NULL CHECK (kind IN ('single', 'essay')),
			stem        TEXT NOT NULL,
			options     TEXT NOT NULL DEFAULT '[]',
			answer      TEXT NOT NULL,
			explanation TEXT NOT NULL,
			source      TEXT NOT NULL DEFAULT '',
			sort        INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_questions_section ON questions(section, sort)`,
		`CREATE TABLE IF NOT EXISTS attempts (
			question_id TEXT PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,
			chosen      TEXT NOT NULL,
			correct     INTEGER NOT NULL CHECK (correct IN (0, 1)),
			answered_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_correct ON attempts(correct)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Done returns all kp_id values currently marked as mastered.
func (s *Store) Done(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kp_id FROM progress WHERE done = 1`)
	if err != nil {
		return nil, fmt.Errorf("query progress: %w", err)
	}
	defer rows.Close()

	done := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		done[id] = true
	}
	return done, rows.Err()
}

// SetDone upserts a single knowledge point's status.
func (s *Store) SetDone(ctx context.Context, id string, done bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO progress (kp_id, done, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(kp_id) DO UPDATE SET done = excluded.done, updated_at = excluded.updated_at`,
		id, boolInt(done), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("set progress %q: %w", id, err)
	}
	return nil
}

// Setting reads a settings value, returning def when it does not exist.
func (s *Store) Setting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting writes a settings value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// Ping verifies the database is reachable (used by /healthz).
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// SeedQuestions inserts or updates the question bank. Attempts are preserved
// because question IDs are stable.
func (s *Store) SeedQuestions(ctx context.Context, qs []Question) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed questions: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO questions (id, section, topic, kind, stem, options, answer, explanation, source, sort)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			section = excluded.section, topic = excluded.topic, kind = excluded.kind,
			stem = excluded.stem, options = excluded.options, answer = excluded.answer,
			explanation = excluded.explanation, source = excluded.source, sort = excluded.sort`)
	if err != nil {
		return fmt.Errorf("prepare seed questions: %w", err)
	}
	defer stmt.Close()

	for _, q := range qs {
		opts, err := json.Marshal(q.Options)
		if err != nil {
			return fmt.Errorf("marshal options for %q: %w", q.ID, err)
		}
		if _, err := stmt.ExecContext(ctx, q.ID, q.Section, q.Topic, q.Kind, q.Stem,
			string(opts), q.Answer, q.Explanation, q.Source, q.Sort); err != nil {
			return fmt.Errorf("upsert question %q: %w", q.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed questions: %w", err)
	}
	return nil
}

// QuestionsBySection returns the ordered question bank for one section.
func (s *Store) QuestionsBySection(ctx context.Context, section string) ([]Question, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, section, topic, kind, stem, options, answer, explanation, source, sort
		FROM questions WHERE section = ? ORDER BY sort, id`, section)
	if err != nil {
		return nil, fmt.Errorf("query questions: %w", err)
	}
	defer rows.Close()

	var out []Question
	for rows.Next() {
		var q Question
		var opts string
		if err := rows.Scan(&q.ID, &q.Section, &q.Topic, &q.Kind, &q.Stem, &opts,
			&q.Answer, &q.Explanation, &q.Source, &q.Sort); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}
		if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
			return nil, fmt.Errorf("decode options for %q: %w", q.ID, err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Question returns one question by ID.
func (s *Store) Question(ctx context.Context, id string) (Question, error) {
	var q Question
	var opts string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, section, topic, kind, stem, options, answer, explanation, source, sort
		FROM questions WHERE id = ?`, id).
		Scan(&q.ID, &q.Section, &q.Topic, &q.Kind, &q.Stem, &opts,
			&q.Answer, &q.Explanation, &q.Source, &q.Sort)
	if err != nil {
		return Question{}, err
	}
	if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
		return Question{}, fmt.Errorf("decode options for %q: %w", id, err)
	}
	return q, nil
}

// AttemptsBySection returns the latest attempt for each answered question.
func (s *Store) AttemptsBySection(ctx context.Context, section string) (map[string]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.question_id, a.chosen, a.correct, a.answered_at
		FROM attempts a JOIN questions q ON q.id = a.question_id
		WHERE q.section = ?`, section)
	if err != nil {
		return nil, fmt.Errorf("query attempts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]Attempt)
	for rows.Next() {
		var a Attempt
		var correct int
		if err := rows.Scan(&a.QuestionID, &a.Chosen, &correct, &a.AnsweredAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.Correct = correct == 1
		out[a.QuestionID] = a
	}
	return out, rows.Err()
}

// SaveAttempt upserts the latest answer for a question.
func (s *Store) SaveAttempt(ctx context.Context, questionID, chosen string, correct bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO attempts (question_id, chosen, correct, answered_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(question_id) DO UPDATE SET
			chosen = excluded.chosen, correct = excluded.correct, answered_at = excluded.answered_at`,
		questionID, chosen, boolInt(correct), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save attempt %q: %w", questionID, err)
	}
	return nil
}

// ResetSection deletes all attempts in one section.
func (s *Store) ResetSection(ctx context.Context, section string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM attempts WHERE question_id IN (SELECT id FROM questions WHERE section = ?)`, section)
	if err != nil {
		return fmt.Errorf("reset section %q: %w", section, err)
	}
	return nil
}

// SectionStats returns question/attempt/correct totals grouped by section.
func (s *Store) SectionStats(ctx context.Context) (map[string]SectionStats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.section, COUNT(*), COUNT(a.question_id),
		       COALESCE(SUM(CASE WHEN a.correct = 1 THEN 1 ELSE 0 END), 0)
		FROM questions q LEFT JOIN attempts a ON a.question_id = q.id
		GROUP BY q.section`)
	if err != nil {
		return nil, fmt.Errorf("query section stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]SectionStats)
	for rows.Next() {
		var section string
		var st SectionStats
		if err := rows.Scan(&section, &st.Total, &st.Attempted, &st.Correct); err != nil {
			return nil, fmt.Errorf("scan section stats: %w", err)
		}
		out[section] = st
	}
	return out, rows.Err()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
