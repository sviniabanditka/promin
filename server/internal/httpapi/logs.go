package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/sviniabanditka/promin/server/internal/logbuf"
)

// logsHandlers serve the /logs debug UI: an HTML page + a history JSON endpoint
// + a realtime SSE stream, all gated by a Basic-Auth password
// (PROMIN_LOGS_PASSWORD). The page sets a cookie so EventSource (which can't
// send an Authorization header reliably) authenticates via the cookie.
type logsHandlers struct {
	buf      *logbuf.Buffer
	password string
}

// authToken is the opaque cookie value proving the password was entered — a
// hash of the password, so the raw secret isn't stored in the browser.
func (h *logsHandlers) authToken() string {
	sum := sha256.Sum256([]byte("promin-logs:" + h.password))
	return hex.EncodeToString(sum[:])
}

// basicOK checks the Basic-Auth password (constant-time).
func (h *logsHandlers) basicOK(r *http.Request) bool {
	_, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(h.password)) == 1
}

// authOK passes if either Basic Auth matches or the session cookie is valid
// (the cookie path is what EventSource/history use after the page loaded).
func (h *logsHandlers) authOK(r *http.Request) bool {
	if h.basicOK(r) {
		return true
	}
	if c, err := r.Cookie("logs_auth"); err == nil {
		return subtle.ConstantTimeCompare([]byte(c.Value), []byte(h.authToken())) == 1
	}
	return false
}

func (h *logsHandlers) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="promin logs"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (h *logsHandlers) page(w http.ResponseWriter, r *http.Request) {
	if !h.basicOK(r) {
		h.challenge(w)
		return
	}
	noFraming(w)
	http.SetCookie(w, &http.Cookie{
		Name:     "logs_auth",
		Value:    h.authToken(),
		Path:     "/logs",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   7 * 24 * 3600,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(logsPageHTML))
}

func parseMinLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (h *logsHandlers) history(w http.ResponseWriter, r *http.Request) {
	if !h.authOK(r) {
		h.challenge(w)
		return
	}
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 1000)
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	recs := h.buf.History(limit, parseMinLevel(q.Get("level")), q.Get("q"))
	writeJSON(w, http.StatusOK, map[string]any{"records": recs})
}

func (h *logsHandlers) stream(w http.ResponseWriter, r *http.Request) {
	if !h.authOK(r) {
		h.challenge(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	minLevel := parseMinLevel(q.Get("level"))
	query := q.Get("q")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := h.buf.Subscribe()
	defer unsub()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case rec, ok := <-ch:
			if !ok {
				return
			}
			if !logbuf.Matches(rec, minLevel, query) {
				continue
			}
			b, err := json.Marshal(rec)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("data: ")); err != nil {
				return
			}
			w.Write(b)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

const logsPageHTML = `<!doctype html>
<html lang="uk"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Promin · logs</title>
<style>
  :root{--bg:#0e1116;--panel:#161b22;--line:#232a34;--ink:#e6edf3;--muted:#8b949e;
    --debug:#8b949e;--info:#58a6ff;--warn:#e3b341;--error:#ff7b72;--accent:#2ea043;}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--ink);font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;height:100vh;display:flex;flex-direction:column}
  header{background:var(--panel);border-bottom:1px solid var(--line);padding:8px 12px;display:flex;gap:8px;align-items:center;flex-wrap:wrap}
  header .title{font-weight:700;margin-right:8px}
  header .title span{color:var(--muted);font-weight:400}
  .lvls{display:flex;gap:4px}
  .lvls button{background:var(--bg);color:var(--muted);border:1px solid var(--line);border-radius:6px;padding:4px 10px;cursor:pointer;font:inherit}
  .lvls button.on{color:var(--ink);border-color:#3d444d}
  .lvls button[data-l=error].on{color:var(--error);border-color:var(--error)}
  .lvls button[data-l=warn].on{color:var(--warn);border-color:var(--warn)}
  input#q{flex:1;min-width:120px;background:var(--bg);color:var(--ink);border:1px solid var(--line);border-radius:6px;padding:5px 10px;font:inherit}
  header .btn{background:var(--bg);color:var(--muted);border:1px solid var(--line);border-radius:6px;padding:4px 10px;cursor:pointer;font:inherit}
  header .btn.on{color:var(--accent);border-color:var(--accent)}
  #status{color:var(--muted);font-size:12px;white-space:nowrap}
  #log{flex:1;overflow:auto;padding:4px 0}
  .row{padding:1px 12px;white-space:pre-wrap;word-break:break-word;border-left:3px solid transparent}
  .row:hover{background:#12171e}
  .row.error{border-left-color:var(--error)}
  .row.warn{border-left-color:var(--warn)}
  .row .t{color:var(--muted)}
  .row .l{font-weight:700;padding:0 6px}
  .row.debug .l{color:var(--debug)} .row.info .l{color:var(--info)}
  .row.warn .l{color:var(--warn)} .row.error .l{color:var(--error)}
  .row .m{color:var(--ink)}
  .row .a{color:var(--muted)}
  .row .a b{color:#adbac7;font-weight:400}
</style></head>
<body>
<header>
  <div class="title">Promin <span>logs</span></div>
  <div class="lvls" id="lvls">
    <button data-l="debug">debug</button>
    <button data-l="info" class="on">info</button>
    <button data-l="warn">warn</button>
    <button data-l="error">error</button>
  </div>
  <input id="q" placeholder="фільтр по тексту / атрибутах…" autocomplete="off">
  <button class="btn on" id="live">● live</button>
  <button class="btn" id="wrap">wrap</button>
  <button class="btn" id="clear">clear</button>
  <span id="status">…</span>
</header>
<div id="log"></div>
<script>
(function(){
  var level="info", live=true, es=null;
  var logEl=document.getElementById("log"), qEl=document.getElementById("q"), statusEl=document.getElementById("status");
  var LV={debug:0,info:1,warn:2,error:3};
  function esc(s){return String(s).replace(/[&<>]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;'}[c]})}
  function fmtTime(t){var d=new Date(t);return d.toLocaleTimeString('uk',{hour12:false})+"."+String(d.getMilliseconds()).padStart(3,'0')}
  function passQ(r,q){if(!q)return true;q=q.toLowerCase();if(r.msg.toLowerCase().indexOf(q)>=0)return true;
    if(r.attrs)for(var i=0;i<r.attrs.length;i++){if((r.attrs[i].k+" "+r.attrs[i].v).toLowerCase().indexOf(q)>=0)return true}return false}
  function rowHTML(r){
    var lv=(r.level||"INFO").toLowerCase().slice(0,5);
    var cls=lv.indexOf("error")==0?"error":lv.indexOf("warn")==0?"warn":lv.indexOf("debug")==0?"debug":"info";
    var a="";if(r.attrs)for(var i=0;i<r.attrs.length;i++){a+=" <span class=a><b>"+esc(r.attrs[i].k)+"</b>="+esc(r.attrs[i].v)+"</span>"}
    return '<div class="row '+cls+'" data-lv="'+cls+'"><span class=t>'+fmtTime(r.time)+'</span>'+
      '<span class=l>'+esc((r.level||"INFO").slice(0,4))+'</span><span class=m>'+esc(r.msg)+'</span>'+a+'</div>';
  }
  function atBottom(){return logEl.scrollHeight-logEl.scrollTop-logEl.clientHeight<40}
  function append(r){
    if(passQ(r,qEl.value)===false)return;
    var stick=atBottom();
    logEl.insertAdjacentHTML('beforeend',rowHTML(r));
    while(logEl.childNodes.length>4000)logEl.removeChild(logEl.firstChild);
    if(stick)logEl.scrollTop=logEl.scrollHeight;
  }
  function loadHistory(){
    statusEl.textContent="завантаження…";
    fetch("/logs/api/history?limit=2000&level="+level,{credentials:"same-origin"})
      .then(function(r){return r.json()}).then(function(d){
        logEl.innerHTML="";
        (d.records||[]).forEach(function(r){logEl.insertAdjacentHTML('beforeend',rowHTML(r))});
        applyFilter();logEl.scrollTop=logEl.scrollHeight;
        statusEl.textContent=(d.records||[]).length+" рядків";
      }).catch(function(){statusEl.textContent="помилка history"});
  }
  function connect(){
    if(es)es.close();
    if(!live)return;
    es=new EventSource("/logs/api/stream?level="+level,{withCredentials:true});
    es.onmessage=function(e){try{append(JSON.parse(e.data))}catch(x){}};
    es.onopen=function(){statusEl.textContent="● live"};
    es.onerror=function(){statusEl.textContent="⟳ reconnect…"};
  }
  function applyFilter(){
    var q=qEl.value.toLowerCase();
    var rows=logEl.childNodes;
    for(var i=0;i<rows.length;i++){var el=rows[i];if(el.nodeType!=1)continue;
      var txt=el.textContent.toLowerCase();el.style.display=(!q||txt.indexOf(q)>=0)?"":"none"}
  }
  document.getElementById("lvls").addEventListener("click",function(e){
    var b=e.target.closest("button");if(!b)return;
    level=b.getAttribute("data-l");
    [].forEach.call(this.children,function(c){c.classList.toggle("on",c===b)});
    loadHistory();connect();
  });
  qEl.addEventListener("input",applyFilter);
  document.getElementById("live").addEventListener("click",function(){
    live=!live;this.classList.toggle("on",live);this.textContent=live?"● live":"❚❚ paused";
    if(live){loadHistory();connect()}else if(es){es.close()}
  });
  document.getElementById("wrap").addEventListener("click",function(){
    this.classList.toggle("on");document.body.classList.toggle("nowrap");
    var s=document.getElementById("wrapStyle");if(!s){s=document.createElement("style");s.id="wrapStyle";
      s.textContent=".nowrap .row{white-space:pre;overflow-x:auto}";document.head.appendChild(s)}
  });
  document.getElementById("clear").addEventListener("click",function(){logEl.innerHTML="";statusEl.textContent="очищено"});
  loadHistory();connect();
})();
</script>
</body></html>`
