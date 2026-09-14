package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func uRecord(hash string) UserRecord {
	return UserRecord{Hash: hash, CreatedAt: time.Now()}
}

func TestUsersCRUD(t *testing.T) {
	st := newTestStore(t)

	if has, err := st.HasUsers(); err != nil || has {
		t.Fatalf("fresh store HasUsers = %v,%v want false,nil", has, err)
	}
	if names, err := st.ListUsers(); err != nil || len(names) != 0 {
		t.Fatalf("fresh store ListUsers = %v,%v want empty", names, err)
	}

	if err := st.PutUser("alice", uRecord("h1")); err != nil {
		t.Fatal(err)
	}
	if err := st.PutUser("bob", uRecord("h2")); err != nil {
		t.Fatal(err)
	}

	rec, exists, err := st.GetUser("alice")
	if err != nil || !exists {
		t.Fatalf("GetUser(alice) = %+v,%v,%v want exists", rec, exists, err)
	}
	if rec.Hash != "h1" {
		t.Fatalf("alice hash = %q, want h1", rec.Hash)
	}
	if _, exists, _ := st.GetUser("nobody"); exists {
		t.Fatal("GetUser(nobody) should not exist")
	}

	if has, err := st.HasUsers(); err != nil || !has {
		t.Fatalf("HasUsers = %v,%v want true", has, err)
	}

	// Overwrite (password reset).
	if err := st.PutUser("alice", uRecord("h1b")); err != nil {
		t.Fatal(err)
	}
	rec, _, _ = st.GetUser("alice")
	if rec.Hash != "h1b" {
		t.Fatalf("alice hash after reset = %q, want h1b", rec.Hash)
	}

	removed, err := st.DeleteUser("alice")
	if err != nil || !removed {
		t.Fatalf("DeleteUser(alice) = %v,%v want true", removed, err)
	}
	if removed, _ := st.DeleteUser("alice"); removed {
		t.Fatal("DeleteUser on absent user should report false")
	}
	if _, exists, _ := st.GetUser("alice"); exists {
		t.Fatal("alice still exists after delete")
	}

	names, _ := st.ListUsers()
	if len(names) != 1 || names[0] != "bob" {
		t.Fatalf("ListUsers after delete = %v, want [bob]", names)
	}
}

func TestUsersPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutUser("alice", uRecord("h1")); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	rec, exists, err := st2.GetUser("alice")
	if err != nil || !exists {
		t.Fatalf("reopened store GetUser(alice) = %+v,%v,%v", rec, exists, err)
	}
	if rec.Hash != "h1" {
		t.Fatalf("reopened alice hash = %q, want h1", rec.Hash)
	}
	if has, _ := st2.HasUsers(); !has {
		t.Fatal("users must survive a reopen")
	}

	// The existing buckets coexist: workers/sessions still load empty.
	snap, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Workers) != 0 || len(snap.Sessions) != 0 {
		t.Fatalf("new store should start with empty workers/sessions")
	}
}

func TestGetPasswordHash(t *testing.T) {
	st := newTestStore(t)
	if err := st.PutUser("alice", uRecord("h1")); err != nil {
		t.Fatal(err)
	}
	hash, exists, err := st.GetPasswordHash("alice")
	if err != nil || !exists || hash != "h1" {
		t.Fatalf("GetPasswordHash(alice) = %q,%v,%v want h1,true,nil", hash, exists, err)
	}
	if _, exists, _ := st.GetPasswordHash("nobody"); exists {
		t.Fatal("GetPasswordHash(nobody) should not exist")
	}
}

// TestOpenCorruptFileTruncated pins the A2 corrupt-file policy for a byte-
// truncated file: Open must return ErrCorrupt WITHOUT handing the file to bbolt,
// because bbolt segfaults the whole process on a truncated file (its freelist
// loader reads past the mmap). The master therefore refuses to boot against a
// truncated DB rather than crash.
func TestOpenCorruptFileTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutUser("alice", uRecord("h")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	// Truncate to half the file — the exact shape that crashes bbolt's freelist
	// loader on open.
	if err := os.Truncate(path, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path)
	if err != ErrCorrupt {
		t.Fatalf("Open on truncated file = %v, want ErrCorrupt (must not segfault or open clean)", err)
	}
}

// TestOpenCorruptFileGarbage pins the A2 corrupt-file policy for a garbage
// file: Open must refuse it with ErrCorrupt.
func TestOpenCorruptFileGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	if err := os.WriteFile(path, []byte("this is not a bbolt database, just prose padding that is long"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != ErrCorrupt {
		t.Fatalf("Open on garbage file = %v, want ErrCorrupt", err)
	}
}

// TestOpenEmptyFileInitializes pins that a brand-new empty file (or an absent
// one) opens clean and is initialised — the first-boot path.
func TestOpenEmptyFileInitializes(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(empty)
	if err != nil {
		t.Fatalf("Open on empty file = %v, want ok", err)
	}
	defer st.Close()
	if has, _ := st.HasUsers(); has {
		t.Fatal("freshly initialised empty DB must have no users")
	}
}

// TestOpenCorruptFileAbsent pins that a not-yet-existing path opens clean (the
// first boot before any worker or account has ever been created).
func TestOpenCorruptFileAbsent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("Open on absent path = %v, want ok", err)
	}
	st.Close()
}

// TestRawPasswordNeverInDB pins a Phase B invariant: only the bcrypt hash is
// persisted, never the raw password. A leaked DB must yield hashes, not
// credentials.
func TestRawPasswordNeverInDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, _ := Open(path)
	const secret = "super-secret-password-S3cret"
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutUser("alice", uRecord(string(hash))); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-password") {
		t.Fatal("raw password bytes found in the DB file — only hashes may be persisted")
	}
}

// TestUserWorkspacesPersist pins the allowlist field end-to-end: it rides the
// same JSON record as the hash, survives a store reopen, and an account
// created without one reads back nil (unrestricted).
func TestUserWorkspacesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.PutUser("alice", UserRecord{Hash: string(hash), CreatedAt: time.Now(), Workspaces: []string{"treemain", "repo-b"}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.PutUser("bob", UserRecord{Hash: string(hash), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	ws, err := reopened.GetUserWorkspaces("alice")
	if err != nil {
		t.Fatalf("get workspaces: %v", err)
	}
	if len(ws) != 2 || ws[0] != "treemain" || ws[1] != "repo-b" {
		t.Fatalf("reopened workspaces = %v, want [treemain repo-b]", ws)
	}
	ws2, err := reopened.GetUserWorkspaces("bob")
	if err != nil || ws2 != nil {
		t.Fatalf("unrestricted account workspaces = %v err=%v, want nil/nil", ws2, err)
	}
	ws3, err := reopened.GetUserWorkspaces("ghost")
	if err != nil || ws3 != nil {
		t.Fatalf("missing account workspaces = %v err=%v, want nil/nil (absence is unrestricted)", ws3, err)
	}
}
