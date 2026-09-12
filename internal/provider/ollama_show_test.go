package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// showServer spins up a stub ollama /api/show and points OLLAMA_SHOW_URL at it.
// The handler keys on the POSTed model name; a name absent from the map
// produces the not-found shape ollama.com serves (200 + error field).
func showServer(t *testing.T, windows map[string]int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		_ = json.Unmarshal(body[:n], &req)
		w.WriteHeader(http.StatusOK)
		if ctx, ok := windows[req.Model]; ok {
			_, _ = w.Write([]byte(`{"model_info":{"` + req.Model + `-arch.context_length":` + itoa(ctx) + `}}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":"model '` + req.Model + `' not found"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_SHOW_URL", srv.URL)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestOllamaShowContextWindowsFillsKnownModels(t *testing.T) {
	showServer(t, map[string]int{"glm-5.3-flash": 1048576, "kimi-k3": 1048576})

	in := []ModelInfo{
		{ID: "glm-5.3-flash:cloud", Name: "glm-5.3-flash"},
		{ID: "kimi-k3-cloud", Name: "kimi-k3"},
	}
	ollamaShowContextWindows(context.Background(), in)

	if in[0].ContextWindow != 1048576 {
		t.Errorf("glm-5.3-flash:cloud window = %d, want 1048576", in[0].ContextWindow)
	}
	if in[1].ContextWindow != 1048576 {
		t.Errorf("kimi-k3-cloud window = %d, want 1048576 (suffix on the tag is stripped for lookup)", in[1].ContextWindow)
	}
}

func TestOllamaShowContextWindowsUnknownStaysZero(t *testing.T) {
	showServer(t, nil) // nothing is described

	in := []ModelInfo{{ID: "mystery-model:cloud", Name: "mystery-model"}}
	ollamaShowContextWindows(context.Background(), in)
	if in[0].ContextWindow != 0 {
		t.Errorf("unknown model window = %d, want 0 (unknown must not be guessed)", in[0].ContextWindow)
	}
}

func TestOllamaShowContextWindowsDeduplicatesNames(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"model_info":{"arch.context_length":131072}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_SHOW_URL", srv.URL)

	// The catalog can list both the tagged and the bare form of the same
	// hosted model; the host is asked once.
	in := []ModelInfo{
		{ID: "glm-5.3:cloud", Name: "glm-5.3"},
		{ID: "glm-5.3", Name: "glm-5.3"},
	}
	ollamaShowContextWindows(context.Background(), in)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("/api/show called %d times for one distinct name, want 1", got)
	}
	if in[0].ContextWindow != 131072 || in[1].ContextWindow != 131072 {
		t.Errorf("windows = %d, %d; want both filled from the one lookup", in[0].ContextWindow, in[1].ContextWindow)
	}
}

func TestOllamaShowContextWindowsSlowHostDoesNotBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // far past ollamaShowTimeout
		_, _ = w.Write([]byte(`{"model_info":{"arch.context_length":999}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_SHOW_URL", srv.URL)

	start := time.Now()
	in := []ModelInfo{{ID: "slow:cloud", Name: "slow"}}
	ollamaShowContextWindows(context.Background(), in)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("lookup pass took %v; a slow /api/show must be abandoned at its timeout, not waited on", elapsed)
	}
	if in[0].ContextWindow != 0 {
		t.Errorf("window = %d, want 0 after the timed-out lookup", in[0].ContextWindow)
	}
}

func TestOllamaShowContextWindowFromShow(t *testing.T) {
	resp := &ollamaShowResponse{ModelInfo: map[string]any{
		"general.architecture":       "glm5_next",
		"glm5_next.context_length":   float64(1048576),
		"glm5_next.embedding_length": float64(4096),
	}}
	if got := ollamaContextWindowFromShow(resp); got != 1048576 {
		t.Errorf("got %d, want 1048576", got)
	}

	absent := &ollamaShowResponse{ModelInfo: map[string]any{"general.architecture": "x"}}
	if got := ollamaContextWindowFromShow(absent); got != 0 {
		t.Errorf("got %d, want 0 when no .context_length key exists", got)
	}
}

func TestOllamaCatalogShowName(t *testing.T) {
	cases := map[string]string{
		"glm-5.3-flash:cloud": "glm-5.3-flash", // tagged form: cloud becomes the tag
		"kimi-k3-cloud":       "kimi-k3",       // suffix form
		"gpt-oss:120b-cloud":  "gpt-oss:120b",  // suffix on an existing tag
		"already:cloud":       "already",       // idempotent
		"glm-5.3-flash":       "glm-5.3-flash", // bare catalog name unchanged
	}
	for in, want := range cases {
		m := ModelInfo{ID: in}
		if got := ollamaCatalogShowName(m); got != want {
			t.Errorf("ollamaCatalogShowName(%q) = %q, want %q", in, got, want)
		}
	}
}

// End-to-end: the catalog fetch must carry real windows into the returned
// ModelInfo list when the show endpoint can describe the models.
func TestFetchOllamaCloudCatalogCarriesContextWindows(t *testing.T) {
	showServer(t, map[string]int{"small:20b": 131072, "mid:120b": 262144})
	catalogServer(t, fakeCatalog, http.StatusOK)

	got, err := FetchOllamaCloudCatalog(context.Background(), "http://localhost:8090/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := map[string]int{}
	for _, m := range got {
		byName[m.Name] = m.ContextWindow
	}
	if byName["small:20b"] != 131072 || byName["mid:120b"] != 262144 {
		t.Errorf("windows = %v; want small:20b=131072 mid:120b=262144", byName)
	}
	// The host could not describe these two; they must stay unknown.
	if byName["big:400b"] != 0 || byName["nosize"] != 0 {
		t.Errorf("undescribed models must keep window 0, got %v", byName)
	}
}

func TestOllamaShowURLOverride(t *testing.T) {
	t.Setenv("OLLAMA_SHOW_URL", "http://127.0.0.1:1/api/show")
	if got := ollamaShowURL(); got != "http://127.0.0.1:1/api/show" {
		t.Errorf("ollamaShowURL() = %q, want the override", got)
	}
	if !strings.HasPrefix(ollamaShowURL(), "http") {
		t.Error("default must be the ollama.com /api/show endpoint")
	}
}
