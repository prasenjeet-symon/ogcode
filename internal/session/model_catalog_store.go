package session

import (
	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// CatalogModel is one entry of a provider's persisted model catalogue: the
// curated list that provider last reported, kept so a fresh process can populate
// the picker before its first background refresh finishes. It carries the
// ModelInfo fields that survive a round trip — the Default flag is not stored
// because it is recomputed per render from the provider's own default.
type CatalogModel struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ProviderID      string  `json:"providerId"`
	ActiveByDefault bool    `json:"activeByDefault"`
	SupportsImages  bool    `json:"supportsImages"`
	ContextWindow   int     `json:"contextWindow"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
	Collection      string  `json:"collection"`
	InputPricePerM  float64 `json:"inputPricePerM"`
	OutputPricePerM float64 `json:"outputPricePerM"`
}

// GetModelCatalog returns every persisted catalogue entry, across all providers.
func GetModelCatalog(database *db.DB) ([]CatalogModel, error) {
	rows, err := database.Query(
		`SELECT provider_id, model_id, name, active_by_default, supports_images,
		        context_window, max_output_tokens, collection,
		        input_price_per_m, output_price_per_m
		 FROM model_catalog`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CatalogModel
	for rows.Next() {
		var m CatalogModel
		var active, images int
		if err := rows.Scan(
			&m.ProviderID, &m.ID, &m.Name, &active, &images,
			&m.ContextWindow, &m.MaxOutputTokens, &m.Collection,
			&m.InputPricePerM, &m.OutputPricePerM,
		); err != nil {
			return nil, err
		}
		m.ActiveByDefault = active == 1
		m.SupportsImages = images == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetModelCatalog replaces one provider's persisted catalogue with models. The
// delete-then-insert runs in a transaction so a reader never sees the catalogue
// half-written, and a model the endpoint stopped listing is dropped rather than
// lingering from a previous refresh. An empty models clears the provider.
func SetModelCatalog(database *db.DB, providerID string, models []CatalogModel) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM model_catalog WHERE provider_id = ?`, providerID); err != nil {
		return err
	}
	now := Now()
	for _, m := range models {
		active, images := 0, 0
		if m.ActiveByDefault {
			active = 1
		}
		if m.SupportsImages {
			images = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO model_catalog
			   (provider_id, model_id, name, active_by_default, supports_images,
			    context_window, max_output_tokens, collection,
			    input_price_per_m, output_price_per_m, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			providerID, m.ID, m.Name, active, images,
			m.ContextWindow, m.MaxOutputTokens, m.Collection,
			m.InputPricePerM, m.OutputPricePerM, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteModelCatalog removes a provider's persisted catalogue, so the picker
// falls back to the static list until the next refresh. Used when a provider is
// unregistered (its credentials were cleared), so a stale catalogue cannot
// outlive the account it came from. A missing row is not an error.
func DeleteModelCatalog(database *db.DB, providerID string) error {
	_, err := database.Exec(`DELETE FROM model_catalog WHERE provider_id = ?`, providerID)
	return err
}
