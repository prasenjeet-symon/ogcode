package permission

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func newStoreTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// A nil store must persist nothing while still granting — this is the path a
// headless run and a bare test take, and it is what kept the old in-memory
// behaviour working.
func TestNilStorePersistsNothingButStillGrants(t *testing.T) {
	m := NewManager(nil)
	m.AddRule("s1", Rule{Permission: "bash", Pattern: "ls", Action: Allow})
	if got := m.Ruleset("s1").Evaluate("bash", "ls"); got != Allow {
		t.Fatalf("grant with nil store = %q, want Allow", got)
	}
	if got := m.DefaultMode(); got != ModeAsk {
		t.Fatalf("nil-store default mode = %q, want ask", got)
	}
}

// A grant is remembered for the exact target the user approved — not the whole
// tool. Approving "ls" must not silently approve "rm -rf /".
func TestGrantAppliesToExactTargetOnly(t *testing.T) {
	m := NewManager(NewStore(newStoreTestDB(t)))
	m.AddRule("s1", Rule{Permission: "bash", Pattern: "ls", Action: Allow})

	if got := m.Ruleset("s1").Evaluate("bash", "ls"); got != Allow {
		t.Fatalf("approved command = %q, want Allow", got)
	}
	if got := m.Ruleset("s1").Evaluate("bash", "rm -rf /"); got != Ask {
		t.Fatalf("unapproved command = %q, want Ask", got)
	}
}

// The whole point of the change: a grant given in one session is honored in a
// different session, and survives a fresh manager over the same database —
// which is what a restart looks like.
func TestGrantIsMachineWideAndSurvivesRestart(t *testing.T) {
	database := newStoreTestDB(t)

	m := NewManager(NewStore(database))
	m.AddRule("session-a", Rule{Permission: "bash", Pattern: "make build", Action: Allow})

	// A different session, on the same manager, sees it.
	if got := m.Ruleset("session-b").Evaluate("bash", "make build"); got != Allow {
		t.Fatalf("grant in another session = %q, want Allow", got)
	}

	// A brand-new manager over the same DB — the restart path — sees it too.
	rebuilt := NewManager(NewStore(database))
	if got := rebuilt.Ruleset("session-c").Evaluate("bash", "make build"); got != Allow {
		t.Fatalf("grant after rebuild = %q, want Allow", got)
	}
}

// Approving the same target twice is one decision, not two rows.
func TestAddGrantIsIdempotent(t *testing.T) {
	database := newStoreTestDB(t)
	m := NewManager(NewStore(database))
	const grant = "git status"
	m.AddRule("s1", Rule{Permission: "bash", Pattern: grant, Action: Allow})
	m.AddRule("s1", Rule{Permission: "bash", Pattern: grant, Action: Allow})

	grants, err := NewStore(database).Grants()
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("stored grants = %d, want 1", len(grants))
	}
}

// A grant must outrank a configured ask, since the user gave it explicitly.
// skillPermissionRules seeds a session with an ask for a named skill; a stored
// allow for that same skill has to win.
func TestStoredGrantOutranksAConfiguredAsk(t *testing.T) {
	m := NewManager(NewStore(newStoreTestDB(t)))
	m.EnsureRules("s1", Ruleset{{Permission: "skill", Pattern: "deploy-prod", Action: Ask}})
	m.AddRule("s1", Rule{Permission: "skill", Pattern: "deploy-prod", Action: Allow})

	if got := m.Ruleset("s1").Evaluate("skill", "deploy-prod"); got != Allow {
		t.Fatalf("stored grant over configured ask = %q, want Allow", got)
	}
}

// The default mode round-trips through the store, and reads as Ask for a
// database that has never stored one.
func TestDefaultModeRoundTripsAndDefaultsToAsk(t *testing.T) {
	database := newStoreTestDB(t)
	store := NewStore(database)

	if got := store.DefaultMode(); got != ModeAsk {
		t.Fatalf("unset default mode = %q, want ask", got)
	}
	if err := store.SetDefaultMode(ModeAuto); err != nil {
		t.Fatalf("SetDefaultMode: %v", err)
	}
	if got := NewStore(database).DefaultMode(); got != ModeAuto {
		t.Fatalf("stored default mode = %q, want auto", got)
	}
	// Last write wins.
	if err := store.SetDefaultMode(ModeAsk); err != nil {
		t.Fatalf("SetDefaultMode: %v", err)
	}
	if got := store.DefaultMode(); got != ModeAsk {
		t.Fatalf("after second write = %q, want ask", got)
	}
}

// A garbage mode must not persist as itself: an unrecognised value falls back
// to the safest mode rather than being stored and read back.
func TestSetDefaultModeRejectsUnknownValue(t *testing.T) {
	database := newStoreTestDB(t)
	store := NewStore(database)
	if err := store.SetDefaultMode("cowboy"); err != nil {
		t.Fatalf("SetDefaultMode: %v", err)
	}
	if got := store.DefaultMode(); got != ModeAsk {
		t.Fatalf("unknown mode persisted as %q, want ask", got)
	}
}

// Yolo is a real mode, not an unknown one: it must round-trip like Auto and
// must NOT be normalised away to Ask.
func TestYoloModeRoundTrips(t *testing.T) {
	database := newStoreTestDB(t)
	store := NewStore(database)
	if err := store.SetDefaultMode(ModeYolo); err != nil {
		t.Fatalf("SetDefaultMode: %v", err)
	}
	if got := NewStore(database).DefaultMode(); got != ModeYolo {
		t.Fatalf("stored yolo mode = %q, want yolo", got)
	}
}

// The manager exposes the stored mode so a new session can be seeded from it.
func TestManagerDefaultModeReflectsStore(t *testing.T) {
	database := newStoreTestDB(t)
	if err := NewStore(database).SetDefaultMode(ModeAuto); err != nil {
		t.Fatalf("SetDefaultMode: %v", err)
	}
	m := NewManager(NewStore(database))
	if got := m.DefaultMode(); got != ModeAuto {
		t.Fatalf("DefaultMode = %q, want auto", got)
	}
	if err := m.SetDefaultMode(ModeAsk); err != nil {
		t.Fatalf("manager SetDefaultMode: %v", err)
	}
	if got := m.DefaultMode(); got != ModeAsk {
		t.Fatalf("after manager write = %q, want ask", got)
	}
}
