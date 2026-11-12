package server

import (
	"encoding/json"
	"net/http"
)

// handleVersion returns current version and update information. It always
// answers 200: the installed version is local, so a failed update check is
// reported in the body's updateError — and the update fields then carry the
// last release known — rather than by withholding the whole response.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	response, _ := s.versionManager.GetResponse(r.Context())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleVersionCheck forces a fresh update check (ignoring cache). A failed
// check is a failed request — the caller asked for the current release and we
// could not reach it — so it answers 503. The body still carries the version
// info and the last release known, and the failure is also in updateError.
func (s *Server) handleVersionCheck(w http.ResponseWriter, r *http.Request) {
	s.versionManager.ClearCache()
	response, err := s.versionManager.GetResponse(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(response)
}
