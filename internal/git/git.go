package git

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TaskWorktree holds the result of creating a worktree for a task.
type TaskWorktree struct {
	BranchName string
	Path       string // absolute path to the worktree directory
}

// CreateTaskWorktree creates a git worktree for a task. Each worktree is an
// independent checkout on its own branch, so multiple agents can work in parallel
// without interfering with each other. The worktree lives under <repo>/.ogcode/worktrees/<branchName>.
// If baseBranch is non-empty the new branch is created from that branch instead of HEAD,
// enabling stacked PRs for dependent tasks.
func CreateTaskWorktree(repoDir string, taskID string, slug string, baseBranch string) (*TaskWorktree, error) {
	branchName := fmt.Sprintf("task/%s-%s", taskID, slug)
	worktreeDir := filepath.Join(repoDir, ".ogcode", "worktrees", branchName)

	// Ensure the worktrees parent dir exists
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		return nil, fmt.Errorf("create worktree parent dir: %w", err)
	}

	// Ensure the repo has at least one commit, otherwise git branch from HEAD fails
	if err := ensureRepoHasCommits(repoDir); err != nil {
		return nil, fmt.Errorf("ensure repo has commits: %w", err)
	}

	// Create branch from baseBranch (stacked) or current HEAD (independent).
	var branchErr error
	if baseBranch != "" {
		branchErr = runGit(repoDir, "branch", branchName, baseBranch)
	} else {
		branchErr = runGit(repoDir, "branch", branchName)
	}
	if branchErr != nil {
		// Ignore "already exists" — that's harmless. All other errors propagate.
		if !strings.Contains(branchErr.Error(), "already exists") {
			return nil, fmt.Errorf("create branch %s: %w", branchName, branchErr)
		}
	}

	// Add worktree at the target path, checking out the branch
	if err := runGit(repoDir, "worktree", "add", worktreeDir, branchName); err != nil {
		// Worktree might already exist — try pruning stale ones first
		_ = runGit(repoDir, "worktree", "prune")
		if err2 := runGit(repoDir, "worktree", "add", worktreeDir, branchName); err2 != nil {
			return nil, fmt.Errorf("worktree add %s: %w", worktreeDir, err2)
		}
	}

	// Configure local git identity so the agent can commit without global git config.
	_ = runGit(worktreeDir, "config", "user.name", "ogcode")
	_ = runGit(worktreeDir, "config", "user.email", "ogcode@local")

	return &TaskWorktree{BranchName: branchName, Path: worktreeDir}, nil
}

// RemoveTaskWorktree removes a git worktree and its local branch.
func RemoveTaskWorktree(repoDir string, branchName string) error {
	removeWorktreeDir(repoDir, branchName)
	_ = runGit(repoDir, "branch", "-D", branchName)
	return nil
}

// DeleteBranch deletes a local branch. Returns nil if the branch does not exist.
func DeleteBranch(repoDir, branchName string) error {
	err := runGit(repoDir, "branch", "-D", branchName)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

// EnsureLocalBranch makes sure branchName exists as a local git ref.
// If the branch already exists locally it is a no-op; otherwise it attempts
// to fetch it from origin. Returns an error only when neither works.
func EnsureLocalBranch(repoDir, branchName string) error {
	if err := runGit(repoDir, "rev-parse", "--verify", branchName); err == nil {
		return nil
	}
	return runGit(repoDir, "fetch", "origin", branchName+":"+branchName)
}

// CreateChainBranch creates the shared branch for a dependency chain, branching
// from the current HEAD. It is a no-op when the branch already exists.
func CreateChainBranch(repoDir, chainBranch string) error {
	err := runGit(repoDir, "branch", chainBranch)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// MergeTaskBranch merges a completed task branch into the shared chain branch.
// A temporary worktree is created on the chain branch to perform the merge,
// then removed. The chain branch itself is kept so subsequent tasks and the
// final chain PR can use it.
func MergeTaskBranch(repoDir, chainBranch, taskBranch, taskTitle string) error {
	tmpDir := filepath.Join(repoDir, ".ogcode", "chain-merges", strings.ReplaceAll(chainBranch, "/", "-"))
	if err := os.MkdirAll(filepath.Dir(tmpDir), 0o755); err != nil {
		return fmt.Errorf("prepare chain merge dir: %w", err)
	}

	if err := runGit(repoDir, "worktree", "add", tmpDir, chainBranch); err != nil {
		_ = runGit(repoDir, "worktree", "prune")
		if err2 := runGit(repoDir, "worktree", "add", tmpDir, chainBranch); err2 != nil {
			return fmt.Errorf("add chain merge worktree: %w", err2)
		}
	}
	defer func() {
		if err := runGit(repoDir, "worktree", "remove", "--force", tmpDir); err != nil {
			_ = os.RemoveAll(tmpDir)
		}
		_ = runGit(repoDir, "worktree", "prune")
	}()

	msg := fmt.Sprintf("Merge task: %s", taskTitle)
	if err := runGit(tmpDir, "merge", "--no-ff", "-m", msg, taskBranch); err != nil {
		_ = runGit(tmpDir, "merge", "--abort")
		return fmt.Errorf("merge %s into chain branch: %w", taskBranch, err)
	}
	return nil
}

// RemoveTaskWorktreeKeepBranch removes the worktree directory but keeps the
// branch intact. Use this when there is no remote to push to, so the work
// remains accessible via the branch ref.
func RemoveTaskWorktreeKeepBranch(repoDir string, branchName string) error {
	removeWorktreeDir(repoDir, branchName)
	return nil
}

func removeWorktreeDir(repoDir string, branchName string) {
	worktreeDir := filepath.Join(repoDir, ".ogcode", "worktrees", branchName)
	if err := runGit(repoDir, "worktree", "remove", worktreeDir, "--force"); err != nil {
		_ = os.RemoveAll(worktreeDir)
		_ = runGit(repoDir, "worktree", "prune")
	}
	// Clean up any empty parent directories left by the branch name's path
	// separator (e.g., ".ogcode/worktrees/task/" after all task/ worktrees
	// are removed). Ignore errors — the directory may not be empty yet.
	_ = os.Remove(filepath.Dir(worktreeDir))
}

// PullRequest holds the result of creating a pull request.
type PullRequest struct {
	URL    string
	Number int
}

// CommitAllChanges stages all changes in worktreeDir and commits them.
// If there is nothing to commit it is a no-op. The commit uses a local
// identity override so it succeeds even when no global git config exists.
func CommitAllChanges(worktreeDir, commitMsg string) error {
	out, err := runGitOutput(worktreeDir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	if err := runGit(worktreeDir, "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	cmd := exec.Command("git",
		"-c", "user.name=ogcode",
		"-c", "user.email=ogcode@local",
		"commit", "-m", commitMsg)
	cmd.Dir = worktreeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return nil
}

// PushBranch pushes branchName to origin using ctx for timeout/cancellation.
// Returns false (with nil error) when no remote is configured.
func PushBranch(ctx context.Context, repoDir, branchName string) (bool, error) {
	remote, err := runGitOutput(repoDir, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(remote) == "" {
		return false, nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", branchName).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("push %s: %s: %w", branchName, string(out), err)
	}
	return true, nil
}

// CreatePR creates a pull request via the gh CLI using ctx for timeout/cancellation.
// It is idempotent: if an open PR already exists for branchName it is returned as-is.
// If baseBranch no longer exists on the remote (e.g. stacked PR was merged and branch
// deleted), it falls back to the repo's default branch automatically.
func CreatePR(ctx context.Context, repoDir, branchName, title, body, baseBranch string) (*PullRequest, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found; install from https://cli.github.com to enable automatic PR creation")
	}

	// Return the existing PR instead of failing when one is already open.
	if existing, err := findExistingPR(ctx, repoDir, branchName); err == nil && existing != nil {
		slog.Info("PR already exists for branch, reusing", "branch", branchName, "pr", existing.URL)
		return existing, nil
	}

	// Verify the requested base branch still exists on the remote before using it.
	// A stacked PR's base is the dependency's branch; if that was merged and deleted
	// we fall back to the repo default so the PR is still created correctly.
	if baseBranch != "" && !remoteRefExists(ctx, repoDir, baseBranch) {
		slog.Warn("stacked PR base branch not found on remote, falling back to default",
			"base", baseBranch)
		baseBranch = ""
	}
	if baseBranch == "" {
		baseBranch = detectDefaultBranch(ctx, repoDir)
	}

	args := []string{
		"pr", "create",
		"--title", title,
		"--body", body,
		"--head", branchName,
	}
	if baseBranch != "" {
		args = append(args, "--base", baseBranch)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr create: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Try to parse the output as the PR URL (old gh versions output just the URL)
	prURL := strings.TrimSpace(string(out))
	if prURL == "" {
		// Fallback: use gh pr view to get the URL (no --json flag for older gh versions)
		viewOut, _ := runGhOutput(ctx, repoDir, "pr", "view", branchName)
		prURL = extractPRURLFromViewOutput(viewOut)
	}

	// Try to extract PR number from URL
	prNumber := 0
	if prURL != "" {
		parts := strings.Split(prURL, "/")
		for i, part := range parts {
			if part == "pull" && i+1 < len(parts) {
				if n, err := fmt.Sscanf(parts[i+1], "%d", &prNumber); n == 1 && err == nil {
					break
				}
			}
		}
	}

	return &PullRequest{URL: prURL, Number: prNumber}, nil
}

// extractPRURLFromViewOutput extracts the PR URL from gh pr view output.
// Older gh versions output plain text URLs, newer versions output JSON.
func extractPRURLFromViewOutput(output string) string {
	// Look for a line that looks like a PR URL
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		// Trim whitespace and look for URL pattern
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://github.com/") {
			// Remove any query parameters
			if idx := strings.Index(line, "?"); idx != -1 {
				line = line[:idx]
			}
			return line
		}
	}
	// If it's just a single line URL, use it
	if strings.HasPrefix(output, "https://github.com/") {
		return strings.TrimSpace(output)
	}
	return ""
}

// findExistingPR returns an open PR for the given head branch, or nil if none exists.
func findExistingPR(ctx context.Context, repoDir, branchName string) (*PullRequest, error) {
	// Try gh pr view first - works if PR already exists for this branch
	viewOut, _ := runGhOutput(ctx, repoDir, "pr", "view", branchName)
	if viewOut != "" {
		url := extractPRURLFromViewOutput(viewOut)
		if url != "" {
			return parsePRFromURL(url), nil
		}
	}
	return nil, fmt.Errorf("no existing PR")
}

// parsePRFromURL creates a PullRequest from a GitHub PR URL.
func parsePRFromURL(url string) *PullRequest {
	pr := &PullRequest{URL: url}
	if url == "" {
		return pr
	}
	parts := strings.Split(url, "/")
	for i, part := range parts {
		if part == "pull" && i+1 < len(parts) {
			if n, err := fmt.Sscanf(parts[i+1], "%d", &pr.Number); n != 1 || err != nil {
				pr.Number = 0
			}
			break
		}
	}
	return pr
}

// remoteRefExists reports whether branchName exists as a branch on origin.
func remoteRefExists(ctx context.Context, repoDir, branchName string) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir,
		"ls-remote", "--heads", "origin", branchName).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// EnsureBranchOnRemote pushes branchName to origin if it is not already present
// there, so it can be used as a PR base. It is a no-op when branchName is empty
// or already on the remote. Returns an error only when the push itself fails.
func EnsureBranchOnRemote(ctx context.Context, repoDir, branchName string) error {
	if branchName == "" {
		return nil
	}
	if remoteRefExists(ctx, repoDir, branchName) {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", branchName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("push base branch %s: %s: %w", branchName, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// detectDefaultBranch returns the default branch of the remote repository.
// It checks the local origin/HEAD symref first (fast, no network), then
// falls back to probing origin for "main" and "master".
func detectDefaultBranch(ctx context.Context, repoDir string) string {
	out, err := runGitOutput(repoDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		ref := strings.TrimSpace(out)
		const prefix = "refs/remotes/origin/"
		if strings.HasPrefix(ref, prefix) {
			return ref[len(prefix):]
		}
	}
	for _, name := range []string{"main", "master"} {
		if remoteRefExists(ctx, repoDir, name) {
			return name
		}
	}
	return ""
}

// GetCurrentBranch returns the current git branch name for the given directory.
func GetCurrentBranch(dir string) string {
	out, err := runGitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// SyncStatus describes how the current branch relates to its upstream.
type SyncStatus struct {
	IsRepo      bool   `json:"isRepo"`      // false when dir is not a git repo
	Branch      string `json:"branch"`      // current branch ("" / "HEAD" when detached)
	HasUpstream bool   `json:"hasUpstream"` // whether the branch tracks a remote branch
	Upstream    string `json:"upstream"`    // e.g. "origin/main"
	Ahead       int    `json:"ahead"`       // local commits not on upstream
	Behind      int    `json:"behind"`      // upstream commits not in local
	Fetched     bool   `json:"fetched"`     // whether the best-effort fetch succeeded
	FetchError  string `json:"fetchError,omitempty"`
}

// BranchSyncStatus reports whether the current branch is in sync with its
// upstream. It first does a best-effort, time-bounded `git fetch` of the
// branch's remote so the ahead/behind counts reflect the real remote state; if
// the fetch fails or times out (offline, auth, no remote) it falls back to the
// last-known remote-tracking ref and records the reason in FetchError.
func BranchSyncStatus(ctx context.Context, repoDir string) (*SyncStatus, error) {
	st := &SyncStatus{}

	// Not a git repo → nothing to report.
	if err := runGit(repoDir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return st, nil
	}
	st.IsRepo = true
	st.Branch = GetCurrentBranch(repoDir)

	// Resolve the upstream tracking branch (errors when none is configured or HEAD is detached).
	up, err := runGitOutput(repoDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil || strings.TrimSpace(up) == "" {
		st.HasUpstream = false
		return st, nil
	}
	st.HasUpstream = true
	st.Upstream = strings.TrimSpace(up)

	// Best-effort fetch of the current branch's remote so the counts are fresh.
	remote := "origin"
	if i := strings.Index(st.Upstream, "/"); i > 0 {
		remote = st.Upstream[:i]
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	args := []string{"-C", repoDir, "fetch", "--quiet", remote}
	if st.Branch != "" && st.Branch != "HEAD" {
		args = append(args, st.Branch)
	}
	if out, ferr := exec.CommandContext(fetchCtx, "git", args...).CombinedOutput(); ferr != nil {
		st.Fetched = false
		st.FetchError = strings.TrimSpace(string(out))
		if st.FetchError == "" {
			st.FetchError = ferr.Error()
		}
	} else {
		st.Fetched = true
	}

	// behind = commits on upstream not in HEAD; ahead = commits in HEAD not on upstream.
	if counts, err := runGitOutput(repoDir, "rev-list", "--left-right", "--count", st.Upstream+"...HEAD"); err == nil {
		fields := strings.Fields(strings.TrimSpace(counts))
		if len(fields) == 2 {
			st.Behind, _ = strconv.Atoi(fields[0])
			st.Ahead, _ = strconv.Atoi(fields[1])
		}
	}
	return st, nil
}

// Slugify converts a task title into a URL-safe slug.
func Slugify(title string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			b.WriteRune(ch)
		}
	}
	result := b.String()
	result = strings.Trim(result, "-")
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	if len(result) > 40 {
		result = result[:40]
		result = strings.TrimRight(result, "-")
	}
	if result == "" {
		result = "task"
	}
	return result
}

// ensureRepoHasCommits checks whether the repo has any commits, and creates an
// initial empty commit if not. This is required because git branch and git
// worktree add both need a valid HEAD commit to branch from.
// Callers must hold their git serialization lock before calling this function
// so that concurrent goroutines do not both attempt to create the first commit.
func ensureRepoHasCommits(repoDir string) error {
	out, err := runGitOutput(repoDir, "rev-list", "--count", "HEAD")
	if err == nil && strings.TrimSpace(out) != "0" {
		return nil
	}
	cmd := exec.Command("git", "-c", "user.name=ogcode", "-c", "user.email=ogcode@local",
		"commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// A concurrent goroutine may have won the race and already created the
		// commit. Re-check before propagating the error.
		if out2, err2 := runGitOutput(repoDir, "rev-list", "--count", "HEAD"); err2 == nil && strings.TrimSpace(out2) != "0" {
			return nil
		}
		return fmt.Errorf("create initial commit: %s: %w", string(out), err)
	}
	return nil
}

// FileStatus is one entry from `git status --porcelain`.
type FileStatus struct {
	Path   string `json:"path"`   // workspace-relative
	X      string `json:"x"`      // index/staged state code, e.g. "M", "A", " ", "?"
	Y      string `json:"y"`      // worktree state code
	Staged bool   `json:"staged"` // true when X is non-blank and not "?"
}

// Commit is one entry from `git log --format=...`.
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Message string `json:"message"` // subject line only
	Author  string `json:"author"`
	Time    string `json:"time"` // ISO 8601
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	return runGit(dir, "rev-parse", "--is-inside-work-tree") == nil
}

// Status returns the working-tree status of dir as a list of FileStatus entries
// (one per changed file). Returns nil, nil when the tree is clean. Returns
// nil, nil (not an error) when dir is not a git repository.
func Status(dir string) ([]FileStatus, error) {
	out, err := runGitOutput(dir, "status", "--porcelain", "-z")
	if err != nil {
		// Not a git repo, or some other benign failure: treat as clean.
		return nil, nil
	}
	return parsePorcelain(out), nil
}

// parsePorcelain parses the NUL-separated output of `git status --porcelain -z`
// into a list of FileStatus entries. Each record is "XY path". For renames and
// copies (X == "R" or "C"), -z mode emits a second NUL-delimited field holding
// the destination path; we keep the destination as Path.
func parsePorcelain(out string) []FileStatus {
	if out == "" {
		return nil
	}
	records := strings.Split(out, "\x00")
	var result []FileStatus
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" || len(rec) < 3 {
			continue
		}
		x := string(rec[0])
		y := string(rec[1])
		path := rec[3:]
		// Rename/copy: the next NUL-delimited field is the destination path.
		if (x == "R" || x == "C") && i+1 < len(records) && records[i+1] != "" {
			i++
			path = records[i]
		}
		result = append(result, FileStatus{
			Path:   path,
			X:      x,
			Y:      y,
			Staged: x != " " && x != "?",
		})
	}
	return result
}

// DiffFile returns the raw unified diff for path. When staged is true it diffs
// the index (cached) instead of the working tree. Returns "" when the file has
// no changes at the requested level. Uses --no-color.
//
// Untracked files (git status "??") are diffed with `git diff --no-index
// /dev/null -- path` so that a brand-new file renders as an all-addition hunk
// instead of the empty string plain `git diff` returns for untracked paths.
// Staged untracked files are handled by the normal --cached path.
func DiffFile(dir, path string, staged bool) (string, error) {
	if !staged && isUntracked(dir, path) {
		// git diff --no-index exits non-zero when the inputs differ (which is
		// always the case here, since /dev/null differs from a non-empty file),
		// so a non-nil error is expected and not a failure.
		out, _ := runGitOutput(dir, "diff", "--no-color", "--no-index", "/dev/null", "--", path)
		return out, nil
	}
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return runGitOutput(dir, args...)
}

// isUntracked reports whether path is an untracked file in dir's working tree
// (git status code "??"). It returns false when path is tracked, staged, or
// when the status lookup fails for any reason.
func isUntracked(dir, path string) bool {
	out, err := runGitOutput(dir, "status", "--porcelain", "-z", "--", path)
	if err != nil || out == "" {
		return false
	}
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 3 {
			continue
		}
		if rec[3:] == path && rec[0] == '?' && rec[1] == '?' {
			return true
		}
	}
	return false
}

// ShowCommit returns the full unified diff for the given commit (raw text from
// `git show --no-color`).
func ShowCommit(dir, sha string) (string, error) {
	return runGitOutput(dir, "show", "--no-color", sha)
}

// RecentCommits returns the last n commits (default 20 when n <= 0) as Commit
// entries. Returns nil, nil when dir is not a git repository.
func RecentCommits(dir string, n int) ([]Commit, error) {
	if n <= 0 {
		n = 20
	}
	out, err := runGitOutput(dir, "log", "-n", strconv.Itoa(n),
		"--format=%H%x01%h%x01%s%x01%an%x01%aI%x00")
	if err != nil {
		return nil, nil
	}
	return parseLog(out), nil
}

// parseLog parses the output of `git log --format=...%x00` (fields separated by
// 0x01, commits separated by 0x00) into a list of Commit entries.
func parseLog(out string) []Commit {
	out = strings.TrimRight(out, "\x00")
	if out == "" {
		return nil
	}
	var result []Commit
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x01")
		if len(fields) < 5 {
			continue
		}
		result = append(result, Commit{
			SHA:     fields[0],
			Short:   fields[1],
			Message: fields[2],
			Author:  fields[3],
			Time:    fields[4],
		})
	}
	return result
}

// Stage adds the given paths to the index (git add -- <paths...>). It uses
// explicit paths only — never -A — so the caller controls scope. Empty paths
// is a no-op.
func Stage(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	return runGit(dir, args...)
}

// Unstage removes the given paths from the index (git reset HEAD -- <paths...>).
// Empty paths is a no-op.
func Unstage(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"reset", "HEAD", "--"}, paths...)
	return runGit(dir, args...)
}

// HasStaged reports whether the index has any staged changes.
func HasStaged(dir string) bool {
	out, err := runGitOutput(dir, "diff", "--cached", "--name-only")
	return err == nil && strings.TrimSpace(out) != ""
}

// CommitStaged commits the currently staged changes with the given message. It
// uses a local identity override so it works in repos without user.name/
// user.email configured. Returns an error when nothing is staged.
func CommitStaged(dir, msg string) error {
	if !HasStaged(dir) {
		return fmt.Errorf("Nothing staged to commit.")
	}
	cmd := exec.Command("git", "-c", "user.name=ogcode", "-c", "user.email=ogcode@local",
		"commit", "-m", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return nil
}

func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func runGhOutput(ctx context.Context, repoDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	return string(out), err
}
