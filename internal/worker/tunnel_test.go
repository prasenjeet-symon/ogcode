package worker

import (
	"strings"
	"testing"
)

// TestWorktreeLabel pins the DNS-safe route label contract: lowercase, runs of
// non-alphanumerics collapse to a single hyphen, no leading/trailing hyphen,
// capped at 63 bytes, and never empty (a label-less dir collapses to "w").
func TestWorktreeLabel(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"/repo/worktrees/feature-x", "feature-x"},
		{"/repo/worktrees/Feature_X v2", "feature-x-v2"},
		{"/repo/worktrees/.hidden", "hidden"},
		{"/repo/worktrees/...", "w"},
		{"/repo/worktrees/", "worktrees"},
		{"/repo/worktrees", "worktrees"},
		{"/repo/worktrees/---", "w"},
		{"/repo/worktrees/bug-fix..and-more", "bug-fix-and-more"},
		{"/repo/worktrees/..", "repo"}, // Clean resolves ".." to the parent
		{"", "w"},                      // Clean("") = "." → sanitizes to nothing
	}
	for _, tc := range cases {
		if got := worktreeLabel(tc.dir); got != tc.want {
			t.Errorf("worktreeLabel(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}

	// A long dir name is truncated to 63 bytes (RFC 1123 label cap).
	long := worktreeLabel("/repo/wt/" + string(makeName(80)))
	if len(long) != 63 {
		t.Errorf("long label len = %d, want 63", len(long))
	}
}

func makeName(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return b
}

// TestRouteLabel pins the repo-qualified route contract: a user worktree
// ("<repoDir>/.ogcode/worktrees/user/<name>") yields "<reposlug>-<name>" so two
// repos for one user never collide on the same worker, while every other path
// keeps the plain worktreeLabel base name unchanged.
func TestRouteLabel(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		// User worktrees: qualified with the repo dir's base name.
		{"/srv/ogcode/repos/github-com-acme-shop/.ogcode/worktrees/user/bob", "github-com-acme-shop-bob"},
		{"/srv/ogcode/repos/other-repo/.ogcode/worktrees/user/bob", "other-repo-bob"},
		{"/srv/ogcode/repos/My_Repo/.ogcode/worktrees/user/Alice Cooper", "my-repo-alice-cooper"},
		// Non-user paths are untouched (same as worktreeLabel).
		{"/repo/worktrees/feature-x", "feature-x"},
		{"/srv/ogcode/repos/github-com-acme-shop", "github-com-acme-shop"},
		{"/srv/ws/alpha", "alpha"},
		// Not the user shape (missing the "user" segment) → base name only.
		{"/repo/.ogcode/worktrees/task/t1", "t1"},
	}
	for _, tc := range cases {
		if got := routeLabel(tc.dir); got != tc.want {
			t.Errorf("routeLabel(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}

	// The composite is capped at 63 bytes, trimming the repo segment first so
	// the user segment survives intact.
	longRepo := string(makeName(80))
	dir := "/srv/repos/" + longRepo + "/.ogcode/worktrees/user/bob"
	got := routeLabel(dir)
	if len(got) > 63 {
		t.Errorf("routeLabel len = %d, want <= 63", len(got))
	}
	if !strings.HasSuffix(got, "-bob") {
		t.Errorf("routeLabel(%q) = %q, want it to keep the user segment (…-bob)", dir, got)
	}
}
