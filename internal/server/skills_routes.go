package server

import (
	"net/http"
)

// skillSummary is the shape the web UI needs to render a skill card. It carries
// the fields a user can discover a skill by — name, description, and where it
// came from — and omits the body and disk paths, which only the agent needs.
type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// handleListSkills returns every skill discoverable in the project directory,
// including built-ins, remote, and anything on disk. Denied skills are
// withheld, matching what the agent itself is told about: advertising one the
// model can never call would only confuse.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillLoader == nil {
		writeJSON(w, http.StatusOK, []skillSummary{})
		return
	}
	reg := s.skillLoader.Load(s.dir)
	visible := reg.Visible()
	out := make([]skillSummary, 0, len(visible))
	for _, sk := range visible {
		out = append(out, skillSummary{
			Name:        sk.Name,
			Description: sk.Description,
			Source:      string(sk.Source),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
