package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// runGit runs a git command in dir, returning a combined-output-carrying error
// on failure. Kept local to internal/worker (do NOT import internal/git — the
// package boundary is deliberate; internal/git remains the reference impl).
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitOutput runs a git command in dir and returns its trimmed stdout.
func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitAvailable reports whether the git CLI is usable at all. Assignment (clone
// + worktree provisioning) is worker-side git work; without git the worker can
// still host pre-existing directories but cannot participate.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// repoLock serializes repo provisioning (clone + worktree creation) so two
// simultaneous StartAgents for the same repo never double-clone or race the
// branch creation. One process-wide lock is enough: the operations are rare and
// the work is on-disk anyway.
var repoLock sync.Mutex

// DefaultRepoRoot is the fallback clone home when the operator does not pass
// --repo-root. Clones must sit under a configured --workspace root for
// discoverWorkspaces to pick up their user worktrees, so operators should
// normally point both at the same directory.
func DefaultRepoRoot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".ogcode", "repos")
	}
	return ".ogcode/repos"
}

// safeRepoName slugifies a repo URL into a single filesystem-safe path segment:
// scheme, credentials, host and the .git suffix are stripped, and whatever
// remains is reduced to lowercase alphanumerics and hyphens (the same shape as
// internal/git.Slugify, kept here to avoid the cross-package import).
func safeRepoName(repoURL string) string {
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

// EnsureRepo returns the local clone directory for repoURL, cloning the repo
// on first use. One clone per repo per worker: every user of that repo works in
// a git worktree off this single checkout.
//
// The clone lands under the worker's repo root (default ~/.ogcode/repos, or the
// --repo-root flag). It only becomes session-hostable when that root is also a
// configured --workspace: workspace containment (workspaceAllowed) refuses any
// hosted directory outside the registration-time roots.
//
// Safe for concurrent callers; holds repoLock for the whole operation.
func (w *Worker) EnsureRepo(ctx context.Context, repoURL string) (string, error) {
	if !gitAvailable() {
		return "", errors.New("git is not available on this worker: hosting pre-existing directories works, but repo assignment (clone + worktree provisioning) requires git")
	}
	if repoURL == "" {
		return "", errors.New("repo url required")
	}
	root := w.repoRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create repo root %q: %w", root, err)
	}
	dir := filepath.Join(root, safeRepoName(repoURL))

	repoLock.Lock()
	defer repoLock.Unlock()
	w.seedReposLocked()

	if dir, ok := w.repos[repoURL]; ok && isGitWorkTree(dir) {
		return dir, nil
	}

	// A clone from a previous run may already be on disk (the in-memory map is
	// not persisted) — only clone when the directory is not a usable repo.
	if isGitWorkTree(dir) {
		w.rememberRepoLocked(repoURL, dir)
		return dir, nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", fmt.Errorf("create parent for %q: %w", dir, err)
	}
	cmd := exec.Command("git", "clone", repoURL, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = ctx // clone honours process-level cancellation only; ctx kept for signature symmetry
		return "", fmt.Errorf("git clone %q failed: %w (%s)", repoURL, err, strings.TrimSpace(string(out)))
	}
	w.rememberRepoLocked(repoURL, dir)
	w.logger.Info("cloned repo", "url", repoURL, "dir", dir)
	return dir, nil
}

// EnsureUserWorktree returns (branch, dir) for userName's worktree off the
// repo at repoDir, creating it on first assignment: branch user/<name>
// checked out at <repoDir>/.ogcode/worktrees/user/<name>. The branch is cut
// from baseBranch when given (as written, or resolved as origin/<name> when
// the clone only knows it as a remote branch) and from the repo's default
// branch otherwise, so every user starts from the same place. Already-existing
// branches and worktrees are tolerated (idempotent backstop for a session
// start after assignment already provisioned them).
func (w *Worker) EnsureUserWorktree(repoDir, userName, baseBranch string) (string, string, error) {
	if !gitAvailable() {
		return "", "", errors.New("git is not available on this worker: repo assignment (clone + worktree provisioning) requires git")
	}
	if repoDir == "" {
		return "", "", errors.New("repo dir required")
	}
	if userName == "" {
		return "", "", errors.New("user name required")
	}
	userName = safeUserName(userName)
	branchName := "user/" + userName
	worktreeDir := filepath.Join(repoDir, ".ogcode", "worktrees", branchName)

	repoLock.Lock()
	defer repoLock.Unlock()

	// Branch and worktree add both need a valid HEAD commit to branch from.
	if err := ensureRepoHasCommitsLocal(repoDir); err != nil {
		return "", "", fmt.Errorf("ensure repo has commits: %w", err)
	}

	// Branch from the requested base (assignment time), falling back to the
	// repo's default branch when the operator left it empty. A name that the
	// clone knows only as a remote branch resolves to its origin/ form.
	base := strings.TrimSpace(baseBranch)
	if base != "" {
		if !gitHasBranch(repoDir, base) {
			if gitHasBranch(repoDir, "origin/"+base) {
				base = "origin/" + base
			}
		}
		if !gitHasBranch(repoDir, base) {
			return "", "", fmt.Errorf("base branch %q not found in the clone — push it (or check the spelling) before assigning", strings.TrimSpace(baseBranch))
		}
	} else {
		base = defaultBranch(repoDir)
	}
	if err := runGit(repoDir, "branch", branchName, base); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", "", fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// Add the worktree checking out the branch. When it already exists
	// (assignment provisioned it earlier) the add is skipped; a stale
	// registration is pruned and the add retried once (mirrors
	// CreateTaskWorktree).
	if !registeredWorktree(repoDir, worktreeDir) {
		if err := runGit(repoDir, "worktree", "add", worktreeDir, branchName); err != nil {
			_ = runGit(repoDir, "worktree", "prune")
			if err2 := runGit(repoDir, "worktree", "add", worktreeDir, branchName); err2 != nil {
				return "", "", fmt.Errorf("worktree add %s: %w", worktreeDir, err2)
			}
		}
	}

	// Per-user commit identity lives on the worktree's own config, never global.
	_ = runGit(worktreeDir, "config", "user.name", userName)
	_ = runGit(worktreeDir, "config", "user.email", userName+"@ogcode.local")

	return branchName, worktreeDir, nil
}

// safeUserName reduces a user name to the segment used in branch and path
// names (branch "user/<name>" and worktree dir <repoDir>/.ogcode/worktrees/
// user/<name>). Same charset as safeRepoName; empty input falls back to
// "user" so the segment is never blank.
func safeUserName(name string) string {
	s := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
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

// registeredWorktree reports whether path is an existing worktree linked to
// the repo at repoDir ("git worktree list" contains it). git prints
// resolved/absolute paths, so both sides go through EvalSymlinks (on macOS
// /var resolves to /private/var; the worktree dir was joined, not resolved).
func registeredWorktree(repoDir, path string) bool {
	if abs, err := filepath.Abs(path); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			path = resolved
		} else {
			path = abs
		}
	}
	for _, wt := range gitWorktrees(repoDir) {
		if wt == path {
			return true
		}
	}
	return false
}

// defaultBranch resolves the repo's default branch (origin's HEAD), falling
// back to master then main when the remote does not advertise one.
func defaultBranch(repoDir string) string {
	for _, probe := range [][]string{
		{"symbolic-ref", "refs/remotes/origin/HEAD"},
		{"rev-parse", "--abbrev-ref", "origin/HEAD"},
	} {
		if out, err := runGitOutput(repoDir, probe...); err == nil && out != "" {
			return strings.TrimPrefix(out, "origin/")
		}
	}
	for _, name := range []string{"master", "main"} {
		if gitHasBranch(repoDir, name) {
			return name
		}
	}
	return "master"
}

// gitHasBranch reports whether the repo at repoDir can resolve name to a
// commit — a local branch, a remote-tracking branch (origin/<name>), a tag or
// a raw commit all count: anything "git branch <new> <name>" would accept.
func gitHasBranch(repoDir, name string) bool {
	return runGit(repoDir, "rev-parse", "--verify", "--quiet", name+"^{commit}") == nil
}

// isGitWorkTree reports whether dir is inside a git work tree (the EnsureRepo
// "already cloned" check).
func isGitWorkTree(dir string) bool {
	return runGit(dir, "rev-parse", "--is-inside-work-tree") == nil
}

// ensureRepoHasCommitsLocal creates an initial empty commit when the repo has
// none — branch and worktree add both need a valid HEAD. Mirrors the
// internal/git helper; kept local for the package boundary. Callers must hold
// repoLock.
func ensureRepoHasCommitsLocal(repoDir string) error {
	if out, err := runGitOutput(repoDir, "rev-list", "--count", "HEAD"); err == nil && out != "0" {
		return nil
	}
	cmd := exec.Command("git", "-c", "user.name=ogcode", "-c", "user.email=ogcode@local",
		"commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// A racing caller may have won; re-check before propagating.
		if out2, err2 := runGitOutput(repoDir, "rev-list", "--count", "HEAD"); err2 == nil && out2 != "0" {
			return nil
		}
		return fmt.Errorf("create initial commit: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// repoRoot is the clone home: the --repo-root flag when set, else ~/.ogcode/repos.
func (w *Worker) repoRoot() string {
	if w.opts.RepoRoot != "" {
		return w.opts.RepoRoot
	}
	return DefaultRepoRoot()
}

// seedReposLocked re-discovers previously cloned repos from disk (the repos
// map is intentionally not persisted). Runs at most once per process, under
// repoLock.
func (w *Worker) seedReposLocked() {
	if w.reposSeeded {
		return
	}
	w.reposSeeded = true
	root := w.repoRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if !isGitWorkTree(dir) {
			continue
		}
		if url := runGitOutputURL(dir); url != "" {
			w.rememberRepoLocked(url, dir)
		}
	}
}

// runGitOutputURL reads the clone's origin URL ("" when it has none).
func runGitOutputURL(dir string) string {
	url, err := runGitOutput(dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return url
}

// rememberRepoLocked records repoURL → cloneDir (callers hold repoLock).
func (w *Worker) rememberRepoLocked(repoURL, dir string) {
	if w.repos == nil {
		w.repos = map[string]string{}
	}
	w.repos[repoURL] = dir
}

// resolveManagedRepoDir returns the clone dir for repoURL: a managed hit, else
// the layout-derived path verified as a real git work tree (the map may have
// lost it across a restart). Fails when the worker does not manage this repo.
func (w *Worker) resolveManagedRepoDir(repoURL string) (string, error) {
	if repoURL == "" {
		return "", errors.New("repo url required")
	}
	for url, dir := range w.managedRepos() {
		if url == repoURL {
			return dir, nil
		}
	}
	dir := filepath.Join(w.repoRoot(), safeRepoName(repoURL))
	if !isGitWorkTree(dir) {
		return "", fmt.Errorf("repo %q is not managed here", repoURL)
	}
	return dir, nil
}

// managedRepos returns the worker's clones (repoURL → cloneDir), including any
// re-discovered from disk on first call.
func (w *Worker) managedRepos() map[string]string {
	repoLock.Lock()
	defer repoLock.Unlock()
	w.seedReposLocked()
	out := make(map[string]string, len(w.repos))
	for k, v := range w.repos {
		out[k] = v
	}
	return out
}

// localBranchFor maps a branch expression to its local branch in the clone:
// "origin/<name>" or "refs/remotes/origin/<name>" → "<name>" when that local
// branch exists, and any already-local name passes through. ok=false when
// there is no local equivalent. Callers hold repoLock.
func localBranchFor(repoDir, base string) (string, bool) {
	if !strings.Contains(base, "/") {
		return base, gitHasBranch(repoDir, base)
	}
	local := strings.TrimPrefix(strings.TrimPrefix(base, "refs/remotes/"), "origin/")
	if local == base || !gitHasBranch(repoDir, local) {
		return "", false
	}
	return local, true
}

// RemoveUserWorktree removes userName's worktree from the repo clone at
// repoDir, keeping the user's branch (the work survives on user/<slug> and can
// be merged later or re-checked out). A session still hosted in the worktree
// refuses the removal; a repo with no such worktree is a successful no-op.
func (w *Worker) RemoveUserWorktree(repoDir, userName string) error {
	if !gitAvailable() {
		return errors.New("git is not available on this worker: worktree removal requires git")
	}
	if repoDir == "" {
		return errors.New("repo dir required")
	}
	if userName == "" {
		return errors.New("user name required")
	}
	slug := safeUserName(userName)
	worktreeDir := userWorktreeDir(repoDir, slug)

	repoLock.Lock()
	defer repoLock.Unlock()

	if id, ok := w.sessionInWorktreeDir(worktreeDir); ok {
		return fmt.Errorf("user worktree %s still hosts session %s — stop it before removing", worktreeDir, id)
	}
	if !registeredWorktree(repoDir, worktreeDir) {
		return nil
	}
	removeWorktreeDir(repoDir, worktreeDir)
	w.logger.Info("removed user worktree", "repo", repoDir, "user", userName, "branch kept", "user/"+slug)
	return nil
}

// MergeUserBranch merges the user/<slug> branch back into the repo's base
// branch and returns what happened ("merged", "already merged", or
// "merged+pushed"). The merge runs in a temporary worktree on the base branch
// (a mirror of internal/git's MergeTaskBranch, never disturbing the checked-
// out user worktrees), and a pushable origin receives the updated base
// best-effort — a failed push does not fail the merge.
func (w *Worker) MergeUserBranch(repoDir, userName, baseBranch string, push bool) (string, error) {
	if !gitAvailable() {
		return "", errors.New("git is not available on this worker: merging requires git")
	}
	if repoDir == "" {
		return "", errors.New("repo dir required")
	}
	if userName == "" {
		return "", errors.New("user name required")
	}
	slug := safeUserName(userName)
	branch := "user/" + slug

	repoLock.Lock()
	defer repoLock.Unlock()

	if err := ensureRepoHasCommitsLocal(repoDir); err != nil {
		return "", fmt.Errorf("ensure repo has commits: %w", err)
	}
	if !gitHasBranch(repoDir, branch) {
		return "", fmt.Errorf("branch %q not found in the clone — nothing to merge", branch)
	}

	// Resolve the base: the operator's branch (origin/<name> fallback, like
	// EnsureUserWorktree) or the repo's default branch when empty. The merge
	// target must be a LOCAL branch — merging into a remote-tracking ref
	// happens on a detached HEAD and the merge commit is orphaned the moment
	// the temporary worktree is removed.
	base := strings.TrimSpace(baseBranch)
	if base != "" {
		if !gitHasBranch(repoDir, base) {
			if gitHasBranch(repoDir, "origin/"+base) {
				base = "origin/" + base
			}
		}
	} else {
		base = defaultBranch(repoDir)
	}
	// Normalize a remote-tracking base to its local branch when the clone has
	// one (a fresh clone checks out the remote HEAD as a local branch), or
	// create the local branch at the remote-tracking tip when it is missing.
	if local, ok := localBranchFor(repoDir, base); ok {
		base = local
	} else {
		local := strings.TrimPrefix(strings.TrimPrefix(base, "refs/remotes/origin/"), "origin/")
		if local != "" && local != base {
			if err := runGit(repoDir, "branch", local, base); err != nil && !strings.Contains(err.Error(), "already exists") {
				return "", fmt.Errorf("create local branch %s from %s: %w", local, base, err)
			}
			base = local
		}
	}
	if !gitHasBranch(repoDir, base) {
		return "", fmt.Errorf("base branch %q not found in the clone", strings.TrimSpace(baseBranch))
	}

	// Already-merged detection: the branch tip sits on the base's history.
	if tip, err := runGitOutput(repoDir, "rev-parse", branch); err == nil {
		if mb, err := runGitOutput(repoDir, "merge-base", base, branch); err == nil && mb == tip {
			return "already merged", nil
		}
	}

	// When the base is the clone's checked-out branch, merge in place — a
	// temporary worktree would be refused ("already checked out") and the
	// in-place merge is the same thing.
	if checkedOut := gitBranch(repoDir); checkedOut == base {
		msg := fmt.Sprintf("Merge user %s", slug)
		if err := runGit(repoDir, "merge", "--no-ff", "-m", msg, branch); err != nil {
			_ = runGit(repoDir, "merge", "--abort")
			return "", fmt.Errorf("merge %s into %s: %w", branch, base, err)
		}
		return w.finishMerge(repoDir, base, push), nil
	}

	// Otherwise merge in a temporary worktree on the base branch so no checked-out
	// worktree is touched.
	tmpDir := filepath.Join(repoDir, ".ogcode", "merges", "user-"+slug)
	if err := os.MkdirAll(filepath.Dir(tmpDir), 0o755); err != nil {
		return "", fmt.Errorf("prepare user merge dir: %w", err)
	}
	if err := runGit(repoDir, "worktree", "add", tmpDir, base); err != nil {
		_ = runGit(repoDir, "worktree", "prune")
		if err2 := runGit(repoDir, "worktree", "add", tmpDir, base); err2 != nil {
			return "", fmt.Errorf("add user merge worktree: %w", err2)
		}
	}
	defer func() {
		if err := runGit(repoDir, "worktree", "remove", "--force", tmpDir); err != nil {
			_ = os.RemoveAll(tmpDir)
		}
		_ = runGit(repoDir, "worktree", "prune")
	}()

	msg := fmt.Sprintf("Merge user %s", slug)
	if err := runGit(tmpDir, "merge", "--no-ff", "-m", msg, branch); err != nil {
		_ = runGit(tmpDir, "merge", "--abort")
		return "", fmt.Errorf("merge %s into %s: %w", branch, base, err)
	}
	return w.finishMerge(repoDir, base, push), nil
}

// finishMerge reports the summary, pushing the updated base to origin
// best-effort when requested — a failed push (no pushable origin, rejected
// non-ff, auth) does not fail the merge: the result lives on the local base.
// Callers hold repoLock.
func (w *Worker) finishMerge(repoDir, base string, push bool) string {
	summary := "merged"
	if push {
		if url, err := runGitOutput(repoDir, "remote", "get-url", "origin"); err == nil && url != "" {
			if err := runGit(repoDir, "push", "origin", base); err == nil {
				summary = "merged+pushed"
			}
		}
	}
	return summary
}

// DeprovisionRepo retires the managed repo repoURL: every remaining user
// worktree is removed (branches kept), and when removeClone is true the clone
// directory itself is deleted and the clone is forgotten from the repos map.
// Any hosted session inside a worktree about to be removed fails the whole
// command without removing anything. Returns a human summary of what was
// removed.
func (w *Worker) DeprovisionRepo(repoURL string, removeClone bool) (string, error) {
	if !gitAvailable() {
		return "", errors.New("git is not available on this worker: repo deprovisioning requires git")
	}
	if repoURL == "" {
		return "", errors.New("repo url required")
	}

	// Resolve the clone dir: a managed hit, else the layout-derived path (the
	// clone exists on disk but the map lost it, e.g. after a restart) —
	// verified as a real git work tree before anything is removed.
	repoLock.Lock()
	defer repoLock.Unlock()
	w.seedReposLocked()
	dir, ok := w.repos[repoURL]
	if !ok {
		dir = filepath.Join(w.repoRoot(), safeRepoName(repoURL))
		if !isGitWorkTree(dir) {
			return "", fmt.Errorf("repo %q is not managed here", repoURL)
		}
	}

	// Remove every user worktree first; refuse as a whole when a session is
	// still hosted in one (the refusals must not leave a half-deprovisioned
	// repo). git prints resolved paths, so both sides go through
	// EvalSymlinks (on macOS /var resolves to /private/var) before the
	// prefix compare — the joined path may not be resolved.
	userDir := filepath.Join(dir, ".ogcode", "worktrees", "user")
	if resolved, err := filepath.EvalSymlinks(userDir); err == nil {
		userDir = resolved
	}
	removed := 0
	for _, wt := range gitWorktrees(dir) {
		if !strings.HasPrefix(wt, userDir+string(filepath.Separator)) {
			continue
		}
		if id, ok := w.sessionInWorktreeDir(wt); ok {
			return "", fmt.Errorf("user worktree %s still hosts session %s — stop it before deprovisioning", wt, id)
		}
		removeWorktreeDir(dir, wt)
		removed++
	}

	summary := fmt.Sprintf("removed %d user worktrees", removed)
	if removeClone {
		_ = os.RemoveAll(dir)
		delete(w.repos, repoURL)
		summary += "; clone deleted"
		w.logger.Info("deprovisioned repo", "url", repoURL, "dir", dir, "worktrees removed", removed, "clone deleted", true)
	} else {
		w.logger.Info("deprovisioned repo", "url", repoURL, "dir", dir, "worktrees removed", removed, "clone deleted", false)
	}
	return summary, nil
}

// userWorktreeDir is the on-disk location of userName's worktree under the
// repo clone at repoDir; the inverse of EnsureUserWorktree's path layout.
func userWorktreeDir(repoDir, slug string) string {
	return filepath.Join(repoDir, ".ogcode", "worktrees", "user", slug)
}

// sessionInWorktreeDir reports whether any hosted session is running in dir
// (the removal refusals). Symlink resolution mirrors registeredWorktree: the
// session registry stores the dir as joined, git prints it resolved.
func (w *Worker) sessionInWorktreeDir(worktreeDir string) (string, bool) {
	if w.sessions == nil {
		return "", false
	}
	if id, ok := w.sessions.sessionInDir(worktreeDir); ok {
		return id, true
	}
	if resolved, err := filepath.EvalSymlinks(worktreeDir); err == nil && resolved != worktreeDir {
		return w.sessions.sessionInDir(resolved)
	}
	return "", false
}

// removeWorktreeDir removes the linked worktree at worktreeDir of the repo
// clone at repoDir: `git worktree remove --force` first, falling back to
// deleting the directory and pruning the stale registration. Empty parent
// directories left behind are cleaned up (mirrors internal/git's
// removeWorktreeDir; kept local for the package boundary). Callers hold
// repoLock.
func removeWorktreeDir(repoDir, worktreeDir string) {
	if err := runGit(repoDir, "worktree", "remove", worktreeDir, "--force"); err != nil {
		_ = os.RemoveAll(worktreeDir)
		_ = runGit(repoDir, "worktree", "prune")
	}
	_ = os.Remove(filepath.Dir(worktreeDir))
}
