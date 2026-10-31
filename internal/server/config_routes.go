package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/search"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func (s *Server) handlePath(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, map[string]string{
		"home":      home,
		"directory": s.dir,
		"state":     s.dir + "/.ogcode",
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]string{
		{"id": "build", "name": "Build", "description": "Full-access coding agent"},
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	builtIn := s.registry.ListModels()

	// Model preferences live in the global config DB (shared across projects).
	// Keyed by (model id, provider id), not the id alone: the same model id can
	// be served by two providers — glm-5.3-flash exists under both OGX and a
	// custom OpenAI-compatible endpoint — and a toggle on one must not flip the
	// other, or a disable somewhere else would darken a plan model the user
	// never touched.
	prefs, _ := session.GetModelPreferences(s.globalDB)
	prefMap := make(map[string]*session.ModelPreference)
	for _, p := range prefs {
		prefMap[p.ID+"\x00"+p.ProviderID] = p
	}

	availableProviders := make(map[string]bool)
	for _, id := range s.registry.List() {
		availableProviders[id] = true
	}

	// ModelOverrideEntry is the user's own configuration for one model: the
	// figures they edited in, not the ones a catalogue or a provider supplied.
	// It rides the response so the settings editor can prefill, and clear,
	// exactly their own numbers.
	type ModelOverrideEntry struct {
		ContextWindow       int     `json:"contextWindow,omitempty"`
		MaxOutputTokens     int     `json:"maxOutputTokens,omitempty"`
		InputPricePerM      float64 `json:"inputPricePerM,omitempty"`
		OutputPricePerM     float64 `json:"outputPricePerM,omitempty"`
		CacheReadPricePerM  float64 `json:"cacheReadPricePerM,omitempty"`
		CacheWritePricePerM float64 `json:"cacheWritePricePerM,omitempty"`
		SupportsImages      bool    `json:"supportsImages,omitempty"`
	}
	type ModelEntry struct {
		ID                  string  `json:"id"`
		Name                string  `json:"name"`
		ProviderID          string  `json:"providerId"`
		Default             bool    `json:"default"`
		Enabled             bool    `json:"enabled"`
		IsCustom            bool    `json:"isCustom"`
		Collection          string  `json:"collection"`
		InputPricePerM      float64 `json:"inputPricePerM"`
		OutputPricePerM     float64 `json:"outputPricePerM"`
		CacheReadPricePerM  float64 `json:"cacheReadPricePerM"`
		CacheWritePricePerM float64 `json:"cacheWritePricePerM"`
		SupportsImages      bool    `json:"supportsImages"`
		// ContextWindow is the window the agent loop sizes compaction against
		// (catalogue, else learned from an overflow; 0 = unknown), and
		// CompactAtTokens is the request size at which it compacts. The context
		// meter reads both, so it agrees with what the loop will actually do.
		ContextWindow   int `json:"contextWindow,omitempty"`
		CompactAtTokens int `json:"compactAtTokens"`
		// MaxOutputTokens is the most output the model produces in one response
		// (0 = unknown: the request carries no explicit limit).
		MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
		// Override is what the user configured about this model themselves — the
		// five facts the settings row lets them edit. Absent when they configured
		// nothing, so the row shows what the catalogue and the provider say.
		Override *ModelOverrideEntry `json:"override,omitempty"`
		// Efforts are the reasoning-effort levels the model takes on its
		// provider, lowest first (empty: no effort picker); DefaultEffort is the
		// one it runs at when none is chosen; EffortNote says why a model that
		// reasons offers no choice here.
		Efforts       []string `json:"efforts,omitempty"`
		DefaultEffort string   `json:"defaultEffort,omitempty"`
		EffortNote    string   `json:"effortNote,omitempty"`
	}
	// overrideOf reads the user's configuration for a model, or nil when they
	// set none. A custom model's record is its whole definition; on a listed
	// model the same facts are the override the registry layers over the
	// catalogue and the provider's listing.
	overrideOf := func(modelID, providerID string, custom bool) *ModelOverrideEntry {
		var info provider.CustomModelInfo
		var ok bool
		if custom {
			info, ok = s.registry.CustomModel(modelID)
		} else {
			info, ok = s.registry.ModelOverride(modelID, providerID)
		}
		if !ok || (info.ContextWindow <= 0 && info.MaxOutputTokens <= 0 &&
			info.InputPricePerM <= 0 && info.OutputPricePerM <= 0 &&
			info.CacheReadPricePerM <= 0 && info.CacheWritePricePerM <= 0 && !info.SupportsImages) {
			return nil
		}
		return &ModelOverrideEntry{
			ContextWindow:       info.ContextWindow,
			MaxOutputTokens:     info.MaxOutputTokens,
			InputPricePerM:      info.InputPricePerM,
			OutputPricePerM:     info.OutputPricePerM,
			CacheReadPricePerM:  info.CacheReadPricePerM,
			CacheWritePricePerM: info.CacheWritePricePerM,
			SupportsImages:      info.SupportsImages,
		}
	}
	windowOf := func(modelID string) (int, int) {
		w, _ := agent.EffectiveContextWindow(s.registry, s.db, modelID)
		return w, agent.CompactionThreshold(w)
	}
	withEffort := func(e ModelEntry) ModelEntry {
		spec := s.registry.EffortSpec(e.ProviderID, e.ID)
		e.Efforts, e.DefaultEffort, e.EffortNote = spec.Levels, spec.Default, spec.Note
		return e
	}

	var result []ModelEntry
	for _, m := range builtIn {
		defaultEnabled := m.ActiveByDefault
		if pref, ok := prefMap[m.ID+"\x00"+m.ProviderID]; ok {
			defaultEnabled = pref.Enabled
		}
		// Prefer a probed/cached capability; otherwise the built-in catalogue,
		// then the provider's own (often heuristic) value. Never probes here —
		// this is a read-only listing.
		supportsImages := m.SupportsImages
		if cm, ok := s.registry.CatalogModel(m.ID); ok {
			supportsImages = cm.SupportsImages
		}
		// What the user pays where the model runs: a provider's own listed price,
		// else the catalogue's; nothing for local and subscription providers.
		price, _ := s.registry.PriceOn(m.ProviderID, m.ID)
		if cap, ok, err := session.GetModelCapability(s.db, m.ID); err == nil && ok {
			supportsImages = cap.SupportsImages
		}
		// The user's own answer decides whether images are sent, so it wins over
		// both the catalogue and the probe — the same rule a custom model gets.
		override := overrideOf(m.ID, m.ProviderID, false)
		if override != nil && override.SupportsImages {
			supportsImages = true
		}
		window, compactAt := windowOf(m.ID)
		entry := ModelEntry{
			ID:                  m.ID,
			Name:                m.Name,
			ProviderID:          m.ProviderID,
			Default:             m.Default,
			Enabled:             defaultEnabled,
			IsCustom:            false,
			Collection:          m.Collection,
			InputPricePerM:      price.Input,
			OutputPricePerM:     price.Output,
			CacheReadPricePerM:  price.CacheRead,
			CacheWritePricePerM: price.CacheWrite,
			SupportsImages:      supportsImages,
			ContextWindow:       window,
			CompactAtTokens:     compactAt,
			MaxOutputTokens:     s.registry.MaxOutputTokens(m.ID),
			Override:            override,
		}
		result = append(result, withEffort(entry))
	}

	for _, p := range prefs {
		if !p.IsCustom {
			continue
		}
		if !availableProviders[p.ProviderID] && !p.Enabled {
			continue
		}
		// A custom model is whatever id the user typed, and the stored preference
		// is the source of truth for what they told us about it: the registry
		// already carries that record (RegisterCustomModelInfo), so PriceOn,
		// ContextWindow and MaxOutputTokens answer with the user's figures. Each
		// field falls back to a catalogue fact, the provider's listing or a
		// probed/cached result when the user said nothing.
		supportsImages := false
		if cm, ok := s.registry.CatalogModel(p.ID); ok {
			supportsImages = cm.SupportsImages
		}
		price, _ := s.registry.PriceOn(p.ProviderID, p.ID)
		if cap, ok, err := session.GetModelCapability(s.db, p.ID); err == nil && ok {
			supportsImages = cap.SupportsImages
		}
		// The user's own answer decides whether images are sent, so it wins over
		// both the catalogue and the probe.
		if p.SupportsImages {
			supportsImages = true
		}
		window, _ := windowOf(p.ID)
		if window <= 0 && p.ContextWindow > 0 {
			window = p.ContextWindow
		}
		maxOutput := s.registry.MaxOutputTokens(p.ID)
		if maxOutput <= 0 {
			maxOutput = p.MaxOutputTokens
		}
		if maxOutput <= 0 {
			if cm, ok := s.registry.CatalogModel(p.ID); ok {
				maxOutput = cm.MaxOutputTokens
			}
		}
		result = append(result, withEffort(ModelEntry{
			ID:                  p.ID,
			Name:                p.DisplayName,
			ProviderID:          p.ProviderID,
			Default:             false,
			Enabled:             p.Enabled,
			IsCustom:            true,
			Collection:          p.Collection,
			InputPricePerM:      price.Input,
			OutputPricePerM:     price.Output,
			CacheReadPricePerM:  price.CacheRead,
			CacheWritePricePerM: price.CacheWrite,
			SupportsImages:      supportsImages,
			ContextWindow:       window,
			CompactAtTokens:     agent.CompactionThreshold(window),
			MaxOutputTokens:     maxOutput,
			Override:            overrideOf(p.ID, p.ProviderID, true),
		}))
	}

	// Enforce a single global default so a new user's model selection is
	// deterministic. Every provider's Models() flags its own default model, which
	// would otherwise surface several "default" models and a nondeterministic
	// pick in the UI. Keep the flag only on the highest-priority registered
	// provider's default (see provider.ProviderPriority / Registry.DefaultUsable).
	defaultProviderID := ""
	if d := s.registry.DefaultUsable(); d != nil {
		defaultProviderID = d.ID()
	}
	for i := range result {
		if result[i].Default && result[i].ProviderID != defaultProviderID {
			result[i].Default = false
		}
	}

	if result == nil {
		result = []ModelEntry{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) configPayload() map[string]any {
	return map[string]any{
		"directory": s.dir,
		"port":      s.port.Load(),
		// os is the host the server runs on. The web UI uses it to show the
		// browser web-search options and their setup guides only where a browser
		// can actually be driven — Safari search runs osascript on macOS, Chrome
		// or Edge search runs a headless binary on Windows.
		"os":            runtime.GOOS,
		"searchEnabled": s.searchBackend != nil,
		"searchRunning": s.searchBackend != nil,
		// installId is the machine's PostHog distinct id — the website's when the
		// install script recorded one, otherwise a locally minted id — so it is
		// always set. The web UI adopts it so that download, install and first
		// session share one person.
		"installId":          s.installID,
		"notesEnabled":       s.notesEnabled.Load(),
		"devicePanelEnabled": s.devicePanelEnabled.Load(),
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.configPayload())
}

func (s *Server) handleGetSearchConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := session.GetSearchConfig(s.globalDB)
	if err != nil {
		http.Error(w, "failed to read search config", http.StatusInternalServerError)
		return
	}
	// Report whether the Tavily key is supplied by the environment so the UI can
	// show a "configured via TAVILY_API_KEY" state, mirroring the provider keys.
	resp := struct {
		*session.SearchConfig
		TavilyEnvKeySet bool `json:"tavilyEnvKeySet"`
	}{
		SearchConfig:    session.MaskedSearchConfig(cfg),
		TavilyEnvKeySet: os.Getenv("TAVILY_API_KEY") != "",
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetSearchConfig(w http.ResponseWriter, r *http.Request) {
	var incoming session.SearchConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// The pre-save config: used both to resolve the mask sentinel and to detect
	// whether the provider/key actually changed (so a slider tweak does not
	// needlessly rebuild the backend and drop the native engine's caches).
	existing, err := session.GetSearchConfig(s.globalDB)
	if err != nil {
		http.Error(w, "failed to read search config", http.StatusInternalServerError)
		return
	}
	// Preserve the stored key when the client echoes the mask sentinel, so
	// saving a provider change never wipes a key the UI never saw. Same
	// convention as handleSetProviderConfig.
	if incoming.TavilyAPIKey == session.MaskedAPIKey {
		incoming.TavilyAPIKey = existing.TavilyAPIKey
	}
	if err := session.SetSearchConfig(s.globalDB, &incoming); err != nil {
		http.Error(w, "failed to save search config", http.StatusInternalServerError)
		return
	}

	// Apply a provider or key change to the running backend without a restart.
	// The enable toggle is deliberately not handled here — it changes which tools
	// are registered, so it still needs a restart; searchSwitch is nil when
	// search was off at startup, which is exactly that case. SetSearchConfig has
	// normalised incoming (normalise), so the comparison uses canonical values.
	if s.searchSwitch != nil && (incoming.Provider != existing.Provider || incoming.TavilyAPIKey != existing.TavilyAPIKey || incoming.Browser != existing.Browser) {
		s.searchSwitch.Set(buildSearchBackend(&incoming))
		logSearchProvider("web search: provider switched live", &incoming)
	}

	writeJSON(w, http.StatusOK, session.MaskedSearchConfig(&incoming))
}

// handleValidateSearchKey tests whether a third-party search key works, without
// persisting anything. The mask sentinel resolves to the stored key so a saved
// provider can be re-tested without re-entering it. Always responds 200 with
// {ok, error?} so the UI can render the outcome inline. Provider selection is
// applied at startup, so this lets the user confirm a key before restarting.
func (s *Server) handleValidateSearchKey(w http.ResponseWriter, r *http.Request) {
	var incoming session.SearchConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	apiKey := incoming.TavilyAPIKey
	if apiKey == session.MaskedAPIKey || apiKey == "" {
		existing, err := session.GetSearchConfig(s.globalDB)
		if err != nil {
			http.Error(w, "failed to read search config", http.StatusInternalServerError)
			return
		}
		apiKey = existing.TavilyAPIKey
	}
	if env := os.Getenv("TAVILY_API_KEY"); apiKey == "" && env != "" {
		apiKey = env
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := search.ValidateTavilyKey(ctx, apiKey); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleModelsRefresh performs an explicit live refresh of every provider's
// catalogue. It runs the fetch through the same guarded path the background
// refresh uses, so it can never overlap another refresh — but unlike the
// background path it returns the refreshed list inline, since the caller asked
// for it and expects the new models in the response.
//
// Models() is a pure read, so the fetch happening here (rather than inside the
// listing) is what keeps GET /api/models instant. When another refresh already
// holds the guard, this returns the current list immediately: the in-flight one
// will publish models.updated when it lands.
func (s *Server) handleModelsRefresh(w http.ResponseWriter, r *http.Request) {
	s.refreshModelCatalogsNow(r.Context())
	s.handleModels(w, r)
}

// The upstream catalogue: a shared model list ogcode fetches from its catalogue
// endpoint and installs over every provider's own rows. handleCatalogStatus
// reports when it last landed; handleCatalogSync forces a fetch now and returns
// what that sync did, on the JSON error shape the rest of the routes use.
func (s *Server) handleCatalogStatus(w http.ResponseWriter, r *http.Request) {
	syncedAt, source, modelCount, ok := provider.UpstreamCatalogInfo()
	var when string
	if ok {
		when = syncedAt.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"synced":     ok,
		"syncedAt":   when,
		"source":     source,
		"modelCount": modelCount,
	})
}

// handleCatalogSync runs one upstream sync synchronously on the request — the
// caller pressed a button and wants the outcome, the same trade the manual
// /models/refresh path makes. Bounded by the refresh timeout. A failed fetch
// is a 502 with the error, never a half-flipped state; the kill switch is
// configuration, not an upstream failure, so it is a 409 instead.
func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), modelCatalogRefreshTimeout)
	defer cancel()
	res, err := provider.SyncUpstreamCatalog(ctx)
	if err != nil {
		if errors.Is(err, provider.ErrUpstreamSyncDisabled) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	_, _, modelCount, _ := provider.UpstreamCatalogInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"syncedAt":   res.FetchedAt.Format(time.RFC3339),
		"source":     res.Source,
		"installed":  res.Installed,
		"updated":    res.Updated,
		"skipped":    res.Skipped,
		"modelCount": modelCount,
	})
}

func (s *Server) handleVCS(w http.ResponseWriter, r *http.Request) {
	branch := getCurrentBranch(s.dir)
	isGitRepo := branch != ""
	hasRemote := isGitRepo && gitHasRemote(s.dir)
	ghInstalled := commandExists("gh")
	writeJSON(w, http.StatusOK, map[string]any{
		"branch":      branch,
		"isGitRepo":   isGitRepo,
		"hasRemote":   hasRemote,
		"ghInstalled": ghInstalled,
	})
}

func getCurrentBranch(dir string) string {
	out, err := execInDir(dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

func gitHasRemote(dir string) bool {
	out, err := execInDir(dir, "git", "remote")
	return err == nil && len(strings.TrimSpace(out)) > 0
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func execInDir(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	result := string(out)
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}
	return result, nil
}
