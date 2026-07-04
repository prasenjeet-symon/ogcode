package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/git"
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
		{"id": "plan", "name": "Plan", "description": "Planning agent — reads and understands code, plans changes but never writes"},
	})
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"mode": string(s.mode),
	})
}

// handleGitSync reports whether the working directory's current branch is in
// sync with its upstream (best-effort fetch first). Used by plan mode to warn
// when the active branch is behind the remote before tasks branch from it.
func (s *Server) handleGitSync(w http.ResponseWriter, r *http.Request) {
	st, err := git.BranchSyncStatus(r.Context(), s.dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	builtIn := s.registry.ListModels()

	prefs, _ := session.GetModelPreferences(s.db)
	prefMap := make(map[string]*session.ModelPreference)
	for _, p := range prefs {
		prefMap[p.ID] = p
	}

	availableProviders := make(map[string]bool)
	for _, id := range s.registry.List() {
		availableProviders[id] = true
	}

	type ModelEntry struct {
		ID              string  `json:"id"`
		Name            string  `json:"name"`
		ProviderID      string  `json:"providerId"`
		Default         bool    `json:"default"`
		Enabled         bool    `json:"enabled"`
		IsCustom        bool    `json:"isCustom"`
		InputPricePerM  float64 `json:"inputPricePerM"`
		OutputPricePerM float64 `json:"outputPricePerM"`
		SupportsImages  bool    `json:"supportsImages"`
	}

	var result []ModelEntry
	for _, m := range builtIn {
		defaultEnabled := m.ActiveByDefault
		if pref, ok := prefMap[m.ID]; ok {
			defaultEnabled = pref.Enabled
		}
		// Prefer a probed/cached capability; otherwise fall back to the catalog
		// or heuristic value. Never probes here — this is a read-only listing.
		supportsImages := m.SupportsImages
		if cap, ok, err := session.GetModelCapability(s.db, m.ID); err == nil && ok {
			supportsImages = cap.SupportsImages
		}
		entry := ModelEntry{
			ID:              m.ID,
			Name:            m.Name,
			ProviderID:      m.ProviderID,
			Default:         m.Default,
			Enabled:         defaultEnabled,
			IsCustom:        false,
			InputPricePerM:  m.InputPricePerM,
			OutputPricePerM: m.OutputPricePerM,
			SupportsImages:  supportsImages,
		}
		result = append(result, entry)
	}

	for _, p := range prefs {
		if !p.IsCustom {
			continue
		}
		if !availableProviders[p.ProviderID] && !p.Enabled {
			continue
		}
		result = append(result, ModelEntry{
			ID:         p.ID,
			Name:       p.DisplayName,
			ProviderID: p.ProviderID,
			Default:    false,
			Enabled:    p.Enabled,
			IsCustom:   true,
		})
	}

	if result == nil {
		result = []ModelEntry{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) configPayload() map[string]any {
	memoryEnabled := s.mem != nil && s.mem.Enabled()
	memoryProvider := ""
	if s.mem != nil && s.mem.Graph != nil && s.mem.Graph.Embed != nil {
		memoryProvider = "ogcode-embedded"
	}
	mcpEnabled := s.mcpClient != nil
	mcpProvider := ""
	if mcpEnabled {
		mcpProvider = s.mcpCfg.Command
	}
	return map[string]any{
		"directory":       s.dir,
		"port":            s.port,
		"memoryEnabled":   memoryEnabled,
		"memoryProvider":  memoryProvider,
		"mcpEnabled":      mcpEnabled,
		"mcpProvider":     mcpProvider,
		"searchEnabled":   s.searchBridge != nil,
		"searchRunning":   s.searchBridge != nil,
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.configPayload())
}

func (s *Server) handleGetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := session.GetMemoryConfig(s.globalDB)
	if err != nil {
		http.Error(w, "failed to read memory config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, session.MaskedMemoryConfig(cfg))
}

func (s *Server) handleSetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	var incoming session.MemoryConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := session.SetMemoryConfig(s.globalDB, &incoming); err != nil {
		http.Error(w, "failed to save memory config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, session.MaskedMemoryConfig(&incoming))
}

func (s *Server) handleGetSearchConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := session.GetSearchConfig(s.globalDB)
	if err != nil {
		http.Error(w, "failed to read search config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSetSearchConfig(w http.ResponseWriter, r *http.Request) {
	var incoming session.SearchConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := session.SetSearchConfig(s.globalDB, &incoming); err != nil {
		http.Error(w, "failed to save search config", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, &incoming)
}

func (s *Server) handleModelsRefresh(w http.ResponseWriter, r *http.Request) {
	s.registry.RefreshModels()
	// Return the updated model list
	s.handleModels(w, r)
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
