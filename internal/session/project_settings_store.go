package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// ProjectSettings holds the general settings scoped to a single project. They
// live in the project's own database (<project>/.ogcode/ogcode.db) rather than
// the global config DB, so one workspace can run differently from the next —
// unlike provider credentials or model preferences, which are the user's and
// are the same everywhere.
type ProjectSettings struct {
	// CompactContext controls whether the agent is offered compact_context, the
	// tool that lets it drop the finished part of a turn in exchange for a
	// summary it writes. On (the default) is the behaviour that predates the
	// setting. Turning it off withholds the tool, its system-prompt guidance and
	// the read-pressure reminder that names it — so a project where the model
	// summarizes badly, or where the endpoint caches a repeated prefix cheaply
	// enough that re-sending beats re-deriving, can simply not have it.
	//
	// It does NOT disable the loop's own size-triggered compaction: that is the
	// safety net that keeps a turn from overflowing the context window, and the
	// turn fails without it.
	CompactContext bool  `json:"compactContext"`
	UpdatedAt      int64 `json:"updatedAt"`
}

// DefaultProjectSettings is what a project with no stored row behaves as.
func DefaultProjectSettings() ProjectSettings {
	return ProjectSettings{CompactContext: true}
}

// GetProjectSettings returns the stored settings for this project, or the
// defaults when nothing has been saved. A project that has never opened the
// settings screen is therefore indistinguishable from one that saved the
// defaults, which is the point: the row records a choice, not a migration step.
func GetProjectSettings(database *db.DB) (*ProjectSettings, error) {
	var compactContext int
	var updatedAt int64
	err := database.QueryRow(
		`SELECT compact_context_enabled, time_updated FROM project_settings WHERE id = 1`,
	).Scan(&compactContext, &updatedAt)
	if err == sql.ErrNoRows {
		def := DefaultProjectSettings()
		return &def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project settings: %w", err)
	}
	return &ProjectSettings{CompactContext: compactContext != 0, UpdatedAt: updatedAt}, nil
}

// SetProjectSettings upserts the singleton row.
func SetProjectSettings(database *db.DB, s *ProjectSettings) error {
	compactContext := 0
	if s.CompactContext {
		compactContext = 1
	}
	s.UpdatedAt = Now()
	_, err := database.Exec(`
		INSERT INTO project_settings (id, compact_context_enabled, time_updated)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			compact_context_enabled = excluded.compact_context_enabled,
			time_updated            = excluded.time_updated
	`, compactContext, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("set project settings: %w", err)
	}
	return nil
}

// CompactContextEnabled answers the one question the agent loop asks, and
// answers it the safe way when the database cannot: a read failure must not
// silently take a tool away from a project that has it turned on, so an error
// reports the default (enabled) rather than off.
//
// nil database — the CLI paths that run without one — is enabled for the same
// reason: that is the behaviour that predates the setting.
func CompactContextEnabled(database *db.DB) bool {
	if database == nil {
		return DefaultProjectSettings().CompactContext
	}
	s, err := GetProjectSettings(database)
	if err != nil || s == nil {
		return DefaultProjectSettings().CompactContext
	}
	return s.CompactContext
}
