package registry

import (
	"reflect"
	"testing"
)

// TestUserRecordAllRepos pins the merge of the multi-repo Repos slice with the
// legacy scalar Repo: Repos first, the scalar folded in only when it is not
// already present, and empty when neither is set.
func TestUserRecordAllRepos(t *testing.T) {
	cases := []struct {
		name string
		rec  UserRecord
		want []string
	}{
		{"none", UserRecord{}, nil},
		{"legacy only", UserRecord{Repo: "a"}, []string{"a"}},
		{"multi only", UserRecord{Repos: []string{"a", "b"}}, []string{"a", "b"}},
		{"legacy folded in", UserRecord{Repos: []string{"a"}, Repo: "b"}, []string{"a", "b"}},
		{"legacy already present", UserRecord{Repos: []string{"a", "b"}, Repo: "a"}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		if got := tc.rec.AllRepos(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: AllRepos() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestUserRecordWithRepoAdded pins additive assignment: adding is idempotent,
// folds the legacy scalar into Repos, and reports whether the repo was new.
func TestUserRecordWithRepoAdded(t *testing.T) {
	// Fresh account gains its first repo.
	rec, added := UserRecord{}.WithRepoAdded("a")
	if !added || !reflect.DeepEqual(rec.AllRepos(), []string{"a"}) || rec.Repo != "" {
		t.Fatalf("first add: added=%v repos=%v repo=%q", added, rec.AllRepos(), rec.Repo)
	}
	// A second, distinct repo accumulates.
	rec, added = rec.WithRepoAdded("b")
	if !added || !reflect.DeepEqual(rec.AllRepos(), []string{"a", "b"}) {
		t.Fatalf("second add: added=%v repos=%v", added, rec.AllRepos())
	}
	// Re-adding an existing repo is a no-op.
	rec, added = rec.WithRepoAdded("a")
	if added || !reflect.DeepEqual(rec.AllRepos(), []string{"a", "b"}) {
		t.Fatalf("dup add: added=%v repos=%v", added, rec.AllRepos())
	}
	// A legacy scalar is folded in on the next add.
	legacy := UserRecord{Repo: "a"}
	legacy, added = legacy.WithRepoAdded("b")
	if !added || legacy.Repo != "" || !reflect.DeepEqual(legacy.Repos, []string{"a", "b"}) {
		t.Fatalf("legacy fold: added=%v repo=%q repos=%v", added, legacy.Repo, legacy.Repos)
	}
}

// TestUserRecordWithRepoRemoved pins un-assignment: removing drops the repo
// (folding in the legacy scalar) and reports whether it was present.
func TestUserRecordWithRepoRemoved(t *testing.T) {
	rec := UserRecord{Repos: []string{"a", "b"}}
	rec, removed := rec.WithRepoRemoved("a")
	if !removed || !reflect.DeepEqual(rec.Repos, []string{"b"}) {
		t.Fatalf("remove a: removed=%v repos=%v", removed, rec.Repos)
	}
	rec, removed = rec.WithRepoRemoved("missing")
	if removed || !reflect.DeepEqual(rec.Repos, []string{"b"}) {
		t.Fatalf("remove missing: removed=%v repos=%v", removed, rec.Repos)
	}
	// Removing the legacy scalar's value works via the fold.
	legacy := UserRecord{Repo: "a"}
	legacy, removed = legacy.WithRepoRemoved("a")
	if !removed || legacy.Repo != "" || len(legacy.Repos) != 0 {
		t.Fatalf("remove legacy: removed=%v repo=%q repos=%v", removed, legacy.Repo, legacy.Repos)
	}
}
