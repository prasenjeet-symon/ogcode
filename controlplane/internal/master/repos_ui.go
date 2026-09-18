package master

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Repositories page paths, under the same /__operator namespace as the users
// and sessions endpoints. Both mutations are POST-only per the console's
// no-CSRF-framework posture: a GET cannot change state, and the session cookie
// is SameSite=Lax.
const (
	operatorReposPath            = "/__operator/repos"
	operatorReposAddPath         = "/__operator/repos/add"
	operatorReposRmPath          = "/__operator/repos/forget"
	operatorReposMergePath       = "/__operator/repos/merge"
	operatorReposDeprovisionPath = "/__operator/repos/deprovision"
	// Container mode (INCUS_WORKERS_PLAN.md §Phase B): the placements table's
	// create (assign = create container) and destroy (unassign = delete
	// container) actions.
	operatorPlacementsCreatePath  = "/__operator/repos/placements/create"
	operatorPlacementsDestroyPath = "/__operator/repos/placements/destroy"
)

// cloneTimeout bounds one CloneRepo round-trip. Like a session start, the
// worker runs the git clone synchronously before acking, so a cold first clone
// of a large repository legitimately takes minutes.
const cloneTimeout = 5 * time.Minute

// lifecycleTimeout bounds one RemoveUserWorktree / MergeUserBranch /
// DeprovisionRepo round-trip — the merge and deprovision may push or delete
// multi-gigabyte clones on the worker, so they get the same generosity as a
// clone.
const lifecycleTimeout = 5 * time.Minute

// repoRow is one row of the repositories page: a repo the master knows about
// (placed at some point) or one a live worker reports, with its placement.
type repoRow struct {
	Slug     string
	URL      string
	WorkerID string
	Online   bool
}

// reposPageVM is the repositories page view model: the repo table, the online
// worker options (the add form's placement choices), and the echoed add form
// fields plus the outcome banner from the mutation that led here. In container
// mode the Placements table replaces the repo table's source rows.
type reposPageVM struct {
	Repos      []repoRow
	Workers    []workerOption
	RepoURL    string
	Worker     string
	FlashText  string
	FlashKind  string // "ok" or "err"; empty means no banner
	OpenAdd    bool   // reopen the add-repository dialog (an add attempt failed)
	Containers []placementRow
}

// placementRow is one row of the container-mode placements table.
type placementRow struct {
	User       string
	RepoSlug   string
	RepoURL    string
	Container  string
	Status     string
	CreatedAgo string
}

// reposPageTmpl renders the repositories page body (inside the shared chrome).
// The table lists known placements; the add form clones a new repo now on the
// chosen online worker via the CloneRepo command.
var reposPageTmpl = template.Must(template.New("repos").Parse(`
    {{if and .FlashText (not .OpenAdd)}}<div class="flash {{.FlashKind}}">{{.FlashText}}</div>{{end}}

    <div class="phead">
      <h1>Repositories</h1>
      <div class="phead-actions">
        <button type="button" onclick="document.getElementById('add-repo').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>Add repository</button>
      </div>
    </div>
    <p class="page-sub">Cloned on workers — one checkout each. Every assigned employee works in their own git worktree off the shared clone.</p>

    {{if .Repos}}
    <div class="panel">
      <table>
        <tr><th>Repository</th><th>Worker</th><th style="text-align:right">Actions</th></tr>
        {{range .Repos}}
        <tr>
          <td class="name wide">
            {{.Slug}}
            <div class="td-sub"><code>{{.URL}}</code></div>
          </td>
          <td>
            <span class="badge {{if .Online}}online{{else}}offline{{end}}">{{if .Online}}online{{else}}offline{{end}}</span>
            <div class="muted" style="margin-top:5px;font-family:var(--mono)">{{.WorkerID}}</div>
          </td>
          <td style="text-align:right;white-space:nowrap">
            <details class="rowmenu">
              <summary title="Repository actions" aria-label="Repository actions">
                <svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>
              </summary>
              <div class="rowmenu-pop menu">
                <form method="post" action="/__operator/repos/forget" title="Drop from this list (the clone stays on the worker)">
                  <input type="hidden" name="slug" value="{{.Slug}}">
                  <button type="submit" class="menu-item"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M8.5 12h7"/></svg>Remove from list</button>
                </form>
                <div class="menu-sep"></div>
                <form method="post" action="/__operator/repos/deprovision" title="Remove every employee worktree (branches kept); the clone stays on the worker">
                  <input type="hidden" name="slug" value="{{.Slug}}">
                  <button type="submit" class="menu-item"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="3" y="7" width="18" height="13" rx="2"/><path d="M8 12h8"/><path d="M8 3h8"/></svg>Deprovision worktrees</button>
                </form>
                <form method="post" action="/__operator/repos/deprovision" title="Remove all worktrees and delete the clone on the worker">
                  <input type="hidden" name="slug" value="{{.Slug}}">
                  <input type="hidden" name="remove" value="1">
                  <button type="submit" class="menu-item danger"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9 7V5a1 1 0 011-1h4a1 1 0 011 1v2"/><path d="M6.5 7l1 12.5a1 1 0 001 .9h7a1 1 0 001-.9L18 7"/></svg>Delete clone &amp; worktrees</button>
                </form>
              </div>
            </details>
          </td>
        </tr>
        {{end}}
      </table>
      <div class="cardfoot">
        <span>{{len .Repos}} {{if eq (len .Repos) 1}}repository{{else}}repositories{{end}} &middot; one checkout per worker</span>
        <span class="live">Worktree engine active</span>
      </div>
    </div>
    {{else}}
    <div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><rect x="8" y="8" width="13" height="13" rx="2"/><path d="M4 16V5a2 2 0 012-2h11"/></svg>
      <b>No repositories yet.</b>
      Add one to clone it on a worker — each assigned employee then gets a git worktree off the clone.
      <div style="margin-top:16px"><button type="button" onclick="document.getElementById('add-repo').showModal()">Add repository</button></div>
    </div>
    {{end}}

    {{if .Containers}}
    <div class="panel" style="margin-top:24px">
      <table>
        <tr><th>Employee</th><th>Repository</th><th>Container</th><th>Status</th><th style="text-align:right">Actions</th></tr>
        {{range .Containers}}
        <tr>
          <td class="name">{{.User}}</td>
          <td>
            {{.RepoSlug}}
            <div class="td-sub"><code>{{.RepoURL}}</code></div>
          </td>
          <td style="font-family:var(--mono)">{{.Container}}</td>
          <td><span class="badge {{if eq .Status "ready"}}online{{else if eq .Status "failed"}}offline{{else}}pending{{end}}">{{.Status}}</span><div class="muted" style="margin-top:5px">{{.CreatedAgo}}</div></td>
          <td style="text-align:right;white-space:nowrap">
            <details class="rowmenu">
              <summary title="Assignment actions" aria-label="Assignment actions">
                <svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>
              </summary>
              <div class="rowmenu-pop menu">
                <form method="post" action="/__operator/repos/placements/create" title="Re-create the container (re-assign; reuses the name)">
                  <input type="hidden" name="user" value="{{.User}}">
                  <input type="hidden" name="repo" value="{{.RepoURL}}">
                  <button type="submit" class="menu-item"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>Create / re-create</button>
                </form>
                <form method="post" action="/__operator/repos/placements/destroy" title="Unassign: destroy the container and forget the placement (the branch survives if it was pushed)">
                  <input type="hidden" name="user" value="{{.User}}">
                  <input type="hidden" name="repo" value="{{.RepoURL}}">
                  <button type="submit" class="menu-item danger"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9 7V5a1 1 0 011-1h4a1 1 0 011 1v2"/><path d="M6.5 7l1 12.5a1 1 0 001 .9h7a1 1 0 001-.9L18 7"/></svg>Destroy assignment</button>
                </form>
              </div>
            </details>
          </td>
        </tr>
        {{end}}
      </table>
      <div class="cardfoot">
        <span>{{len .Containers}} {{if eq (len .Containers) 1}}assignment{{else}}assignments{{end}} &middot; one container per employee-repository</span>
        <span class="live">Container mode</span>
      </div>
    </div>
    {{end}}

    <dialog id="add-repo" class="modal">
      <form method="post" action="/__operator/repos/add">
        <div class="modal-head">
          <h2>Add repository</h2>
          <button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
          </button>
        </div>
        <div class="modal-body">
          {{if .OpenAdd}}<div class="flash {{.FlashKind}}" style="margin:0 0 16px">{{.FlashText}}</div>{{end}}
          <label for="repo-url">Repository URL</label>
          <input type="text" id="repo-url" name="repo" autocomplete="off" value="{{.RepoURL}}" placeholder="https://github.com/org/repo">
          <label for="repo-worker">Worker</label>
          <select id="repo-worker" name="worker" required>
            <option value="" {{if not .Worker}}selected{{end}}>— choose a worker —</option>
            {{range .Workers}}<option value="{{.ID}}" {{if .Selected}}selected{{end}}>{{.Name}} — {{.ID}}</option>{{end}}
          </select>
          <div class="hint">Cloned once per worker under its repo root, using the worker&rsquo;s existing git credentials. Every assigned employee works in a git worktree off that single checkout.</div>
          {{if not .Workers}}<div class="hint">No workers are online right now — start a worker (ogcode worker) first.</div>{{end}}
        </div>
        <div class="modal-foot">
          <button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button>
          <button type="submit">Add repository</button>
        </div>
      </form>
    </dialog>
    {{if .OpenAdd}}<script>document.getElementById('add-repo').showModal()</script>{{end}}`))

// handleReposPage serves the repositories page. Like the other console pages
// it is operator-gated (handleApexConsole checked the gate before dispatching
// here).
func (s *Server) handleReposPage(w http.ResponseWriter, r *http.Request) {
	s.renderReposFlash(w, r, reposPageVM{})
}

// renderReposFlash renders the repositories page, optionally with an outcome
// banner from a completed mutation. Placement records are in-memory (bare
// mode) or in the placements bucket (container mode), so a fresh master lists
// only what it has seen this run; the add/assign flows re-record eagerly.
func (s *Server) renderReposFlash(w http.ResponseWriter, r *http.Request, vm reposPageVM) {
	if s.incus != nil {
		if placements, err := s.placements.list(); err == nil {
			now := time.Now()
			for _, p := range placements {
				ago := ""
				if !p.CreatedAt.IsZero() {
					ago = "created " + humanSince(now.Sub(p.CreatedAt))
				}
				vm.Containers = append(vm.Containers, placementRow{
					User:       p.User,
					RepoSlug:   p.RepoSlug,
					RepoURL:    p.RepoURL,
					Container:  p.ContainerName,
					Status:     p.Status,
					CreatedAgo: ago,
				})
			}
		} else {
			s.logger.Error("list placements for the page", "err", err)
		}
	}
	entries := s.repos.list()
	for _, e := range entries {
		online := false
		if info, ok := s.reg.Get(e.WorkerID); ok {
			online = info.Status == registry.StatusOnline
		}
		vm.Repos = append(vm.Repos, repoRow{
			Slug: e.Slug, URL: e.URL, WorkerID: e.WorkerID, Online: online,
		})
	}
	infos := s.reg.List()
	for _, info := range infos {
		if info.Status != registry.StatusOnline {
			continue
		}
		vm.Workers = append(vm.Workers, workerOption{
			ID: info.ID, Name: info.Name, Selected: info.ID == vm.Worker,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache the page (it reflects live worker state).
	w.Header().Set("Cache-Control", "no-store")
	var body strings.Builder
	_ = reposPageTmpl.Execute(&body, vm)
	s.writeChrome(w, r, "Repositories — ogcode control plane", navRepos, body.String())
}

// handleReposAdd registers a repository: it resolves the worker (the recorded
// holder of the clone when one exists, else the roomiest online worker by free
// disk), asks that worker to clone the repo now via CloneRepo, and records the
// placement. The clone is idempotent on the worker — re-adding a repo that is
// already there reuses the existing checkout.
func (s *Server) handleReposAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := reposPageVM{
		RepoURL: strings.TrimSpace(r.PostFormValue("repo")),
		Worker:  strings.TrimSpace(r.PostFormValue("worker")),
	}
	fail := func(msg string) {
		// Reopen the add-repository dialog with the error shown inside it, so the
		// operator can fix the URL/worker without retyping.
		vm.FlashText, vm.FlashKind, vm.OpenAdd = msg, "err", true
		s.renderReposFlash(w, r, vm)
	}

	switch {
	case vm.RepoURL == "":
		fail("Enter a repository URL.")
		return
	case vm.Worker == "":
		fail("Choose a worker.")
		return
	}
	repoURL, err := normalizeRepoURL(vm.RepoURL)
	if err != nil {
		fail(err.Error())
		return
	}
	if info, ok := s.reg.Get(vm.Worker); !ok {
		fail(fmt.Sprintf("Worker %q is not registered.", vm.Worker))
		return
	} else if info.Status != registry.StatusOnline {
		fail(fmt.Sprintf("Worker %q is not online.", vm.Worker))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cloneTimeout)
	defer cancel()
	if err := s.CloneRepo(ctx, repoURL, vm.Worker); err != nil {
		s.logger.Info("repo add failed", "repo", repoURL, "worker", vm.Worker, "err", err)
		fail(err.Error())
		return
	}
	s.repos.set(repoSlugFromURL(repoURL), repoURL, vm.Worker)
	s.logger.Info("repository added via console", "repo", repoURL, "worker", vm.Worker,
		"slug", repoSlugFromURL(repoURL))
	vm.FlashText = fmt.Sprintf("Repository %s added on worker %q — assign a user to it on the Sessions page.",
		repoURL, vm.Worker)
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}

// handleReposForget drops the master's placement record for a repo. The clone
// itself is left on the worker's disk (EnsureRepo re-records the placement the
// next time the repo is used); this only unlists it here.
func (s *Server) handleReposForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	vm := reposPageVM{}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderReposFlash(w, r, vm)
	}
	if slug == "" {
		fail("Repository slug is required.")
		return
	}
	if _, ok := s.repos.get(slug); !ok {
		fail(fmt.Sprintf("Repository %q is not listed.", slug))
		return
	}
	s.repos.forget(slug)
	s.logger.Info("repository placement forgotten via console", "slug", slug)
	vm.FlashText = fmt.Sprintf("Repository %q removed from the list — its clone stays on the worker until a session or assignment needs it.", slug)
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}

// handleReposMerge merges one user's branch back into the repo's base branch
// via the placement worker. The form carries the slug, the user name, and an
// optional push checkbox; the base branch stays the worker's default-branch
// resolution (the merge runs against whatever the clone tracks).
func (s *Server) handleReposMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	vm := reposPageVM{Worker: strings.TrimSpace(r.PostFormValue("worker"))}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderReposFlash(w, r, vm)
	}
	user := strings.TrimSpace(r.PostFormValue("user"))
	push := r.PostFormValue("push") == "1"
	switch {
	case slug == "":
		fail("Repository slug is required.")
		return
	case user == "":
		fail("User name is required.")
		return
	}
	rec, ok := s.repos.get(slug)
	if !ok {
		fail(fmt.Sprintf("Repository %q is not listed.", slug))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
	defer cancel()
	summary, err := s.MergeUserBranch(ctx, rec.URL, user, "", push)
	if err != nil {
		s.logger.Info("user merge failed via console", "repo", rec.URL, "user", user, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("user merged via console", "repo", rec.URL, "user", user, "summary", summary)
	vm.FlashText = fmt.Sprintf("Merged %s on %s: %s.", user, slug, summary)
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}

// handleReposDeprovision retires a repo: the placement worker removes every
// remaining user worktree (branches kept) and, when the remove checkbox is
// set, deletes the clone itself. The placement is forgotten and this repo's
// user sessions are unrouted on success.
func (s *Server) handleReposDeprovision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	vm := reposPageVM{}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderReposFlash(w, r, vm)
	}
	if slug == "" {
		fail("Repository slug is required.")
		return
	}
	rec, ok := s.repos.get(slug)
	if !ok {
		fail(fmt.Sprintf("Repository %q is not listed.", slug))
		return
	}
	removeClone := r.PostFormValue("remove") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
	defer cancel()
	summary, err := s.DeprovisionRepo(ctx, rec.URL, removeClone)
	if err != nil {
		s.logger.Info("repo deprovision failed via console", "repo", rec.URL, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("repo deprovisioned via console", "repo", rec.URL, "cloneDeleted", removeClone)
	vm.FlashText = fmt.Sprintf("Repository %s deprovisioned: %s.", slug, summary)
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}

// CloneRepo asks workerID to clone repoURL now (EnsureRepo on the worker) —
// the eager provisioning behind the Repositories page's add form, before any
// user is assigned.
func (s *Server) CloneRepo(ctx context.Context, repoURL, workerID string) error {
	res, err := s.Call(ctx, workerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_CloneRepo{CloneRepo: &cpv1.CloneRepo{
			RepoUrl: repoURL,
		}},
	})
	if err != nil {
		return err
	}
	if !res.GetOk() {
		return fmt.Errorf("worker %q could not clone %s: %s", workerID, repoURL, res.GetError())
	}
	return nil
}

// RepoPlacements is the exported test seam for the placement table: the same
// snapshot the repositories page renders (sorted by slug).
func (s *Server) RepoPlacements() []repoEntry { return s.repos.list() }

// SeedRepoPlacement records a repo placement directly — the test seam for the
// bookkeeping CloneRepo/AssignUser perform on a successful worker round-trip.
// Production callers reach it through those flows; tests that need a placement
// without a live clone (e.g. a worker that rejects the next command) use this.
func (s *Server) SeedRepoPlacement(repoURL, workerID string) {
	s.repos.set(repoSlugFromURL(repoURL), repoURL, workerID)
}

// humanSince renders a duration as a coarse human age ("3m", "2h5m", "4d").
func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// handlePlacementsCreate (re-)creates one user-repo container: the
// placements table's Create action. In container mode this is AssignUser —
// idempotent for a live placement, re-provisioning for a missing one. In bare
// mode the route is not reachable (the action renders only in container mode).
func (s *Server) handlePlacementsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := reposPageVM{}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderReposFlash(w, r, vm)
	}
	user := strings.TrimSpace(r.PostFormValue("user"))
	repoURL, err := normalizeRepoURL(strings.TrimSpace(r.PostFormValue("repo")))
	if user == "" || err != nil {
		fail("Employee and repository are required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloneTimeout)
	defer cancel()
	name, err := s.AssignUser(ctx, repoURL, user, "")
	if err != nil {
		s.logger.Info("placement create failed via console", "user", user, "repo", repoURL, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("placement created via console", "user", user, "repo", repoURL, "container", name)
	vm.FlashText = fmt.Sprintf("Container %s created for %s on %s — it registers with the master at boot; the assignment turns ready then.", name, user, repoSlugFromURL(repoURL))
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}

// handlePlacementsDestroy unassigns one user-repo container: the placements
// table's Destroy action. It deletes the container, forgets the placement,
// and applies the standard unassignment bookkeeping.
func (s *Server) handlePlacementsDestroy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	vm := reposPageVM{}
	fail := func(msg string) {
		vm.FlashText, vm.FlashKind = msg, "err"
		s.renderReposFlash(w, r, vm)
	}
	user := strings.TrimSpace(r.PostFormValue("user"))
	repoURL, err := normalizeRepoURL(strings.TrimSpace(r.PostFormValue("repo")))
	if user == "" || err != nil {
		fail("Employee and repository are required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
	defer cancel()
	if err := s.RemoveUserWorktree(ctx, repoURL, user); err != nil {
		s.logger.Info("placement destroy failed via console", "user", user, "repo", repoURL, "err", err)
		fail(err.Error())
		return
	}
	s.logger.Info("placement destroyed via console", "user", user, "repo", repoURL)
	vm.FlashText = fmt.Sprintf("Assignment of %s to %s destroyed — the container is gone; the branch survives if it was pushed.", user, repoSlugFromURL(repoURL))
	vm.FlashKind = "ok"
	s.renderReposFlash(w, r, vm)
}
