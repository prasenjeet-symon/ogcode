package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/note"
)

// resetNotesFlagCache clears the package-level decision cache between cases:
// featureFlags reuses an answer for half of featureFlagTTL, so a stale entry
// would leak one case's answer into the next.
func resetNotesFlagCache(t *testing.T) {
	t.Helper()
	clear := func() {
		featureFlagCache.mu.Lock()
		featureFlagCache.flags = nil
		featureFlagCache.fetched = time.Time{}
		featureFlagCache.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// stubDecide points the /decide call at a local server answering with the given
// status and body, and restores the real endpoint and client afterwards. It
// returns a counter of how many times the stub was hit, so a caching test can
// assert the flag is not re-fetched.
func stubDecide(t *testing.T, status int, body string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	oldURL, oldClient := decideURL, featureFlagHTTPClient
	decideURL, featureFlagHTTPClient = srv.URL, srv.Client()
	t.Cleanup(func() {
		decideURL, featureFlagHTTPClient = oldURL, oldClient
		srv.Close()
	})
	return &hits
}

// stubFeatureFlagsOn points /decide at a stub that turns every gated feature on,
// clearing the package cache first so the answer is fetched fresh. A test that
// runs a real Serve() (whose background refresher re-reads the flags) uses this
// so the refresher's decision agrees with the test instead of clobbering it with
// the live PostHog value. A caller should still Store(true) on the atomic to
// cover the window before the refresher's first fetch completes.
func stubFeatureFlagsOn(t *testing.T) {
	t.Helper()
	resetNotesFlagCache(t)
	stubDecide(t, http.StatusOK, `{"featureFlags":{"notes-feature":true,"device-panel":true,"plan-mode":true}}`)
}

// TestNotesEnabled pins the fail-safe contract of the /decide read: only a
// boolean true in the flag map turns the feature on; every other shape of
// answer — a false, an absent or non-boolean flag, a non-200, a malformed body
// — reads as off.
func TestNotesEnabled(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"flag on", http.StatusOK, `{"featureFlags":{"notes-feature":true}}`, true},
		{"flag off", http.StatusOK, `{"featureFlags":{"notes-feature":false}}`, false},
		{"flag absent", http.StatusOK, `{"featureFlags":{}}`, false},
		{"non-bool value", http.StatusOK, `{"featureFlags":{"notes-feature":"yes"}}`, false},
		{"non-200", http.StatusInternalServerError, `{"featureFlags":{"notes-feature":true}}`, false},
		{"malformed body", http.StatusOK, `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNotesFlagCache(t)
			stubDecide(t, tc.status, tc.body)
			if got := NotesEnabled("install-abc", featureFlagHTTPTimeout); got != tc.want {
				t.Errorf("NotesEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNotesEnabledNetworkErrorIsOff pins that an unreachable PostHog reads as
// off rather than panicking or blocking past the client timeout.
func TestNotesEnabledNetworkErrorIsOff(t *testing.T) {
	resetNotesFlagCache(t)
	oldURL := decideURL
	decideURL = "http://127.0.0.1:0" // unroutable
	t.Cleanup(func() { decideURL = oldURL })
	if NotesEnabled("install-abc", featureFlagHTTPTimeout) {
		t.Error("NotesEnabled over a dead endpoint = true, want false")
	}
}

// TestNotesEnabledReuseWindow pins how long an answer is reused: calls in
// quick succession share one lookup, an answer under half an interval old is
// still reused, and one a little under a whole interval old — the age the
// refresher's next tick finds it at, since it was stamped when its fetch
// completed — is fetched again. Reusing it there would skip every other tick
// and let a flip take two intervals to land.
func TestNotesEnabledReuseWindow(t *testing.T) {
	resetNotesFlagCache(t)
	hits := stubDecide(t, http.StatusOK, `{"featureFlags":{"notes-feature":true}}`)
	age := func(d time.Duration) {
		featureFlagCache.mu.Lock()
		featureFlagCache.fetched = time.Now().Add(-d)
		featureFlagCache.mu.Unlock()
	}

	for i := 0; i < 3; i++ {
		if !NotesEnabled("install-abc", featureFlagHTTPTimeout) {
			t.Fatalf("call %d: NotesEnabled = false, want true", i)
		}
	}
	if *hits != 1 {
		t.Fatalf("decide endpoint hit %d times for calls in quick succession, want 1", *hits)
	}

	age(featureFlagTTL/2 - time.Second)
	NotesEnabled("install-abc", featureFlagHTTPTimeout)
	if *hits != 1 {
		t.Errorf("an answer under half an interval old was fetched again (%d hits), want it reused", *hits)
	}

	age(featureFlagTTL - 50*time.Millisecond)
	NotesEnabled("install-abc", featureFlagHTTPTimeout)
	if *hits != 2 {
		t.Errorf("an answer just under one interval old was reused (%d hits), want the refresher's tick to fetch again", *hits)
	}
}

// TestNotesEnabledSingleFlight pins that concurrent callers do not each fire a
// request: the mutex is held across the fetch, so the waiters read the value the
// first caller stored rather than racing their own.
func TestNotesEnabledSingleFlight(t *testing.T) {
	resetNotesFlagCache(t)
	hits := stubDecide(t, http.StatusOK, `{"featureFlags":{"notes-feature":true}}`)

	const callers = 8
	var wg sync.WaitGroup
	results := make([]bool, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = NotesEnabled("install-abc", featureFlagHTTPTimeout)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, got := range results {
		if !got {
			t.Errorf("caller %d = false, want true", i)
		}
	}
	if *hits != 1 {
		t.Errorf("decide endpoint hit %d times under %d concurrent callers, want 1 (single flight)", *hits, callers)
	}
}

// TestNotesRoutesGatedByFlag pins the HTTP gate: with the flag off the notes
// endpoint answers as an unknown endpoint (404, the same body), and /api/config
// reports the flag so the web UI can hide the feature. With the flag on the
// same endpoint serves normally.
func TestNotesRoutesGatedByFlag(t *testing.T) {
	srv := newTestServer(t)
	srv.noteStore = note.NewStore(srv.db)
	h := srv.routes()

	srv.notesEnabled.Store(false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notes/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/notes/ with flag off = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "no such API endpoint") {
		t.Errorf("flag-off body = %q, want the unknown-endpoint 404", body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
	}
	if cfg["notesEnabled"] != false {
		t.Errorf("/api/config notesEnabled = %v, want false", cfg["notesEnabled"])
	}

	srv.notesEnabled.Store(true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notes/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/notes/ with flag on = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
	}
	if cfg["notesEnabled"] != true {
		t.Errorf("/api/config notesEnabled = %v, want true", cfg["notesEnabled"])
	}
}

// TestDevicePanelEnabled pins the device-panel read the same way as the notes
// one: only a boolean true under the panel's key turns it on; an absent flag, or
// one carried for another feature, reads as off.
func TestDevicePanelEnabled(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"flag on", `{"featureFlags":{"device-panel":true}}`, true},
		{"flag off", `{"featureFlags":{"device-panel":false}}`, false},
		{"flag absent", `{"featureFlags":{}}`, false},
		{"only another flag", `{"featureFlags":{"notes-feature":true}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNotesFlagCache(t)
			stubDecide(t, http.StatusOK, tc.body)
			if got := DevicePanelEnabled("install-abc", featureFlagHTTPTimeout); got != tc.want {
				t.Errorf("DevicePanelEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanModeEnabled pins the plan-mode read the same way as the notes and
// device ones: only a boolean true under the flag's key turns it on; an absent
// flag, or one carried for another feature, reads as off. The flag withholds no
// feature — it only lets the plan-mode warning banner be hidden from PostHog.
func TestPlanModeEnabled(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"flag on", `{"featureFlags":{"plan-mode":true}}`, true},
		{"flag off", `{"featureFlags":{"plan-mode":false}}`, false},
		{"flag absent", `{"featureFlags":{}}`, false},
		{"only another flag", `{"featureFlags":{"notes-feature":true}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNotesFlagCache(t)
			stubDecide(t, http.StatusOK, tc.body)
			if got := PlanModeEnabled("install-abc", featureFlagHTTPTimeout); got != tc.want {
				t.Errorf("PlanModeEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanModeConfigReportsFlag pins the delivery surface: /api/config carries
// the plan-mode flag, which is what lets the web banner react to a flip without
// a reload.
func TestPlanModeConfigReportsFlag(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	for _, want := range []bool{false, true} {
		srv.planModeEnabled.Store(want)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
		var cfg map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
			t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
		}
		if cfg["planModeEnabled"] != want {
			t.Errorf("/api/config planModeEnabled = %v, want %v", cfg["planModeEnabled"], want)
		}
	}
}

// TestDevicePanelRoutesGatedByFlag pins the HTTP gate: with the flag off the
// device endpoints answer as unknown endpoints and the /scrcpy proxy answers a
// plain 404, so no stream can be opened; with the flag on the endpoints serve
// and the proxy forwards to the configured target.
func TestDevicePanelRoutesGatedByFlag(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	srv.devicePanelEnabled.Store(false)
	for _, path := range []string{"/api/scrcpy/status", "/api/scrcpy/devices"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s with flag off = %d, want 404 (body %s)", path, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "no such API endpoint") {
			t.Errorf("GET %s flag-off body = %q, want the unknown-endpoint 404", path, body)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scrcpy/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /scrcpy/ with flag off = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
	}
	if cfg["devicePanelEnabled"] != false {
		t.Errorf("/api/config devicePanelEnabled = %v, want false", cfg["devicePanelEnabled"])
	}

	// Flag on: the API endpoints serve, and the proxy forwards to the target.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("device-ui"))
	}))
	t.Cleanup(backend.Close)
	setenvScrappyTargetForTest(t, backend.URL)

	srv.devicePanelEnabled.Store(true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scrcpy/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/scrcpy/status with flag on = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scrcpy/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "device-ui") {
		t.Fatalf("GET /scrcpy/ with flag on = %d body %q, want the proxied backend", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
	}
	if cfg["devicePanelEnabled"] != true {
		t.Errorf("/api/config devicePanelEnabled = %v, want true", cfg["devicePanelEnabled"])
	}
}
