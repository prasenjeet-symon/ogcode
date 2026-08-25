package skill

import "sort"

// Registry is the set of skills resolved for one project directory, together
// with the permission rules that apply to them.
//
// It is built fresh by Loader.Load and never mutated afterwards, so the prompt
// listing and the skill tool always read the same snapshot.
type Registry struct {
	rules  Rules
	skills map[string]Skill
}

// NewRegistry returns an empty registry governed by rules.
func NewRegistry(rules Rules) *Registry {
	return &Registry{rules: rules, skills: map[string]Skill{}}
}

// Register adds a skill, replacing any skill already registered under the same
// name. Sources are added least-specific first — built-in, then remote, then
// global, then config paths, then the project — so the closest one to the user
// wins, and a project skill can shadow a built-in of the same name.
func (r *Registry) Register(s Skill) {
	r.skills[s.Name] = s
}

// Get returns the skill by name.
func (r *Registry) Get(name string) (Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// Action returns the configured permission action for a name.
func (r *Registry) Action(name string) Action {
	return r.rules.Evaluate(name)
}

// List returns every registered skill, sorted by name.
func (r *Registry) List() []Skill {
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Visible returns the skills the agent may be told about, sorted by name.
// Denied skills are withheld: naming one in the prompt would advertise a call
// that is refused the moment it is made.
func (r *Registry) Visible() []Skill {
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		if r.rules.Evaluate(s.Name) == Deny {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Len reports how many skills are registered, denied ones included.
func (r *Registry) Len() int { return len(r.skills) }
