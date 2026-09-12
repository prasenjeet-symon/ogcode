package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// newCapabilityDB opens a throwaway database with the migrations applied — the
// model_capability table must exist for every case below.
func newCapabilityDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestGetSetModelCapability_RoundTripsContextWindow pins the new column: the
// window learned from an overflow error survives a write and a read, including
// the "no record existed yet" insert path LearnModelContextWindow takes.
func TestGetSetModelCapability_RoundTripsContextWindow(t *testing.T) {
	database := newCapabilityDB(t)

	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "m", SupportsImages: true, ProbedAt: 10, ContextWindow: 131072,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := GetModelCapability(database, "m")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ContextWindow != 131072 || !got.SupportsImages || got.ProbedAt != 10 {
		t.Errorf("round trip = %+v, want window 131072, images, probedAt 10", got)
	}

	// Absent model → ok=false, zero record.
	if _, ok, err := GetModelCapability(database, "missing"); err != nil || ok {
		t.Errorf("missing model: ok=%v err=%v, want false/nil", ok, err)
	}
}

// TestSetModelCapability_MergesLearnedWindow pins why the upsert is a merge and
// not an INSERT OR REPLACE: the image probe always writes ContextWindow 0, and
// a replace would erase a window the loop learned from an overflow error
// between probes. The image fields, meanwhile, stay authoritative from the
// incoming record — each probe re-decides them.
func TestSetModelCapability_MergesLearnedWindow(t *testing.T) {
	database := newCapabilityDB(t)

	// Seed a learned window via a capability write that carries it.
	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "m", SupportsImages: true, ProbedAt: 10, ContextWindow: 131072,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// An image-probe-shaped write: window 0, fresh image verdict. The learned
	// window must survive; the image fields must NOT.
	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "m", SupportsImages: false, ProbedAt: 20, ContextWindow: 0,
	}); err != nil {
		t.Fatalf("image-probe write: %v", err)
	}
	got, ok, _ := GetModelCapability(database, "m")
	if !ok {
		t.Fatal("record vanished")
	}
	if got.ContextWindow != 131072 {
		t.Errorf("ContextWindow = %d, want 131072 (learned window must survive a ContextWindow-0 write)", got.ContextWindow)
	}
	if got.SupportsImages || got.ProbedAt != 20 {
		t.Errorf("image fields = %+v, want overwritten from the incoming record", got)
	}

	// An explicit positive window in the write overwrites the stored one.
	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "m", SupportsImages: false, ProbedAt: 30, ContextWindow: 200000,
	}); err != nil {
		t.Fatalf("explicit write: %v", err)
	}
	if got, _, _ := GetModelCapability(database, "m"); got.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000 (explicit positive wins)", got.ContextWindow)
	}
}

// TestLearnModelContextWindow covers the learning helper: inserts when no
// record exists, preserves everything else when one does, and ignores
// nothing-to-learn values.
func TestLearnModelContextWindow(t *testing.T) {
	database := newCapabilityDB(t)

	// No record yet → created with just the window.
	if err := LearnModelContextWindow(database, "new-model", 1048576); err != nil {
		t.Fatalf("learn: %v", err)
	}
	got, ok, _ := GetModelCapability(database, "new-model")
	if !ok {
		t.Fatal("learn did not create a record")
	}
	if got.ContextWindow != 1048576 || got.SupportsImages || got.ProbedAt == 0 {
		t.Errorf("learned record = %+v, want window 1048576, no image claim, probedAt set", got)
	}

	// Existing record with an image verdict → image fields preserved.
	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "known-model", SupportsImages: true, ProbedAt: 5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := LearnModelContextWindow(database, "known-model", 262144); err != nil {
		t.Fatalf("learn: %v", err)
	}
	got, ok, _ = GetModelCapability(database, "known-model")
	if !ok {
		t.Fatal("record vanished")
	}
	if got.ContextWindow != 262144 || !got.SupportsImages || got.ProbedAt <= 5 {
		t.Errorf("after learn = %+v, want window 262144 with image verdict kept and probedAt advanced (learning refreshes the record's freshness)", got)
	}

	// Learning again moves the window (a later, better figure wins).
	if err := LearnModelContextWindow(database, "known-model", 131072); err != nil {
		t.Fatalf("relearn: %v", err)
	}
	if got, _, _ = GetModelCapability(database, "known-model"); got.ContextWindow != 131072 {
		t.Errorf("relearned ContextWindow = %d, want 131072", got.ContextWindow)
	}

	// Nothing parsed (window ≤ 0) is a no-op — it must not create a record
	// with a zero window nor clear an existing one.
	if err := LearnModelContextWindow(database, "untouched-model", 0); err != nil {
		t.Fatalf("learn 0: %v", err)
	}
	if _, ok, _ := GetModelCapability(database, "untouched-model"); ok {
		t.Error("learning 0 created a record")
	}
	if err := LearnModelContextWindow(database, "known-model", -5); err != nil {
		t.Fatalf("learn -5: %v", err)
	}
	if got, _, _ = GetModelCapability(database, "known-model"); got.ContextWindow != 131072 {
		t.Errorf("ContextWindow after learn(-5) = %d, want unchanged 131072", got.ContextWindow)
	}
}

// TestDeleteModelCapability_DropsLearnedWindow pins the manual-refresh
// semantics: clearing a model's capability forgets everything derived about it
// — image verdict AND learned window — so the next run re-derives both.
func TestDeleteModelCapability_DropsLearnedWindow(t *testing.T) {
	database := newCapabilityDB(t)

	if err := SetModelCapability(database, &ModelCapability{
		ModelID: "m", SupportsImages: true, ProbedAt: 10, ContextWindow: 65536,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := DeleteModelCapability(database, "m"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := GetModelCapability(database, "m"); ok {
		t.Fatal("record survived delete")
	}
}
