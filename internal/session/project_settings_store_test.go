package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func newProjectSettingsTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// A project that has never touched the settings screen must behave exactly as
// it did before the setting existed: compact_context available.
func TestProjectSettingsDefaultsToCompactContextEnabled(t *testing.T) {
	database := newProjectSettingsTestDB(t)

	settings, err := GetProjectSettings(database)
	if err != nil {
		t.Fatalf("GetProjectSettings: %v", err)
	}
	if !settings.CompactContext {
		t.Error("compact_context should default to enabled for a project with no stored row")
	}
	if !CompactContextEnabled(database) {
		t.Error("CompactContextEnabled should agree with the stored default")
	}
}

// Off must survive the round trip. A false bool is the value most easily lost to
// an "unset means default" shortcut, which would silently re-enable the tool for
// every project that turned it off.
func TestProjectSettingsRoundTrip(t *testing.T) {
	database := newProjectSettingsTestDB(t)

	if err := SetProjectSettings(database, &ProjectSettings{CompactContext: false}); err != nil {
		t.Fatalf("SetProjectSettings: %v", err)
	}
	settings, err := GetProjectSettings(database)
	if err != nil {
		t.Fatalf("GetProjectSettings: %v", err)
	}
	if settings.CompactContext {
		t.Error("compact_context should stay disabled after being saved off")
	}
	if settings.UpdatedAt == 0 {
		t.Error("UpdatedAt should be stamped on write")
	}
	if CompactContextEnabled(database) {
		t.Error("CompactContextEnabled should report the stored off value")
	}

	// And back on, through the same singleton row rather than a second one.
	if err := SetProjectSettings(database, &ProjectSettings{CompactContext: true}); err != nil {
		t.Fatalf("SetProjectSettings (re-enable): %v", err)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM project_settings`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("project_settings should hold exactly one row, got %d", rows)
	}
	if !CompactContextEnabled(database) {
		t.Error("compact_context should be enabled again after re-saving")
	}
}

// The setting is per project: two workspaces each keep their own answer, which
// is the whole reason it does not live in the global config DB.
func TestProjectSettingsAreScopedToOneProject(t *testing.T) {
	projectA, projectB := newProjectSettingsTestDB(t), newProjectSettingsTestDB(t)

	if err := SetProjectSettings(projectA, &ProjectSettings{CompactContext: false}); err != nil {
		t.Fatalf("SetProjectSettings: %v", err)
	}
	if CompactContextEnabled(projectA) {
		t.Error("project A should have compact_context off")
	}
	if !CompactContextEnabled(projectB) {
		t.Error("project B should be untouched by project A's choice")
	}
}

// A database that cannot be read must not silently withhold a tool the project
// has turned on. Enabled is the safe answer, so that is what a failure reports.
func TestCompactContextEnabledFailsSafe(t *testing.T) {
	if !CompactContextEnabled(nil) {
		t.Error("a nil database should report the default (enabled), not off")
	}
}
