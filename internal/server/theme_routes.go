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
	Directory    string `json:"directory"`
	PrimaryColor string `json:"primaryColor"` // hex input from user, e.g. "#6366f1"
	Accent       string `json:"accent"`       // --accent
	AccentHover  string `json:"accentHover"`  // --accent-hover
	AccentSoft   string `json:"accentSoft"`   // --accent-soft (rgba)
	AccentRing   string `json:"accentRing"`   // --accent-ring (rgba)
	OnPrimary    string `json:"onPrimary"`    // text color on accent bg
	Glow         string `json:"glow"`          // --glow (rgba, for background accents)
	Tint         string `json:"tint"`          // --tint (rgba, ~5% for sidebar/header)
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
	// dark backgrounds; near-white (L > 0.94) loses hover distinction and causes
	// hardcoded "text-white" to become invisible on white buttons.
	displayL := l
	if l < 0.06 {
		displayL = 0.35
	} else if l > 0.94 {
		displayL = 0.88
	}
	if displayL != l {
		r, g, b = hslToRGB(h, s, displayL)
	}

	// accent = primary color itself
	accent := fmt.Sprintf("#%02x%02x%02x", r, g, b)

	// accent-hover: darken slightly from the display lightness, boost saturation
	darkL := displayL - 0.08
	if darkL < 0.15 {
		darkL = 0.15
	}
	boostS := s + 0.05
	if boostS > 1 {
		boostS = 1
	}
	hr, hg, hb := hslToRGB(h, boostS, darkL)
	accentHover := fmt.Sprintf("#%02x%02x%02x", hr, hg, hb)

	// accent-soft: primary at 12% opacity on dark bg
	accentSoft := rgbaStr(r, g, b, 0.12)

	// accent-ring: primary at 35% opacity for focus rings
	accentRing := rgbaStr(r, g, b, 0.35)

	// on-primary: white or dark text depending on contrast
	lum := relativeLuminance(r, g, b)
	onPrimary := "#ffffff"
	if lum > 0.45 {
		onPrimary = "#0a0a0b"
	}

	// glow: primary at 12% for background accents (like the home page gradient)
	glow := rgbaStr(r, g, b, 0.12)

	// tint: primary at ~5% for subtle sidebar/header backgrounds
	tint := rgbaStr(r, g, b, 0.05)

	return Theme{
		Directory:    directory,
		PrimaryColor: primaryHex,
		Accent:       accent,
		AccentHover:  accentHover,
		AccentSoft:   accentSoft,
		AccentRing:   accentRing,
		OnPrimary:    onPrimary,
		Glow:         glow,
		Tint:         tint,
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
		t = deriveTheme("#5e6ad2", directory)
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