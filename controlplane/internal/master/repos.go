package master

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// Repo bookkeeping on the master: which worker holds the clone of which
// repository. Deliberately in-memory (mirroring the worker's own repos map,
// which re-seeds from disk): AddUserWorktree is idempotent, so a master
// restart re-records a repo lazily at its next assignment or session start
// instead of persisting a table that can drift from the workers' disk state.
type repoRecord struct {
	URL      string
	WorkerID string
}

// repoEntry is one row of repoStore.list — a snapshot copy, safe to hand to
// handlers.
type repoEntry struct {
	Slug     string
	URL      string
	WorkerID string
}

// repoStore maps repo slug -> placement record, guarded by a mutex (Call-based
// provisioning is sequential per assignment, but concurrent console posts are
// possible).
type repoStore struct {
	mu    sync.Mutex
	repos map[string]repoRecord // slug -> record
}

func newRepoStore() *repoStore {
	return &repoStore{repos: map[string]repoRecord{}}
}

func (rs *repoStore) set(slug, url, workerID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.repos[slug] = repoRecord{URL: url, WorkerID: workerID}
}

func (rs *repoStore) get(slug string) (repoRecord, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rec, ok := rs.repos[slug]
	return rec, ok
}

func (rs *repoStore) forget(slug string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.repos, slug)
}

// list returns snapshot copies sorted by slug.
func (rs *repoStore) list() []repoEntry {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	slugs := make([]string, 0, len(rs.repos))
	for slug := range rs.repos {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	out := make([]repoEntry, 0, len(slugs))
	for _, slug := range slugs {
		rec := rs.repos[slug]
		out = append(out, repoEntry{Slug: slug, URL: rec.URL, WorkerID: rec.WorkerID})
	}
	return out
}

// repoSlugFromURL slugifies a repo URL into the exact segment the worker's
// safeRepoName uses for its clone directory. Keeping the two byte-identical is
// load-bearing: the master keys its placement map by slug, so assignment
// provisioning must land on the same clone a session start resolves to. The
// mirror lives here (not an import) because internal/worker belongs to the
// ogcode module, not the control-plane module.
func repoSlugFromURL(repoURL string) string {
	s := repoURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Drop any userinfo (git@… for scp-like form, user:pass@… for URLs) up to
	// the last @, then ':' becomes a separator (scp-like host:path, :port).
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.TrimSuffix(s, ".git")
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '/' || ch == '.' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	s = strings.Trim(b.String(), "-./_")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.ReplaceAll(s, "/", "-")
	if s == "" {
		return "repo"
	}
	return s
}

// userWorktreeSlug reduces a user name to the exact segment the worker's
// safeUserName uses for branch "user/<name>" and worktree dir
// "<repoDir>/.ogcode/worktrees/user/<name>" — whose base name is also the
// tunnel route label the worker derives via worktreeLabel, and therefore the
// allowlist identifier a repo-scoped account is auto-pinned to.
func userWorktreeSlug(userName string) string {
	s := strings.ToLower(strings.ReplaceAll(userName, " ", "-"))
	var b strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			b.WriteRune(ch)
		}
	}
	s = strings.Trim(b.String(), "-._")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "user"
	}
	return s
}

// RepoSlugFromURL is the exported test seam for repoSlugFromURL: the master's
// repo-placement key must stay byte-identical to the worker's safeRepoName,
// and tests on both sides pin the same goldens.
func RepoSlugFromURL(repoURL string) string { return repoSlugFromURL(repoURL) }

// UserWorktreeSlug is the exported test seam for userWorktreeSlug: the
// allowlist identifier a repo-scoped account is pinned to must stay
// byte-identical to the worker's safeUserName (which names both the branch and
// the worktree dir whose base doubles as the tunnel route label).
func UserWorktreeSlug(userName string) string { return userWorktreeSlug(userName) }

// ErrNoWorkerOnline is returned by pickWorker when no registered worker is
// online to provision or host a repo session.
var ErrNoWorkerOnline = errors.New("no worker is online to host the repository")

// statsTimeout bounds each per-worker Stats round-trip inside pickWorker, so
// one silent worker cannot stall placement for the whole command timeout — it
// is skipped instead of counted as zero.
const statsTimeout = 5 * time.Second

// pickWorker chooses the placement for a repo clone: the worker already
// holding the clone when one does (one clone per repo), otherwise the online
// worker reporting the most free disk (its Stats reply). A worker that does
// not answer Stats in time is skipped — guessing its capacity could stack
// every repo on one silent worker.
func (s *Server) pickWorker(ctx context.Context, repoURL string) (string, error) {
	slug := repoSlugFromURL(repoURL)
	if rec, ok := s.repos.get(slug); ok {
		if _, attached := s.reg.Get(rec.WorkerID); attached {
			return rec.WorkerID, nil
		}
		// The recorded holder deregistered; fall through and re-place.
		s.repos.forget(slug)
	}
	best, bestFree := "", int64(-1)
	online := false
	for _, info := range s.reg.List() {
		if info.Status != registry.StatusOnline {
			continue
		}
		online = true
		callCtx, cancel := context.WithTimeout(ctx, statsTimeout)
		res, err := s.Call(callCtx, info.ID, &cpv1.MasterToWorker{
			Command: &cpv1.MasterToWorker_Stats{Stats: &cpv1.Stats{}},
		})
		cancel()
		if err != nil || !res.GetOk() {
			continue
		}
		if free := res.GetFreeBytes(); free > bestFree {
			best, bestFree = info.ID, free
		}
	}
	if best == "" {
		if online {
			return "", fmt.Errorf("no online worker reported its free disk space")
		}
		return "", ErrNoWorkerOnline
	}
	return best, nil
}

// AssignUser provisions one user's worktree on a repository: it picks (or
// reuses) the worker holding the clone, asks that worker to clone the repo if
// missing and add the user's branch + worktree, and records the repo's
// placement. baseBranch is the branch the user's worktree is cut from; empty
// means the repo's default branch (the worker's origin/HEAD resolution). It
// is recorded on the account so the Users page and any later re-provisioning
// can show what the worktree tracks. Administrators are refused — assignment
// is how an account becomes repo-scoped, which is meaningless (and surprising)
// for an admin; create a plain user instead. A plain account without an
// explicit workspace allowlist is pinned to its own worktree's tunnel label
// (the worktree dir's base name, identical to the worker's worktreeLabel
// derivation); one WITH an explicit allowlist keeps it untouched — the
// operator chose those identifiers.
func (s *Server) AssignUser(ctx context.Context, repoURL, userName, baseBranch string) (string, error) {
	repoURL = strings.TrimSpace(repoURL)
	userName = strings.TrimSpace(userName)
	baseBranch = strings.TrimSpace(baseBranch)
	if repoURL == "" {
		return "", errors.New("repo url required")
	}
	if userName == "" {
		return "", errors.New("user name required")
	}
	if s.gate == nil || !s.gate.Enabled() {
		return "", errors.New("user accounts are disabled on this control plane")
	}
	store := s.reg.Store()
	if store == nil {
		return "", errors.New("no account store is configured")
	}
	exists, err := store.UserExists(userName)
	if err != nil {
		return "", fmt.Errorf("look up account %q: %w", userName, err)
	}
	if !exists {
		return "", fmt.Errorf("account %q does not exist — create it on the Users page first", userName)
	}
	if rec, found, err := store.GetUser(userName); err == nil && found && rec.IsAdmin() {
		return "", fmt.Errorf("account %q is an administrator — assignment scopes plain users only", userName)
	}

	// Container mode (INCUS §Phase B): the assignment IS a container. Bypass
	// pickWorker/Call entirely — capacity is the container cap, provisioning
	// is the driver Create, readiness arrives via Register.
	if s.incus != nil {
		return s.assignUserContainer(ctx, repoURL, userName, baseBranch)
	}

	workerID, err := s.pickWorker(ctx, repoURL)
	if err != nil {
		return "", err
	}
	res, err := s.Call(ctx, workerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_AddUserWorktree{AddUserWorktree: &cpv1.AddUserWorktree{
			RepoUrl:    repoURL,
			UserName:   userName,
			BaseBranch: baseBranch,
		}},
	})
	if err != nil {
		return "", err
	}
	if !res.GetOk() {
		return "", fmt.Errorf("worker %q could not provision %q on %s: %s", workerID, userName, repoURL, res.GetError())
	}

	s.repos.set(repoSlugFromURL(repoURL), repoURL, workerID)
	s.logger.Info("user worktree provisioned", "user", userName,
		"repo", repoSlugFromURL(repoURL), "workerID", workerID)

	if err := s.scopeAccount(store, userName, repoURL, baseBranch); err != nil {
		return "", err
	}
	return workerID, nil
}

// scopeUserAccount applies the assignment scoping to the account record:
// add the repo to the account's set, record the base branch, and auto-pin an
// empty allowlist to the user's own worktree slug. Shared by the bare-worker
// and container-mode assignment paths.
func (s *Server) scopeAccount(store *registry.Store, userName, repoURL, baseBranch string) error {
	// Scope the account in the store now — the login and allowlist paths read
	// the store's live record, so the pin takes effect without a re-login
	// cycle beyond the cookie fingerprint invalidation this write triggers.
	rec, ok, err := store.GetUser(userName)
	if err != nil {
		return fmt.Errorf("provisioned, but scoping the account failed: %w", err)
	}
	if !ok {
		return errors.New("provisioned, but the account vanished mid-assignment")
	}
	// Additive assignment: add this repo to the account's set (idempotent),
	// folding the legacy scalar Repo into the multi-repo slice.
	rec, added := rec.WithRepoAdded(repoURL)
	changed := added
	if rec.BaseBranch != baseBranch {
		rec.BaseBranch = baseBranch
		changed = true
	}
	// Auto-scope: a single allowlist entry — the user slug — already matches
	// this user's worktree in EVERY assigned repo, because the worker names each
	// worktree dir after the user (its base name is the allowlist-matched
	// workspace Name). So one pin covers all repos; no per-repo entry is needed.
	if len(rec.Workspaces) == 0 {
		rec.Workspaces = []string{userWorktreeSlug(userName)}
		changed = true
	}
	if changed {
		if err := store.PutUser(userName, rec); err != nil {
			return fmt.Errorf("provisioned, but scoping the account failed: %w", err)
		}
	}
	return nil
}

// RemoveUserWorktree and DeprovisionRepo live in placement.go — they fork on
// container mode there, falling back to the bare-worker Call path unchanged.

// MergeUserBranch merges the user's branch back into the repo's base branch on
// the placement worker and returns the worker's outcome summary ("merged",
// "already merged", or "merged+pushed").
func (s *Server) MergeUserBranch(ctx context.Context, repoURL, userName, baseBranch string, push bool) (string, error) {
	repoURL = strings.TrimSpace(repoURL)
	userName = strings.TrimSpace(userName)
	if repoURL == "" {
		return "", errors.New("repo url required")
	}
	if userName == "" {
		return "", errors.New("user name required")
	}
	rec, ok := s.repos.get(repoSlugFromURL(repoURL))
	if !ok {
		return "", fmt.Errorf("repo %s has no placement — assign a user first", repoURL)
	}
	res, err := s.Call(ctx, rec.WorkerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_MergeUserBranch{MergeUserBranch: &cpv1.MergeUserBranch{
			RepoUrl:    rec.URL,
			UserName:   userName,
			BaseBranch: strings.TrimSpace(baseBranch),
			Push:       push,
		}},
	})
	if err != nil {
		return "", err
	}
	if !res.GetOk() {
		return "", fmt.Errorf("worker %q could not merge %q on %s: %s", rec.WorkerID, userName, rec.URL, res.GetError())
	}
	s.logger.Info("user branch merged", "user", userName, "repo", repoSlugFromURL(rec.URL),
		"workerID", rec.WorkerID, "summary", res.GetSummary())
	return res.GetSummary(), nil
}

// DeprovisionRepo retires a repo across the control plane. Bare mode (here)
// drives the placement worker: it removes every remaining user worktree
// (branches kept) and, when removeClone is true, deletes the clone itself.
// Container mode (placement.go) destroys every container holding the repo.
// On success the placement(s) are forgotten and any of this repo's user
// sessions still routed to the retired worker(s) are unrouted, so a stopped
// session does not resurrect a retired repo. Returns the worker's removal
// summary (bare) or the destroyed container list (container mode).
func (s *Server) DeprovisionRepo(ctx context.Context, repoURL string, removeClone bool) (string, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return "", errors.New("repo url required")
	}
	if s.incus != nil {
		names, err := s.deprovisionContainerRepo(ctx, repoURL)
		if err != nil {
			return "", err
		}
		return "destroyed: " + strings.Join(names, ", "), nil
	}
	slug := repoSlugFromURL(repoURL)
	rec, ok := s.repos.get(slug)
	if !ok {
		return "", fmt.Errorf("repo %s has no placement — assign a user first", repoURL)
	}
	res, err := s.Call(ctx, rec.WorkerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_DeprovisionRepo{DeprovisionRepo: &cpv1.DeprovisionRepo{
			RepoUrl:     rec.URL,
			RemoveClone: removeClone,
		}},
	})
	if err != nil {
		return "", err
	}
	if !res.GetOk() {
		return "", fmt.Errorf("worker %q could not deprovision %s: %s", rec.WorkerID, rec.URL, res.GetError())
	}

	s.repos.forget(slug)

	// The worker removed every user worktree under this clone, so the repo is no
	// longer assigned to anyone: drop it from each account's repository set.
	store := s.reg.Store()
	deassigned := map[string]struct{}{}
	if store != nil {
		if names, err := store.ListUsers(); err == nil {
			for _, name := range names {
				uRec, found, err := store.GetUser(name)
				if err != nil || !found {
					continue
				}
				next, removed := uRec.WithRepoRemoved(repoURL)
				if !removed {
					continue
				}
				if err := store.PutUser(name, next); err != nil {
					s.logger.Error("deprovision: drop repo from account", "user", name, "repo", slug, "err", err)
					continue
				}
				deassigned[name] = struct{}{}
			}
		}
	}

	// Unroute the deassigned users' sessions still pointed at the worker. The
	// session-user map is the only join the master keeps between a routed session
	// id and the account it was started for; it does not record which repo a
	// session runs in, so a multi-repo user with a live session in a DIFFERENT
	// repo on this same worker may also be unrouted — recoverable (the session
	// re-routes on its next start), and the common single-repo case is exact.
	s.unrouteUserSessions(rec.WorkerID, deassigned)

	s.logger.Info("repo deprovisioned", "repo", slug, "workerID", rec.WorkerID,
		"cloneDeleted", removeClone, "summary", res.GetSummary())
	return res.GetSummary(), nil
}
