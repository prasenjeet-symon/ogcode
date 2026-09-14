package master

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
)

// Operator auth endpoints are namespaced under /__operator/ so they cannot
// collide with any route in the proxied ogcode UI.
const (
	operatorLoginPath  = "/__operator/login"
	operatorLogoutPath = "/__operator/logout"
)

// handleOperatorLogin renders the login form (GET) and verifies the credentials
// (POST), issuing a session cookie on success. The form carries username+password
// for per-employee login; in the legacy single-password mode the username field
// is shown but optional.
func (s *Server) handleOperatorLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		username := r.PostFormValue("username")
		password := r.PostFormValue("password")
		if userID, workspaces, ok := s.gate.AuthenticateScoped(username, password); ok {
			// Scoped cookie: pins the account's workspace allowlist so an
			// admin-side allowlist change signs live sessions out (the A1
			// mechanism, applied to the allowlist).
			s.gate.SetCookieScoped(w, userID, workspaces)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		if s.gate.Mode() == auth.ModeLegacy {
			renderLogin(w, "Incorrect password.")
		} else {
			renderLogin(w, "Incorrect username or password.")
		}
		return
	}
	renderLogin(w, "")
}

// handleOperatorLogout clears the session and returns to the login page.
func (s *Server) handleOperatorLogout(w http.ResponseWriter, r *http.Request) {
	s.gate.ClearCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// wantsHTML reports whether a request is a browser navigation (so an
// unauthenticated hit should show the login page rather than a bare 401).
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>ogcode control plane — sign in</title>
<style>
  :root{ color-scheme:light dark;
    --bg:#f6f7f9; --card:#ffffff; --border:#e7e9ee; --border2:#d7dae1;
    --text:#1b1e26; --muted:#5f6672; --faint:#9297a2;
    --accent:#5b63d6; --accent-hover:#4a51c4; --accent-soft:rgba(91,99,214,.10);
    --err:#c22c2c; --err-bg:rgba(194,44,44,.09); --err-border:rgba(194,44,44,.28);
    --shadow:0 14px 44px rgba(20,24,33,.12); }
  @media (prefers-color-scheme:dark){ :root{
    --bg:#0d0f14; --card:#171a24; --border:#262b39; --border2:#333a4c;
    --text:#e7e9f0; --muted:#9aa0b0; --faint:#697083;
    --accent:#6b74e8; --accent-hover:#7d86ef; --accent-soft:rgba(107,116,232,.16);
    --err:#f2a5a5; --err-bg:rgba(220,80,80,.12); --err-border:rgba(220,80,80,.30);
    --shadow:0 18px 50px rgba(0,0,0,.55); } }
  *{box-sizing:border-box}
  body{ margin:0; min-height:100vh; display:grid; place-items:center; padding:24px;
    font:14px/1.55 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
    background:var(--bg); color:var(--text); -webkit-font-smoothing:antialiased; }
  .card{ width:360px; max-width:100%; padding:32px 30px;
    background:var(--card); border:1px solid var(--border); border-radius:14px; box-shadow:var(--shadow); }
  .logo{ display:flex; align-items:center; gap:11px; margin-bottom:6px; color:var(--accent); }
  .logo svg{ width:30px; height:30px; }
  .logo b{ font-size:19px; font-weight:680; letter-spacing:-.02em; color:var(--text); }
  p.sub{ margin:14px 0 22px; color:var(--muted); font-size:13px; }
  label{ display:block; font-size:12px; font-weight:550; color:var(--muted); margin:14px 0 6px; }
  input{ width:100%; padding:10px 12px; font-size:14px; font-family:inherit;
    background:var(--bg); border:1px solid var(--border2); border-radius:9px; color:var(--text);
    transition:border-color .12s,box-shadow .12s; }
  input:focus{ outline:none; border-color:var(--accent); box-shadow:0 0 0 3px var(--accent-soft); }
  button{ width:100%; margin-top:22px; padding:11px 12px; font-size:14px; font-weight:600; font-family:inherit;
    background:var(--accent); color:#fff; border:0; border-radius:9px; cursor:pointer; transition:background .12s; }
  button:hover{ background:var(--accent-hover); }
  .err{ margin:0 0 16px; padding:10px 13px; font-size:13px; border-radius:9px; display:flex; gap:8px; align-items:flex-start;
    background:var(--err-bg); border:1px solid var(--err-border); color:var(--err); }
  .err::before{ content:"\26A0"; font-weight:700; }
</style></head><body>
  <form class="card" method="post" action="/__operator/login">
    <div class="logo">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="4" width="19" height="16" rx="3"/><path d="M7 9.5l3.5 3L7 15.5"/><path d="M13 15.5h4.5"/></svg>
      <b>ogcode</b>
    </div>
    <p class="sub">Operator sign-in required to open the control plane.</p>
    {{if .Error}}<p class="err">{{.Error}}</p>{{end}}
    <label for="username">Username</label>
    <input id="username" name="username" type="text" autofocus autocomplete="username">
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password">
    <button type="submit">Sign in</button>
  </form>
</body></html>`))

func renderLogin(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache the login page (it reflects auth state).
	w.Header().Set("Cache-Control", "no-store")
	_ = loginTmpl.Execute(w, struct{ Error string }{Error: errMsg})
}
