package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func previewPortTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return NewStore(database)
}

// TestRecordAnnouncedPortRoundTrips pins the durable record: a port announced in
// a directory comes back for that directory only, sorted.
func TestRecordAnnouncedPortRoundTrips(t *testing.T) {
	store := previewPortTestStore(t)

	for _, p := range []int{5173, 3000} {
		if err := store.RecordAnnouncedPort("/proj", p); err != nil {
			t.Fatalf("record %d: %v", p, err)
		}
	}
	// A different project must not see them.
	if err := store.RecordAnnouncedPort("/other", 6000); err != nil {
		t.Fatalf("record other: %v", err)
	}

	got, err := store.AnnouncedPorts("/proj")
	if err != nil {
		t.Fatalf("announced: %v", err)
	}
	if len(got) != 2 || got[0] != 3000 || got[1] != 5173 {
		t.Fatalf("got %v, want [3000 5173]", got)
	}
	if other, _ := store.AnnouncedPorts("/other"); len(other) != 1 || other[0] != 6000 {
		t.Fatalf("other = %v, want [6000]", other)
	}
}

// TestRecordAnnouncedPortIsIdempotent pins that re-announcing a port in the same
// directory is a no-op, so a long-lived session records a service once.
func TestRecordAnnouncedPortIsIdempotent(t *testing.T) {
	store := previewPortTestStore(t)

	for i := 0; i < 3; i++ {
		if err := store.RecordAnnouncedPort("/proj", 5173); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	got, err := store.AnnouncedPorts("/proj")
	if err != nil {
		t.Fatalf("announced: %v", err)
	}
	if len(got) != 1 || got[0] != 5173 {
		t.Fatalf("got %v, want [5173]", got)
	}
}

// TestRecordAnnouncedPortRejectsInvalid pins that a missing directory and an
// out-of-range port are dropped rather than stored.
func TestRecordAnnouncedPortRejectsInvalid(t *testing.T) {
	store := previewPortTestStore(t)

	for _, p := range []int{0, -1, 70000} {
		if err := store.RecordAnnouncedPort("/proj", p); err != nil {
			t.Fatalf("record %d: %v", p, err)
		}
	}
	if err := store.RecordAnnouncedPort("", 5173); err != nil {
		t.Fatalf("record empty dir: %v", err)
	}

	got, err := store.AnnouncedPorts("/proj")
	if err != nil {
		t.Fatalf("announced: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing recorded", got)
	}
}

// TestAnnouncedPortsNilSafe pins that a nil store and an empty directory yield
// nothing instead of panicking.
func TestAnnouncedPortsNilSafe(t *testing.T) {
	var store *Store
	if err := store.RecordAnnouncedPort("/proj", 5173); err != nil {
		t.Fatalf("nil record: %v", err)
	}
	if got, err := store.AnnouncedPorts("/proj"); err != nil || got != nil {
		t.Fatalf("nil announced = %v, %v, want nil, nil", got, err)
	}
}

// TestPreviewPortPublished pins the proxy's allowlist question: a port recorded
// in any directory is published, an unrecorded one is not, and a nil store
// publishes nothing — a server without a record refuses every preview rather
// than serving every port.
func TestPreviewPortPublished(t *testing.T) {
	store := previewPortTestStore(t)
	if err := store.RecordAnnouncedPort("/proj", 5173); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := store.RecordAnnouncedPort("/other", 6000); err != nil {
		t.Fatalf("record other: %v", err)
	}

	for _, p := range []int{5173, 6000} {
		if ok, err := store.PreviewPortPublished(p); err != nil || !ok {
			t.Fatalf("PreviewPortPublished(%d) = %v, %v, want true", p, ok, err)
		}
	}
	for _, p := range []int{3000, 0, 70000} {
		if ok, err := store.PreviewPortPublished(p); err != nil || ok {
			t.Fatalf("PreviewPortPublished(%d) = %v, %v, want false", p, ok, err)
		}
	}

	var nilStore *Store
	if ok, err := nilStore.PreviewPortPublished(5173); err != nil || ok {
		t.Fatalf("nil store PreviewPortPublished = %v, %v, want false", ok, err)
	}
}

// TestForgetAnnouncedPort pins un-publishing: the port leaves every directory's
// record (publication is per server), other ports stay, and forgetting an
// unrecorded port is a no-op.
func TestForgetAnnouncedPort(t *testing.T) {
	store := previewPortTestStore(t)
	for _, rec := range []struct {
		dir  string
		port int
	}{{"/proj", 5173}, {"/proj", 3000}, {"/other", 5173}} {
		if err := store.RecordAnnouncedPort(rec.dir, rec.port); err != nil {
			t.Fatalf("record %v: %v", rec, err)
		}
	}

	if err := store.ForgetAnnouncedPort(5173); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if ok, _ := store.PreviewPortPublished(5173); ok {
		t.Fatal("5173 still published after forget")
	}
	if got, _ := store.AnnouncedPorts("/proj"); len(got) != 1 || got[0] != 3000 {
		t.Fatalf("/proj = %v, want [3000]", got)
	}
	if got, _ := store.AnnouncedPorts("/other"); len(got) != 0 {
		t.Fatalf("/other = %v, want nothing", got)
	}
	if err := store.ForgetAnnouncedPort(4444); err != nil {
		t.Fatalf("forget unrecorded: %v", err)
	}
	var nilStore *Store
	if err := nilStore.ForgetAnnouncedPort(5173); err != nil {
		t.Fatalf("nil forget: %v", err)
	}
}
