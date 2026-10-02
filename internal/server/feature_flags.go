package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// notesFeatureFlagKey is the PostHog feature flag that gates the notes feature.
// While it is off (or absent), no note route is served and no notes text reaches
// an agent's prompt.
const notesFeatureFlagKey = "notes-feature"

// notesFlagTTL is the interval at which the refresher re-reads the flag, so a
// flip in PostHog reaches the server within one interval. A decision younger
// than half of it is reused rather than fetched again (see NotesEnabled). The
// flag changes rarely, so a minute keeps serving traffic off the PostHog API.
// A variable so a test can shorten it.
var notesFlagTTL = 60 * time.Second

// notesFlagHTTPTimeout bounds the background refresher's /decide request, and
// NotesFlagCLITimeout the CLI's one startup lookup (exported because the CLI
// packages are its callers). A one-shot CLI run must not stall on a slow
// PostHog, so its budget is short; the server retries on the next tick anyway.
const (
	notesFlagHTTPTimeout = 5 * time.Second
	NotesFlagCLITimeout  = 1500 * time.Millisecond
)

// decideURL is the PostHog feature-flag endpoint. A variable so tests can point
// it at a stub server.
var decideURL = PostHogAPIHost + "/decide/?v=3"

// notesHTTPClient carries no timeout of its own; each call sets a context
// deadline from the timeout it was given. A package-level client rather than one
// per call so connections are reused across refreshes.
var notesHTTPClient = &http.Client{}

type notesFlagCache struct {
	mu      sync.Mutex
	enabled bool
	fetched time.Time
}

var notesFeatureCache notesFlagCache

// startNotesFlagRefresh keeps the notes feature-flag decision current for the
// life of ctx. The server starts with notes off (fail closed); this decides
// immediately in the background — not after the first TTL — so the flag reaches
// the routes and prompts seconds after boot rather than a minute, and re-reads
// it on notesFlagTTL thereafter. A change is stored on the server — the loop
// runner reads that same flag — and published so an open UI can re-render
// without a reload.
func (s *Server) startNotesFlagRefresh(ctx context.Context) {
	refresh := func() {
		enabled := NotesEnabled(s.installID, notesFlagHTTPTimeout)
		if enabled == s.notesEnabled.Load() {
			return
		}
		s.notesEnabled.Store(enabled)
		if s.bus != nil {
			s.bus.Publish("notes.changed", map[string]any{"enabled": enabled})
		}
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("notes flag refresh panicked", "panic", r)
			}
		}()
		refresh()
		ticker := time.NewTicker(notesFlagTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
}

// NotesEnabled asks PostHog for the notes flag. It is called from exactly two
// places — the server's background refresher and the CLI's one startup lookup —
// because it may block on the network for up to the timeout it is given.
// Request paths never call it: they read the server's atomic flag (see
// notesUnavailable), which the refresher keeps current.
//
// An answer younger than half of notesFlagTTL is reused. Half, not the whole
// interval: an answer is timestamped when its fetch completes, so the
// refresher's next tick finds it a little under one interval old — reusing it
// then would skip every other tick and let a flip take two intervals to land.
// Within the half-interval, refreshers that share this package cache (a worker
// hosts one server per worktree) reuse one lookup between them.
//
// The mutex is held across the fetch, so concurrent callers wait and then read
// the fresh value rather than racing a second request. Any failure (network
// error, non-200, missing flag, non-bool value) reads as off.
func NotesEnabled(distinctID string, timeout time.Duration) bool {
	notesFeatureCache.mu.Lock()
	defer notesFeatureCache.mu.Unlock()
	if !notesFeatureCache.fetched.IsZero() && time.Since(notesFeatureCache.fetched) < notesFlagTTL/2 {
		return notesFeatureCache.enabled
	}
	enabled := fetchNotesFlag(distinctID, timeout)
	notesFeatureCache.enabled = enabled
	notesFeatureCache.fetched = time.Now()
	return enabled
}

// fetchNotesFlag asks PostHog's /decide endpoint for this install's feature
// flags and reads the notes flag out of the response. It mirrors the capture
// path in posthog.go: raw REST, no SDK. The request is bounded by a context
// deadline so a slow endpoint cannot stall the caller; a false result is
// returned for any error so the caller fails safe to "notes off".
func fetchNotesFlag(distinctID string, timeout time.Duration) bool {
	body, err := json.Marshal(map[string]string{
		"api_key":     PostHogAPIKey,
		"distinct_id": distinctID,
	})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, decideURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := notesHTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var decide struct {
		FeatureFlags map[string]any `json:"featureFlags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decide); err != nil {
		return false
	}
	enabled, ok := decide.FeatureFlags[notesFeatureFlagKey].(bool)
	return ok && enabled
}
