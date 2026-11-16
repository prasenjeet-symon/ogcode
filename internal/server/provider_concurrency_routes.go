package server

import (
	"encoding/json"
	"net/http"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// concurrencyPayload is the wire shape for the machine-wide provider request
// ceiling. Value is the ceiling in force, which is the environment's when envSet
// is true — that is what the settings screen shows read-only.
type concurrencyPayload struct {
	Value        int  `json:"value"`
	EnvSet       bool `json:"envSet"`
	Min          int  `json:"min"`
	DefaultValue int  `json:"defaultValue"`
}

// concurrencyState reports the ceiling the process budget is using right now.
func (s *Server) concurrencyState() concurrencyPayload {
	return concurrencyPayload{
		Value:        provider.MaxConcurrentRequests(),
		EnvSet:       provider.ConcurrencyFromEnv(),
		Min:          provider.MinConcurrentRequests,
		DefaultValue: provider.DefaultMaxConcurrentRequests,
	}
}

// handleGetProviderConcurrency reports the ceiling on in-flight provider
// requests. The setting is global (one machine-wide number for the process), so
// it is read from the global config DB and the live budget.
func (s *Server) handleGetProviderConcurrency(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.concurrencyState())
}

// handleSetProviderConcurrency stores a new ceiling and applies it to the
// running budget without a restart. Zero clears the stored value back to the
// default. While the environment names a ceiling nothing is stored: the env var
// wins, and the response says so.
func (s *Server) handleSetProviderConcurrency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value int `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Value != 0 && req.Value < provider.MinConcurrentRequests {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "at least two concurrent provider requests are required",
		})
		return
	}
	if !provider.ConcurrencyFromEnv() {
		if err := session.SetProviderConcurrency(s.globalDB, req.Value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save provider concurrency"})
			return
		}
		provider.ApplyConcurrency(req.Value)
	}
	writeJSON(w, http.StatusOK, s.concurrencyState())
}
