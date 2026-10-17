package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// UI theme modes. The mode is a machine-wide preference stored in the GLOBAL
// config DB: "system" follows the operating system, "light" and "dark" pin the
// interface regardless. Any other value (including an empty one) reads back as
// "system", so an unknown stored value can never leave the UI without a theme.
const (
	ThemeModeSystem = "system"
	ThemeModeLight  = "light"
	ThemeModeDark   = "dark"
)

// NormalizeThemeMode collapses a caller-supplied mode to one of the three known
// values, defaulting to "system".
func NormalizeThemeMode(mode string) string {
	switch mode {
	case ThemeModeLight, ThemeModeDark:
		return mode
	default:
		return ThemeModeSystem
	}
}

// GetThemeMode returns the stored UI theme mode, defaulting to "system" when no
// row exists or the store is unavailable.
func GetThemeMode(database *db.DB) (string, error) {
	if database == nil {
		return ThemeModeSystem, nil
	}
	var mode string
	err := database.QueryRow(`SELECT theme_mode FROM ui_prefs WHERE id = 1`).Scan(&mode)
	if err == sql.ErrNoRows {
		return ThemeModeSystem, nil
	}
	if err != nil {
		return "", fmt.Errorf("get theme mode: %w", err)
	}
	return NormalizeThemeMode(mode), nil
}

// SetThemeMode upserts the singleton UI-preferences row.
func SetThemeMode(database *db.DB, mode string) error {
	if database == nil {
		return nil
	}
	_, err := database.Exec(`
		INSERT INTO ui_prefs (id, theme_mode, time_updated)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			theme_mode   = excluded.theme_mode,
			time_updated = excluded.time_updated
	`, NormalizeThemeMode(mode), Now())
	if err != nil {
		return fmt.Errorf("set theme mode: %w", err)
	}
	return nil
}
