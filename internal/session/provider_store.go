package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// ProviderConfig holds credentials for a single LLM provider stored in the DB.
type ProviderConfig struct {
	ProviderID string `json:"providerId"`
	APIKey     string `json:"apiKey"`
	BaseURL    string `json:"baseUrl"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// GetProviderConfig returns the stored config for a provider. Returns a zero-value
// config (empty fields) when no row exists.
func GetProviderConfig(database *db.DB, providerID string) (*ProviderConfig, error) {
	var c ProviderConfig
	var updated int64
	err := database.QueryRow(
		`SELECT provider_id, api_key, base_url, time_updated FROM provider_config WHERE provider_id = ?`,
		providerID,
	).Scan(&c.ProviderID, &c.APIKey, &c.BaseURL, &updated)
	if err == sql.ErrNoRows {
		return &ProviderConfig{ProviderID: providerID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider config: %w", err)
	}
	c.UpdatedAt = updated
	return &c, nil
}

// GetAllProviderConfigs returns stored configs for all providers.
func GetAllProviderConfigs(database *db.DB) ([]*ProviderConfig, error) {
	rows, err := database.Query(`SELECT provider_id, api_key, base_url, time_updated FROM provider_config`)
	if err != nil {
		return nil, fmt.Errorf("list provider configs: %w", err)
	}
	defer rows.Close()
	var out []*ProviderConfig
	for rows.Next() {
		var c ProviderConfig
		if err := rows.Scan(&c.ProviderID, &c.APIKey, &c.BaseURL, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// SetProviderConfig upserts a provider config row.
func SetProviderConfig(database *db.DB, c *ProviderConfig) error {
	_, err := database.Exec(`
		INSERT INTO provider_config (provider_id, api_key, base_url, time_updated)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET
			api_key      = excluded.api_key,
			base_url     = excluded.base_url,
			time_updated = excluded.time_updated
	`, c.ProviderID, c.APIKey, c.BaseURL, Now())
	if err != nil {
		return fmt.Errorf("set provider config: %w", err)
	}
	return nil
}

// DeleteProviderConfig removes a provider's stored credentials entirely — the
// only call that un-registers a vendor slot: upserting an empty row would
// leave the id in GetAllProviderConfigs and keep it surfaced in the settings
// list. Deleting an id with no row is a no-op, not an error — the outcome the
// caller wants already holds.
func DeleteProviderConfig(database *db.DB, providerID string) error {
	_, err := database.Exec(`DELETE FROM provider_config WHERE provider_id = ?`, providerID)
	if err != nil {
		return fmt.Errorf("delete provider config: %w", err)
	}
	return nil
}

// MaskedProviderConfig returns a copy with the API key replaced by a sentinel
// so it can be sent to the UI without leaking the real value.
func MaskedProviderConfig(c *ProviderConfig) *ProviderConfig {
	mc := *c
	if mc.APIKey != "" {
		mc.APIKey = "__SET__"
	}
	return &mc
}
