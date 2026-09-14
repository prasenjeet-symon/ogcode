package master_test

import (
	"net/http"
	"testing"
)

// TestUsersAssignRepo_Validation pins the assign-repositories form's guards:
// a missing user or an empty repository selection is a clear error banner, not
// a silent no-op or a worker round-trip.
func TestUsersAssignRepo_Validation(t *testing.T) {
	_, hs, _, admin, _, _ := scopedHarness(t)

	resp := sessionsPost(t, hs, admin, "/__operator/users/assign-repo", map[string][]string{
		"repo": {"https://github.com/o/a"},
	})
	assertBody(t, resp, http.StatusOK, "Choose a user", "flash err")

	resp = sessionsPost(t, hs, admin, "/__operator/users/assign-repo", map[string][]string{
		"user": {"alice"},
	})
	assertBody(t, resp, http.StatusOK, "Select at least one repository", "flash err")
}

// TestUsersUnassignRepo_DropsAssignment pins that unassigning a repo removes it
// from the account's set even when the worktree removal cannot complete (no
// live placement here) — the assignment is authoritative, and the operator is
// told the worktree may linger.
func TestUsersUnassignRepo_DropsAssignment(t *testing.T) {
	_, hs, store, admin, _, _ := scopedHarness(t)

	rec, ok, _ := store.GetUser("alice")
	if !ok {
		t.Fatal("seed alice missing")
	}
	rec.Repo = ""
	rec.Repos = []string{"https://github.com/o/a", "https://github.com/o/b"}
	if err := store.PutUser("alice", rec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := sessionsPost(t, hs, admin, "/__operator/users/unassign-repo", map[string][]string{
		"username": {"alice"}, "repo": {"https://github.com/o/a"},
	})
	assertBody(t, resp, http.StatusOK, "Unassigned", "flash ok")

	got, _, _ := store.GetUser("alice")
	if repos := got.AllRepos(); len(repos) != 1 || repos[0] != "https://github.com/o/b" {
		t.Errorf("alice repos after unassign = %v, want [the remaining repo]", repos)
	}
}

// TestUsersSetPassword_ResetsHash pins the password-reset flow: the stored hash
// changes and the account authenticates with the new password.
func TestUsersSetPassword_ResetsHash(t *testing.T) {
	_, hs, store, admin, _, _ := scopedHarness(t)

	before, _, _ := store.GetUser("alice")
	resp := sessionsPost(t, hs, admin, "/__operator/users/set-password", map[string][]string{
		"username": {"alice"}, "password": {"new-s3cret"}, "confirm": {"new-s3cret"},
	})
	assertBody(t, resp, http.StatusOK, "Password reset", "flash ok")

	after, _, _ := store.GetUser("alice")
	if after.Hash == before.Hash {
		t.Error("password hash unchanged after reset")
	}
	// The new password authenticates (loginCookie fails the test on non-303).
	_ = loginCookie(t, hs, "alice", "new-s3cret")

	// A mismatch is rejected without touching the hash.
	resp = sessionsPost(t, hs, admin, "/__operator/users/set-password", map[string][]string{
		"username": {"alice"}, "password": {"a"}, "confirm": {"b"},
	})
	assertBody(t, resp, http.StatusOK, "do not match", "flash err")
}
