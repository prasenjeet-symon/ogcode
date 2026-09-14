package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedOriginRepo creates a local "origin" repo with one committed file,
// returning its path (usable as a clone URL).
func seedOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	writeCommit(t, dir, "hello.txt", "hello world\n", "seed")
	return dir
}

func TestEnsureRepo_ClonesOnce(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})

	dir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("cloned repo missing seed file: %v", err)
	}
	if dir != filepath.Join(root, safeRepoName(origin)) {
		t.Fatalf("clone dir = %q, want under repo root %q", dir, root)
	}

	// Second call must return the same dir without re-cloning (the map hit).
	dir2, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("second EnsureRepo: %v", err)
	}
	if dir2 != dir {
		t.Fatalf("second EnsureRepo = %q, want %q", dir2, dir)
	}

	// The clone registers in managed repos (url → dir).
	repos := w.managedRepos()
	if repos[origin] != dir {
		t.Fatalf("managedRepos[%q] = %q, want %q", origin, repos[origin], dir)
	}
}

func TestEnsureRepo_ReusesCloneFromDisk(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})

	dir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	// A fresh Worker (as after a restart) finds the clone on disk instead of
	// cloning again.
	w2 := New(Options{Workspaces: []string{root}, RepoRoot: root})
	dir2, err := w2.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo after restart: %v", err)
	}
	if dir2 != dir {
		t.Fatalf("re-discovered dir = %q, want %q", dir2, dir)
	}
}

func TestEnsureRepo_EmptyURL(t *testing.T) {
	w := New(Options{RepoRoot: t.TempDir()})
	if _, err := w.EnsureRepo(context.Background(), ""); err == nil {
		t.Fatal("empty repo url should error")
	}
}

func TestEnsureUserWorktree_CreatesBranchAndDir(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	branch, dir, err := w.EnsureUserWorktree(repoDir, "Alice Cooper", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}
	if branch != "user/alice-cooper" {
		t.Fatalf("branch = %q, want %q", branch, "user/alice-cooper")
	}
	wantDir := filepath.Join(repoDir, ".ogcode", "worktrees", "user", "alice-cooper")
	if dir != wantDir {
		t.Fatalf("worktree dir = %q, want %q", dir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("worktree missing seed file: %v", err)
	}

	// The worktree checked out its branch, and the identity lives on the
	// worktree's own config. (Read the file: the test git helper injects
	// `-c user.name=t` on every call, which would override a config lookup.)
	if b := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(b) != branch {
		t.Fatalf("worktree HEAD = %q, want %q", strings.TrimSpace(b), branch)
	}
	// Worktree config is shared with the clone's config file unless
	// extensions.worktreeConfig is set; accept either location.
	var identity string
	for _, p := range []string{filepath.Join(dir, ".git", "config"), filepath.Join(repoDir, ".git", "config")} {
		if b, err := os.ReadFile(p); err == nil {
			identity += string(b)
		}
	}
	if !strings.Contains(identity, "[user]") || !strings.Contains(identity, "alice-cooper") {
		t.Fatalf("worktree identity not configured; config:\n%s", identity)
	}
}

func TestEnsureUserWorktree_Idempotent(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	branch1, dir1, err := w.EnsureUserWorktree(repoDir, "alice", "")
	if err != nil {
		t.Fatalf("first EnsureUserWorktree: %v", err)
	}
	branch2, dir2, err := w.EnsureUserWorktree(repoDir, "alice", "")
	if err != nil {
		t.Fatalf("second EnsureUserWorktree: %v", err)
	}
	if branch1 != branch2 || dir1 != dir2 {
		t.Fatalf("second call = (%q, %q), want (%q, %q)", branch2, dir2, branch1, dir1)
	}
}

// The base branch asked for at assignment time decides what the user's branch
// is cut from: as written, as origin/<name> when the clone only knows it as a
// remote branch, and an error when the clone has never heard of it. Empty
// falls back to the default-branch resolution.
func TestEnsureUserWorktree_BaseBranch(t *testing.T) {
	origin := seedOriginRepo(t)
	git(t, origin, "checkout", "--quiet", "-b", "develop")
	writeCommit(t, origin, "develop.txt", "from develop\n", "develop seed")
	git(t, origin, "checkout", "--quiet", "-")
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	// A branch the clone knows locally is used as written.
	branch, dir, err := w.EnsureUserWorktree(repoDir, "alice", "develop")
	if err != nil {
		t.Fatalf("develop base: %v", err)
	}
	if head := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(head) != branch {
		t.Fatalf("worktree HEAD = %q, want %q", strings.TrimSpace(head), branch)
	}
	if _, err := os.Stat(filepath.Join(dir, "develop.txt")); err != nil {
		t.Fatalf("worktree cut from develop missing develop's seed file: %v", err)
	}

	// An unknown base is refused — never silently fall back to the default.
	if _, _, err := w.EnsureUserWorktree(repoDir, "bob", "nonexistent"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown base: err = %v, want not-found error", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".ogcode", "worktrees", "user", "bob")); err == nil {
		t.Fatal("failed assignment must not leave bob's worktree behind")
	}

	// The session-time backstop (empty base) still cuts from the default.
	_, dirDefault, err := w.EnsureUserWorktree(repoDir, "carol", "")
	if err != nil {
		t.Fatalf("empty base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirDefault, "develop.txt")); err == nil {
		t.Fatal("empty base must cut from the default branch, not develop")
	}
	if _, err := os.Stat(filepath.Join(dirDefault, "hello.txt")); err != nil {
		t.Fatalf("default-base worktree missing the default branch's seed file: %v", err)
	}
}

func TestEnsureUserWorktree_DifferentUsersSeparateWorktrees(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	_, dirA, err := w.EnsureUserWorktree(repoDir, "alice", "")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	_, dirB, err := w.EnsureUserWorktree(repoDir, "bob", "")
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if dirA == dirB {
		t.Fatal("alice and bob must get separate worktrees")
	}
}

func TestEnsureUserWorktree_ShowsUpInDiscovery(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	branch, _, err := w.EnsureUserWorktree(repoDir, "alice", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}

	ws := discoverWorkspaces([]string{root})
	found := false
	for _, wk := range ws {
		if strings.HasSuffix(wk.GetPath(), filepath.Join(".ogcode", "worktrees", "user", "alice")) {
			if wk.GetBranch() != branch {
				t.Fatalf("discovered branch = %q, want %q", wk.GetBranch(), branch)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("user worktree not discovered under %s; got %d workspaces", root, len(ws))
	}
}

func TestEnsureRepo_ConcurrentCallersCloneOnce(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})

	const n = 4
	errs := make(chan error, n)
	dirs := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			dir, err := w.EnsureRepo(context.Background(), origin)
			errs <- err
			dirs <- dir
		}()
	}
	want := filepath.Join(root, safeRepoName(origin))
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent EnsureRepo: %v", err)
		}
		if got := <-dirs; got != want {
			t.Fatalf("concurrent EnsureRepo dir = %q, want %q", got, want)
		}
	}
}

func TestSafeRepoName(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://github.com/acme/shop.git", "github.com-acme-shop"},
		{"https://github.com/acme/shop", "github.com-acme-shop"},
		{"git@github.com:acme/shop.git", "github.com-acme-shop"},
		{"http://user:pass@host.tld:8080/r/repo.git", "host.tld-8080-r-repo"},
		{"file:///srv/git/repo.git", "srv-git-repo"},
		{"", "repo"},
		{"   ", "repo"},
	}
	for _, tc := range cases {
		if got := safeRepoName(tc.url); got != tc.want {
			t.Errorf("safeRepoName(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestSafeUserName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alice", "alice"},
		{"alice cooper", "alice-cooper"},
		{"bob_smith", "bob_smith"},
		{"  ", "user"},
		{"", "user"},
	}
	for _, tc := range cases {
		if got := safeUserName(tc.in); got != tc.want {
			t.Errorf("safeUserName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// lifecycle helpers -----------------------------------------------------------

// userWorktreeCommit commits one file inside an existing user worktree so the
// user branch has something mergeable.
func userWorktreeCommit(t *testing.T, worktreeDir, file, content, msg string) {
	t.Helper()
	writeCommit(t, worktreeDir, file, content, msg)
}

func TestRemoveUserWorktree_RemovesDirKeepsBranch(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	branch, wtDir, err := w.EnsureUserWorktree(repoDir, "Alice", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}
	userWorktreeCommit(t, wtDir, "notes.md", "user work\n", "user change")

	if err := w.RemoveUserWorktree(repoDir, "Alice"); err != nil {
		t.Fatalf("RemoveUserWorktree: %v", err)
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir %q still exists after removal (err=%v)", wtDir, err)
	}
	// The branch survives with its commit.
	if !gitHasBranch(repoDir, branch) {
		t.Fatalf("branch %s should have been kept", branch)
	}
	if tip, err := runGitOutput(repoDir, "rev-parse", branch); err != nil || tip == "" {
		t.Fatalf("branch %s not resolvable: tip=%q err=%v", branch, tip, err)
	}
	// And the worktree is no longer registered.
	if registeredWorktree(repoDir, wtDir) {
		t.Fatalf("worktree still registered after removal")
	}
}

func TestRemoveUserWorktree_UnregisteredNoOp(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	// No worktree was ever created for this user.
	if err := w.RemoveUserWorktree(repoDir, "nobody"); err != nil {
		t.Fatalf("RemoveUserWorktree on missing worktree: %v", err)
	}
}

func TestRemoveUserWorktree_UnknownUser(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if err := w.RemoveUserWorktree(repoDir, ""); err == nil {
		t.Fatal("empty user name should error")
	}
}

func TestMergeUserBranch_MergesIntoBase(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	_, wtDir, err := w.EnsureUserWorktree(repoDir, "bob", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}
	userWorktreeCommit(t, wtDir, "bob.txt", "bob's work\n", "bob change")

	summary, err := w.MergeUserBranch(repoDir, "bob", "", false)
	if err != nil {
		t.Fatalf("MergeUserBranch: %v", err)
	}
	// The origin is a local path, so the push usually succeeds — accept both
	// outcomes.
	if summary != "merged" && summary != "merged+pushed" {
		t.Fatalf("summary = %q, want \"merged\" or \"merged+pushed\"", summary)
	}
	// The merged base carries bob's change and a merge commit naming him.
	// An empty baseBranch means the clone's default branch — the local
	// branch the clone checked out, which is where the merge lands.
	base := gitBranch(repoDir)
	if base == "" || base == "HEAD" {
		t.Fatalf("clone has no checked-out branch to assert on")
	}
	log, err := runGitOutput(repoDir, "log", "--format=%s", "-1", base)
	if err != nil {
		t.Fatalf("log %s: %v", base, err)
	}
	if !strings.Contains(log, "Merge user bob") {
		t.Fatalf("merge commit message = %q, want it to mention the user", log)
	}
	if out, err := runGitOutput(repoDir, "grep", "bob's work", "--", base, "bob.txt"); err != nil {
		t.Fatalf("bob's change not on the merged base %s (out=%q): %v", base, out, err)
	}
	// The temp merge worktree is gone.
	if _, err := os.Stat(filepath.Join(repoDir, ".ogcode", "merges", "user-bob")); !os.IsNotExist(err) {
		t.Fatalf("temp merge worktree left behind: %v", err)
	}
}

func TestMergeUserBranch_AlreadyMerged(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	_, wtDir, err := w.EnsureUserWorktree(repoDir, "carol", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}
	userWorktreeCommit(t, wtDir, "carol.txt", "carol's work\n", "carol change")

	if _, err := w.MergeUserBranch(repoDir, "carol", "", false); err != nil {
		t.Fatalf("first MergeUserBranch: %v", err)
	}
	summary, err := w.MergeUserBranch(repoDir, "carol", "", false)
	if err != nil {
		t.Fatalf("second MergeUserBranch: %v", err)
	}
	if summary != "already merged" {
		t.Fatalf("second summary = %q, want \"already merged\"", summary)
	}
}

func TestMergeUserBranch_NoBranch(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if _, err := w.MergeUserBranch(repoDir, "dave", "", false); err == nil {
		t.Fatal("merge without a user branch should error")
	}
}

func TestMergeUserBranch_UnknownBase(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	_, wtDir, err := w.EnsureUserWorktree(repoDir, "erin", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}
	userWorktreeCommit(t, wtDir, "erin.txt", "erin's work\n", "erin change")

	if _, err := w.MergeUserBranch(repoDir, "erin", "no-such-branch", false); err == nil {
		t.Fatal("unknown base branch should error")
	}
}

func TestDeprovisionRepo_RemovesWorktreesAndClone(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	_, wtA, err := w.EnsureUserWorktree(repoDir, "alice", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree alice: %v", err)
	}
	_, wtB, err := w.EnsureUserWorktree(repoDir, "bob", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree bob: %v", err)
	}

	summary, err := w.DeprovisionRepo(origin, true)
	if err != nil {
		t.Fatalf("DeprovisionRepo: %v", err)
	}
	if !strings.Contains(summary, "removed 2 user worktrees") || !strings.Contains(summary, "clone deleted") {
		t.Fatalf("summary = %q, want worktree count + clone deleted", summary)
	}
	for _, d := range []string{wtA, wtB, repoDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("dir %q still exists after deprovision (err=%v)", d, err)
		}
	}
	if repos := w.managedRepos(); len(repos) != 0 {
		t.Fatalf("managedRepos after deprovision = %v, want empty", repos)
	}
	// The user branches survive the deprovisioning... on the deleted clone
	// they are gone by definition; assert the worktree dirs specifically.
}

func TestDeprovisionRepo_KeepsClone(t *testing.T) {
	origin := seedOriginRepo(t)
	root := t.TempDir()
	w := New(Options{Workspaces: []string{root}, RepoRoot: root})
	repoDir, err := w.EnsureRepo(context.Background(), origin)
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	_, wt, err := w.EnsureUserWorktree(repoDir, "frank", "")
	if err != nil {
		t.Fatalf("EnsureUserWorktree: %v", err)
	}

	summary, err := w.DeprovisionRepo(origin, false)
	if err != nil {
		t.Fatalf("DeprovisionRepo: %v", err)
	}
	if !strings.Contains(summary, "removed 1 user worktrees") || strings.Contains(summary, "clone deleted") {
		t.Fatalf("summary = %q, want 1 worktree and kept clone", summary)
	}
	if _, err := os.Stat(repoDir); err != nil {
		t.Fatalf("clone dir should have been kept: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists: %v", err)
	}
}

func TestDeprovisionRepo_UnknownRepo(t *testing.T) {
	w := New(Options{RepoRoot: t.TempDir()})
	if _, err := w.DeprovisionRepo("https://github.com/org/nope.git", true); err == nil {
		t.Fatal("deprovisioning an unmanaged repo should error")
	}
}
