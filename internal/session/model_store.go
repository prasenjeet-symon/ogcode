package session

import (
	"database/sql"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func modelStore() *ModelPreferenceStore {
	return &ModelPreferenceStore{}
}

type ModelPreferenceStore struct{}

// GetModelPreferences returns all model preference overrides from the database.
func GetModelPreferences(database *db.DB) ([]*ModelPreference, error) {
	rows, err := database.Query(
		`SELECT id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated
		 FROM model_preference ORDER BY time_created ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prefs []*ModelPreference
	for rows.Next() {
		var p ModelPreference
		var enabled int
		var isCustom int
		var collection sql.NullString
		if err := rows.Scan(&p.ID, &enabled, &p.ProviderID, &p.DisplayName, &isCustom, &collection, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		p.IsCustom = isCustom == 1
		p.Collection = collection.String
		prefs = append(prefs, &p)
	}
	return prefs, nil
}

// SetModelPreference upserts a model preference into the database.
func SetModelPreference(database *db.DB, p *ModelPreference) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	isCustom := 0
	if p.IsCustom {
		isCustom = 1
	}
	_, err := database.Exec(
		`INSERT OR REPLACE INTO model_preference (id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, enabled, p.ProviderID, p.DisplayName, isCustom, p.Collection, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// DeleteModelPreference removes a model preference from the database.
func DeleteModelPreference(database *db.DB, id string) error {
	_, err := database.Exec(`DELETE FROM model_preference WHERE id = ?`, id)
	return err
}

// GetModelCapability returns the persisted capability record for a model.
// The second return value is false when no record exists (not yet probed).
func GetModelCapability(database *db.DB, modelID string) (*ModelCapability, bool, error) {
	row := database.QueryRow(
		`SELECT model_id, supports_images, probed_at FROM model_capability WHERE model_id = ?`,
		modelID,
	)
	var c ModelCapability
	var supportsImages int
	if err := row.Scan(&c.ModelID, &supportsImages, &c.ProbedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	c.SupportsImages = supportsImages == 1
	return &c, true, nil
}

// SetModelCapability upserts a probed capability record for a model.
func SetModelCapability(database *db.DB, c *ModelCapability) error {
	supportsImages := 0
	if c.SupportsImages {
		supportsImages = 1
	}
	_, err := database.Exec(
		`INSERT OR REPLACE INTO model_capability (model_id, supports_images, probed_at)
		 VALUES (?, ?, ?)`,
		c.ModelID, supportsImages, c.ProbedAt,
	)
	return err
}

// DeleteModelCapability clears a model's cached capability so it is re-probed on
// next use. An empty modelID clears every cached capability. Used by the
// manual-refresh path.
func DeleteModelCapability(database *db.DB, modelID string) error {
	if modelID == "" {
		_, err := database.Exec(`DELETE FROM model_capability`)
		return err
	}
	_, err := database.Exec(`DELETE FROM model_capability WHERE model_id = ?`, modelID)
	return err
}

// GetModelCacheSupport returns the persisted cache verdict for a model on a
// specific endpoint. The second return value is false when the pair has not
// been resolved yet.
func GetModelCacheSupport(database *db.DB, modelID, endpoint string) (string, bool, error) {
	row := database.QueryRow(
		`SELECT verdict FROM model_cache_support WHERE model_id = ? AND endpoint = ?`,
		modelID, endpoint,
	)
	var verdict string
	if err := row.Scan(&verdict); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return verdict, true, nil
}

// SetModelCacheSupport records a resolved cache verdict so later sessions skip
// the observation window entirely.
func SetModelCacheSupport(database *db.DB, modelID, endpoint, verdict string, observedAt int64) error {
	_, err := database.Exec(
		`INSERT OR REPLACE INTO model_cache_support (model_id, endpoint, verdict, observed_at)
		 VALUES (?, ?, ?, ?)`,
		modelID, endpoint, verdict, observedAt,
	)
	return err
}

// DeleteModelCacheSupport clears a persisted verdict so the pair is observed
// again on next use. An empty modelID clears every verdict — the escape hatch
// for an endpoint that changed behaviour under a stable URL.
func DeleteModelCacheSupport(database *db.DB, modelID string) error {
	if modelID == "" {
		_, err := database.Exec(`DELETE FROM model_cache_support`)
		return err
	}
	_, err := database.Exec(`DELETE FROM model_cache_support WHERE model_id = ?`, modelID)
	return err
}
