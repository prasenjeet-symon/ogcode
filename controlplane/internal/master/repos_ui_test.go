package master_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
)

// pairingToken mints a short-lived worker pairing token for direct registry
// Add calls in tests.
func pairingToken(now time.Time) pairing.Token {
	tok, _ := pairing.New(testSecret, time.Minute).Mint(now)
	return tok
}

// TestReposPage_FormAndListing pins the repositories page wiring: the add form
// posts to the add endpoint and lists only ONLINE workers; a registered but
// offline worker must not be offered.
func TestReposPage_FormAndListing(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")
	// A registered but offline worker must NOT be offered.
	srv.Registry().Add("ghost", "ghost", nil, nil,
		pairingToken(time.Now()), time.Now())

	resp := sessionsGet(t, hs, "/__operator/repos")
	body := assertBody(t, resp, http.StatusOK,
		"Add repository", "/__operator/repos/add", "Repository URL", "choose a worker")
	if strings.Contains(body, "ghost") {
		t.Errorf("offline worker offered in select (body=%s)", body)
	}
}

// TestReposAdd_HappyPath pins the add flow end to end: the form POST sends a
// CloneRepo command down the worker stream, the placement is recorded, and the
// success banner names the repo and worker.
func TestReposAdd_HappyPath(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	assertBody(t, resp, http.StatusOK, "added on worker", "alpha")

	entries := srv.RepoPlacements()
	if len(entries) != 1 {
		t.Fatalf("placements = %d, want 1", len(entries))
	}
	if entries[0].URL != "https://github.com/org/repo" || entries[0].WorkerID != "alpha" {
		t.Errorf("placement = %+v, want the posted repo on alpha", entries[0])
	}
	if entries[0].Slug != "github.com-org-repo" {
		t.Errorf("slug = %q, want github.com-org-repo", entries[0].Slug)
	}
}

// TestReposAdd_ValidationGates pins the form validations — and that a failed
// add records no placement.
func TestReposAdd_ValidationGates(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	for _, tc := range []struct {
		name string
		form map[string][]string
		want string
	}{
		{"missing repo", map[string][]string{"worker": {"alpha"}}, "Enter a repository URL."},
		{"missing worker", map[string][]string{"repo": {"https://github.com/org/repo"}}, "Choose a worker."},
		{"unknown worker", map[string][]string{"repo": {"https://github.com/org/repo"}, "worker": {"nope"}}, "not registered"},
		{"bad scheme", map[string][]string{"repo": {"ftp://h/r"}, "worker": {"alpha"}}, "unsupported repository URL scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := sessionsPost(t, hs, nil, "/__operator/repos/add", tc.form)
			assertBody(t, resp, http.StatusOK, tc.want, "flash err")
		})
	}
	if n := len(srv.RepoPlacements()); n != 0 {
		t.Errorf("a failed add recorded %d placements, want 0", n)
	}
}

// TestReposAdd_WorkerFailureSurfaces pins that a worker-side CloneRepo failure
// (a failing git clone) surfaces verbatim as the error banner and records no
// placement.
func TestReposAdd_WorkerFailureSurfaces(t *testing.T) {
	srv, hs := newSessionPanelServerWithRejectingWorker(t, "alpha", "git clone failed: bad object")

	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	assertBody(t, resp, http.StatusOK, "git clone failed: bad object", "flash err")
	if n := len(srv.RepoPlacements()); n != 0 {
		t.Errorf("a failed clone recorded %d placements, want 0", n)
	}
}

// TestReposForget_DropsPlacement pins the forget flow: the placement leaves the
// listing, and forgetting an unknown slug is refused.
func TestReposForget_DropsPlacement(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp.Body.Close()

	resp2 := sessionsPost(t, hs, nil, "/__operator/repos/forget", map[string][]string{
		"slug": {"github.com-org-repo"},
	})
	assertBody(t, resp2, http.StatusOK, "removed from the list")
	if n := len(srv.RepoPlacements()); n != 0 {
		t.Fatalf("placements after forget = %d, want 0", n)
	}

	resp3 := sessionsPost(t, hs, nil, "/__operator/repos/forget", map[string][]string{
		"slug": {"github.com-org-repo"},
	})
	assertBody(t, resp3, http.StatusOK, "not listed", "flash err")
}

// TestReposPage_ListsPlacements pins that an existing placement renders as a
// table row with its slug, URL and worker.
func TestReposPage_ListsPlacements(t *testing.T) {
	_, hs := newSessionPanelServer(t, "alpha")
	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp.Body.Close()

	resp2 := sessionsGet(t, hs, "/__operator/repos")
	assertBody(t, resp2, http.StatusOK, "github.com-org-repo",
		"https://github.com/org/repo", "alpha", "/__operator/repos/forget")
}

// TestReposMerge_HappyPath pins the merge flow: the form POST sends the
// MergeUserBranch command through the placement worker and the success banner
// carries the worker's outcome summary.
func TestReposMerge_HappyPath(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp.Body.Close()

	resp2 := sessionsPost(t, hs, nil, "/__operator/repos/merge", map[string][]string{
		"slug": {"github.com-org-repo"},
		"user": {"alice"},
		"push": {"1"},
	})
	assertBody(t, resp2, http.StatusOK, "Merged alice on github.com-org-repo")
	if n := len(srv.RepoPlacements()); n != 1 {
		t.Errorf("placements after merge = %d, want 1 (merge keeps the placement)", n)
	}
}

// TestReposMerge_ValidationGates pins the merge form's validations.
func TestReposMerge_ValidationGates(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	for _, tc := range []struct {
		name string
		form map[string][]string
		want string
	}{
		{"missing slug", map[string][]string{"user": {"alice"}}, "Repository slug is required."},
		{"missing user", map[string][]string{"slug": {"github.com-org-repo"}}, "User name is required."},
		{"unknown slug", map[string][]string{"slug": {"nope"}, "user": {"alice"}}, "not listed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := sessionsPost(t, hs, nil, "/__operator/repos/merge", tc.form)
			assertBody(t, resp, http.StatusOK, tc.want, "flash err")
		})
	}
	if n := len(srv.RepoPlacements()); n != 0 {
		t.Errorf("a failed merge recorded %d placements, want 0", n)
	}
}

// TestReposDeprovision_HappyPath pins the deprovision flow: the placement is
// forgotten and the banner reports the worker's summary.
func TestReposDeprovision_HappyPath(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp.Body.Close()

	resp2 := sessionsPost(t, hs, nil, "/__operator/repos/deprovision", map[string][]string{
		"slug":   {"github.com-org-repo"},
		"remove": {"1"},
	})
	assertBody(t, resp2, http.StatusOK, "deprovisioned", "github.com-org-repo")
	if n := len(srv.RepoPlacements()); n != 0 {
		t.Errorf("placements after deprovision = %d, want 0", n)
	}
}

// TestReposDeprovision_FailureKeepsPlacement pins that a worker-side failure
// surfaces verbatim and leaves the placement in place (the operator can retry
// or fix the session situation first).
func TestReposDeprovision_FailureKeepsPlacement(t *testing.T) {
	srv, hs := newSessionPanelServerWithRejectingWorker(t, "alpha", "user worktree still hosts session ses_x")
	// Seed the placement the deprovision acts on (the rejecting worker cannot
	// clone one into existence): the failure we exercise is the worker rejecting
	// the deprovision command itself, not a missing placement.
	srv.SeedRepoPlacement("https://github.com/org/repo", "alpha")
	resp := sessionsPost(t, hs, nil, "/__operator/repos/deprovision", map[string][]string{
		"slug": {"github.com-org-repo"},
	})
	assertBody(t, resp, http.StatusOK, "user worktree still hosts session ses_x", "flash err")
	if n := len(srv.RepoPlacements()); n != 1 {
		t.Errorf("placements after failed deprovision = %d, want 1 (kept)", n)
	}
}

// TestReposPage_ListsLifecycleForms pins that a placement row renders the
// deprovision and forget actions in its row menu.
func TestReposPage_ListsLifecycleForms(t *testing.T) {
	_, hs := newSessionPanelServer(t, "alpha")
	resp := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp.Body.Close()

	resp2 := sessionsGet(t, hs, "/__operator/repos")
	assertBody(t, resp2, http.StatusOK, "/__operator/repos/deprovision", "/__operator/repos/forget")
}
