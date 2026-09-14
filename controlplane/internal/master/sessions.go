package master

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Sessions page paths, under the same /__operator namespace as the users
// endpoints. The mutation is POST-only per the console's no-CSRF-framework
// posture: a GET cannot change state, and the session cookie is SameSite=Lax.
const (
	operatorSessionsPath      = "/__operator/sessions"
	operatorSessionsStartPath = "/__operator/sessions/start"

	// startTimeout bounds one StartRemoteAgent round-trip. The worker runs the
	// git clone synchronously before acking, so a cold first clone of a large
	// repository legitimately takes minutes.
	startTimeout = 5 * time.Minute

	// workspaceListTimeout bounds the live ListWorkspaces round-trip the form
	// makes to the chosen worker. A worker that does not answer in time falls
	// back to the set it advertised at registration.
	workspaceListTimeout = 10 * time.Second

	// defaultPrompt is used when the operator leaves the first prompt blank:
	// the hosted agent starts in a freshly cloned repository it has never seen.
	defaultPrompt = "Review this repository and summarize what it does."
)

// agentChoices are the session kinds the console offers. They mirror the agent
// names the per-directory server resolves through GetAgent (internal/agent);
// anything unrecognized falls through to the build agent there anyway, so the
// form validates against exactly this set and passes the value through.
var agentChoices = []string{"build", "plan", "task", "breakdown"}

// workerOption is one entry of the worker select on the start-session form.
type workerOption struct {
	ID       string
	Name     string
	Selected bool
}

// workspaceOption is one entry of the workspace select: a directory the chosen
// worker advertised (one of its --workspace roots, or a git worktree under
// one), plus whether it is the echoed selection.
type workspaceOption struct {
	Path     string
	Name     string
	Branch   string
	Present  bool
	Selected bool
}

// sessionStartVM is the start-session form view model: the online worker
// options, the chosen worker's advertised workspaces, the known repositories
// (the user-session form's datalist), the echoed form fields after a failed
// attempt, and the outcome banner from the attempt that led here. The user
// fields echo the start-user-session and assign-user cards.
type sessionStartVM struct {
	Sessions   []sessionRow // active routed sessions the viewer may see
	Workers    []workerOption
	Workspaces []workspaceOption
	Agents     []string
	Accounts   []string // non-admin accounts, for the user/assign selects
	Repos      []repoEntry
	Worker     string
	Repo       string
	Workspace  string
	Agent      string
	Prompt     string
	User       string
	UserRepo   string
	UserBase   string
	UserAgent  string
	FlashText  string
	FlashKind  string // "ok" or "err"; empty means no banner
}

// sessionRow is one active session in the Sessions list: its global id, the
// employee it was started for, the worker hosting it, and the monitor link.
type sessionRow struct {
	ID         string
	Employee   string
	WorkerID   string
	WorkerName string
	Online     bool
	OpenURL    string
}

// sessionsTmpl renders the start-session page body (inside the shared chrome).
// The primary card is the user workflow — pick a user, a repository, optionally
// a base branch; the handler assigns the user (provisioning the worktree, the
// base branch included) and then starts the session in it. The advanced
// worker+workspace path and the split assign / start steps stay below as
// collapsed details cards; their endpoints are unchanged.
var sessionsTmpl = template.Must(template.New("sessions").Parse(`
    {{if .FlashText}}<div class="flash {{.FlashKind}}">{{.FlashText}}</div>{{end}}

    <div class="phead">
      <h1>Sessions</h1>
      <div class="phead-actions">
        <button type="button" onclick="document.getElementById('start-session').showModal()"{{if not .Accounts}} disabled{{end}}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>Start session</button>
      </div>
    </div>
    <p class="page-sub">Agent sessions running across your workers. Each runs in an employee&rsquo;s own git worktree off the repository&rsquo;s clone; open a session&rsquo;s monitor to watch it live.</p>

    {{if .Sessions}}
    <div class="panel">
      <table>
        <tr><th>Session</th><th>Employee</th><th>Worker</th><th style="text-align:right">Actions</th></tr>
        {{range .Sessions}}
        <tr>
          <td class="name"><code>{{.ID}}</code></td>
          <td>{{if .Employee}}{{.Employee}}{{else}}<span class="muted">—</span>{{end}}</td>
          <td><span class="badge {{if .Online}}online{{else}}offline{{end}}">{{if .Online}}online{{else}}offline{{end}}</span> {{if .WorkerName}}<span class="muted" style="font-family:var(--mono)">{{.WorkerName}}</span>{{end}}</td>
          <td style="text-align:right"><a class="btn linkish sm" href="{{.OpenURL}}">Open monitor &rarr;</a></td>
        </tr>
        {{end}}
      </table>
      <div class="cardfoot"><span>{{len .Sessions}} active {{if eq (len .Sessions) 1}}session{{else}}sessions{{end}}</span><span class="live">Live routing active</span></div>
    </div>
    {{else}}
    <div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M7 8l4 4-4 4"/><path d="M13 16h4"/><rect x="2.5" y="3.5" width="19" height="17" rx="2.5"/></svg>
      <b>No active sessions.</b>
      Start a session for an employee — it shows up here while it runs, with a link to its live monitor.
      <div style="margin-top:16px"><button type="button" onclick="document.getElementById('start-session').showModal()"{{if not .Accounts}} disabled{{end}}>Start session</button></div>
    </div>
    {{end}}

    <dialog id="start-session" class="modal">
      <form method="post" action="/__operator/sessions/user">
        <div class="modal-head"><h2>Start a session</h2><button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button></div>
        <div class="modal-body" style="text-align:left">
          {{if not .Accounts}}<div class="hint" style="margin:0 0 14px">No employees yet — invite one on the Employees page first, then start a session for them.</div>{{end}}
          <div class="frow">
            <div>
              <label for="ss-user">Employee</label>
              <select id="ss-user" name="user" required>
                <option value="">— choose an employee —</option>
                {{range .Accounts}}<option value="{{.}}">{{.}}</option>{{end}}
              </select>
            </div>
            <div>
              <label for="ss-agent">Agent</label>
              <select id="ss-agent" name="agent">{{range .Agents}}<option value="{{.}}"{{if eq . $.UserAgent}} selected{{end}}>{{.}}</option>{{end}}</select>
            </div>
          </div>
          <label for="user-repo">Repository</label>
          {{if .Repos}}
          <select id="user-repo" name="repo" required>
            <option value="">— choose a repository —</option>
            {{range .Repos}}<option value="{{.URL}}">{{.Slug}}</option>{{end}}
          </select>
          {{else}}
          <input type="text" id="user-repo" name="repo" autocomplete="off" placeholder="https://github.com/org/repo">
          <div class="hint">No repositories yet — add one on the Repositories page, or paste its URL.</div>
          {{end}}
          <label for="ss-base">Base branch (optional)</label>
          <input type="text" id="ss-base" name="base" autocomplete="off" placeholder="the repo&rsquo;s default branch">
          <label for="ss-prompt">First prompt (optional)</label>
          <textarea id="ss-prompt" name="prompt" placeholder="Left blank, the agent reviews the repository on arrival."></textarea>
        </div>
        <div class="modal-foot"><button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button><button type="submit"{{if not .Accounts}} disabled{{end}}>Start session</button></div>
      </form>
    </dialog>`))

// handleSessionsPage serves the start-session form. Like the users page it is
// operator-gated (handleApexConsole checked the gate before dispatching here).
// A ?worker=<id> query — the console's per-worker "Start session" link, or the
// worker select reloading the page — preselects that worker and lists its
// workspaces; the remaining fields ride along so switching workers does not
// wipe what the operator already typed.
func (s *Server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	vm := sessionStartVM{
		Worker:    strings.TrimSpace(q.Get("worker")),
		Repo:      strings.TrimSpace(q.Get("repo")),
		Workspace: strings.TrimSpace(q.Get("workspace")),
		Agent:     strings.TrimSpace(q.Get("agent")),
		Prompt:    strings.TrimSpace(q.Get("prompt")),
	}
	if vm.Agent == "" {
		vm.Agent = agentChoices[0]
	}
	s.renderSessions(w, r, vm)
}

// handleSessionsStart starts a hosted session from the form. The workspace is
// chosen, never typed: the posted value is resolved against the workspaces the
// chosen worker advertises (fetched live, falling back to the registration-time
// set) and only the advertised path is sent down the stream. The worker then
// clones the repository when the workspace is absent on disk, spawns the
// per-directory server and opens the tunnel; the master routes the session.
// The outcome is rendered as a banner carrying the hosted session's URL.
func (s *Server) handleSessionsStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := sessionStartVM{
		Worker:    strings.TrimSpace(r.PostFormValue("worker")),
		Repo:      strings.TrimSpace(r.PostFormValue("repo")),
		Workspace: strings.TrimSpace(r.PostFormValue("workspace")),
		Agent:     strings.TrimSpace(r.PostFormValue("agent")),
		Prompt:    strings.TrimSpace(r.PostFormValue("prompt")),
	}
	fail := func(msg string) {
		if vm.Agent == "" {
			vm.Agent = agentChoices[0]
		}
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderSessions(w, r, vm)
	}

	switch {
	case vm.Worker == "":
		fail("Choose a worker.")
		return
	case vm.Workspace == "":
		fail("Choose a workspace.")
		return
	}
	if _, ok := s.reg.Get(vm.Worker); !ok {
		fail(fmt.Sprintf("Worker %q is not registered.", vm.Worker))
		return
	}
	offered, err := s.workerWorkspaces(r, vm.Worker)
	if err != nil {
		fail(err.Error())
		return
	}
	vm.Workspaces = workspaceOptions(offered, vm.Workspace)
	ws, err := selectWorkspace(vm.Worker, offered, vm.Workspace)
	if err != nil {
		fail(err.Error())
		return
	}
	repo := ""
	if vm.Repo != "" {
		repo, err = normalizeRepoURL(vm.Repo)
		if err != nil {
			fail(err.Error())
			return
		}
	}
	agent, err := normalizeAgentName(vm.Agent)
	if err != nil {
		fail(err.Error())
		return
	}

	prompt := vm.Prompt
	if prompt == "" {
		prompt = defaultPrompt
	}
	ctx, cancel := context.WithTimeout(r.Context(), startTimeout)
	defer cancel()
	sessionID, err := s.StartRemoteAgent(ctx, vm.Worker, StartSpec{
		Workspace: ws.Path,
		AgentName: agent,
		Prompt:    prompt,
		Ref:       repo,
	})
	if err != nil {
		s.logger.Info("console session start failed", "worker", vm.Worker,
			"workspace", ws.Path, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("session started via console", "sessionID", sessionID,
		"worker", vm.Worker, "workspace", ws.Path, "agent", agent,
		"cloned", repo != "")

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	open := fmt.Sprintf("%s://%s/sessions/%s", scheme, r.Host, sessionID)
	vm.Agent = agent
	if vm.Agent == "" {
		vm.Agent = agentChoices[0]
	}
	vm.FlashText = fmt.Sprintf("Session %s started on worker %q — open %s", sessionID, vm.Worker, open)
	vm.FlashKind = "ok"
	s.renderSessions(w, r, vm)
}

// renderSessions renders the start-session form. The worker select degrades
// gracefully: offline workers drop off it (they cannot host a session), and a
// preselect that matches nothing is ignored. The workspace select reflects the
// chosen worker's advertised set — fetched live when the caller has not
// already resolved it.
func (s *Server) renderSessions(w http.ResponseWriter, r *http.Request, vm sessionStartVM) {
	infos := s.reg.List()
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].Status != infos[j].Status {
			return infos[i].Status == registry.StatusOnline
		}
		return infos[i].Name < infos[j].Name
	})
	for _, info := range infos {
		if info.Status != registry.StatusOnline {
			continue
		}
		vm.Workers = append(vm.Workers, workerOption{
			ID:       info.ID,
			Name:     info.Name,
			Selected: info.ID == vm.Worker,
		})
	}
	vm.Agents = agentChoices
	if vm.UserAgent == "" {
		vm.UserAgent = agentChoices[0]
	}
	vm.Accounts = s.assignableAccounts()
	vm.Repos = s.repos.list()
	vm.Sessions = s.visibleSessions(r)
	if vm.Worker != "" && len(vm.Workspaces) == 0 {
		if offered, err := s.workerWorkspaces(r, vm.Worker); err == nil {
			vm.Workspaces = workspaceOptions(offered, vm.Workspace)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache the form (it reflects live worker state).
	w.Header().Set("Cache-Control", "no-store")
	var body strings.Builder
	_ = sessionsTmpl.Execute(&body, vm)
	s.writeChrome(w, r, "Sessions — ogcode control plane", navSessions, body.String())
}

// visibleSessions lists the active routed sessions the requester may see:
// administrators see every session; a scoped employee sees only sessions
// started under its own account. Each row carries the hosting worker's live
// status and a link to the session's monitor.
func (s *Server) visibleSessions(r *http.Request) []sessionRow {
	userID, admin, ok := s.currentUser(r)
	if !ok {
		return nil
	}
	all := s.reg.AllSessions()
	rows := make([]sessionRow, 0, len(all))
	for sid, wid := range all {
		startedBy, scoped := s.SessionUser(sid)
		if !admin && (!scoped || startedBy != userID) {
			continue
		}
		row := sessionRow{ID: sid, Employee: startedBy, WorkerID: wid, OpenURL: operatorSessionsViewPrefix + sid}
		if info, ok := s.reg.Get(wid); ok {
			row.WorkerName = info.Name
			row.Online = info.Status == registry.StatusOnline
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

// assignableAccounts lists the non-admin accounts (sorted) for the
// assign-user select. Administrators are excluded: assigning an admin to a
// single repo contradicts what the flag means.
func (s *Server) assignableAccounts() []string {
	store := s.reg.Store()
	if store == nil {
		return nil
	}
	names, err := store.ListUsers()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		rec, found, err := store.GetUser(name)
		if err != nil || !found || rec.IsAdmin() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// workerWorkspaces resolves the workspaces a worker offers, preferring a live
// ListWorkspaces round-trip (which also refreshes the registration cache) and
// falling back to the set it advertised at registration when it cannot answer.
func (s *Server) workerWorkspaces(r *http.Request, workerID string) ([]registry.Workspace, error) {
	ctx, cancel := context.WithTimeout(r.Context(), workspaceListTimeout)
	defer cancel()
	if ws, err := s.ListWorkspaces(ctx, workerID); err == nil {
		return ws, nil
	}
	info, ok := s.reg.Get(workerID)
	if !ok {
		return nil, fmt.Errorf("worker %q is not registered", workerID)
	}
	if len(info.Workspaces) > 0 {
		return info.Workspaces, nil
	}
	return nil, fmt.Errorf("could not load the workspaces worker %q offers (it did not answer and none were cached)", workerID)
}

// selectWorkspace resolves the posted workspace value against the set the
// worker actually offers. Only the advertised path is ever forwarded to the
// worker — never the posted string — so a hand-forged form cannot point a
// session, or a git clone, anywhere the worker did not volunteer.
func selectWorkspace(workerID string, offered []registry.Workspace, posted string) (registry.Workspace, error) {
	cleaned := filepath.Clean(posted)
	for _, ws := range offered {
		if ws.Path == posted || filepath.Clean(ws.Path) == cleaned {
			return ws, nil
		}
	}
	return registry.Workspace{}, fmt.Errorf("workspace %q is not offered by worker %q — choose one of its listed workspaces", posted, workerID)
}

// workspaceOptions maps a worker's advertised workspaces to the select's view
// model, marking the echoed selection.
func workspaceOptions(offered []registry.Workspace, selected string) []workspaceOption {
	out := make([]workspaceOption, 0, len(offered))
	for _, ws := range offered {
		out = append(out, workspaceOption{
			Path:     ws.Path,
			Name:     ws.Name,
			Branch:   ws.Branch,
			Present:  ws.Present,
			Selected: ws.Path == selected,
		})
	}
	return out
}

// normalizeRepoURL validates the clone source the way the worker's git clone
// consumes it: a supported remote URL, an scp-like "git@host:owner/repo"
// shortcut, or a bare local path on the worker. The scheme check exists to
// reject the common paste mistakes at the form instead of surfacing them as a
// confusing clone failure on the worker. The value is passed through verbatim
// — no ogcode-level auth is added; private repos use the worker's credential
// helper.
func normalizeRepoURL(raw string) (string, error) {
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http", "https", "ssh", "git", "git+ssh", "file":
			return raw, nil
		default:
			return "", fmt.Errorf("unsupported repository URL scheme %q (use https://, ssh://, or a local path)", u.Scheme)
		}
	}
	// No parsable scheme: scp-like git@host:owner/repo (url.Parse rejects the
	// colon) or a bare local path — both are valid clone sources.
	return raw, nil
}

// normalizeAgentName validates the form's agent choice: empty means the
// default (build); anything else must be one of agentChoices.
func normalizeAgentName(agent string) (string, error) {
	if agent == "" {
		return "", nil
	}
	for _, a := range agentChoices {
		if a == agent {
			return a, nil
		}
	}
	return "", fmt.Errorf("agent must be one of: %s", strings.Join(agentChoices, ", "))
}

// operatorUserAssignPath provisions a user's worktree on a repository — the
// assignment step that must run before a user session can start.
const operatorUserAssignPath = "/__operator/users/assign"

// operatorUserSessionPath starts a session via logical targeting: repo + user,
// no workspace path. The worker resolves the pair to the user's worktree.
const operatorUserSessionPath = "/__operator/sessions/user"

// assignTimeout bounds one AssignUser round-trip. Like a session start, the
// worker runs the (first-time) git clone synchronously before acking.
const assignTimeout = 5 * time.Minute

// handleUserAssign runs the assignment flow: pick the worker holding the
// repo's clone (or the roomiest online one), have it clone-and-worktree, and
// scope the account to its worktree. The account must already exist —
// credentials are minted on the Users page, never as a side effect here.
func (s *Server) handleUserAssign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := sessionStartVM{
		UserRepo: strings.TrimSpace(r.PostFormValue("repo")),
		User:     strings.TrimSpace(r.PostFormValue("user")),
		UserBase: strings.TrimSpace(r.PostFormValue("base")),
	}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderSessions(w, r, vm)
	}
	switch {
	case vm.UserRepo == "":
		fail("Enter a repository URL.")
		return
	case vm.User == "":
		fail("Choose a user.")
		return
	}
	repoURL, err := normalizeRepoURL(vm.UserRepo)
	if err != nil {
		fail(err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), assignTimeout)
	defer cancel()
	if _, err := s.AssignUser(ctx, repoURL, vm.User, vm.UserBase); err != nil {
		s.logger.Info("user assignment failed", "user", vm.User, "repo", repoURL, "err", err)
		fail(err.Error())
		return
	}
	vm.FlashText = fmt.Sprintf("Assigned %s to %s — their worktree is ready; start their session any time.",
		vm.User, repoURL)
	vm.FlashKind = "ok"
	s.renderSessions(w, r, vm)
}

// handleUserSessionStart runs the combined user flow: assign (provisioning the
// user's worktree off the chosen base branch, idempotent when the pair is
// already assigned) and then start the session in it via logical targeting —
// the master routes to the worker holding the repo's clone and the worker
// targets the user's worktree. Only the user's own (slug) name rides to the
// worker.
func (s *Server) handleUserSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := sessionStartVM{
		UserRepo:  strings.TrimSpace(r.PostFormValue("repo")),
		User:      strings.TrimSpace(r.PostFormValue("user")),
		UserBase:  strings.TrimSpace(r.PostFormValue("base")),
		UserAgent: strings.TrimSpace(r.PostFormValue("agent")),
		Prompt:    strings.TrimSpace(r.PostFormValue("prompt")),
	}
	fail := func(msg string) {
		if vm.UserAgent == "" {
			vm.UserAgent = agentChoices[0]
		}
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderSessions(w, r, vm)
	}
	switch {
	case vm.UserRepo == "":
		fail("Enter a repository URL.")
		return
	case vm.User == "":
		fail("Choose a user.")
		return
	}
	repoURL, err := normalizeRepoURL(vm.UserRepo)
	if err != nil {
		fail(err.Error())
		return
	}
	agent, err := normalizeAgentName(vm.UserAgent)
	if err != nil {
		fail(err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), assignTimeout+startTimeout)
	defer cancel()
	// Assign first — it provisions the worktree (with the base branch) and is
	// a no-op when this (repo, user) pair is already assigned.
	if _, err := s.AssignUser(ctx, repoURL, vm.User, vm.UserBase); err != nil {
		s.logger.Info("user assignment failed", "user", vm.User, "repo", repoURL, "err", err)
		fail(err.Error())
		return
	}

	prompt := vm.Prompt
	if prompt == "" {
		prompt = defaultPrompt
	}
	sessionID, err := s.StartUserSession(ctx, repoURL, vm.User, StartSpec{
		AgentName: agent,
		Prompt:    prompt,
	})
	if err != nil {
		s.logger.Info("user session start failed", "user", vm.User, "repo", repoURL, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("user session started via console", "sessionID", sessionID,
		"user", vm.User, "repo", repoURL, "agent", agent, "base", vm.UserBase)

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	open := fmt.Sprintf("%s://%s/sessions/%s", scheme, r.Host, sessionID)
	vm.FlashText = fmt.Sprintf("Session %s started for %s on %s — open %s",
		sessionID, vm.User, repoURL, open)
	vm.FlashKind = "ok"
	s.renderSessions(w, r, vm)
}
