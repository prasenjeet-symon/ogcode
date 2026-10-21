package server

import (
	"math"
	"testing"
)

// themePresets mirrors PRESETS in web/src/pages/settings/general.tsx — the
// colours a user can pick without touching the hex field.
var themePresets = []struct {
	name string
	hex  string
}{
	{"Orange", "#ff5e1f"},
	{"Violet", "#5e6ad2"},
	{"Blue", "#3b82f6"},
	{"Indigo", "#6366f1"},
	{"Purple", "#8b5cf6"},
	{"Rose", "#f43f5e"},
	{"Amber", "#f59e0b"},
	{"Emerald", "#10b981"},
	{"Cyan", "#06b6d4"},
	{"Pink", "#ec4899"},
}

// worstOn returns the lowest contrast an ink hex reaches on the given surfaces,
// each bare and under its 12% accent wash (the selected-row / pill background).
func worstOn(ink string, surfaces []string) float64 {
	w := math.Inf(1)
	ir, ig, ib, err := parseHex(ink)
	if err != nil {
		return 0
	}
	for _, surface := range surfaces {
		w = math.Min(w, contrastRatio(ink, surface))
		w = math.Min(w, contrastRatio(ink, compositeHex(ir, ig, ib, 0.12, surface)))
	}
	return w
}

// TestDeriveTheme_LabelsClearAAOnAccentAndHover pins the whole point of the
// label picker: the text drawn on a filled button must clear AA whether or not
// the pointer is resting on it, for every preset the UI offers.
func TestDeriveTheme_LabelsClearAAOnAccentAndHover(t *testing.T) {
	for _, preset := range themePresets {
		t.Run(preset.name, func(t *testing.T) {
			th := deriveTheme(preset.hex, "/proj")
			if got := contrastRatio(th.OnPrimary, th.Accent); got < minLabelContrast {
				t.Errorf("label %s on accent %s = %.2f:1, want >= %.1f", th.OnPrimary, th.Accent, got, minLabelContrast)
			}
			if got := contrastRatio(th.OnPrimary, th.AccentHover); got < minLabelContrast {
				t.Errorf("label %s on hover %s = %.2f:1, want >= %.1f", th.OnPrimary, th.AccentHover, got, minLabelContrast)
			}
			if th.OnPrimary != "#ffffff" && th.OnPrimary != "#0a0a0b" {
				t.Errorf("label = %s, want one of the two fixed labels", th.OnPrimary)
			}
		})
	}
}

// TestDeriveTheme_AccentInkClearsAAOnEverySurface pins the per-mode accent ink:
// the accent used as TEXT has to read on every surface of its mode, including
// the tinted chip a selected row or pill draws behind it.
func TestDeriveTheme_AccentInkClearsAAOnEverySurface(t *testing.T) {
	for _, preset := range themePresets {
		t.Run(preset.name, func(t *testing.T) {
			th := deriveTheme(preset.hex, "/proj")
			if got := worstOn(th.AccentInk, darkSurfaces); got < minLabelContrast {
				t.Errorf("dark ink %s worst = %.2f:1, want >= %.1f", th.AccentInk, got, minLabelContrast)
			}
			if got := worstOn(th.AccentInkLight, lightSurfaces); got < minLabelContrast {
				t.Errorf("light ink %s worst = %.2f:1, want >= %.1f", th.AccentInkLight, got, minLabelContrast)
			}
			// The ink must stay recognisably the chosen hue, not collapse to a
			// neutral: the accent's hue survives the lightness walk.
			if ah, _, _ := mustHSL(t, th.Accent); math.Abs(ah-mustHue(t, th.AccentInk)) > 0.01 {
				t.Errorf("dark ink hue %.3f drifted from accent hue %.3f", mustHue(t, th.AccentInk), ah)
			}
		})
	}
}

func mustHSL(t *testing.T, hex string) (h, s, l float64) {
	t.Helper()
	r, g, b, err := parseHex(hex)
	if err != nil {
		t.Fatalf("parseHex(%q): %v", hex, err)
	}
	return rgbToHSL(r, g, b)
}

func mustHue(t *testing.T, hex string) float64 {
	t.Helper()
	h, _, _ := mustHSL(t, hex)
	return h
}

// TestDeriveTheme_DegeneratePrimariesStillClearAA covers the ends of the range:
// near-black and near-white inputs are clamped into a visible accent and still
// get a passing label and ink, and garbage falls back to the brand orange.
func TestDeriveTheme_DegeneratePrimariesStillClearAA(t *testing.T) {
	for _, hex := range []string{"#000000", "#050505", "#ffffff", "#fefefe", "#0000ff", "#ffff00", "#808080", "not-a-color"} {
		t.Run(hex, func(t *testing.T) {
			th := deriveTheme(hex, "/proj")
			if got := contrastRatio(th.OnPrimary, th.Accent); got < minLabelContrast {
				t.Errorf("label on accent = %.2f:1, want >= %.1f", got, minLabelContrast)
			}
			if got := contrastRatio(th.OnPrimary, th.AccentHover); got < minLabelContrast {
				t.Errorf("label on hover = %.2f:1, want >= %.1f", got, minLabelContrast)
			}
			if got := worstOn(th.AccentInk, darkSurfaces); got < minLabelContrast {
				t.Errorf("dark ink worst = %.2f:1, want >= %.1f", got, minLabelContrast)
			}
			if got := worstOn(th.AccentInkLight, lightSurfaces); got < minLabelContrast {
				t.Errorf("light ink worst = %.2f:1, want >= %.1f", got, minLabelContrast)
			}
		})
	}

	if th := deriveTheme("not-a-color", "/proj"); th.PrimaryColor != "#6366f1" {
		t.Errorf("fallback primary = %s, want #6366f1", th.PrimaryColor)
	}
}

// TestLabelFor_PrefersTheMarginOverBothFills documents the rule: the label is
// whichever fixed colour clears accent AND hover by the wider margin.
func TestLabelFor_PrefersTheMarginOverBothFills(t *testing.T) {
	if got := labelFor("#6366f1", "#373bf3"); got != "#ffffff" {
		t.Errorf("indigo label = %s, want #ffffff", got)
	}
	if got := labelFor("#ff5e1f", "#f54500"); got != "#0a0a0b" {
		t.Errorf("orange label = %s, want #0a0a0b", got)
	}
}
