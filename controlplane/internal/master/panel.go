package master

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"
)

// apexRoute is one worktree tunnel a worker currently serves.
type apexRoute struct {
	Label string
	URL   string
}

// apexWorker is one row of the base-host console.
type apexWorker struct {
	Name     string
	ID       string
	Status   string
	LastSeen string
	Caps     []string
	Routes   []apexRoute
	URL      string // legacy bare subdomain; rendered only when Routes is empty
}

// isApexConsole reports whether a request to the apex host (not a worker
// subdomain) should be served by the operator console rather than falling
// through to the base mux. The console owns "/" plus the operator login/logout
// endpoints; everything else (ConnectRPC, /healthz) stays on the base mux.
func isApexConsole(r *http.Request) bool {
	switch r.URL.Path {
	case operatorLoginPath, operatorLogoutPath, "/",
		operatorUsersPath, operatorUsersAddPath, operatorUsersRmPath, operatorUsersSetWSPath,
		operatorUsersAssignRepoPath, operatorUsersUnassignRepoPath, operatorUsersSetPWPath,
		operatorReposPath, operatorReposAddPath, operatorReposRmPath,
		operatorReposMergePath, operatorReposDeprovisionPath,
		operatorPlacementsCreatePath, operatorPlacementsDestroyPath,
		operatorSessionsPath, operatorSessionsStartPath,
		operatorUserAssignPath, operatorUserSessionPath:
		return true
	}
	// The session monitors are prefix-routed: /sessions/<id> renders the page,
	// /sessions/<id>/events is its SSE stream.
	return strings.HasPrefix(r.URL.Path, operatorSessionsViewPrefix)
}

// handleApexConsole serves the base-host landing page (list of connected
// workers with links) and the operator login/logout endpoints for the apex host.
// It is operator-gated like the worker UI proxy: an unauthenticated browser sees
// the login page.
func (s *Server) handleApexConsole(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case operatorLoginPath:
		s.handleOperatorLogin(w, r)
		return
	case operatorLogoutPath:
		s.handleOperatorLogout(w, r)
		return
	}
	// Operator-gated surfaces: an unauthenticated browser sees the login page.
	if s.gate != nil && s.gate.Enabled() && !s.gate.Authenticated(r) {
		renderLogin(w, "")
		return
	}
	switch r.URL.Path {
	case operatorUsersPath:
		s.handleUsersPage(w, r)
	case operatorUsersAddPath:
		s.handleUsersAdd(w, r)
	case operatorUsersRmPath:
		s.handleUsersRm(w, r)
	case operatorUsersSetWSPath:
		s.handleUsersSetWorkspaces(w, r)
	case operatorUsersAssignRepoPath:
		s.handleUsersAssignRepo(w, r)
	case operatorUsersUnassignRepoPath:
		s.handleUsersUnassignRepo(w, r)
	case operatorUsersSetPWPath:
		s.handleUsersSetPassword(w, r)
	case operatorReposPath:
		s.handleReposPage(w, r)
	case operatorReposAddPath:
		s.handleReposAdd(w, r)
	case operatorReposRmPath:
		s.handleReposForget(w, r)
	case operatorReposMergePath:
		s.handleReposMerge(w, r)
	case operatorReposDeprovisionPath:
		s.handleReposDeprovision(w, r)
	case operatorPlacementsCreatePath:
		s.handlePlacementsCreate(w, r)
	case operatorPlacementsDestroyPath:
		s.handlePlacementsDestroy(w, r)
	case operatorSessionsPath:
		s.handleSessionsPage(w, r)
	case operatorSessionsStartPath:
		s.handleSessionsStart(w, r)
	case operatorUserAssignPath:
		s.handleUserAssign(w, r)
	case operatorUserSessionPath:
		s.handleUserSessionStart(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, operatorSessionsViewPrefix) {
			s.handleSessionView(w, r, strings.TrimPrefix(r.URL.Path, operatorSessionsViewPrefix))
			return
		}
		s.renderApex(w, r)
	}
}

// renderApex renders the dashboard: the workflow tiles (users → repositories →
// sessions, mirroring the sidebar), connected workers, and their worktree UIs.
// Online workers sort first, then by name.
func (s *Server) renderApex(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	infos := s.visibleWorkers(r, s.reg.List())
	workers := make([]apexWorker, 0, len(infos))
	online, offline, routes := 0, 0, 0
	for _, info := range infos {
		aw := apexWorker{
			Name:     info.Name,
			ID:       info.ID,
			Status:   string(info.Status),
			LastSeen: ago(info.LastSeen),
			Caps:     info.Capabilities,
		}
		if aw.Status == "online" {
			online++
		} else {
			offline++
		}
		// Per-worktree tunnels link to their "<workerID>-<route>" subdomains;
		// a worker serving none (legacy bare tunnel, or tunnels not yet
		// opened) keeps the bare worker id link.
		for _, route := range s.tunnels.routes(info.ID) {
			aw.Routes = append(aw.Routes, apexRoute{
				Label: route,
				URL:   fmt.Sprintf("%s://%s.%s/", scheme, tunnelKey(info.ID, route), r.Host),
			})
		}
		routes += len(aw.Routes)
		if len(aw.Routes) == 0 {
			aw.URL = fmt.Sprintf("%s://%s.%s/", scheme, info.ID, r.Host)
		}
		workers = append(workers, aw)
	}
	sort.SliceStable(workers, func(i, j int) bool {
		if workers[i].Status != workers[j].Status {
			return workers[i].Status == "online"
		}
		return workers[i].Name < workers[j].Name
	})
	users, repos := 0, 0
	if store := s.reg.Store(); store != nil {
		if names, err := store.ListUsers(); err == nil {
			users = len(names)
		}
	}
	repos = len(s.repos.list())

	var body strings.Builder
	_ = apexTmpl.Execute(&body, struct {
		Workers []apexWorker
		Online  int
		Offline int
		Routes  int
		Users   int
		Repos   int
	}{Workers: workers, Online: online, Offline: offline, Routes: routes, Users: users, Repos: repos})
	s.writeChrome(w, r, "Dashboard — ogcode control plane", navDashboard, body.String())
}

// ago renders a time as a short relative "… ago" string.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// apexTmpl renders the dashboard body (inside the shared chrome). The tiles
// mirror the sidebar workflow — Users → Repositories → Sessions — each with a
// link into its step.
var apexTmpl = template.Must(template.New("apex").Parse(`
    <div class="page-title">Dashboard</div>
    <p class="page-sub">Connected workers, running sessions, and operator accounts across the control plane.</p>

    <div class="tiles">
      <a class="tile tile-link" href="/__operator/users"><div class="num">{{.Users}}</div><div class="lbl">Employees</div></a>
      <a class="tile tile-link" href="/__operator/repos"><div class="num">{{.Repos}}</div><div class="lbl">Repositories</div></a>
      <a class="tile tile-link" href="/__operator/sessions"><div class="num">{{.Online}}</div><div class="lbl">Workers online</div></a>
      <a class="tile tile-link" href="/__operator/sessions"><div class="num">{{.Routes}}</div><div class="lbl">Worktree UIs</div></a>
    </div>

    {{if .Workers}}
    <div class="worker-grid">
    {{range .Workers}}
      <div class="worker">
        <div class="top">
          <h2>{{.Name}}</h2>
          <span class="badge {{.Status}}">{{.Status}}</span>
        </div>
        <div class="id">{{.ID}}</div>
        <div class="meta">Last seen {{.LastSeen}}</div>
        {{if .Caps}}<div class="caps">{{range .Caps}}<span class="cap">{{.}}</span>{{end}}</div>{{end}}
        {{if eq .Status "online"}}<a class="open" href="/__operator/sessions?worker={{.ID}}">Start session &rarr;</a>{{end}}
        {{if .Routes}}
          {{range .Routes}}<a class="open" href="{{.URL}}">Open {{.Label}} UI &rarr;</a>{{end}}
        {{else}}
          <a class="open" href="{{.URL}}">Open worker UI &rarr;</a>
        {{end}}
      </div>
    {{end}}
    </div>
    {{else}}
    <div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>
      <b>No workers connected yet.</b>
      Start a worker (<code>ogcode worker</code>) and it appears here.
    </div>
    {{end}}`))
