package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The PostHog feature flags this server gates optional features on. One /decide
// response carries every flag, so the cache below holds them all and each
// accessor reads its own key; a flag that is off or absent reads as off.
const (
	// notesFeatureFlagKey gates the notes feature: while it is off, no note
	// route is served and no notes text reaches an agent's prompt.
	notesFeatureFlagKey = "notes-feature"
	// devicePanelFlagKey gates the scrcpy/device panel: while it is off, the
	// /device UI, its /api/scrcpy/* endpoints and the /scrcpy/* stream proxy are
	// all withheld — a live Android screen and input never leave this machine.
	devicePanelFlagKey = "device-panel"
	// planModeFlagKey gates the plan-mode instability warning: while it is on,
	// the UI shows the notice that plan mode is still under rapid development.
	// Unlike the two above it withholds no feature — plan mode stays reachable
	// in either case — so it is a remote kill-switch for the warning alone.
	planModeFlagKey = "plan-mode"
)

// featureFlagTTL is the interval at which the refresher re-reads the flags, so
// a flip in PostHog reaches the server within one interval. A decision younger
// than half of it is reused rather than fetched again (see featureFlags). The
// flags change rarely, so a minute keeps serving traffic off the PostHog API.
// A variable so a test can shorten it.
var featureFlagTTL = 60 * time.Second

// featureFlagHTTPTimeout bounds the background refresher's /decide request, and
// NotesFlagCLITimeout the CLI's one startup lookup (exported because the CLI
// packages are its callers). A one-shot CLI run must not stall on a slow
// PostHog, so its budget is short; the server retries on the next tick anyway.
const (
	featureFlagHTTPTimeout = 5 * time.Second
	NotesFlagCLITimeout    = 1500 * time.Millisecond
)

// decideURL is the PostHog feature-flag endpoint. A variable so tests can point
// it at a stub server.
var decideURL = PostHogAPIHost + "/decide/?v=3"

// featureFlagHTTPClient carries no timeout of its own; each call sets a context
// deadline from the timeout it was given. A package-level client rather than one
// per call so connections are reused across refreshes.
var featureFlagHTTPClient = &http.Client{}

// flagCache holds the last /decide answer for the whole process. One fetch
// populates every flag, so a worker hosting several servers (one per worktree)
// shares a single lookup between them.
type flagCache struct {
	mu      sync.Mutex
	flags   map[string]bool
	fetched time.Time
}

var featureFlagCache flagCache

// startFeatureFlagRefresh keeps every feature-flag decision current for the life
// of ctx. The server starts with each feature off (fail closed); this decides
// immediately in the background — not after the first TTL — so a flip reaches
// the routes and prompts seconds after boot rather than a minute, and re-reads
// it on featureFlagTTL thereafter. A change is stored on the server — the loop
// runner reads the same atomic — and published so an open UI can re-render
// without a reload. With no PostHog key there is nothing to ask: it starts
// nothing, and every feature keeps the value it has. Only a test binary blanks
// the key, so a test's stored decision is never overwritten.
func (s *Server) startFeatureFlagRefresh(ctx context.Context) {
	if PostHogAPIKey == "" {
		return
	}
	refresh := func() {
		flags := featureFlags(s.installID, featureFlagHTTPTimeout)
		s.applyFeatureFlag(&s.notesEnabled, flags[notesFeatureFlagKey], "notes.changed")
		s.applyFeatureFlag(&s.devicePanelEnabled, flags[devicePanelFlagKey], "device-panel.changed")
		s.applyFeatureFlag(&s.planModeEnabled, flags[planModeFlagKey], "plan-mode.changed")
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("feature flag refresh panicked", "panic", r)
			}
		}()
		refresh()
		ticker := time.NewTicker(featureFlagTTL)
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

// applyFeatureFlag stores one decision on its atomic and, on a change, publishes
// the event the web UI listens for. Writing only on a change keeps the SSE
// stream quiet on every tick.
func (s *Server) applyFeatureFlag(dst *atomic.Bool, enabled bool, event string) {
	if enabled == dst.Load() {
		return
	}
	dst.Store(enabled)
	if s.bus != nil {
		s.bus.Publish(event, map[string]any{"enabled": enabled})
	}
}

// NotesEnabled asks PostHog whether the notes feature is on for this install.
// It is called from exactly two places — the server's background refresher and
// the CLI's one startup lookup — because it may block on the network for up to
// the timeout it is given. Request paths never call it: they read the server's
// atomic flag (see notesUnavailable), which the refresher keeps current.
func NotesEnabled(distinctID string, timeout time.Duration) bool {
	return featureFlags(distinctID, timeout)[notesFeatureFlagKey]
}

// DevicePanelEnabled asks PostHog whether the device panel is on for this
// install. Same contract as NotesEnabled: only the CLI and the refresher call
// it, and request paths read the server's atomic flag instead (see
// devicePanelUnavailable).
func DevicePanelEnabled(distinctID string, timeout time.Duration) bool {
	return featureFlags(distinctID, timeout)[devicePanelFlagKey]
}

// PlanModeEnabled asks PostHog whether the plan-mode instability warning is
// shown for this install. Only the server's background refresher calls it; the
// UI reads the atomic flag the refresher maintains (see configPayload). It
// withholds no feature — plan mode stays reachable whatever it returns — and an
// unreachable PostHog reads as off, so a failed lookup hides the warning rather
// than flashing it on.
func PlanModeEnabled(distinctID string, timeout time.Duration) bool {
	return featureFlags(distinctID, timeout)[planModeFlagKey]
}

// featureFlags returns this install's boolean flags, fetching them if the cached
// answer is stale. An answer younger than half of featureFlagTTL is reused.
// Half, not the whole interval: an answer is timestamped when its fetch
// completes, so the refresher's next tick finds it a little under one interval
// old — reusing it then would skip every other tick and let a flip take two
// intervals to land. Within the half-interval, callers that share this package
// cache (a worker hosts one server per worktree) reuse one lookup between them.
//
// The mutex is held across the fetch, so concurrent callers wait and then read
// the fresh value rather than racing a second request. Any failure (network
// error, non-200, malformed body) reads as no flags, and every accessor then
// reports its feature off.
func featureFlags(distinctID string, timeout time.Duration) map[string]bool {
	featureFlagCache.mu.Lock()
	defer featureFlagCache.mu.Unlock()
	if !featureFlagCache.fetched.IsZero() && time.Since(featureFlagCache.fetched) < featureFlagTTL/2 {
		return featureFlagCache.flags
	}
	flags := fetchFeatureFlags(distinctID, timeout)
	featureFlagCache.flags = flags
	featureFlagCache.fetched = time.Now()
	return flags
}

// fetchFeatureFlags asks PostHog's /decide endpoint for this install's feature
// flags and returns the boolean ones. It mirrors the capture path in posthog.go:
// raw REST, no SDK. The request is bounded by a context deadline so a slow
// endpoint cannot stall the caller; a nil map is returned for any error so the
// caller fails safe to "every feature off".
func fetchFeatureFlags(distinctID string, timeout time.Duration) map[string]bool {
	body, err := json.Marshal(map[string]string{
		"api_key":     PostHogAPIKey,
		"distinct_id": distinctID,
	})
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, decideURL, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := featureFlagHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var decide struct {
		FeatureFlags map[string]any `json:"featureFlags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decide); err != nil {
		return nil
	}
	flags := make(map[string]bool, len(decide.FeatureFlags))
	for key, value := range decide.FeatureFlags {
		if enabled, ok := value.(bool); ok {
			flags[key] = enabled
		}
	}
	return flags
}
