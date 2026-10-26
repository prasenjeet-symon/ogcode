package provider

import "testing"

// The mechanical hold rewrites a level only where the level came from the
// default path. A provider's live spec can change mid-run — the ladder below
// is what the hosted endpoint might report after dropping "high" — and an
// explicit session effort must never be pushed down to make it fit: writing
// low over the session's high on such a step would silently downgrade the
// user's choice instead of sending nothing.
func TestMechanicalStepNeverRewritesAnExplicitEffort(t *testing.T) {
	p := effortProvider("ollama", "http://localhost:11434/v1",
		ModelInfo{ID: "kimi-k3:cloud", Efforts: []string{EffortNone, EffortLow, EffortMedium}, DefaultEffort: EffortLow})

	req := effortRequest("kimi-k3:cloud", EffortHigh, true)
	req.StepMechanical = true
	if got, ok := flashEffortLevel(t, flashEffortBody(t, p, req)); ok {
		t.Errorf("a mechanical step rewrote the session's high to %q; want no effort field sent at all", got)
	}

	// An explicit level the ladder still takes goes out unchanged.
	req = effortRequest("kimi-k3:cloud", EffortMedium, true)
	req.StepMechanical = true
	if got, ok := flashEffortLevel(t, flashEffortBody(t, p, req)); !ok || got != EffortMedium {
		t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortMedium)
	}
}

// The mechanical hold still applies to every level the default path produces:
// held to "none" where the ladder takes it, to the light default where it
// does not — even when that default is the catalogue's recorded medium.
func TestMechanicalStepHoldsADefaultedEffort(t *testing.T) {
	gem := effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/")

	t.Run("a ladder with none is held to none", func(t *testing.T) {
		req := effortRequest("gemini-2.5-flash-lite", "", true)
		req.StepMechanical = true
		got, ok := flashEffortLevel(t, flashEffortBody(t, gem, req))
		if !ok || got != EffortNone {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortNone)
		}
	})

	t.Run("a ladder without none falls back to the light default", func(t *testing.T) {
		req := effortRequest("models/gemini-3.8-flash", "", true)
		req.StepMechanical = true
		got, ok := flashEffortLevel(t, flashEffortBody(t, gem, req))
		if !ok || got != EffortLow {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortLow)
		}
	})
}
