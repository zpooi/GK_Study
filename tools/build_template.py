# -*- coding: utf-8 -*-
"""Convert the static study page into a Go html/template.

Only the dynamic bits are touched:
  - each knowledge-point checkbox gets a server-rendered checked attribute
  - progress counters / bar / countdown / exam-date get template values
  - localStorage JS is replaced with same-origin fetch calls to the Go API
"""
import os
import re
import sys

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = r"E:\gongkaovideo\study\index.html"
DST = os.path.join(BASE, "web", "templates", "index.html")

html = open(SRC, encoding="utf-8").read()
assert "{{" not in html, "source already contains template actions"

# 0) mobile viewport (safe-area support for notched phones)
html = html.replace(
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    '<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">',
    1,
)

# wording: progress now lives in SQLite
html = html.replace(
    "勾选知识点自动保存；刷新不丢。",
    "勾选后自动保存到服务器；刷新、换设备都保留。",
    1,
)
html = html.replace(
    "勾选左侧知识点会保存在本机",
    "勾选左侧知识点会自动保存到服务器",
    1,
)

# favicon served by the Go app
html, n = re.subn(r'(<title>[^<]*</title>)', r'\1\n<link rel="icon" type="image/svg+xml" href="/favicon.svg">', html)
assert n == 1, n

# shorter placeholder so it doesn't get cut off in the narrow mobile search box
html = html.replace(
    'placeholder="搜索考点 / 公式 / 关键词  ( / )"',
    'placeholder="搜索考点 / 公式" title="按 / 快速聚焦搜索框"',
    1,
)

# 1) checkboxes -> checked when the server says done
checkbox_re = re.compile(r'(<input type="checkbox" data-id="([^"]+)" id="[^"]+")>')


def checkbox(m):
    return '%s{{if index $.Done "%s"}} checked{{end}}>' % (m.group(1), m.group(2))


html, n_cb = checkbox_re.subn(checkbox, html)
assert n_cb == 160, "expected 160 checkboxes, got %d" % n_cb

# 2) countdown + editable exam date
html, n = re.subn(r'<b id="days">--</b>', '<b id="days">{{.DaysLeft}}</b>', html)
assert n == 1, n

html, n = re.subn(
    r'<input type="date" id="examDate" title="可修改考试日期">',
    '<input type="date" id="examDate" title="可修改考试日期" value="{{.ExamDate}}">',
    html,
)
assert n == 1, n

# 3) progress counter, bar and sync indicator
html, n = re.subn(
    r'<span id="done">0</span> / <span id="total">0</span>',
    '<span id="done">{{.DoneCount}}</span> / <span id="total">{{.Total}}</span>',
    html,
)
assert n == 1, n

html, n = re.subn(
    r'<div class="bar"><i id="bar"></i></div>',
    '<div class="bar"><i id="bar" style="width:{{.Percent}}%"></i></div>\n'
    '      <div class="savestate" id="saveState" aria-live="polite"></div>',
    html,
)
assert n == 1, n

# 4) CSS for the save indicator + practice entry points
css_anchor = ".bar>i{display:block;height:100%;width:0;background:var(--red);transition:width .25s}"
assert css_anchor in html
html = html.replace(
    css_anchor,
    css_anchor + "\n"
    ".savestate{font-size:12px;color:var(--sub);min-height:16px;margin-top:6px}\n"
    ".savestate.saving{color:var(--gold)}\n"
    ".savestate.ok{color:var(--green)}\n"
    ".savestate.err{color:var(--red)}\n"
    ".pbtn{font-size:13px;font-weight:700;color:var(--red);border:1px solid #e3c4be;background:#fff;"
    "border-radius:999px;padding:6px 14px;white-space:nowrap}\n"
    ".pbtn:hover{background:var(--red-soft)}\n"
    ".practice-cta{margin:22px 0 0;padding:16px 18px;border:1px solid #ead6cf;border-radius:12px;"
    "background:linear-gradient(180deg,#fff9f6,#fdf5ef);display:flex;align-items:center;"
    "justify-content:space-between;gap:14px;flex-wrap:wrap}\n"
    ".pc-kicker{font-size:12px;font-weight:800;color:var(--red);letter-spacing:.5px}\n"
    ".pc-title{font-size:16px;font-weight:800;margin:2px 0}\n"
    ".pc-desc{font-size:12.5px;color:var(--sub)}\n"
    ".pc-btn{display:inline-block;background:var(--red);color:#fff;border-radius:999px;"
    "padding:9px 22px;font-weight:700;font-size:14px;white-space:nowrap}\n"
    ".pc-btn:hover{filter:brightness(1.08)}",
    1,
)

# header practice link
html, n = re.subn(r'<div class="tools">', '<div class="tools"><a class="pbtn" href="/practice">✎ 练习</a>', html)
assert n == 1, n

# mobile hamburger button (CSS keeps it hidden on desktop)
old_count = ('      <input type="date" id="examDate" title="可修改考试日期" value="{{.ExamDate}}"></div>\n'
             '    <div class="tools">')
new_count = ('      <input type="date" id="examDate" title="可修改考试日期" value="{{.ExamDate}}"></div>\n'
             '    <button class="menu-btn" id="menuBtn" type="button" aria-label="打开章节菜单" '
             'aria-expanded="false" aria-controls="mnav">'
             '<span></span><span></span><span></span></button>\n'
             '    <div class="tools">')
assert old_count in html
html = html.replace(old_count, new_count, 1)

# per-section practice CTA, rendered server-side with the question count
for slug in ("politics", "common", "verbal", "math", "logic", "data", "essay", "tax"):
    pat = re.compile(r'(<section id="%s">.*?)(\n</section>)' % slug, re.S)
    m = pat.search(html)
    assert m, slug
    cta = (
        '<div class="practice-cta">{{with index $.Practice "%s"}}'
        '<div class="pc-text">'
        '<div class="pc-kicker">✎ 本章练习</div>'
        '<div class="pc-title">学完就练 · {{.Total}} 道题</div>'
        '<div class="pc-desc">{{if .Attempted}}已做 {{.Attempted}} 题，答对 {{.Correct}} 题'
        '{{else}}提交后立即显示答案和解析，做错也能看懂为什么{{end}}</div>'
        '</div>'
        '<a class="pc-btn" href="/practice/%s">{{if .Attempted}}继续练习{{else}}开始练习{{end}} →</a>'
        '{{end}}</div>' % (slug, slug)
    )
    html = html[:m.end(1)] + "\n" + cta + html[m.end(1):]

# 5) replace localStorage logic with server-backed fetch logic
start_marker = '  var KEY="gk2026.progress.v1";'
end_marker = ('  dEl.addEventListener(\'change\',function(){localStorage.setItem("gk2026.examdate",dEl.value);tick()});\n'
              '  tick();\n')
i = html.index(start_marker)
j = html.index(end_marker, i)
new_js = r'''  var saveState=document.getElementById('saveState');
  var saveTimer=0;

  function allKp(){return Array.prototype.slice.call(document.querySelectorAll('.kp input[type=checkbox]'))}

  function setSave(text,cls){
    if(!saveState)return;
    saveState.textContent=text;
    saveState.className='savestate'+(cls?' '+cls:'');
    clearTimeout(saveTimer);
    if(cls==='ok')saveTimer=setTimeout(function(){saveState.textContent=''},1800);
  }

  function refresh(){
    var boxes=allKp(),done=0;
    boxes.forEach(function(b){
      if(b.checked){b.closest('li').classList.add('done');done++}
      else{b.closest('li').classList.remove('done')}
    });
    document.getElementById('done').textContent=done;
    document.getElementById('total').textContent=boxes.length;
    var pct=boxes.length?Math.round(done/boxes.length*100):0;
    document.getElementById('bar').style.width=pct+"%";
  }

  document.addEventListener('change',function(e){
    var b=e.target;
    if(!b.matches('.kp input[type=checkbox]'))return;
    var id=b.dataset.id,done=b.checked;
    b.closest('li').classList.toggle('done',done);
    refresh();
    setSave('保存中…','saving');
    fetch('/api/progress',{
      method:'POST',credentials:'same-origin',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({id:id,done:done})
    }).then(function(r){
      if(!r.ok)throw new Error('HTTP '+r.status);
      return r.json();
    }).then(function(d){
      document.getElementById('done').textContent=d.done_count;
      document.getElementById('total').textContent=d.total;
      document.getElementById('bar').style.width=d.percent+'%';
      setSave('已保存','ok');
    }).catch(function(err){
      b.checked=!done;
      b.closest('li').classList.toggle('done',b.checked);
      refresh();
      setSave('保存失败，请检查服务','err');
      console.error(err);
    });
  });

  // countdown, editable (date lives in SQLite via /api/exam-date)
  var dEl=document.getElementById('examDate');
  function tick(){
    if(!dEl.value){document.getElementById('days').textContent='--';return}
    var t=new Date(dEl.value+"T00:00:00").getTime();
    var now=new Date(); now.setHours(0,0,0,0);
    var d=Math.round((t-now.getTime())/86400000);
    document.getElementById('days').textContent = isNaN(d)?"--":(d>=0?d:"已考试");
  }
  dEl.addEventListener('change',function(){
    if(!dEl.value)return;
    setSave('保存中…','saving');
    fetch('/api/exam-date',{
      method:'POST',credentials:'same-origin',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({date:dEl.value})
    }).then(function(r){
      if(!r.ok)throw new Error('HTTP '+r.status);
      return r.json();
    }).then(function(d){
      dEl.value=d.exam_date;tick();setSave('已保存','ok');
    }).catch(function(err){
      setSave('保存失败，请检查服务','err');
      console.error(err);
    });
  });
  tick();
'''
html = html[:i] + new_js + html[j + len(end_marker):]

# 5b) mobile layout: compact header, full-width search, visible progress,
#     larger touch targets. Later rules override the earlier max-width:900px block.
mobile_css = """
.menu-btn{display:none}
@media(max-width:900px){
  html{-webkit-text-size-adjust:100%}
  body{overflow-wrap:break-word}
  .top-inner{padding:8px max(12px,env(safe-area-inset-left)) 8px max(12px,env(safe-area-inset-right));
    gap:6px 10px}
  .brand{font-size:15px;line-height:1.35}
  .count{font-size:12.5px;padding:3px 9px}
  .count input{font-size:16px;width:128px;height:26px;padding:0 4px}
  .tools{gap:6px}
  .tools input[type=search]{font-size:16px;padding:6px 10px;flex:1 1 110px}
  .tools input[type=search]::placeholder{font-size:13px}
  .btn,.pbtn{font-size:12px;padding:6px 10px}
  aside{display:block;position:static;max-height:none;overflow:visible}
  nav.toc{display:none}
  .progressbox{display:flex;flex-wrap:wrap;align-items:center;gap:4px 10px;
    margin:2px 0 0;padding:8px 12px}
  .progressbox .small{font-size:11.5px}
  .progressbox .num{font-size:15px}
  .progressbox .bar{flex:1 1 100%;margin-top:0}
  .progressbox .savestate{flex:1 1 100%;margin-top:0;min-height:0;font-size:11px}
  .progressbox .small[style]{display:none}
  .layout{gap:14px;padding:14px 14px 60px}
  .menu-btn{display:inline-flex;flex:none;margin-left:auto;width:40px;height:40px;padding:0;
    border:1px solid var(--line);border-radius:10px;background:#fff;cursor:pointer;
    align-items:center;justify-content:center;flex-direction:column;gap:4px}
  .menu-btn span{display:block;width:18px;height:2px;border-radius:2px;background:var(--ink);
    transition:transform .18s,opacity .18s}
  .menu-btn[aria-expanded=true]{background:var(--red-soft);border-color:#e3c4be}
  .menu-btn[aria-expanded=true] span:nth-child(1){transform:translateY(6px) rotate(45deg)}
  .menu-btn[aria-expanded=true] span:nth-child(2){opacity:0}
  .menu-btn[aria-expanded=true] span:nth-child(3){transform:translateY(-6px) rotate(-45deg)}
  .mobile-nav{display:none;position:absolute;top:100%;left:0;right:0;z-index:60;
    grid-template-columns:repeat(2,minmax(0,1fr));gap:6px;white-space:normal;
    max-height:min(70vh,520px);overflow:auto;overscroll-behavior:contain;
    padding:10px max(12px,env(safe-area-inset-left)) 14px max(12px,env(safe-area-inset-right));
    background:var(--paper);border-bottom:1px solid var(--line);
    box-shadow:0 18px 30px rgba(29,29,31,.16);-webkit-overflow-scrolling:touch}
  .mobile-nav.open{display:grid}
  .mobile-nav a{padding:12px 10px;font-size:14px;text-align:center;background:#fff;
    border:1px solid var(--line);border-radius:9px}
  .mobile-nav a.active{background:var(--red-soft);color:var(--red);border-color:#eccfc8;font-weight:600}
  body.menu-open{overflow:hidden}
  body.menu-open::before{content:"";position:fixed;inset:0;background:rgba(29,29,31,.22);z-index:40}
  .kp li{padding:6px}
  .kp input{width:18px;height:18px;margin-top:4px}
  .practice-cta{padding:14px;gap:12px}
  .practice-cta .pc-btn{flex:1 1 100%;text-align:center;padding:11px 18px}
  h1{font-size:22px}
  .card{padding:15px 14px}
  .tablewrap{margin:8px -2px}
  footer{padding:0 max(14px,env(safe-area-inset-right)) 30px max(14px,env(safe-area-inset-left))}
}
@media(max-width:520px){
  /* brand is not a link on the handbook and repeats the countdown row;
     dropping it keeps the sticky header to three compact rows */
  .brand{display:none}
  #printBtn{display:none}
  .tools input[type=search]{flex:1 1 96px}
}
"""
html = html.replace("@media print{", mobile_css + "@media print{", 1)

# 5c) when arriving from a practice page via /#section, realign the anchor
#     after --hdr has been set (hash scrolling happens before JS runs)
old = "  setHdr(); addEventListener('resize',setHdr);\n\n  refresh();"
new = (
    "  setHdr(); addEventListener('resize',setHdr);\n\n"
    "  if(location.hash){\n"
    "    var anchor=document.getElementById(decodeURIComponent(location.hash.slice(1)));\n"
    "    if(anchor)requestAnimationFrame(function(){\n"
    "      var rootStyle=document.documentElement.style,prev=rootStyle.scrollBehavior;\n"
    "      rootStyle.scrollBehavior='auto';anchor.scrollIntoView();rootStyle.scrollBehavior=prev;\n"
    "    });\n"
    "  }\n\n  refresh();"
)
assert old in html
html = html.replace(old, new, 1)

# 5d) hamburger menu behaviour (mobile only; CSS hides the button on desktop)
menu_js = """  // mobile chapter menu (hamburger)
  var menuBtn=document.getElementById('menuBtn');
  var mnav=document.getElementById('mnav');
  function setMenu(open){
    if(!menuBtn||!mnav)return;
    mnav.classList.toggle('open',open);
    document.body.classList.toggle('menu-open',open);
    menuBtn.setAttribute('aria-expanded',open?'true':'false');
    menuBtn.setAttribute('aria-label',open?'关闭章节菜单':'打开章节菜单');
  }
  if(menuBtn&&mnav){
    menuBtn.addEventListener('click',function(e){
      e.stopPropagation();
      setMenu(!mnav.classList.contains('open'));
    });
    mnav.addEventListener('click',function(e){
      var a=e.target.closest('a');
      if(!a)return;
      e.preventDefault();
      setMenu(false);
      var id=(a.getAttribute('href')||'').replace(/^#/,'');
      var target=id&&document.getElementById(id);
      if(target){
        var rs=document.documentElement.style,prev=rs.scrollBehavior;
        rs.scrollBehavior='auto';target.scrollIntoView();rs.scrollBehavior=prev;
        if(history.replaceState)history.replaceState(null,'','#'+id);
      }
    });
    document.addEventListener('click',function(e){
      if(!mnav.classList.contains('open'))return;
      if(mnav.contains(e.target)||menuBtn.contains(e.target))return;
      setMenu(false);
    });
    document.addEventListener('keydown',function(e){
      if(e.key==='Escape'&&mnav.classList.contains('open'))setMenu(false);
    });
    addEventListener('resize',function(){
      if(innerWidth>900)setMenu(false);
    });
  }

  refresh();
})();"""
assert "  refresh();\n})();" in html
html = html.replace("  refresh();\n})();", menu_js, 1)

# 6) sanity checks
assert "localStorage" not in html, "localStorage still present"
assert html.count("{{if index $.Done") == 160
assert "{{.DoneCount}}" in html and "{{.Total}}" in html and "{{.Percent}}" in html

os.makedirs(os.path.dirname(DST), exist_ok=True)
open(DST, "w", encoding="utf-8", newline="\n").write(html)
print("wrote", DST, len(html), "bytes")
print("checkboxes:", n_cb, "| save indicator:", "saveState" in html)
