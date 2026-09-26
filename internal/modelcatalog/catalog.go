// Package modelcatalog persists the model catalogue each provider last reported
// and seeds a provider registry's in-memory catalogue from that copy.
//
// It exists because the provider package must not import the session store (the
// dependency runs the other way) and the server is not the only entry point that
// builds a registry: `ogcode index` and a hosted worker do too, and each needs
// its picker populated before its first network fetch. Keeping the conversions
// and the seed/persist logic here means all three entry points share one
// implementation rather than three drift-prone copies.
package modelcatalog

import (
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// toStored converts a provider's live catalogue into storable rows.
func toStored(models []provider.ModelInfo) []session.CatalogModel {
	out := make([]session.CatalogModel, 0, len(models))
	for _, m := range models {
		out = append(out, session.CatalogModel{
			ID:              m.ID,
			Name:            m.Name,
			ProviderID:      m.ProviderID,
			ActiveByDefault: m.ActiveByDefault,
			SupportsImages:  m.SupportsImages,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			Collection:      m.Collection,
			InputPricePerM:  m.InputPricePerM,
			OutputPricePerM: m.OutputPricePerM,
		})
	}
	return out
}

// toProvider converts persisted rows back into the in-memory shape.
func toProvider(rows []session.CatalogModel) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(rows))
	for _, m := range rows {
		out = append(out, provider.ModelInfo{
			ID:              m.ID,
			Name:            m.Name,
			ProviderID:      m.ProviderID,
			ActiveByDefault: m.ActiveByDefault,
			SupportsImages:  m.SupportsImages,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			Collection:      m.Collection,
			InputPricePerM:  m.InputPricePerM,
			OutputPricePerM: m.OutputPricePerM,
		})
	}
	return out
}

// Seed populates every registered provider's in-memory catalogue from the
// persisted copy, so Models() answers with the last known list immediately on
// startup instead of waiting for a background fetch. A read failure is returned
// with the seed left partial — the caller logs it and the providers simply fall
// back to their compiled-in lists.
//
// Seed only ever ADDS to a provider's catalogue; it never deletes rows. A
// registry built from a subset of the configured providers (the headless CLI
// registers only the ones its environment names) must not be able to wipe
// another entry point's stored catalogue — see Prune for the deliberate removal,
// which only a registry holding the full provider set may call.
func Seed(registry *provider.Registry, database *db.DB) error {
	if registry == nil || database == nil {
		return nil
	}
	rows, err := session.GetModelCatalog(database)
	if err != nil {
		return err
	}
	registered := make(map[string]bool)
	for _, id := range registry.List() {
		registered[id] = true
	}
	byProvider := make(map[string][]session.CatalogModel)
	for _, m := range rows {
		if !registered[m.ProviderID] {
			continue
		}
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], m)
	}
	for id, models := range byProvider {
		p := registry.Get(id)
		setter, ok := p.(provider.CatalogSetter)
		if !ok {
			continue
		}
		setter.SetCatalog(toProvider(models))
	}
	return nil
}

// Prune drops the stored catalogue of every provider the registry does not hold,
// so a link that was disconnected (or a key that was cleared) does not leave a
// list that would reappear — stale, from a previous account — the moment the
// provider is re-added. Call it only with a registry that holds the FULL
// configured provider set (the server's), never with a partial one.
func Prune(registry *provider.Registry, database *db.DB) error {
	if registry == nil || database == nil {
		return nil
	}
	rows, err := session.GetModelCatalog(database)
	if err != nil {
		return err
	}
	registered := make(map[string]bool)
	for _, id := range registry.List() {
		registered[id] = true
	}
	for _, m := range rows {
		if registered[m.ProviderID] {
			continue
		}
		if err := session.DeleteModelCatalog(database, m.ProviderID); err != nil {
			return err
		}
	}
	return nil
}

// Persist writes each refreshed catalogue back to the database, so the next
// process start seeds from it. Rows are replaced per provider, so a model the
// endpoint stopped serving disappears rather than lingering.
func Persist(database *db.DB, refreshed map[string][]provider.ModelInfo) error {
	if database == nil {
		return nil
	}
	for id, models := range refreshed {
		if err := session.SetModelCatalog(database, id, toStored(models)); err != nil {
			return err
		}
	}
	return nil
}
