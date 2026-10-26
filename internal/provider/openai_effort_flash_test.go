package provider

import (
	"testing"
)

func TestDefaultEffortForFlashClass(t *testing.T) {
	cases := []struct {
		model string
		spec  EffortSpec
		want  string
	}{
		// No recorded default: the substring heuristic, unchanged.
		{"ogx-deepseek-v4.1-flash", EffortSpec{}, EffortLow},
		{"deepseek-v4.1-flash", EffortSpec{}, EffortLow},
		{"GLM-5.3-Flash", EffortSpec{}, EffortLow},
		{"gemini-3-flash", EffortSpec{}, EffortLow},
		{"glm-5.3-flashx", EffortSpec{}, EffortLow},
		{"deepseek-v4-pro", EffortSpec{}, ""},
		{"deepseek-v4.1", EffortSpec{}, ""},
		{"", EffortSpec{}, ""},
		// A recorded default lighter than high is the vendor's tuned setting
		// and stands on a flash model: there is no deep default to claw back.
		{"gemini-2.5-flash-lite", EffortSpec{Levels: noneToHighEfforts, Default: EffortNone}, EffortNone},
		{"gemini-3.5-flash-lite", EffortSpec{Levels: minimalToHighEfforts, Default: EffortMinimal}, EffortMinimal},
		{"gemini-3.8-flash", EffortSpec{Levels: lowToHighEfforts, Default: EffortMedium}, EffortMedium},
		// A recorded default of high or deeper IS the deep default the hold
		// exists for: low.
		{"ogx-deepseek-v4.1-flash", EffortSpec{Levels: noneLowHighMaxEfforts, Default: EffortHigh}, EffortLow},
		{"glm-5.3-flash", EffortSpec{Levels: lowHighMaxEfforts, Default: EffortMax}, EffortLow},
		// Non-flash models never take a default from here, recorded or not:
		// the endpoint's own default applies, and nothing is sent.
		{"deepseek-v4-pro", EffortSpec{Levels: noneLowHighMaxEfforts, Default: EffortLow}, ""},
		{"gemini-4-argon", EffortSpec{Levels: lowToHighEfforts, Default: EffortHigh}, ""},
	}
	for _, c := range cases {
		if got := defaultEffortFor(c.model, c.spec); got != c.want {
			t.Errorf("defaultEffortFor(%q, %+v) = %q, want %q", c.model, c.spec, got, c.want)
		}
	}
}

func flashEffortBody(t *testing.T, p Provider, req StreamRequest) map[string]any {
	t.Helper()
	body, _ := captureWireRequest(t, p, req)
	return body
}

func flashEffortLevel(t *testing.T, body map[string]any) (string, bool) {
	t.Helper()
	got, ok := body["reasoning_effort"]
	if !ok {
		return "", false
	}
	s, _ := got.(string)
	return s, true
}

func TestFlashDefaultEffortOnTheWire(t *testing.T) {
	ogx := effortProvider(OGXProviderID, "https://ogx.ogcode.in/v1")
	flash := "ogx-deepseek-v4.1-flash"

	t.Run("unset effort plans low", func(t *testing.T) {
		body := flashEffortBody(t, ogx, effortRequest(flash, "", true))
		got, ok := flashEffortLevel(t, body)
		if !ok || got != EffortLow {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortLow)
		}
	})

	t.Run("mechanical step plans none", func(t *testing.T) {
		req := effortRequest(flash, "", true)
		req.StepMechanical = true
		got, ok := flashEffortLevel(t, flashEffortBody(t, ogx, req))
		if !ok || got != EffortNone {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortNone)
		}
	})

	t.Run("explicit effort wins even on a mechanical step", func(t *testing.T) {
		got, ok := flashEffortLevel(t, flashEffortBody(t, ogx, effortRequest(flash, EffortHigh, true)))
		if !ok || got != EffortHigh {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortHigh)
		}
		req := effortRequest(flash, EffortHigh, true)
		req.StepMechanical = true
		got, ok = flashEffortLevel(t, flashEffortBody(t, ogx, req))
		if !ok || got != EffortHigh {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortHigh)
		}
	})

	t.Run("non-flash model keeps the endpoint default", func(t *testing.T) {
		if got, ok := flashEffortLevel(t, flashEffortBody(t, ogx, effortRequest("deepseek-v4-pro", "", true))); ok {
			t.Errorf("reasoning_effort = %q, want the field absent", got)
		}
	})

	t.Run("mechanical on a ladder without none keeps low", func(t *testing.T) {
		req := effortRequest("glm-5.3-flash", "", true)
		req.StepMechanical = true
		got, ok := flashEffortLevel(t, flashEffortBody(t, ogx, req))
		if !ok || got != EffortLow {
			t.Errorf("reasoning_effort = %q (present %v), want %q", got, ok, EffortLow)
		}
	})

	t.Run("not-thinking request carries nothing", func(t *testing.T) {
		if got, ok := flashEffortLevel(t, flashEffortBody(t, ogx, effortRequest(flash, "", false))); ok {
			t.Errorf("reasoning_effort = %q, want the field absent", got)
		}
	})

	t.Run("unknown host sends no effort field at all", func(t *testing.T) {
		u := effortProvider("custom", "https://llm.example.dev/v1")
		body := flashEffortBody(t, u, effortRequest("my-flash-9b", "", true))
		for _, key := range []string{"reasoning_effort", "reasoning", "thinking", "enable_thinking"} {
			if got, ok := body[key]; ok {
				t.Errorf("%s = %v, want the field absent on an unknown host", key, got)
			}
		}
	})

	// The catalogue records what the vendor tuned a gemini flash model to run
	// at with no level asked for, and the flash hold must not stomp it: on
	// 2.5 Flash-Lite low would even switch reasoning ON.
	t.Run("recorded gemini flash defaults stand over the hold", func(t *testing.T) {
		gem := effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/")
		if got, ok := flashEffortLevel(t, flashEffortBody(t, gem, effortRequest("gemini-2.5-flash-lite", "", true))); !ok || got != EffortNone {
			t.Errorf("gemini-2.5-flash-lite reasoning_effort = %q (present %v), want %q", got, ok, EffortNone)
		}
		if got, ok := flashEffortLevel(t, flashEffortBody(t, gem, effortRequest("gemini-3.5-flash-lite", "", true))); !ok || got != EffortMinimal {
			t.Errorf("gemini-3.5-flash-lite reasoning_effort = %q (present %v), want %q", got, ok, EffortMinimal)
		}
		if got, ok := flashEffortLevel(t, flashEffortBody(t, gem, effortRequest("models/gemini-3.8-flash", "", true))); !ok || got != EffortMedium {
			t.Errorf("models/gemini-3.8-flash reasoning_effort = %q (present %v), want %q", got, ok, EffortMedium)
		}
	})
}
