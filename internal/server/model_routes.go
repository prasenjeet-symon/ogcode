package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func (s *Server) handleSetModelPreference(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID          string `json:"id"`
		ProviderID  string `json:"providerId"`
		DisplayName string `json:"displayName"`
		Enabled     bool   `json:"enabled"`
		IsCustom    bool   `json:"isCustom"`
		Collection  string `json:"collection"`
		// What the user knows about the model — a window, output ceiling, price
		// and image support that stand in for, and win over, whatever a catalogue
		// or a provider's listing says. Zero means "not configured", so those
		// facts still answer.
		ContextWindow       int     `json:"contextWindow"`
		MaxOutputTokens     int     `json:"maxOutputTokens"`
		InputPricePerM      float64 `json:"inputPricePerM"`
		OutputPricePerM     float64 `json:"outputPricePerM"`
		CacheReadPricePerM  float64 `json:"cacheReadPricePerM"`
		CacheWritePricePerM float64 `json:"cacheWritePricePerM"`
		SupportsImages      bool    `json:"supportsImages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// For custom models, validate provider exists
	if input.IsCustom {
		if input.ProviderID == "" {
			http.Error(w, "providerId is required for custom models", http.StatusBadRequest)
			return
		}
		if s.registry.Get(input.ProviderID) == nil {
			http.Error(w, "provider not available", http.StatusBadRequest)
			return
		}
	}

	now := session.Now()
	pref := &session.ModelPreference{
		ID:                  input.ID,
		Enabled:             input.Enabled,
		ProviderID:          input.ProviderID,
		DisplayName:         input.DisplayName,
		IsCustom:            input.IsCustom,
		Collection:          input.Collection,
		ContextWindow:       input.ContextWindow,
		MaxOutputTokens:     input.MaxOutputTokens,
		InputPricePerM:      input.InputPricePerM,
		OutputPricePerM:     input.OutputPricePerM,
		CacheReadPricePerM:  input.CacheReadPricePerM,
		CacheWritePricePerM: input.CacheWritePricePerM,
		SupportsImages:      input.SupportsImages,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	// Model preferences (custom definitions + enable/disable) are stored in the
	// global config DB so a custom model added in one project is available in all.
	if err := session.SetModelPreference(s.globalDB, pref); err != nil {
		slog.Error("set model preference", "err", err)
		http.Error(w, "failed to save preference", http.StatusInternalServerError)
		return
	}

	// Tell the registry what the user configured before answering, so the list
	// this response carries already reflects it.
	s.registerModelConfig(pref)
	if pref.IsCustom {
		slog.Info("registered custom model", "id", pref.ID, "provider", pref.ProviderID, "collection", pref.Collection,
			"contextWindow", pref.ContextWindow, "maxOutputTokens", pref.MaxOutputTokens)
	}

	// Re-fetch models to return updated list
	s.handleModels(w, r)
}

// registerModelConfig hands the registry what the user configured about a model,
// from a preference row. A custom model's record is its whole definition — it is
// not in any catalogue, so the user's figures are all there is. On a model a
// catalogue or a provider already lists the same facts are an override layered
// over theirs: routing, naming and every field the user left alone still answer
// exactly as before, and a record that configures nothing is no override at all
// (the user cleared it). The registry is the one copy of these numbers, so the
// loop's request path, the context meter and price accounting cannot disagree.
func (s *Server) registerModelConfig(p *session.ModelPreference) {
	info := provider.CustomModelInfo{
		ProviderID:          p.ProviderID,
		ContextWindow:       p.ContextWindow,
		MaxOutputTokens:     p.MaxOutputTokens,
		InputPricePerM:      p.InputPricePerM,
		OutputPricePerM:     p.OutputPricePerM,
		CacheReadPricePerM:  p.CacheReadPricePerM,
		CacheWritePricePerM: p.CacheWritePricePerM,
		SupportsImages:      p.SupportsImages,
	}
	if p.IsCustom {
		s.registry.RegisterCustomModelInfo(p.ID, info)
		return
	}
	s.registry.SetModelOverride(p.ID, info)
}

func (s *Server) handleDeleteModelPreference(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	providerID := r.URL.Query().Get("providerId")

	// Only allow deleting custom models. Match the provider too when the
	// caller said which: the same id can exist as a listed row under another
	// provider, and first-match-by-id alone would misclassify it.
	prefs, _ := session.GetModelPreferences(s.globalDB)
	var isCustom bool
	for _, p := range prefs {
		if p.ID == id && (providerID == "" || p.ProviderID == providerID) {
			isCustom = p.IsCustom
			break
		}
	}
	if !isCustom {
		http.Error(w, "can only delete custom models", http.StatusBadRequest)
		return
	}

	if err := session.DeleteModelPreference(s.globalDB, id, providerID); err != nil {
		slog.Error("delete model preference", "err", err)
		http.Error(w, "failed to delete preference", http.StatusInternalServerError)
		return
	}

	s.registry.UnregisterCustomModel(id)
	slog.Info("unregistered custom model", "id", id)

	w.WriteHeader(http.StatusNoContent)
}

// handleClearModelCapability clears a model's cached image-support result so it
// is re-probed on next use. The model ID is taken from the JSON body because IDs
// can contain slashes (e.g. "openai/gpt-4o"), which path params don't handle.
// An empty modelId clears all cached capabilities.
func (s *Server) handleClearModelCapability(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ModelID string `json:"modelId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := session.DeleteModelCapability(s.db, input.ModelID); err != nil {
		slog.Error("clear model capability", "err", err)
		http.Error(w, "failed to clear capability", http.StatusInternalServerError)
		return
	}
	slog.Info("cleared cached model capability", "model", input.ModelID)
	w.WriteHeader(http.StatusNoContent)
}
