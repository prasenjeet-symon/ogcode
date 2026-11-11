package session

import (
	"database/sql"
	"fmt"
	"strings"

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

// BackgroundModelPref holds the machine-wide model override for behind-the-scenes
// LLM calls (title generation, turn memory summary, context compaction, command
// risk assessment, intent tool matching, auto-indexing, deep search, memory
// recall, and task sub-agents). Empty Model means inherit from the active session.
type BackgroundModelPref struct {
	Model      string `json:"model"`
	ProviderID string `json:"providerId"`
}

// GetBackgroundModel returns the stored background-tasks model preference, or
// an empty preference (meaning "inherit from active session") when unset.
func GetBackgroundModel(database *db.DB) (BackgroundModelPref, error) {
	if database == nil {
		return BackgroundModelPref{}, nil
	}
	var pref BackgroundModelPref
	err := database.QueryRow(`SELECT background_model, background_provider FROM ui_prefs WHERE id = 1`).
		Scan(&pref.Model, &pref.ProviderID)
	if err == sql.ErrNoRows {
		return BackgroundModelPref{}, nil
	}
	if err != nil {
		return BackgroundModelPref{}, fmt.Errorf("get background model: %w", err)
	}
	pref.Model = strings.TrimSpace(pref.Model)
	pref.ProviderID = strings.TrimSpace(pref.ProviderID)
	if pref.Model == "" {
		pref.ProviderID = ""
	}
	return pref, nil
}

// SetBackgroundModel upserts the machine-wide background-tasks model preference
// on the singleton ui_prefs row. Passing an empty model clears the override so
// behind-the-scenes calls inherit from the active session.
func SetBackgroundModel(database *db.DB, model, providerID string) error {
	if database == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	providerID = strings.TrimSpace(providerID)
	if model == "" {
		providerID = ""
	}
	_, err := database.Exec(`
		INSERT INTO ui_prefs (id, background_model, background_provider, time_updated)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			background_model    = excluded.background_model,
			background_provider = excluded.background_provider,
			time_updated        = excluded.time_updated
	`, model, providerID, Now())
	if err != nil {
		return fmt.Errorf("set background model: %w", err)
	}
	return nil
}

