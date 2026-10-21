package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Theme holds a per-directory color palette derived from a user-chosen primary color.
type Theme struct {
	Directory      string `json:"directory"`
	PrimaryColor   string `json:"primaryColor"`   // hex input from user, e.g. "#6366f1"
	Accent         string `json:"accent"`         // --accent
	AccentHover    string `json:"accentHover"`    // --accent-hover
	AccentSoft     string `json:"accentSoft"`     // --accent-soft (rgba)
	AccentRing     string `json:"accentRing"`     // --accent-ring (rgba)
	OnPrimary      string `json:"onPrimary"`      // text color on accent bg
	Glow           string `json:"glow"`           // --glow (rgba, for background accents)
	Tint           string `json:"tint"`           // --tint (rgba, ~5% for sidebar/header)
	AccentInk      string `json:"accentInk"`      // --accent-ink in dark mode
	AccentInkLight string `json:"accentInkLight"` // --accent-ink in light mode
}

func parseHex(s string) (r, g, b uint8, err error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color: #%s", s)
	}
	buf, err := hex.DecodeString(s)
	if err != nil || len(buf) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid hex color: #%s", s)
	}
	return buf[0], buf[1], buf[2], nil
}

func rgbToHSL(r, g, b uint8) (h, s, l float64) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0
	max := math.Max(rf, math.Max(gf, bf))
	min := math.Min(rf, math.Min(gf, bf))
	l = (max + min) / 2.0
	if max == min {
		h = 0
		s = 0
		return
	}
	d := max - min
	if l > 0.5 {
		s = d / (2.0 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case rf:
		h = (gf - bf) / d
		if gf < bf {
			h += 6
		}
	case gf:
		h = (bf-rf)/d + 2
	case bf:
		h = (rf-gf)/d + 4
	}
	h /= 6.0
	return
}

func hslToRGB(h, s, l float64) (r, g, b uint8) {
	if s == 0 {
		v := uint8(l * 255)
		return v, v, v
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hueToRGB := func(p, q, t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		if t < 1.0/6.0 {
			return p + (q-p)*6*t
		}
		if t < 0.5 {
			return q
		}
		if t < 2.0/3.0 {
			return p + (q-p)*(2.0/3.0-t)*6
		}
		return p
	}
	r = uint8(math.Round(hueToRGB(p, q, h+1.0/3.0) * 255))
	g = uint8(math.Round(hueToRGB(p, q, h) * 255))
	b = uint8(math.Round(hueToRGB(p, q, h-1.0/3.0) * 255))
	return
}

// relativeLuminance returns the WCAG relative luminance of a color.
func relativeLuminance(r, g, b uint8) float64 {
	linearize := func(c uint8) float64 {
		f := float64(c) / 255.0
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)
}

func rgbaStr(r, g, b uint8, a float64) string {
	return fmt.Sprintf("rgba(%d, %d, %d, %.2f)", r, g, b, a)
}

// Contrast targets and the lightness step each derived colour walks by. The
// label step is small because a visible bump in the accent means a different
// brand colour on screen; the ink step can be a little larger because the ink
// is a text token and is expected to differ from the fill.
const (
	minLabelContrast = 4.5
	labelStep        = 0.0025
	inkStep          = 0.005
)

// Surfaces an accent is drawn on as TEXT, per mode (--bg-base … --bg-elevated).
// Each is also checked with a 12% accent wash composited over it, which is what
// --accent-soft produces on a selected row, pill or chip.
var (
	darkSurfaces  = []string{"#09090b", "#0f0f12", "#161619", "#1b1b1f", "#222226"}
	lightSurfaces = []string{"#ffffff", "#f8f8fa", "#f0f0f3", "#e9e9ee"}
)

func hexOf(r, g, b uint8) string {
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// contrastRatio returns the WCAG contrast ratio between two hex colors.
func contrastRatio(a, b string) float64 {
	ar, ag, ab, err := parseHex(a)
	if err != nil {
		return 0
	}
	br, bg, bb, err := parseHex(b)
	if err != nil {
		return 0
	}
	la := relativeLuminance(ar, ag, ab)
	lb := relativeLuminance(br, bg, bb)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// compositeHex returns the colour of an accent at the given opacity over a surface.
func compositeHex(r, g, b uint8, alpha float64, surfaceHex string) string {
	sr, sg, sb, err := parseHex(surfaceHex)
	if err != nil {
		return surfaceHex
	}
	mix := func(fg, bg uint8) uint8 {
		return uint8(math.Round(float64(fg)*alpha + float64(bg)*(1-alpha)))
	}
	return hexOf(mix(r, sr), mix(g, sg), mix(b, sb))
}

// labelFor picks the label colour drawn ON an accent fill: whichever of
// near-black/near-white clears the accent AND its hover by the wider margin.
// The fill a pointer rests on is the hovered one, and it is darker than the
// accent for every preset, so a label chosen on the accent alone can fail there.
func labelFor(accent, hover string) string {
	white := math.Min(contrastRatio("#ffffff", accent), contrastRatio("#ffffff", hover))
	black := math.Min(contrastRatio("#0a0a0b", accent), contrastRatio("#0a0a0b", hover))
	if black >= white {
		return "#0a0a0b"
	}
	return "#ffffff"
}

// clampForLabel nudges an HSL lightness the shortest distance that lets the
// given label reach minLabelContrast on that colour: darker for a white label,
// lighter for a near-black one. Returns l unchanged if the range runs out.
func clampForLabel(h, s, l float64, label string, step float64) float64 {
	dir := step
	if label == "#ffffff" {
		dir = -step
	}
	for contrastRatio(hexOf(hslToRGB(h, s, l)), label) < minLabelContrast {
		next := l + dir
		if next < 0 || next > 1 {
			return l
		}
		l = next
	}
	return l
}

// inkFor walks a hue's lightness away from its fill lightness until the colour
// is legible as TEXT on every surface of the mode (and on each surface's 12%
// accent wash). Light mode walks down, dark mode up.
func inkFor(h, s, l float64, mode string) string {
	surfaces, dir := darkSurfaces, inkStep
	if mode == "light" {
		surfaces, dir = lightSurfaces, -inkStep
	}
	worst := func(r, g, b uint8) float64 {
		c := hexOf(r, g, b)
		w := math.Inf(1)
		for _, surface := range surfaces {
			w = math.Min(w, contrastRatio(c, surface))
			w = math.Min(w, contrastRatio(c, compositeHex(r, g, b, 0.12, surface)))
		}
		return w
	}
	for {
		r, g, b := hslToRGB(h, s, l)
		if worst(r, g, b) >= minLabelContrast {
			return hexOf(r, g, b)
		}
		l += dir
		if l < 0 || l > 1 {
			return hexOf(r, g, b)
		}
	}
}

// deriveTheme computes a full palette from a single hex primary color.
func deriveTheme(primaryHex, directory string) Theme {
	r, g, b, err := parseHex(primaryHex)
	if err != nil {
		// Fallback to indigo
		r, g, b = 99, 102, 241
		primaryHex = "#6366f1"
	}

	h, s, l := rgbToHSL(r, g, b)

	// Clamp near-black/near-white so accent is always visible on dark surfaces.
	// Near-black (#000–#1e1e1e, L < 0.06) would be invisible against the app's
	// dark backgrounds; near-white (L > 0.94) loses hover distinction on a light one.
	displayL := l
	if l < 0.06 {
		displayL = 0.35
	} else if l > 0.94 {
		displayL = 0.88
	}

	// accent-hover: darken slightly from the display lightness, boost saturation
	boostS := s + 0.05
	if boostS > 1 {
		boostS = 1
	}
	hoverL := func(from float64) float64 {
		hl := from - 0.08
		if hl < 0.15 {
			hl = 0.15
		}
		return hl
	}

	// Pick the label from the unclamped pair, then move each colour the shortest
	// distance that lets that label clear AA on both. Checking hover matters: the
	// fill a pointer rests on is the hovered one, and it is darker for every preset.
	label := labelFor(hexOf(hslToRGB(h, s, displayL)), hexOf(hslToRGB(h, boostS, hoverL(displayL))))
	displayL = clampForLabel(h, s, displayL, label, labelStep)
	accentL := clampForLabel(h, boostS, hoverL(displayL), label, labelStep)

	// accent = primary color itself
	ar, ag, ab := hslToRGB(h, s, displayL)
	accent := hexOf(ar, ag, ab)
	hr, hg, hb := hslToRGB(h, boostS, accentL)
	accentHover := hexOf(hr, hg, hb)

	// accent-soft: primary at 12% opacity on dark bg
	accentSoft := rgbaStr(ar, ag, ab, 0.12)

	// accent-ring: primary at 35% opacity for focus rings
	accentRing := rgbaStr(ar, ag, ab, 0.35)

	// accent-ink: the same hue as TEXT, per mode — a fill can sit too close to a
	// surface (or to its own 12% wash) to be read as a label.
	accentInk := inkFor(h, s, displayL, "dark")
	accentInkLight := inkFor(h, s, displayL, "light")

	// glow: primary at 12% for background accents (like the home page gradient)
	glow := rgbaStr(ar, ag, ab, 0.12)

	// tint: primary at ~5% for subtle sidebar/header backgrounds
	tint := rgbaStr(ar, ag, ab, 0.05)

	return Theme{
		Directory:      directory,
		PrimaryColor:   primaryHex,
		Accent:         accent,
		AccentHover:    accentHover,
		AccentSoft:     accentSoft,
		AccentRing:     accentRing,
		OnPrimary:      label,
		Glow:           glow,
		Tint:           tint,
		AccentInk:      accentInk,
		AccentInkLight: accentInkLight,
	}
}

func (s *Server) handleGetTheme(w http.ResponseWriter, r *http.Request) {
	directory := r.URL.Query().Get("directory")
	if directory == "" {
		directory = s.dir
	}

	var t Theme
	err := s.db.QueryRow(
		"SELECT directory, primary_color, accent, accent_hover, accent_soft, accent_ring, on_primary, glow, tint FROM theme WHERE directory = ?",
		directory,
	).Scan(&t.Directory, &t.PrimaryColor, &t.Accent, &t.AccentHover, &t.AccentSoft, &t.AccentRing, &t.OnPrimary, &t.Glow, &t.Tint)
	if err != nil {
		// No theme saved yet — return defaults
		t = deriveTheme("#ff5e1f", directory)
	} else {
		// Every derived column is cached from primary_color, and rows written under
		// an older derivation rule would keep a failing label or ink. The derivation
		// is deterministic, so recompute the whole palette on read rather than
		// trusting the stored columns.
		t = deriveTheme(t.PrimaryColor, directory)
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory    string `json:"directory"`
		PrimaryColor string `json:"primaryColor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.PrimaryColor == "" {
		http.Error(w, "primaryColor is required", http.StatusBadRequest)
		return
	}
	dir := input.Directory
	if dir == "" {
		dir = s.dir
	}

	t := deriveTheme(input.PrimaryColor, dir)

	now := session.Now()
	_, err := s.db.Exec(
		`INSERT INTO theme (directory, primary_color, accent, accent_hover, accent_soft, accent_ring, on_primary, glow, tint, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(directory) DO UPDATE SET
		   primary_color = excluded.primary_color,
		   accent = excluded.accent,
		   accent_hover = excluded.accent_hover,
		   accent_soft = excluded.accent_soft,
		   accent_ring = excluded.accent_ring,
		   on_primary = excluded.on_primary,
		   glow = excluded.glow,
		   tint = excluded.tint,
		   updated_at = excluded.updated_at`,
		t.Directory, t.PrimaryColor, t.Accent, t.AccentHover, t.AccentSoft, t.AccentRing, t.OnPrimary, t.Glow, t.Tint, now,
	)
	if err != nil {
		http.Error(w, "failed to save theme", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTheme(w http.ResponseWriter, r *http.Request) {
	directory := chi.URLParam(r, "directory")
	if directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	_, err := s.db.Exec("DELETE FROM theme WHERE directory = ?", directory)
	if err != nil {
		http.Error(w, "failed to delete theme", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ensure color.RGBA satisfies nothing unexpected — we just use image/color for luminance.
var _ color.Color = color.RGBA{}
