package master

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Users page paths, under the same /__operator namespace as login/logout.
// Every mutation is POST-only per the console's no-CSRF-framework posture:
// a GET cannot change state, and the session cookie is SameSite=Lax.
const (
	operatorUsersPath      = "/__operator/users"
	operatorUsersAddPath   = "/__operator/users/add"
	operatorUsersRmPath    = "/__operator/users/rm"
	operatorUsersSetWSPath = "/__operator/users/set-workspaces"
	// Multi-repo assignment and password reset from the Users page. Kept
	// distinct from the Sessions page's /__operator/users/assign (which
	// re-renders the sessions form) so each surface re-renders itself.
	operatorUsersAssignRepoPath   = "/__operator/users/assign-repo"
	operatorUsersUnassignRepoPath = "/__operator/users/unassign-repo"
	operatorUsersSetPWPath        = "/__operator/users/set-password"

	// forceDeleteField is the checkbox that mirrors the CLI's --force guard on
	// deleting the last operator account.
	forceDeleteField = "force"
)

// assignedRepo is one repository an account is assigned to, rendered as a
// removable tag: URL is the unassign form value, Slug the short display label.
type assignedRepo struct {
	URL  string
	Slug string
}

// accountRow is one row of the users page: the account name (plus its contact
// email and assigned repositories), its workspace allowlist (nil =
// unrestricted), and whether it is an administrator. The bcrypt hash is
// deliberately never surfaced to templates. Last marks the final row, which is
// the only one whose delete form needs the force checkbox.
type accountRow struct {
	Name       string
	Email      string
	Repos      []assignedRepo
	Workspaces []string
	Admin      bool
	Last       bool
	Num        string // display id, e.g. "#001"
	Idx        int    // 1-based row index, for per-employee dialog ids
	Av         string // avatar gradient class
	Ini        string // avatar initials
	Role       string // "admin" or "user", for the role filter
	// UnassignedRepos are the known repositories this employee is NOT yet
	// assigned to — the options in its assign dialog (one repo per assignment,
	// since each may start from its own base branch).
	UnassignedRepos []repoChoice
}

// repoChoice is one known repository offered in an employee's assign dialog.
type repoChoice struct {
	URL  string
	Slug string
}

// usersPageVM is the employees page view model: the employee table, the known
// repositories (for the assign form), roll-up counts for the header/footer, plus
// an optional outcome banner from the mutation that led here.
type usersPageVM struct {
	Accounts    []accountRow
	KnownRepos  []repoChoice
	Agents      []string // agent kinds offered in the per-employee start-session dialog
	Total       int
	AdminCount  int
	MemberCount int
	RepoConns   int
	FlashText   string
	FlashKind   string // "ok" or "err"; empty means no banner
	// Invite dialog reopen-on-error state (echoed form values).
	OpenInvite     bool
	InviteEmail    string
	InviteUsername string
	InviteAdmin    bool
}

// usersPageTmpl renders the users page body (inside the shared chrome). Two
// cards side by side: the account table (each row carries its allowlist pills
// and delete form) and the add-account form (name, email, password, role).
// add/rm/set-workspaces forms post to their apex endpoints; the delete form on
// the last row carries a force checkbox mirroring the CLI's --force guard.
var usersPageTmpl = template.Must(template.New("users").Parse(`
    {{if and .FlashText (not .OpenInvite)}}<div class="flash {{.FlashKind}}">{{.FlashText}}</div>{{end}}

    <div class="phead">
      <h1>Employees {{if .Accounts}}<span class="statuspill">access synced</span>{{end}}</h1>
      <div class="phead-actions">
        {{if .Accounts}}<div class="seg">
          <a href="#" onclick="return filterRole('all',this)" class="active">All <span class="n">{{.Total}}</span></a>
          <a href="#" onclick="return filterRole('user',this)">Members <span class="n">{{.MemberCount}}</span></a>
          <a href="#" onclick="return filterRole('admin',this)">Admins <span class="n">{{.AdminCount}}</span></a>
        </div>{{end}}
        <button type="button" onclick="document.getElementById('invite-employee').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>Invite employee</button>
      </div>
    </div>
    <p class="page-sub">Invite employees with an email and a password, then assign each one the repositories they work in. Every employee signs in with its own credentials.</p>

    {{if .Accounts}}
    <div class="panel">
      <table>
        <tr><th>Employee</th><th>Connected repositories</th><th style="text-align:right">Actions</th></tr>
        {{range .Accounts}}{{$name := .Name}}
        <tr data-role="{{.Role}}">
          <td>
            <div class="emp">
              <span class="avatar {{.Av}}">{{.Ini}}<span class="dot"></span></span>
              <div>
                <div class="emp-name">{{.Name}} <span class="emp-id">{{.Num}}</span>{{if .Admin}} <span class="pill admin">admin</span>{{end}}</div>
                <div class="emp-email">{{if .Email}}{{.Email}}{{else}}{{.Name}} · no email{{end}}</div>
              </div>
            </div>
          </td>
          <td class="wide">
            {{if .Admin}}
              <span class="repochip all"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>All repositories</span>
            {{else if .Repos}}
              <details class="repopop">
                <summary class="repochip"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 012-2h8"/></svg>{{len .Repos}} {{if eq (len .Repos) 1}}repository{{else}}repositories{{end}}<span class="caret">&#9662;</span></summary>
                <div class="rowmenu-pop wide">
                  <div class="rowmenu-sec">
                    <div class="t">Assigned repositories</div>
                    <div class="tags">
                      {{range .Repos}}<span class="tag"><code>{{.Slug}}</code>
                        <form method="post" action="/__operator/users/unassign-repo" title="Unassign {{.Slug}}">
                          <input type="hidden" name="username" value="{{$name}}">
                          <input type="hidden" name="repo" value="{{.URL}}">
                          <button type="submit" aria-label="Unassign">&times;</button>
                        </form></span>{{end}}
                    </div>
                  </div>
                </div>
              </details>
            {{else}}
              <span class="repochip none"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 012-2h8"/></svg>none assigned</span>
            {{end}}
          </td>
          <td style="text-align:right">
            <details class="rowmenu">
              <summary title="Employee actions" aria-label="Employee actions">
                <svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>
              </summary>
              <div class="rowmenu-pop menu">
                <div class="menu-info">Access: {{if .Workspaces}}{{range .Workspaces}}<span class="pill">{{.}}</span>{{end}}{{else}}<span class="unrestricted">all workspaces</span>{{end}}</div>
                <div class="menu-sep"></div>
                {{if not .Admin}}<button type="button" class="menu-item" onclick="document.getElementById('start-{{.Idx}}').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M7 8l4 4-4 4"/><path d="M13 16h4"/></svg>Start session</button>
                <button type="button" class="menu-item" onclick="document.getElementById('assign-{{.Idx}}').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 012-2h8"/></svg>Assign a repository</button>{{end}}
                <button type="button" class="menu-item" onclick="document.getElementById('pw-{{.Idx}}').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4.5" y="10.5" width="15" height="10" rx="2"/><path d="M8 10.5V7a4 4 0 018 0v3.5"/></svg>Reset password</button>
                <div class="menu-sep"></div>
                <button type="button" class="menu-item danger" onclick="document.getElementById('del-{{.Idx}}').showModal()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9 7V5a1 1 0 011-1h4a1 1 0 011 1v2"/><path d="M6.5 7l1 12.5a1 1 0 001 .9h7a1 1 0 001-.9L18 7"/></svg>Delete employee</button>
              </div>
            </details>

            {{if not .Admin}}
            <dialog id="assign-{{.Idx}}" class="modal">
              <form method="post" action="/__operator/users/assign-repo">
                <input type="hidden" name="user" value="{{.Name}}">
                <div class="modal-head"><h2>Assign a repository</h2><button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button></div>
                <div class="modal-body" style="text-align:left">
                  <div class="muted" style="margin:0 0 16px">Assign one repository to <b style="color:var(--text)">{{.Name}}</b> — they get their own git worktree off its clone. Assign one at a time so each can start from its own base branch.</div>
                  {{if .UnassignedRepos}}
                  <label for="assign-repo-{{.Idx}}">Repository</label>
                  <select id="assign-repo-{{.Idx}}" name="repo" required>
                    <option value="">— choose a repository —</option>
                    {{range .UnassignedRepos}}<option value="{{.URL}}">{{.Slug}}</option>{{end}}
                  </select>
                  <label for="assign-base-{{.Idx}}">Base branch (optional)</label>
                  <input type="text" id="assign-base-{{.Idx}}" name="base" autocomplete="off" placeholder="the repo&rsquo;s default branch">
                  {{if .Repos}}<div class="hint">Already assigned: {{range $i, $r := .Repos}}{{if $i}}, {{end}}{{$r.Slug}}{{end}}.</div>{{end}}
                  {{else if $.KnownRepos}}
                  <div class="hint">{{.Name}} is already assigned to every repository. Remove one from its repository chip to reassign it.</div>
                  {{else}}
                  <div class="hint">No repositories yet — add one on the Repositories page first, then assign it here.</div>
                  {{end}}
                </div>
                <div class="modal-foot"><button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button><button type="submit"{{if not .UnassignedRepos}} disabled{{end}}>Assign</button></div>
              </form>
            </dialog>

            <dialog id="start-{{.Idx}}" class="modal">
              <form method="post" action="/__operator/sessions/user">
                <input type="hidden" name="user" value="{{.Name}}">
                <div class="modal-head"><h2>Start a session</h2><button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button></div>
                <div class="modal-body" style="text-align:left">
                  <div class="muted" style="margin:0 0 16px">Start an agent session for <b style="color:var(--text)">{{.Name}}</b> in one of its repositories.</div>
                  {{if $.KnownRepos}}
                  <div class="frow">
                    <div>
                      <label for="start-repo-{{.Idx}}">Repository</label>
                      <select id="start-repo-{{.Idx}}" name="repo" required>
                        <option value="">— choose a repository —</option>
                        {{range $.KnownRepos}}<option value="{{.URL}}">{{.Slug}}</option>{{end}}
                      </select>
                    </div>
                    <div>
                      <label for="start-agent-{{.Idx}}">Agent</label>
                      <select id="start-agent-{{.Idx}}" name="agent">{{range $.Agents}}<option value="{{.}}">{{.}}</option>{{end}}</select>
                    </div>
                  </div>
                  <label for="start-base-{{.Idx}}">Base branch (optional)</label>
                  <input type="text" id="start-base-{{.Idx}}" name="base" autocomplete="off" placeholder="the repo&rsquo;s default branch">
                  <label for="start-prompt-{{.Idx}}">First prompt (optional)</label>
                  <textarea id="start-prompt-{{.Idx}}" name="prompt" placeholder="Left blank, the agent reviews the repository on arrival."></textarea>
                  {{else}}
                  <div class="hint">No repositories yet — add one on the Repositories page first.</div>
                  {{end}}
                </div>
                <div class="modal-foot"><button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button><button type="submit"{{if not $.KnownRepos}} disabled{{end}}>Start session</button></div>
              </form>
            </dialog>
            {{end}}

            <dialog id="pw-{{.Idx}}" class="modal">
              <form method="post" action="/__operator/users/set-password">
                <input type="hidden" name="username" value="{{.Name}}">
                <div class="modal-head"><h2>Reset password</h2><button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button></div>
                <div class="modal-body" style="text-align:left">
                  <div class="muted" style="margin:0 0 16px">Set a new password for <b style="color:var(--text)">{{.Name}}</b>, then share it with them.</div>
                  <label for="pw-new-{{.Idx}}">New password</label>
                  <input type="password" id="pw-new-{{.Idx}}" name="password" autocomplete="new-password" required>
                  <label for="pw-conf-{{.Idx}}">Confirm new password</label>
                  <input type="password" id="pw-conf-{{.Idx}}" name="confirm" autocomplete="new-password" required>
                </div>
                <div class="modal-foot"><button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button><button type="submit">Reset password</button></div>
              </form>
            </dialog>

            <dialog id="del-{{.Idx}}" class="modal">
              <form method="post" action="/__operator/users/rm">
                <input type="hidden" name="username" value="{{.Name}}">
                <div class="modal-head"><h2>Delete employee</h2><button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button></div>
                <div class="modal-body" style="text-align:left">
                  <div class="muted">Delete <b style="color:var(--text)">{{.Name}}</b>? This removes the account{{if .Repos}} and its worktrees (branches are kept){{end}}. This cannot be undone.</div>
                  {{if .Last}}<label class="chk" style="margin-top:16px;display:flex;align-items:center;gap:8px;font-size:13px;color:var(--muted)"><input type="checkbox" name="force" value="1" style="width:auto"> I understand this is the last account.</label>{{end}}
                </div>
                <div class="modal-foot"><button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button><button type="submit" class="danger">Delete employee</button></div>
              </form>
            </dialog>
          </td>
        </tr>
        {{end}}
      </table>
      <div class="cardfoot">
        <span>{{.Total}} invited {{if eq .Total 1}}employee{{else}}employees{{end}} &middot; {{.RepoConns}} repository connections</span>
        <span class="live">Access synchronization active</span>
      </div>
    </div>
    {{else}}
    <div class="empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="9" cy="8" r="3.2"/><path d="M3.5 20c.6-3.4 3.1-5.2 6-5.2s5.4 1.8 6 5.2"/><path d="M17 8h5M19.5 5.5v5"/></svg>
      <b>No employees yet.</b>
      Invite your first employee — set an email and a password so they can sign in.
      <div style="margin-top:16px"><button type="button" onclick="document.getElementById('invite-employee').showModal()">Invite employee</button></div>
    </div>
    {{end}}

    <dialog id="invite-employee" class="modal">
      <form method="post" action="/__operator/users/add">
        <div class="modal-head">
          <h2>Invite employee</h2>
          <button type="button" class="modal-x" onclick="this.closest('dialog').close()" aria-label="Close">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
          </button>
        </div>
        <div class="modal-body">
          {{if .OpenInvite}}<div class="flash {{.FlashKind}}" style="margin:0 0 16px">{{.FlashText}}</div>{{end}}
          <label for="email">Work email</label>
          <input type="email" id="email" name="email" autocomplete="off" value="{{.InviteEmail}}" placeholder="employee@acme.dev">
          <label for="username">Username (sign-in name)</label>
          <input type="text" id="username" name="username" autocomplete="off" value="{{.InviteUsername}}" required>
          <label for="password">Password</label>
          <input type="password" id="password" name="password" autocomplete="new-password" required>
          <label for="confirm">Confirm password</label>
          <input type="password" id="confirm" name="confirm" autocomplete="new-password" required>
          <label for="admin">Role</label>
          <select id="admin" name="admin">
            <option value="" {{if not .InviteAdmin}}selected{{end}}>Member — sees only its own sessions and assigned repositories</option>
            <option value="1" {{if .InviteAdmin}}selected{{end}}>Admin — sees every worker and session</option>
          </select>
          <div class="hint">The password is stored hashed (never in plain text) — share it with the employee so they can sign in.</div>
        </div>
        <div class="modal-foot">
          <button type="button" class="linkish" onclick="this.closest('dialog').close()">Cancel</button>
          <button type="submit">Invite employee</button>
        </div>
      </form>
    </dialog>
    {{if .OpenInvite}}<script>document.getElementById('invite-employee').showModal()</script>{{end}}`))

// flash carries one operator-visible outcome banner on the users page. When an
// invite attempt failed, openInvite reopens the invite dialog with the error and
// the echoed field values so the operator can fix it without retyping.
type flash struct {
	kind       string // "ok" or "err"
	text       string
	openInvite bool
	email      string
	username   string
	admin      bool
}

// handleUsersPage serves the operator accounts management page. Like the
// console itself it is operator-gated (handleApexConsole checked the gate
// before dispatching here).
func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	s.renderUsersFlash(w, r, flash{})
}

// renderUsersFlash renders the users page, optionally with an outcome banner
// from a completed mutation. The listing degrades gracefully: an account whose
// record vanished between ListUsers and GetUser (a concurrent rm) just drops
// off the page; a hard store failure on ListUsers is a 500.
func (s *Server) renderUsersFlash(w http.ResponseWriter, r *http.Request, f flash) {
	store := s.reg.Store()
	names, err := store.ListUsers()
	if err != nil {
		s.logger.Error("users page: list accounts", "err", err)
		http.Error(w, "account store error", http.StatusInternalServerError)
		return
	}
	// Known repositories (placements) offered in each employee's assign dialog.
	var known []repoChoice
	for _, e := range s.repos.list() {
		known = append(known, repoChoice{URL: e.URL, Slug: e.Slug})
	}

	sort.Strings(names)
	rows := make([]accountRow, 0, len(names))
	admins, repoConns := 0, 0
	for _, name := range names {
		rec, ok, err := store.GetUser(name)
		if err != nil || !ok {
			continue
		}
		repos := rec.AllRepos()
		assigned := make([]assignedRepo, 0, len(repos))
		has := make(map[string]bool, len(repos))
		for _, u := range repos {
			assigned = append(assigned, assignedRepo{URL: u, Slug: repoSlugFromURL(u)})
			has[u] = true
		}
		// The assign dialog offers only repositories this employee lacks — one
		// repo per assignment, each with its own base branch.
		var unassigned []repoChoice
		for _, k := range known {
			if !has[k.URL] {
				unassigned = append(unassigned, k)
			}
		}
		admin := rec.IsAdmin()
		role := "user"
		if admin {
			role = "admin"
			admins++
		} else {
			repoConns += len(assigned)
		}
		rows = append(rows, accountRow{
			Name:            name,
			Email:           rec.Email,
			Repos:           assigned,
			Workspaces:      rec.Workspaces,
			Admin:           admin,
			Num:             fmt.Sprintf("#%03d", len(rows)+1),
			Idx:             len(rows) + 1,
			Av:              avatarClass(name),
			Ini:             initials(name),
			Role:            role,
			UnassignedRepos: unassigned,
		})
	}
	if len(rows) > 0 {
		rows[len(rows)-1].Last = true
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache the users page (it reflects live account state).
	w.Header().Set("Cache-Control", "no-store")
	vm := usersPageVM{
		Accounts:    rows,
		KnownRepos:  known,
		Agents:      agentChoices,
		Total:       len(rows),
		AdminCount:  admins,
		MemberCount: len(rows) - admins,
		RepoConns:   repoConns,
	}
	if f.text != "" {
		vm.FlashText = f.text
		vm.FlashKind = f.kind
	}
	vm.OpenInvite = f.openInvite
	vm.InviteEmail = f.email
	vm.InviteUsername = f.username
	vm.InviteAdmin = f.admin
	var body strings.Builder
	_ = usersPageTmpl.Execute(&body, vm)
	s.writeChrome(w, r, "Employees — ogcode control plane", navUsers, body.String())
}

// handleUsersAdd creates an account from the users page form: username,
// password, confirm, and an optional comma-separated workspace allowlist.
// Mirrors `users add` semantics — bcrypt-hashed at DefaultCost, the raw
// password never persisted, and a duplicate is an error rather than a silent
// overwrite (the CLI prints added=false; the console states it).
func (s *Server) handleUsersAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")
	email := strings.TrimSpace(r.PostFormValue("email"))

	fail := func(msg string) {
		s.renderUsersFlash(w, r, flash{
			kind: "err", text: msg,
			openInvite: true, email: email, username: username,
			admin: r.PostFormValue("admin") == "1",
		})
	}
	switch {
	case username == "":
		fail("Username is required.")
		return
	case password == "":
		fail("Password is required.")
		return
	case password != confirm:
		fail("Passwords do not match.")
		return
	}
	if err := auth.ValidateUsername(username); err != nil {
		fail(err.Error())
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Error("users add: hash password", "err", err)
		fail("Could not hash the password.")
		return
	}
	store := s.reg.Store()
	if _, exists, err := store.GetUser(username); err != nil {
		s.logger.Error("users add: lookup", "err", err)
		fail("Account store error.")
		return
	} else if exists {
		fail(fmt.Sprintf("Employee %q already exists.", username))
		return
	}
	record := registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Email: email}
	if ws := parseWorkspaceCSV(r.PostFormValue("workspaces")); len(ws) > 0 {
		record.Workspaces = ws
	}
	// The role select: "1" = admin, anything else (including empty) = plain
	// user. New records pin the flag explicitly so the account's reach is what
	// the form showed, never the pre-accounts default.
	admin := r.PostFormValue("admin") == "1"
	record.Admin = &admin
	if err := store.PutUser(username, record); err != nil {
		s.logger.Error("users add: persist", "err", err)
		fail("Could not save the account.")
		return
	}
	s.logger.Info("operator account added via console", "user", username,
		"email", email, "workspaces", len(record.Workspaces), "admin", admin)
	s.renderUsersFlash(w, r, flash{kind: "ok",
		text: fmt.Sprintf("Employee %q added.", username)})
}

// handleUsersRm deletes an account, refusing the last one unless the force
// checkbox is set — the console mirror of the CLI's --force guard, so the
// browser cannot trivially lock every operator out. Deleting the currently
// signed-in operator signs them out at their next request (the A1
// validate-time existence check), which the flash on this very response still
// reaches them on.
func (s *Server) handleUsersRm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))

	fail := func(msg string) {
		s.renderUsersFlash(w, r, flash{kind: "err", text: msg})
	}
	if username == "" {
		fail("Username is required.")
		return
	}
	// The record is read before the delete: it carries the account's repo
	// assignment, which the deletion must unwind (the worktree removal runs
	// after the account is gone).
	store := s.reg.Store()
	rec, _, err := store.GetUser(username)
	if err != nil {
		s.logger.Error("users rm: lookup", "err", err)
		fail("Account store error.")
		return
	}
	if _, exists, err := store.GetUser(username); err != nil {
		s.logger.Error("users rm: lookup", "err", err)
		fail("Account store error.")
		return
	} else if !exists {
		fail(fmt.Sprintf("Employee %q does not exist.", username))
		return
	}
	names, err := store.ListUsers()
	if err != nil {
		s.logger.Error("users rm: list accounts", "err", err)
		fail("Account store error.")
		return
	}
	if len(names) <= 1 && r.PostFormValue(forceDeleteField) != "1" {
		fail("Refusing to delete the last operator account — tick the force checkbox to proceed.")
		return
	}
	if _, err := store.DeleteUser(username); err != nil {
		s.logger.Error("users rm: delete", "err", err)
		fail("Could not delete the account.")
		return
	}
	s.logger.Info("operator account deleted via console", "user", username)

	// Best-effort lifecycle hook: a repo-scoped account's worktrees go with the
	// account (branches are kept, so the work survives). Each assigned repo is
	// unwound; a failure is logged and surfaced in the flash but does not
	// resurrect the account — the operator can still merge or remove a leftover
	// worktree from the Repositories page.
	flashText := fmt.Sprintf("Employee %q deleted.", username)
	if repos := rec.AllRepos(); len(repos) > 0 {
		removed, failed := 0, 0
		for _, repoURL := range repos {
			ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
			err := s.RemoveUserWorktree(ctx, repoURL, username)
			cancel()
			if err != nil {
				failed++
				s.logger.Error("users rm: remove worktree", "user", username, "repo", repoURL, "err", err)
			} else {
				removed++
			}
		}
		switch {
		case failed == 0 && removed == 1:
			flashText += " Its worktree was removed (branch kept)."
		case failed == 0:
			flashText += fmt.Sprintf(" Its %d worktrees were removed (branches kept).", removed)
		case removed == 0:
			flashText += fmt.Sprintf(" Its worktree(s) on %d repositories could not be removed — remove them from the Repositories page.", failed)
		default:
			flashText += fmt.Sprintf(" %d worktrees removed; %d could not be — remove those from the Repositories page.", removed, failed)
		}
	}
	s.renderUsersFlash(w, r, flash{kind: "ok", text: flashText})
}

// handleUsersSetWorkspaces rewrites an account's workspace allowlist. Same
// record (hash, created-at), new list; an empty field clears the allowlist
// back to unrestricted. The cookie fingerprint makes the change bite live
// sessions immediately — the account's next request fails validate and
// re-logs-in (the same immediacy A1 gives `users rm`).
func (s *Server) handleUsersSetWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))

	fail := func(msg string) {
		s.renderUsersFlash(w, r, flash{kind: "err", text: msg})
	}
	if username == "" {
		fail("Username is required.")
		return
	}
	store := s.reg.Store()
	rec, exists, err := store.GetUser(username)
	if err != nil {
		s.logger.Error("users set-workspaces: lookup", "err", err)
		fail("Account store error.")
		return
	} else if !exists {
		fail(fmt.Sprintf("Employee %q does not exist.", username))
		return
	}
	rec.Workspaces = parseWorkspaceCSV(r.PostFormValue("workspaces"))
	if err := store.PutUser(username, rec); err != nil {
		s.logger.Error("users set-workspaces: persist", "err", err)
		fail("Could not save the allowlist.")
		return
	}
	s.logger.Info("operator account allowlist changed", "user", username,
		"workspaces", len(rec.Workspaces))
	if len(rec.Workspaces) == 0 {
		s.renderUsersFlash(w, r, flash{kind: "ok",
			text: fmt.Sprintf("Allowlist cleared — %q may open all workspaces.", username)})
		return
	}
	s.renderUsersFlash(w, r, flash{kind: "ok",
		text: fmt.Sprintf("Allowlist updated for %q: %s.", username,
			strings.Join(rec.Workspaces, ", "))})
}

// handleUsersAssignRepo assigns one user to one or more repositories from the
// Users page: each selected repo is provisioned (the user's own git worktree is
// added off the clone) and recorded on the account. Assignment is additive and
// idempotent — re-assigning a repo the user already has is a no-op — so a user's
// set can grow over time. Administrators are refused by AssignUser (assignment
// scopes plain users only).
func (s *Server) handleUsersAssignRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	user := strings.TrimSpace(r.PostFormValue("user"))
	base := strings.TrimSpace(r.PostFormValue("base"))
	fail := func(msg string) { s.renderUsersFlash(w, r, flash{kind: "err", text: msg}) }
	if user == "" {
		fail("Choose a user.")
		return
	}
	repos := trimmedNonEmpty(r.PostForm["repo"])
	if len(repos) == 0 {
		fail("Select at least one repository.")
		return
	}
	var done, failed []string
	for _, repoURL := range repos {
		ctx, cancel := context.WithTimeout(r.Context(), assignTimeout)
		_, err := s.AssignUser(ctx, repoURL, user, base)
		cancel()
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s)", repoSlugFromURL(repoURL), err))
		} else {
			done = append(done, repoSlugFromURL(repoURL))
		}
	}
	switch {
	case len(failed) == 0:
		s.renderUsersFlash(w, r, flash{kind: "ok",
			text: fmt.Sprintf("Assigned %q to %s.", user, strings.Join(done, ", "))})
	case len(done) == 0:
		s.renderUsersFlash(w, r, flash{kind: "err",
			text: fmt.Sprintf("Could not assign %q: %s.", user, strings.Join(failed, "; "))})
	default:
		s.renderUsersFlash(w, r, flash{kind: "err",
			text: fmt.Sprintf("Assigned %q to %s; could not assign %s.", user, strings.Join(done, ", "), strings.Join(failed, "; "))})
	}
}

// handleUsersUnassignRepo removes one repository from a user: the worktree is
// dropped on the placement worker (branch kept) and the repo is removed from the
// account's set. A worker-side failure is surfaced, but the assignment is
// dropped regardless — the operator can clean up any lingering worktree from the
// Repositories page.
func (s *Server) handleUsersUnassignRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	user := strings.TrimSpace(r.PostFormValue("username"))
	repoURL := strings.TrimSpace(r.PostFormValue("repo"))
	fail := func(msg string) { s.renderUsersFlash(w, r, flash{kind: "err", text: msg}) }
	if user == "" || repoURL == "" {
		fail("User and repository are required.")
		return
	}
	store := s.reg.Store()
	rec, exists, err := store.GetUser(user)
	if err != nil {
		s.logger.Error("users unassign: lookup", "err", err)
		fail("Account store error.")
		return
	}
	if !exists {
		fail(fmt.Sprintf("Employee %q does not exist.", user))
		return
	}
	next, removed := rec.WithRepoRemoved(repoURL)
	if !removed {
		fail(fmt.Sprintf("%q is not assigned to %s.", user, repoSlugFromURL(repoURL)))
		return
	}
	// Best-effort worktree removal (branch kept); drop the assignment regardless.
	ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
	wtErr := s.RemoveUserWorktree(ctx, repoURL, user)
	cancel()
	if err := store.PutUser(user, next); err != nil {
		s.logger.Error("users unassign: persist", "err", err)
		fail("Could not update the account.")
		return
	}
	s.logger.Info("user unassigned from repo via console", "user", user, "repo", repoSlugFromURL(repoURL))
	msg := fmt.Sprintf("Unassigned %q from %s.", user, repoSlugFromURL(repoURL))
	if wtErr != nil {
		s.logger.Error("users unassign: remove worktree", "user", user, "repo", repoURL, "err", wtErr)
		msg += " Its worktree could not be removed — remove it from the Repositories page."
	} else {
		msg += " Its worktree was removed (branch kept)."
	}
	s.renderUsersFlash(w, r, flash{kind: "ok", text: msg})
}

// handleUsersSetPassword resets one account's password from the Users page:
// same validation as add (non-empty, confirmed), bcrypt-hashed at DefaultCost,
// the raw password never persisted. The account keeps its role and repository
// assignments; only Hash changes.
func (s *Server) handleUsersSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	user := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")
	fail := func(msg string) { s.renderUsersFlash(w, r, flash{kind: "err", text: msg}) }
	switch {
	case user == "":
		fail("Choose a user.")
		return
	case password == "":
		fail("Password is required.")
		return
	case password != confirm:
		fail("Passwords do not match.")
		return
	}
	store := s.reg.Store()
	rec, exists, err := store.GetUser(user)
	if err != nil {
		s.logger.Error("users set-password: lookup", "err", err)
		fail("Account store error.")
		return
	}
	if !exists {
		fail(fmt.Sprintf("Employee %q does not exist.", user))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Error("users set-password: hash", "err", err)
		fail("Could not hash the password.")
		return
	}
	rec.Hash = string(hash)
	if err := store.PutUser(user, rec); err != nil {
		s.logger.Error("users set-password: persist", "err", err)
		fail("Could not save the new password.")
		return
	}
	s.logger.Info("operator account password reset via console", "user", user)
	s.renderUsersFlash(w, r, flash{kind: "ok", text: fmt.Sprintf("Password reset for %q.", user)})
}

// trimmedNonEmpty trims each value and drops the empties — used for repeated
// form fields (the assign form's repository checkboxes).
func trimmedNonEmpty(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseWorkspaceCSV splits a comma-separated allowlist field into trimmed,
// de-duplicated workspace identifiers. Empty pieces are dropped; an empty
// field yields nil (unrestricted).
func parseWorkspaceCSV(field string) []string {
	if strings.TrimSpace(field) == "" {
		return nil
	}
	parts := strings.Split(field, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
