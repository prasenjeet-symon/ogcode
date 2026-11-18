package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// NavState is the folded/expanded state of the left-hand navigation panels. It
// is read from the project database, so each project remembers its own layout.
type NavState struct {
	SidebarCollapsed     bool `json:"sidebarCollapsed"`
	SettingsNavCollapsed bool `json:"settingsNavCollapsed"`
}

// GetNavState returns the stored navigation state for this project. A project
// that has never folded a panel reports both expanded — the behaviour from
// before the setting existed.
func GetNavState(database *db.DB) (NavState, error) {
	var state NavState
	if database == nil {
		return state, nil
	}
	var sidebar, settings int
	err := database.QueryRow(`
		SELECT sidebar_collapsed, settings_nav_collapsed
		FROM project_ui_prefs WHERE id = 1
	`).Scan(&sidebar, &settings)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("get nav state: %w", err)
	}
	state.SidebarCollapsed = sidebar != 0
	state.SettingsNavCollapsed = settings != 0
	return state, nil
}

// SetNavState upserts the navigation state on the singleton row. A nil field
// leaves that panel as it was, so a client can report one fold at a time.
func SetNavState(database *db.DB, sidebar, settings *bool) (NavState, error) {
	if database == nil {
		return NavState{}, nil
	}
	state, err := GetNavState(database)
	if err != nil {
		return state, err
	}
	if sidebar != nil {
		state.SidebarCollapsed = *sidebar
	}
	if settings != nil {
		state.SettingsNavCollapsed = *settings
	}
	_, err = database.Exec(`
		INSERT INTO project_ui_prefs (id, sidebar_collapsed, settings_nav_collapsed, time_updated)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sidebar_collapsed      = excluded.sidebar_collapsed,
			settings_nav_collapsed = excluded.settings_nav_collapsed,
			time_updated           = excluded.time_updated
	`, boolColumn(state.SidebarCollapsed), boolColumn(state.SettingsNavCollapsed), Now())
	if err != nil {
		return NavState{}, fmt.Errorf("set nav state: %w", err)
	}
	return state, nil
}

func boolColumn(v bool) int {
	if v {
		return 1
	}
	return 0
}
