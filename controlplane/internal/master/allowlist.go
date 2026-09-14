package master

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Per-user workspace allowlist, enforced on every worker-subdomain request
// after the operator gate passes. An account's allowlist is a list of
// workspace identifiers: a per-worktree route label ("treemain"), a workspace
// name, or a path suffix of a workspace ("repo-b"). An empty allowlist means
// unrestricted — accounts created before the allowlist existed (and
// ModeLegacy sessions, which carry no user id at all) keep working unchanged.

// workspaceAllowed reports whether the request's session may open the tunnel
// named by hostLabel. Fail-closed on every seam: a store error, a missing
// tunnel owner, or an unknown worker denies rather than falls through. It is
// called only after gate.Authenticated passed.
func (s *Server) workspaceAllowed(r *http.Request, label string) bool {
	allowed, ok := s.gate.WorkspacesForUser(r)
	if !ok {
		return false // fail closed: store error or unusable session
	}
	if len(allowed) == 0 {
		return true // unrestricted account
	}
	workerID, _, ok := s.tunnels.owner(label)
	if !ok {
		return false // the tunnel vanished between auth and allowlist check
	}
	info, ok := s.reg.Get(workerID)
	if !ok {
		return false // worker deregistered; nothing known about its workspaces
	}
	return tunnelMatchesAllowlist(label, info, allowed)
}

// tunnelMatchesAllowlist decides whether one tunnel (label + the worker's
// registry entry) matches any identifier in the allowlist. Match rules, in
// order: the route label equals an identifier; a workspace NAME equals one; or
// a workspace PATH ends with the identifier as a path segment (so "repo-b"
// matches "/srv/repo-b" but not "/srv/other-repo-b"). For the legacy bare
// tunnel (route == "", label == worker id) the worker id itself may also be an
// identifier, since there is no route label to name the workspace.
func tunnelMatchesAllowlist(label string, info registry.WorkerInfo, allowed []string) bool {
	route := strings.TrimPrefix(label, info.ID+"-")
	for _, want := range allowed {
		if route != "" && route == want {
			return true
		}
		if route == "" && label == want {
			// Legacy bare tunnel: the label IS the worker id, so an identifier
			// may name the worker directly.
			return true
		}
		for _, ws := range info.Workspaces {
			if ws.Name == want {
				return true
			}
			if ws.Path == want || strings.HasSuffix(ws.Path, "/"+want) {
				return true
			}
		}
	}
	return false
}

// forbiddenTmpl renders the dark 403 page a restricted operator sees on a
// workspace their account is not allowed to open.
var forbiddenTmpl = template.Must(template.New("forbidden").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>ogcode control plane — forbidden</title>
<style>
  :root{ color-scheme:light dark;
    --bg:#f6f7f9; --card:#ffffff; --border:#e7e9ee;
    --text:#1b1e26; --muted:#5f6672; --accent:#5b63d6;
    --warn:#8a6100; --warn-bg:rgba(190,140,20,.14);
    --shadow:0 14px 44px rgba(20,24,33,.12); }
  @media (prefers-color-scheme:dark){ :root{
    --bg:#0d0f14; --card:#171a24; --border:#262b39;
    --text:#e7e9f0; --muted:#9aa0b0; --accent:#8b93f5;
    --warn:#e8c878; --warn-bg:rgba(224,176,80,.13);
    --shadow:0 18px 50px rgba(0,0,0,.55); } }
  *{box-sizing:border-box}
  body{ margin:0; min-height:100vh; display:grid; place-items:center; padding:24px;
    font:15px/1.55 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
    background:var(--bg); color:var(--text); -webkit-font-smoothing:antialiased; }
  .card{ width:420px; max-width:100%; padding:30px 28px;
    background:var(--card); border:1px solid var(--border); border-radius:14px; box-shadow:var(--shadow); }
  .icon{ width:40px; height:40px; border-radius:10px; display:grid; place-items:center; margin-bottom:16px;
    background:var(--warn-bg); color:var(--warn); }
  .icon svg{ width:22px; height:22px; }
  h1{ margin:0 0 6px; font-size:18px; font-weight:660; letter-spacing:-.01em; }
  p{ margin:0 0 12px; color:var(--muted); font-size:13.5px; line-height:1.55; }
  code{ font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:12.5px; color:var(--text); }
  a{ color:var(--accent); font-size:13px; font-weight:550; text-decoration:none; }
  a:hover{ text-decoration:underline; }
</style></head><body>
  <div class="card">
    <div class="icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><rect x="4.5" y="10.5" width="15" height="10" rx="2"/><path d="M8 10.5V7a4 4 0 018 0v3.5"/></svg></div>
    <h1>Workspace not allowed</h1>
    <p>Your operator account is not permitted to open <code>{{.Label}}</code>.</p>
    <p>Ask an administrator to add it to your account&rsquo;s workspace allowlist.</p>
    <a href="/">&larr; Back to console</a>
  </div>
</body></html>`))

// renderForbidden writes the 403 page, no-store like every console surface.
// The label is interpolated so the operator can see exactly which workspace
// was denied.
func renderForbidden(w http.ResponseWriter, label string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = forbiddenTmpl.Execute(w, struct{ Label string }{Label: label})
}
