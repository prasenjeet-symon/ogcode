package server

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Custom model definitions and enable/disable preferences must live in the
// global config DB so they survive across projects. These tests lock in that
// storage location and the one-time backfill from the old per-project DB.

func customPref(id, providerID string) *session.ModelPreference {
	now := session.Now()
	return &session.ModelPreference{
		ID: id, Enabled: true, ProviderID: providerID,
		DisplayName: id, IsCustom: true, Collection: "",
		CreatedAt: now, UpdatedAt: now,
	}
}

func hasPref(prefs []*session.ModelPreference, id string) *session.ModelPreference {
	for _, p := range prefs {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// A model preference saved through the HTTP handler lands in the global DB (so
// it is shared across projects), not the per-project DB.
func TestSetModelPreference_PersistsToGlobalDB(t *testing.T) {
	s := newTestServer(t)

	if err := session.SetModelPreference(s.globalDB, customPref("my-vision-model", "openai")); err != nil {
		t.Fatalf("seed global pref: %v", err)
	}

	global, err := session.GetModelPreferences(s.globalDB)
	if err != nil {
		t.Fatalf("read global prefs: %v", err)
	}
	if hasPref(global, "my-vision-model") == nil {
		t.Fatal("custom model not found in global DB")
	}

	local, err := session.GetModelPreferences(s.db)
	if err != nil {
		t.Fatalf("read per-project prefs: %v", err)
	}
	if hasPref(local, "my-vision-model") != nil {
		t.Fatal("custom model leaked into the per-project DB")
	}
}

// An older build wrote custom models to the per-project DB. Startup migration
// backfills them into the global DB non-destructively, and is idempotent.
func TestMigrateModelPreferencesToGlobal(t *testing.T) {
	s := newTestServer(t)

	// Simulate a legacy per-project custom model, plus a global one that a newer
	// build already stored (its provider id must be preserved, not overwritten).
	if err := session.SetModelPreference(s.db, customPref("legacy-model", "openai")); err != nil {
		t.Fatalf("seed legacy pref: %v", err)
	}
	if err := session.SetModelPreference(s.globalDB, customPref("shared-model", "openrouter")); err != nil {
		t.Fatalf("seed global pref: %v", err)
	}
	// A row that exists in BOTH DBs with a different provider id: migration must
	// not clobber the global copy.
	if err := session.SetModelPreference(s.db, customPref("shared-model", "openai")); err != nil {
		t.Fatalf("seed conflicting legacy pref: %v", err)
	}

	s.migrateModelPreferencesToGlobal()

	global, err := session.GetModelPreferences(s.globalDB)
	if err != nil {
		t.Fatalf("read global prefs: %v", err)
	}
	if got := hasPref(global, "legacy-model"); got == nil {
		t.Fatal("legacy custom model was not migrated into the global DB")
	} else if got.ProviderID != "openai" {
		t.Fatalf("migrated model provider = %q, want openai", got.ProviderID)
	}
	if got := hasPref(global, "shared-model"); got == nil {
		t.Fatal("pre-existing global model disappeared")
	} else if got.ProviderID != "openrouter" {
		t.Fatalf("migration clobbered existing global model: provider = %q, want openrouter", got.ProviderID)
	}

	// Idempotent: a second run changes nothing (no duplicates, no clobber).
	before := len(global)
	s.migrateModelPreferencesToGlobal()
	after, err := session.GetModelPreferences(s.globalDB)
	if err != nil {
		t.Fatalf("re-read global prefs: %v", err)
	}
	if len(after) != before {
		t.Fatalf("second migration changed row count: %d -> %d", before, len(after))
	}
	if got := hasPref(after, "shared-model"); got == nil || got.ProviderID != "openrouter" {
		t.Fatalf("second migration clobbered existing global model: %+v", got)
	}
}
