package server

import (
	"net/http"

	"github.com/prasenjeet-symon/ogcode/internal/resource"
)

// handleResources returns the retained resource-usage window. The SSE stream
// carries live samples one at a time, so this exists to give a freshly mounted
// client its history in one shot rather than making it watch a graph fill in
// from empty.
func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	if s.resources == nil {
		writeJSON(w, http.StatusOK, resource.Snapshot{Samples: []resource.Sample{}})
		return
	}
	writeJSON(w, http.StatusOK, s.resources.Snapshot())
}
