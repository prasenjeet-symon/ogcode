package provider

import "slices"

// Reasoning effort is how hard a model thinks before it answers: the user picks
// a level per session, and each turn of the agent loop asks the model for it.
// Every vendor names the levels its own way and takes them in its own request
// field — output_config.effort on Claude, reasoning_effort on OpenAI's Chat
// Completions and the hosts that copy it, a reasoning object on OpenRouter, an
// on/off switch on models that only have one — so ogcode keeps one vocabulary
// and each provider translates it.
//
// Two facts decide what a session can pick, and they are kept apart on
// purpose. What a MODEL accepts is a fact about the model, and lives in the
// catalogue (CatalogModel.Efforts). Whether an ENDPOINT can carry it is a fact
// about the host: the same gpt-oss takes reasoning_effort on Groq and on
// Ollama, but nothing at all through a host that ignores the field — or worse,
// rejects it. A provider combines the two in its EffortSpec, and offers a level
// only when both agree.

// The levels, in ogcode's vocabulary. Lowest first.
const (
	// EffortNone turns reasoning off.
	EffortNone = "none"
	// EffortOn turns reasoning on at the model's own depth. It is the only
	// level besides EffortNone for models that switch thinking on or off but
	// take no depth.
	EffortOn      = "on"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	// EffortXHigh sits between high and max (Claude Opus 4.7 and later, GPT-5.2
	// and later).
	EffortXHigh = "xhigh"
	EffortMax   = "max"
)

// effortOrder ranks every level, lowest first. EffortOn shares a rank with
// nothing it could be mistaken for: a model offers it only beside EffortNone.
var effortOrder = []string{EffortNone, EffortOn, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// IsEffort reports whether level is one of ogcode's effort levels.
func IsEffort(level string) bool {
	return slices.Contains(effortOrder, level)
}

// EffortSpec is what a session may pick for one model on one endpoint.
type EffortSpec struct {
	// Levels are the selectable levels, lowest first. Empty means the model
	// takes no effort here, and the picker is not offered.
	Levels []string `json:"levels,omitempty"`
	// Default is the level the endpoint applies when none is sent — what the
	// picker shows before the user chooses. Empty when it is not known.
	Default string `json:"default,omitempty"`
	// Note says why there is no choice, for a model the user would expect to
	// have one (it reasons, but not on request through this endpoint). Empty
	// when Levels is set or there is nothing worth saying.
	Note string `json:"note,omitempty"`
}

// Allows reports whether level is one the spec offers.
func (s EffortSpec) Allows(level string) bool {
	return level != "" && slices.Contains(s.Levels, level)
}

// EffortSpecifier is implemented by providers that can carry a reasoning effort
// to at least some of the models they serve.
type EffortSpecifier interface {
	// EffortSpec reports the levels the model takes through this provider.
	EffortSpec(model string) EffortSpec
}

// EffortSpecFor reports what a session may pick for the model on the given
// provider. A provider that carries no effort, or a model it cannot carry one
// to, yields an empty spec.
func EffortSpecFor(p Provider, model string) EffortSpec {
	if p == nil {
		return EffortSpec{}
	}
	if es, ok := p.(EffortSpecifier); ok {
		return es.EffortSpec(model)
	}
	return EffortSpec{}
}

// EffortSpec reports what a session may pick for modelID on providerID,
// resolving the provider the way a run does (see ResolveProviderFor).
func (r *Registry) EffortSpec(providerID, modelID string) EffortSpec {
	p := r.ResolveProviderFor(modelID, providerID)
	if p == nil {
		return EffortSpec{}
	}
	return EffortSpecFor(p, modelID)
}

// ResolveEffort returns the level a run should request: the session's choice
// when the model takes it on this provider, otherwise "" — the vendor's
// default. A stored choice can outlive what made it valid (the session moved to
// a model with other levels, or the endpoint changed under it), and a level the
// model does not take fails the whole request on most hosts, so it is dropped
// here rather than sent.
func ResolveEffort(p Provider, model, chosen string) string {
	if chosen == "" {
		return ""
	}
	if EffortSpecFor(p, model).Allows(chosen) {
		return chosen
	}
	return ""
}

// catalogEffortSpec is the spec a catalogue entry gives a model on an endpoint
// that can carry every level the model accepts.
func catalogEffortSpec(m CatalogModel) EffortSpec {
	if len(m.Efforts) == 0 {
		return EffortSpec{}
	}
	return EffortSpec{Levels: slices.Clone(m.Efforts), Default: m.DefaultEffort}
}
