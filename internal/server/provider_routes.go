package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

var knownProviders = []string{"anthropic", "openai", "openrouter", "ollama"}

// providerEnvVars maps each provider ID to the environment variables that
// configure its key and base URL. These are checked at runtime so the UI
// can show the correct "configured via env" state even when the DB is empty.
var providerEnvVars = map[string]struct{ key, baseURL string }{
	"anthropic":  {key: "ANTHROPIC_API_KEY", baseURL: "ANTHROPIC_BASE_URL"},
	"openai":     {key: "OPENAI_API_KEY", baseURL: "OPENAI_BASE_URL"},
	"openrouter": {key: "OPENROUTER_API_KEY"},
	"ollama":     {key: "OLLAMA_API_KEY", baseURL: "OLLAMA_BASE_URL"},
}

// providerConfigResponse extends ProviderConfig with env-sourced status flags.
type providerConfigResponse struct {
	ProviderID string `json:"providerId"`
	APIKey     string `json:"apiKey"`  // "__SET__" if stored in DB, "" otherwise
	BaseURL    string `json:"baseUrl"` // the persisted value — what the edit form shows
	// EffectiveBaseURL is the endpoint the registered provider is actually
	// talking to right now. It differs from BaseURL whenever an env var
	// overrides the stored config, or when detection resolved a live endpoint
	// in place of a stale one — cases where showing only the persisted value
	// tells the user the app is calling somewhere it is not.
	EffectiveBaseURL string `json:"effectiveBaseUrl"`
	UpdatedAt        int64  `json:"updatedAt"`
	EnvKeySet        bool   `json:"envKeySet"`     // PROVIDER_API_KEY env var is present
	EnvBaseURLSet    bool   `json:"envBaseURLSet"` // PROVIDER_BASE_URL env var is present
}

// baseURLReporter is implemented by providers that talk to a configurable
// endpoint (the OpenAI-compatible ones). Used to report the live endpoint
// without the route needing to know concrete provider types.
type baseURLReporter interface {
	BaseURL() string
}

// effectiveBaseURL returns the endpoint the registered provider is using, or
// "" when the provider is not registered or does not expose one.
func (s *Server) effectiveBaseURL(providerID string) string {
	if s.registry == nil {
		return ""
	}
	p := s.registry.Get(providerID)
	if p == nil {
		return ""
	}
	if r, ok := p.(baseURLReporter); ok {
		return r.BaseURL()
	}
	return ""
}

// ollamaStatusResponse is the lightweight Ollama runtime status surfaced to the
// frontend so the onboarding gate can treat a running instance as already
// configured (zero-config flow) and show an appropriate detection state.
type ollamaStatusResponse struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	BaseURL   string `json:"baseUrl"`
}

// handleGetProviderConfigs returns masked credentials for all known providers,
// including whether each is configured via environment variable.
func (s *Server) handleGetProviderConfigs(w http.ResponseWriter, r *http.Request) {
	var out []providerConfigResponse
	for _, id := range knownProviders {
		cfg, err := session.GetProviderConfig(s.globalDB, id)
		if err != nil {
			http.Error(w, "failed to read provider config", http.StatusInternalServerError)
			return
		}
		masked := session.MaskedProviderConfig(cfg)

		envVars := providerEnvVars[id]
		resp := providerConfigResponse{
			ProviderID:       masked.ProviderID,
			APIKey:           masked.APIKey,
			BaseURL:          masked.BaseURL,
			EffectiveBaseURL: s.effectiveBaseURL(id),
			UpdatedAt:        masked.UpdatedAt,
			EnvKeySet:        os.Getenv(envVars.key) != "",
			EnvBaseURLSet:    envVars.baseURL != "" && os.Getenv(envVars.baseURL) != "",
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetProviderConfig upserts credentials for a single provider.
func (s *Server) handleSetProviderConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var incoming session.ProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	incoming.ProviderID = id

	// Preserve existing API key when the sentinel is sent.
	existing, err := session.GetProviderConfig(s.globalDB, id)
	if err != nil {
		http.Error(w, "failed to read provider config", http.StatusInternalServerError)
		return
	}
	if incoming.APIKey == "__SET__" {
		incoming.APIKey = existing.APIKey
	}

	if err := session.SetProviderConfig(s.globalDB, &incoming); err != nil {
		http.Error(w, "failed to save provider config", http.StatusInternalServerError)
		return
	}
	// Apply the new credentials to the running server so they take effect
	// without requiring a restart.
	s.reloadProviders()
	writeJSON(w, http.StatusOK, session.MaskedProviderConfig(&incoming))
}

// handleValidateProviderConfig tests whether the supplied credentials work by
// making a minimal request to the provider. It never persists anything. The
// "__SET__" sentinel resolves to the stored key so a saved provider can be
// re-tested without re-entering the key. Always responds 200 with
// {ok, error?} so the UI can render the outcome inline.
func (s *Server) handleValidateProviderConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var incoming session.ProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	apiKey := incoming.APIKey
	if apiKey == "__SET__" {
		existing, err := session.GetProviderConfig(s.globalDB, id)
		if err != nil {
			http.Error(w, "failed to read provider config", http.StatusInternalServerError)
			return
		}
		apiKey = existing.APIKey
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := provider.ValidateCredentials(ctx, id, apiKey, incoming.BaseURL); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOllamaStatus reports whether a local Ollama install is detected
// (binary on $PATH) and whether the server is currently running (liveness
// probe). The frontend uses this to satisfy the onboarding gate without
// forcing the user through the API-key wizard when Ollama is already running.
func (s *Server) handleOllamaStatus(w http.ResponseWriter, r *http.Request) {
	st := provider.DetectOllama()
	writeJSON(w, http.StatusOK, ollamaStatusResponse{
		Installed: st.Installed,
		Running:   st.Running,
		BaseURL:   st.BaseURL,
	})
}

// freeProviderResponse describes one active free-tier provider from the
// community key pool. Keys are never sent to the frontend.
type freeProviderResponse struct {
	Collection   string `json:"collection"`
	BaseURL      string `json:"baseUrl"`
	DefaultModel string `json:"defaultModel"`
}

// handleFreeProviders lists the active free-tier providers sourced from the
// shared community key pool (a public GitHub-hosted JSON). The frontend uses
// this to satisfy the onboarding gate — when any free provider is available,
// the user can start chatting immediately without configuring their own keys.
// Keys are never exposed; only the collection name, base URL, and default
// model are returned.
func (s *Server) handleFreeProviders(w http.ResponseWriter, r *http.Request) {
	defs := provider.FreeProviderList()
	out := make([]freeProviderResponse, 0, len(defs))
	for _, d := range defs {
		out = append(out, freeProviderResponse{
			Collection:   d.Collection,
			BaseURL:      d.BaseURL,
			DefaultModel: d.DefaultModel,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
