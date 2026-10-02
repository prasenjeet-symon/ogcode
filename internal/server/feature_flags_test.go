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
// NotesEnabled reuses an answer for half of notesFlagTTL, so a stale entry would
// leak one case's answer into the next.
func resetNotesFlagCache(t *testing.T) {
	t.Helper()
	clear := func() {
		notesFeatureCache.mu.Lock()
		notesFeatureCache.enabled = false
		notesFeatureCache.fetched = time.Time{}
		notesFeatureCache.mu.Unlock()
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
	oldURL, oldClient := decideURL, notesHTTPClient
	decideURL, notesHTTPClient = srv.URL, srv.Client()
	t.Cleanup(func() {
		decideURL, notesHTTPClient = oldURL, oldClient
		srv.Close()
	})
	return &hits
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
			if got := NotesEnabled("install-abc", notesFlagHTTPTimeout); got != tc.want {
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
	if NotesEnabled("install-abc", notesFlagHTTPTimeout) {
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
		notesFeatureCache.mu.Lock()
		notesFeatureCache.fetched = time.Now().Add(-d)
		notesFeatureCache.mu.Unlock()
	}

	for i := 0; i < 3; i++ {
		if !NotesEnabled("install-abc", notesFlagHTTPTimeout) {
			t.Fatalf("call %d: NotesEnabled = false, want true", i)
		}
	}
	if *hits != 1 {
		t.Fatalf("decide endpoint hit %d times for calls in quick succession, want 1", *hits)
	}

	age(notesFlagTTL/2 - time.Second)
	NotesEnabled("install-abc", notesFlagHTTPTimeout)
	if *hits != 1 {
		t.Errorf("an answer under half an interval old was fetched again (%d hits), want it reused", *hits)
	}

	age(notesFlagTTL - 50*time.Millisecond)
	NotesEnabled("install-abc", notesFlagHTTPTimeout)
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
			results[i] = NotesEnabled("install-abc", notesFlagHTTPTimeout)
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
