package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
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
		s.registry.RegisterCustomModel(input.ID, input.ProviderID)
		slog.Info("registered custom model", "id", input.ID, "provider", input.ProviderID, "collection", input.Collection)
	}

	now := session.Now()
	pref := &session.ModelPreference{
		ID:          input.ID,
		Enabled:     input.Enabled,
		ProviderID:  input.ProviderID,
		DisplayName: input.DisplayName,
		IsCustom:    input.IsCustom,
		Collection:  input.Collection,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	// Model preferences (custom definitions + enable/disable) are stored in the
	// global config DB so a custom model added in one project is available in all.
	if err := session.SetModelPreference(s.globalDB, pref); err != nil {
		slog.Error("set model preference", "err", err)
		http.Error(w, "failed to save preference", http.StatusInternalServerError)
		return
	}

	// Re-fetch models to return updated list
	s.handleModels(w, r)
}

func (s *Server) handleDeleteModelPreference(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Only allow deleting custom models
	prefs, _ := session.GetModelPreferences(s.globalDB)
	var isCustom bool
	for _, p := range prefs {
		if p.ID == id {
			isCustom = p.IsCustom
			break
		}
	}
	if !isCustom {
		http.Error(w, "can only delete custom models", http.StatusBadRequest)
		return
	}

	if err := session.DeleteModelPreference(s.globalDB, id); err != nil {
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
