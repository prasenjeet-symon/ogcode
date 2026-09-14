package master

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// operatorSessionsViewPrefix is the apex-host path prefix of the live session
// monitor pages. Unlike the rest of the console these are prefix routes: the
// session id is the next path segment (and "/events" appends the SSE stream).
// The start-session form's success banner already advertises this URL shape.
const operatorSessionsViewPrefix = "/sessions/"

// sessionIDPattern constrains what the monitor accepts as a session id: ogcode
// mints ids like "ses_<random>", and refusing anything outside that shape keeps
// stray path segments out of templates, EventSource URLs and log lines.
var sessionIDPattern = regexp.MustCompile(`^ses_[A-Za-z0-9_-]+$`)

// sessionEventHeartbeat keeps intermediaries from timing out an idle stream.
const sessionEventHeartbeat = 15 * time.Second

// sessionViewVM is the session monitor's view model. NotFound renders the
// "no such session" page: the master only routes sessions it started (or a
// worker told it about) in memory, so an unknown id is the common case after a
// master restart or for a session started outside the console.
type sessionViewVM struct {
	SessionID    string
	WorkerID     string
	WorkerName   string
	WorkerStatus string
	Routes       []apexRoute // per-worktree tunnel links (label + absolute URL)
	OpenURL      string      // legacy bare worker subdomain, when no routes are up
	FeedPath     string      // the SSE endpoint the page's EventSource connects to
	NotFound     bool
}

// sessionViewTmpl renders the live session monitor body (inside the shared
// chrome): who hosts it, the links into its worktree UI, and an SSE-fed event
// feed. Message payloads relayed here carry ids, not full transcripts — the
// chat itself stays in the workspace UI, so the feed is a monitor, not a
// transcript.
var sessionViewTmpl = template.Must(template.New("sessions_view").Parse(`
  {{if .NotFound}}
    <div class="page-title">Session</div>
    <p class="page-sub">Live monitor.</p>
    <div class="card">
      <h2>No such session</h2>
      <p>The console has no routing for <code>{{.SessionID}}</code>. It only tracks
      sessions it routed to a worker, in memory — a master restart, a finished
      session, or one started outside this console all look like this.</p>
      <p class="muted">If the session is still running, its live UI is on the
      worker&rsquo;s own address; the <a href="/__operator/sessions">start-session
      form</a> and the <a href="/">console</a> list what is reachable.</p>
    </div>
  {{else}}
    <div class="page-title">Session <code>{{.SessionID}}</code></div>
    <p class="page-sub">Live monitor — events stream from the moment this page opened.</p>
    <div class="card">
      <div class="kv"><span>worker</span>
        <span>{{if .WorkerName}}{{.WorkerName}}{{else}}{{.WorkerID}}{{end}}
          <span class="muted">{{.WorkerID}} · {{.WorkerStatus}}</span></span></div>
      <div class="kv"><span>status</span>
        <span class="pill wait" id="status">waiting for events</span></div>
      <div class="kv"><span>stream</span>
        <span><span class="dot" id="conn"></span><span id="connstate">connecting…</span><span class="muted" id="gaps"></span></span></div>
      {{if or .Routes .OpenURL}}
      <div class="kv"><span>live UI</span>
        <span>{{range .Routes}}<a href="{{.URL}}" target="_blank" rel="noopener">workspace {{.Label}} →</a>{{end}}{{if .OpenURL}}<a href="{{.OpenURL}}" target="_blank" rel="noopener">open worker UI →</a>{{end}}</span></div>
      {{end}}
    </div>
    <div class="card">
      <h2>Live events</h2>
      <p class="hint">Streams events from the moment this page opened — earlier
      history lives in the workspace UI. The feed shows relayed event types and
      payloads, not the chat transcript.</p>
      <ul class="feed" id="feed">
        <li class="placeholder" id="empty">Waiting for the first event…</li>
      </ul>
    </div>
    <script>
      var es = new EventSource({{.FeedPath}});
      var feed = document.getElementById('feed');
      var conn = document.getElementById('conn');
      var connstate = document.getElementById('connstate');
      var gapsEl = document.getElementById('gaps');
      var lastSeq = 0, gaps = 0;
      es.onopen = function () {
        connstate.textContent = 'live';
        conn.className = 'dot live';
      };
      es.onerror = function () {
        connstate.textContent = 'reconnecting…';
        conn.className = 'dot';
      };
      es.onmessage = function (e) {
        var ev;
        try { ev = JSON.parse(e.data); } catch (err) { return; }
        if (typeof ev.seq === 'number' && ev.seq > 0) {
          if (lastSeq && ev.seq > lastSeq + 1) {
            gaps += ev.seq - lastSeq - 1;
            gapsEl.textContent = ' (missed ' + gaps + ' while busy)';
          }
          lastSeq = ev.seq;
        }
        var empty = document.getElementById('empty');
        if (empty) empty.parentNode.removeChild(empty);
        var li = document.createElement('li');
        var head = document.createElement('div');
        head.className = 'evt-head';
        var t = document.createElement('span');
        t.className = 'evt-type';
        t.textContent = ev.type || 'event';
        var s = document.createElement('span');
        s.className = 'evt-seq';
        s.textContent = ev.seq ? '#' + ev.seq : '';
        head.appendChild(t);
        head.appendChild(s);
        li.appendChild(head);
        var pre = document.createElement('pre');
        pre.textContent = ev.properties ? JSON.stringify(ev.properties, null, 2) : '';
        li.appendChild(pre);
        feed.insertBefore(li, feed.firstChild);
        while (feed.children.length > 200) feed.removeChild(feed.lastChild);
        applyStatus(ev);
      };
      function applyStatus(ev) {
        var p = ev.properties || {};
        switch (ev.type) {
          case 'session.created':
            setStatus('running', 'run'); break;
          case 'permission.requested':
            setStatus('waiting for permission' + (p.tool ? ': ' + p.tool : ''), 'wait'); break;
          case 'permission.replied':
            setStatus('running', 'run'); break;
          case 'loop.compacted':
            setStatus('compacting context', 'wait'); break;
          case 'loop.done':
            setStatus('finished — ' + (p.reason || 'stop'),
              (p.reason === 'stop' || p.reason === 'length') ? 'done' : 'err'); break;
          case 'session.failed':
            setStatus('failed' + (p.finishReason ? ' — ' + p.finishReason : ''), 'err'); break;
        }
      }
      function setStatus(text, kind) {
        var el = document.getElementById('status');
        el.textContent = text;
        el.className = 'pill ' + kind;
      }
    </script>
  {{end}}`))

// handleSessionView renders the live monitor for one session, or the
// not-found page when the master holds no routing for it. The session id is
// everything between the /sessions/ prefix and the (optional) /events suffix;
// anything else malformed is treated as unknown rather than echoed back.
//
// Operator-gated by handleApexConsole before dispatch.
func (s *Server) handleSessionView(w http.ResponseWriter, r *http.Request, tail string) {
	sessionID, events := parseSessionViewPath(tail)
	// Scoped accounts may open only their own sessions' monitors; a foreign
	// session renders as not-found rather than forbidden so its id and
	// existence are not revealed.
	if !s.sessionVisible(r, sessionID) {
		if events {
			http.Error(w, "no such session", http.StatusNotFound)
		} else {
			vm := sessionViewVM{SessionID: sessionID, NotFound: true}
			s.renderSessionView(w, r, vm, http.StatusNotFound)
		}
		return
	}
	if events {
		s.handleSessionEvents(w, r, sessionID)
		return
	}

	vm := sessionViewVM{SessionID: sessionID}
	workerID, ok := s.reg.WorkerForSession(sessionID)
	if !ok {
		vm.NotFound = true
		s.renderSessionView(w, r, vm, http.StatusNotFound)
		return
	}
	vm.WorkerID = workerID
	if info, ok := s.reg.Get(workerID); ok {
		vm.WorkerName = info.Name
		vm.WorkerStatus = string(info.Status)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Links to the session's worktree UI: one per tunnel the worker currently
	// serves (the session's own workspace is among them), falling back to the
	// legacy bare worker subdomain when no per-worktree tunnel is open.
	for _, route := range s.tunnels.routes(workerID) {
		vm.Routes = append(vm.Routes, apexRoute{
			Label: route,
			URL:   fmt.Sprintf("%s://%s.%s/", scheme, tunnelKey(workerID, route), r.Host),
		})
	}
	if len(vm.Routes) == 0 {
		vm.OpenURL = fmt.Sprintf("%s://%s.%s/", scheme, workerID, r.Host)
	}
	vm.FeedPath = operatorSessionsViewPrefix + sessionID + "/events"
	s.renderSessionView(w, r, vm, http.StatusOK)
}

// handleSessionEvents streams the master bus to one browser as SSE, filtered
// to a single session id. Relayed payloads carry the session id inside their
// JSON properties (the bus event type never does), so each frame is probed
// before forwarding. The subscription is taken BEFORE the headers are written:
// the ": hello" comment the client reads is the proof it is wired up.
func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	if !sessionIDPattern.MatchString(sessionID) {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	if _, ok := s.reg.WorkerForSession(sessionID); !ok {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := s.bus.SubscribeAll()
	defer s.bus.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": hello\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(sessionEventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case evt, open := <-ch:
			if !open {
				return
			}
			props := normaliseSessionPayload(evt.Properties)
			if sessionIDOf(props) != sessionID {
				continue
			}
			if !writeSessionSSEEvent(w, evt.Type, evt.Seq, props) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSessionSSEEvent emits one SSE data frame carrying the event type, the
// master seq (for the browser's gap detection) and the payload. It returns
// false only when the client went away.
func writeSessionSSEEvent(w http.ResponseWriter, eventType string, seq int64, props json.RawMessage) bool {
	frame, err := json.Marshal(struct {
		Type       string          `json:"type"`
		Seq        int64           `json:"seq"`
		Properties json.RawMessage `json:"properties"`
	}{Type: eventType, Seq: seq, Properties: props})
	// Marshaling a string/int/RawMessage struct cannot fail; on the impossible
	// path keep the stream open rather than tear it down over one frame.
	if err != nil {
		return true
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", frame)
	return err == nil
}

// sessionIDOf extracts the sessionId a relayed event payload carries ("" when
// it names no session). This mirrors the worker relay's own parseSessionID.
func sessionIDOf(props json.RawMessage) string {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return ""
	}
	return p.SessionID
}

// normaliseSessionPayload re-keys a session.created payload to the
// "sessionId" field every other relayed event carries: the bus forwards the
// worker's session object verbatim and the session model's id field is "id".
// Everything else passes through untouched.
func normaliseSessionPayload(props json.RawMessage) json.RawMessage {
	var probe struct {
		SessionID string `json:"sessionId"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(props, &probe); err != nil ||
		probe.SessionID != "" || probe.ID == "" {
		return props
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(props, &obj); err != nil {
		return props
	}
	if _, ok := obj["id"]; !ok {
		return props
	}
	obj["sessionId"] = obj["id"]
	out, err := json.Marshal(obj)
	if err != nil {
		return props
	}
	return out
}

// parseSessionViewPath splits the path tail after the /sessions/ prefix into
// the session id and whether this is the SSE endpoint. It returns an id of ""
// for malformed tails; callers render the not-found page for those.
func parseSessionViewPath(tail string) (sessionID string, events bool) {
	if strings.HasSuffix(tail, "/events") {
		events = true
		tail = strings.TrimSuffix(tail, "/events")
	}
	if tail == "" || strings.ContainsAny(tail, "/\\") {
		return "", events
	}
	return tail, events
}

// renderSessionView writes the session monitor (or its not-found variant).
func (s *Server) renderSessionView(w http.ResponseWriter, r *http.Request, vm sessionViewVM, status int) {
	var body strings.Builder
	_ = sessionViewTmpl.Execute(&body, vm)
	title := "Session " + vm.SessionID + " — ogcode control plane"
	s.writeChromeStatus(w, r, title, navSessions, body.String(), status)
}
