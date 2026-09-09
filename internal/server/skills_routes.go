package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
)

// skillSummary is the shape the web UI needs to render a skill card. It carries
// the fields a user can discover a skill by — name, description, and where it
// came from — and omits the body and disk paths, which only the agent needs.
type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	// Enabled is false when the skill is denied by configuration. A denied skill
	// is withheld from the agent's prompt entirely — its frontmatter costs no
	// tokens — but the settings screen still lists it, switch off, so it can be
	// turned back on. That is why this endpoint returns skills the agent is never
	// told about; Enabled is how the UI tells the two states apart.
	Enabled bool `json:"enabled"`
}

func summarizeSkill(reg *skill.Registry, s skill.Skill) skillSummary {
	return skillSummary{
		Name:        s.Name,
		Description: s.Description,
		Source:      string(s.Source),
		Enabled:     reg.Action(s.Name) != skill.Deny,
	}
}

// handleListSkills returns every skill discoverable in the project directory,
// including built-ins, remote, anything on disk — and, unlike what the agent is
// told, including denied ones. The settings screen needs to show a disabled
// skill so it can be switched back on; each summary's Enabled flag carries the
// state.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillLoader == nil {
		writeJSON(w, http.StatusOK, []skillSummary{})
		return
	}
	reg := s.skillLoader.Load(s.dir)
	all := reg.List()
	out := make([]skillSummary, 0, len(all))
	for _, sk := range all {
		out = append(out, summarizeSkill(reg, sk))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetSkillEnabled turns one skill on or off for this project. It writes a
// permission rule into the project-local ogcode.json and syncs the live loader,
// so the change takes effect on the next turn without a restart.
//
// Off writes a "deny" for the skill's exact name — that is what drops it from
// the prompt. On removes that deny; if a broader rule (a glob, or the global
// config) still denies the skill afterwards, an explicit "allow" for the exact
// name is written, since an exact name outranks any glob. So the switch always
// lands the state it shows.
func (s *Server) handleSetSkillEnabled(w http.ResponseWriter, r *http.Request) {
	if s.skillLoader == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "skills are not configured"})
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill name is required"})
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// The skill must exist before we write a rule for it: a rule naming a skill
	// that is not there is dead config the user cannot see failing.
	reg := s.skillLoader.Load(s.dir)
	sk, ok := reg.Get(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no skill named " + name})
		return
	}

	perms, _ := config.ProjectSkillPermissions(s.dir)
	if body.Enabled {
		delete(perms, name)
	} else {
		perms[name] = string(skill.Deny)
	}
	if err := s.applySkillPermissions(perms); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Re-enabling has to win over a broader deny. If the skill is still denied
	// after removing its own rule, a glob or the global config is denying it, so
	// pin an explicit allow for the exact name.
	if body.Enabled {
		if reg = s.skillLoader.Load(s.dir); reg.Action(name) == skill.Deny {
			perms[name] = string(skill.Allow)
			if err := s.applySkillPermissions(perms); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}

	reg = s.skillLoader.Load(s.dir)
	writeJSON(w, http.StatusOK, summarizeSkill(reg, sk))
}

// applySkillPermissions persists perms to the project ogcode.json and syncs the
// live loader with the freshly merged (global + project) result, so the file on
// disk and the running process agree.
func (s *Server) applySkillPermissions(perms map[string]string) error {
	if _, err := config.SetProjectSkillPermissions(s.dir, perms); err != nil {
		return err
	}
	s.skillLoader.SetPermissions(config.Load(s.dir).Skills.Permissions)
	return nil
}
