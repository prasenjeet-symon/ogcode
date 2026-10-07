package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func patchSession(t *testing.T, h http.Handler, id session.SessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/session/"+string(id), strings.NewReader(body)))
	return rec
}

// The effort picked in the composer lands on the session row — that row is what
// a resumed or restarted turn reads — and only ogcode's own level names do.
func TestSessionEffortRoutes(t *testing.T) {
	srv, h := newPermissionTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session",
		strings.NewReader(`{"directory":"`+srv.dir+`","model":"claude-opus-5-5","effort":"high"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/session = %d (%s)", rec.Code, rec.Body.String())
	}
	var sess session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if sess.Effort != provider.EffortHigh {
		t.Fatalf("created effort = %q, want high", sess.Effort)
	}

	if rec := patchSession(t, h, sess.ID, `{"effort":"max"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH effort = %d (%s)", rec.Code, rec.Body.String())
	}
	if got, _ := srv.store.Get(sess.ID); got.Effort != provider.EffortMax {
		t.Fatalf("stored effort = %q, want max", got.Effort)
	}

	// An unknown level is refused and leaves the stored one alone.
	if rec := patchSession(t, h, sess.ID, `{"effort":"turbo"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH unknown effort = %d, want 400", rec.Code)
	}
	if got, _ := srv.store.Get(sess.ID); got.Effort != provider.EffortMax {
		t.Fatalf("a refused PATCH changed effort to %q", got.Effort)
	}

	// "" is the model's default, and a PATCH without the field leaves it be.
	if rec := patchSession(t, h, sess.ID, `{"title":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH title = %d", rec.Code)
	}
	if got, _ := srv.store.Get(sess.ID); got.Effort != provider.EffortMax {
		t.Fatalf("a PATCH without effort changed it to %q", got.Effort)
	}
	if rec := patchSession(t, h, sess.ID, `{"effort":""}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH effort reset = %d", rec.Code)
	}
	if got, _ := srv.store.Get(sess.ID); got.Effort != "" {
		t.Fatalf("reset effort = %q, want empty", got.Effort)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session",
		strings.NewReader(`{"directory":"`+srv.dir+`","effort":"warp"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with unknown effort = %d, want 400", rec.Code)
	}
}

// The model listing tells the picker what to offer: each model's own levels on
// its provider, and the default it runs at.
func TestModelListingCarriesEffortLevels(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.Register(provider.NewAnthropicProvider())
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d", rec.Code)
	}
	var models []struct {
		ID            string   `json:"id"`
		Efforts       []string `json:"efforts"`
		DefaultEffort string   `json:"defaultEffort"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, m := range models {
		byID[m.ID] = i
	}
	opus, ok := byID["claude-opus-5-5"]
	if !ok {
		t.Fatal("claude-opus-5-5 missing from the listing")
	}
	want := []string{provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortXHigh, provider.EffortMax}
	if !slices.Equal(models[opus].Efforts, want) || models[opus].DefaultEffort != provider.EffortMedium {
		t.Errorf("Opus 5.5: %v default %q", models[opus].Efforts, models[opus].DefaultEffort)
	}
	haiku, ok := byID["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatal("Haiku 4.5 missing from the listing")
	}
	if len(models[haiku].Efforts) != 0 {
		t.Errorf("Haiku 4.5 rejects effort, so it offers none; got %v", models[haiku].Efforts)
	}
}
