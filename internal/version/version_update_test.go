package version

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// newFakeGitHub serves one release payload at the releases/latest path, the only
// endpoint the update check reads. It keeps these tests off the real network.
func newFakeGitHub(t *testing.T, release githubRelease) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckUpdateAgainstFakeGitHub(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "v1.0.0"

	published := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	srv := newFakeGitHub(t, githubRelease{
		TagName:     "v1.1.0",
		HTMLURL:     "https://example.test/releases/tag/v1.1.0",
		PublishedAt: published,
		Body:        "# Notes\n\nFaster and safer.",
	})
	m := New(WithAPIBase(srv.URL))

	update, err := m.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate() error = %v; want nil", err)
	}
	if update.LatestVersion != "v1.1.0" {
		t.Errorf("LatestVersion = %q; want %q", update.LatestVersion, "v1.1.0")
	}
	if !update.UpdateAvailable {
		t.Error("UpdateAvailable = false; want true for v1.1.0 over v1.0.0")
	}
	if update.ReleaseURL != "https://example.test/releases/tag/v1.1.0" {
		t.Errorf("ReleaseURL = %q", update.ReleaseURL)
	}
	if update.PublishedAt == nil || !update.PublishedAt.Equal(published) {
		t.Errorf("PublishedAt = %v; want %v", update.PublishedAt, published)
	}
	if update.ReleaseNotes != "# Notes\n\nFaster and safer." {
		t.Errorf("ReleaseNotes = %q", update.ReleaseNotes)
	}
	if update.InstallCommand == "" {
		t.Error("InstallCommand is empty; want the detected install command")
	}
}

// A failed check must keep serving the last release fetched successfully, so
// the UI can show what it knows instead of an empty "up to date".
func TestCheckUpdateFailureServesLastKnownRelease(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "v1.0.0"

	srv := newFakeGitHub(t, githubRelease{TagName: "v1.1.0"})
	m := New(WithAPIBase(srv.URL))

	if _, err := m.CheckUpdate(context.Background()); err != nil {
		t.Fatalf("first CheckUpdate() error = %v; want nil", err)
	}

	srv.Close()
	m.ClearCache() // an explicit check must reach for GitHub again

	update, err := m.CheckUpdate(context.Background())
	if err == nil {
		t.Fatal("CheckUpdate() error = nil; want a failure after the server closed")
	}
	if update == nil {
		t.Fatal("CheckUpdate() update = nil; want the last known release alongside the error")
	}
	if update.LatestVersion != "v1.1.0" {
		t.Errorf("LatestVersion = %q; want the cached %q", update.LatestVersion, "v1.1.0")
	}
}

func TestGetResponseReportsCheckFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	m := New(WithAPIBase(srv.URL))
	response, err := m.GetResponse(context.Background())
	if err == nil {
		t.Fatal("GetResponse() error = nil; want the fetch error")
	}
	if response == nil {
		t.Fatal("GetResponse() response = nil; want version info even on failure")
	}
	if response.UpdateError == "" {
		t.Error("UpdateError is empty; a failed check must be visible in the response")
	}
	if response.Version == "" || response.GoVersion == "" {
		t.Errorf("local version info dropped on failure: %+v", response.Info)
	}
	if response.LatestVersion != "" {
		t.Errorf("LatestVersion = %q; want empty when no release was ever fetched", response.LatestVersion)
	}

	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if fields["updateError"] == "" {
		t.Errorf("JSON has no updateError: %s", body)
	}
	if _, ok := fields["publishedAt"]; ok {
		t.Errorf("JSON carries publishedAt for an unknown release: %s", body)
	}
}

// A failure is remembered, so a page that asks repeatedly does not pay the
// request timeout each time; ClearCache is the way to force a real retry.
func TestFailedCheckIsNotRetriedBeforeFailedCheckTTL(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer srv.Close()

	m := New(WithAPIBase(srv.URL))
	for i := 0; i < 3; i++ {
		if _, err := m.GetResponse(context.Background()); err == nil {
			t.Fatal("GetResponse() error = nil; want a failure")
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("GitHub hits = %d over three requests; want 1 (failure is cached)", got)
	}

	m.ClearCache()
	if _, err := m.GetResponse(context.Background()); err == nil {
		t.Fatal("GetResponse() error = nil; want a failure")
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("GitHub hits = %d after ClearCache; want 2 (explicit check retries)", got)
	}
}

func TestSummarizeReleaseNotesKeepsUTF8(t *testing.T) {
	notes := strings.Repeat("é", 600)
	got := summarizeReleaseNotes(notes)

	if !utf8.ValidString(got) {
		t.Error("summarizeReleaseNotes() produced invalid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n != releaseNotesLimit+3 {
		t.Errorf("summarizeReleaseNotes() rune count = %d; want %d", n, releaseNotesLimit+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("summarizeReleaseNotes() = %q; want a trailing ellipsis", got)
	}
}
