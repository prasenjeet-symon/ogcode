package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// fakeGitHub stands in for the GitHub releases API. These tests used to call
// the real API and tolerate whatever came back, which is how the endpoint's
// silent failure went unnoticed; a fake makes success and failure both
// deterministic.
type fakeGitHub struct {
	mu     sync.Mutex
	status int
	body   string
	hits   int
	server *httptest.Server
}

func newFakeGitHub(t *testing.T, body string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{status: http.StatusOK, body: body}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status, body := f.status, f.body
		f.hits++
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// respond changes what the next requests answer, e.g. turning a working API
// into an unreachable one.
func (f *fakeGitHub) respond(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}

func (f *fakeGitHub) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func releaseJSON(tag string) string {
	return fmt.Sprintf(`{"tag_name":%q,"name":%q,"html_url":"https://example.test/%s",
		"published_at":"2026-03-01T12:00:00Z","body":"Notes for %s","draft":false,"prerelease":false}`,
		tag, tag, tag, tag)
}

func newVersionTestServer(t *testing.T, fake *fakeGitHub) *Server {
	t.Helper()
	return &Server{versionManager: version.New(version.WithAPIBase(fake.server.URL))}
}

func TestHandleVersionStatusCodes(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	srv.handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleVersion() status = %v, want %v", rec.Code, http.StatusOK)
	}
}

func TestHandleVersionContentType(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	srv.handleVersion(rec, req)

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("handleVersion() Content-Type = %v, want application/json", contentType)
	}
}

func TestHandleVersionResponseStructure(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	srv.handleVersion(rec, req)

	var response version.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.Version == "" {
		t.Error("Response missing version")
	}
	if response.GoVersion == "" {
		t.Error("Response missing goVersion")
	}
	if response.LatestVersion != "v9.9.9" {
		t.Errorf("Response latestVersion = %q; want %q", response.LatestVersion, "v9.9.9")
	}
	if !response.UpdateAvailable {
		t.Error("Response updateAvailable = false; want true for v9.9.9")
	}
	if response.UpdateError != "" {
		t.Errorf("Response updateError = %q; want empty on success", response.UpdateError)
	}
}

// The reported bug: with GitHub unreachable the GET used to answer 200 with
// empty update fields, which the UI rendered as "Up to date". It must still
// answer 200 (the version is local) but carry the failure.
func TestHandleVersionReportsCheckFailureInBody(t *testing.T) {
	fake := newFakeGitHub(t, "")
	fake.respond(http.StatusForbidden, `{"message":"API rate limit exceeded"}`)
	srv := newVersionTestServer(t, fake)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	srv.handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleVersion() status = %v, want %v", rec.Code, http.StatusOK)
	}

	var response version.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.UpdateError == "" {
		t.Error("Response updateError is empty; the failed check is invisible")
	}
	if response.Version == "" {
		t.Error("Response missing version")
	}
	if response.LatestVersion != "" {
		t.Errorf("Response latestVersion = %q; want empty", response.LatestVersion)
	}
	if response.InstallCommand != "" {
		t.Errorf("Response installCommand = %q; want empty when no release is known", response.InstallCommand)
	}
}

func TestHandleVersionCheckStatusCode(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodPost, "/version/check", nil)
	rec := httptest.NewRecorder()

	srv.handleVersionCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleVersionCheck() status = %v, want %v", rec.Code, http.StatusOK)
	}
}

// An explicit check that fails is a failed request, not a silent success.
func TestHandleVersionCheckReportsFailure(t *testing.T) {
	fake := newFakeGitHub(t, "")
	fake.respond(http.StatusServiceUnavailable, "")
	srv := newVersionTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/version/check", nil)
	rec := httptest.NewRecorder()
	srv.handleVersionCheck(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("handleVersionCheck() status = %v, want %v", rec.Code, http.StatusServiceUnavailable)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("handleVersionCheck() Content-Type = %v, want application/json", contentType)
	}

	var response version.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.UpdateError == "" {
		t.Error("Response updateError is empty; the failed check is invisible")
	}
	if response.Version == "" {
		t.Error("Response missing version")
	}
}

// A forced check that fails after an earlier success still serves the last
// release, so the UI can show what it knows along with the error.
func TestHandleVersionCheckFallsBackToLastKnownRelease(t *testing.T) {
	fake := newFakeGitHub(t, releaseJSON("v9.9.9"))
	srv := newVersionTestServer(t, fake)

	get := httptest.NewRequest(http.MethodGet, "/version", nil)
	srv.handleVersion(httptest.NewRecorder(), get)

	fake.respond(http.StatusInternalServerError, "")
	post := httptest.NewRequest(http.MethodPost, "/version/check", nil)
	rec := httptest.NewRecorder()
	srv.handleVersionCheck(rec, post)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("handleVersionCheck() status = %v, want %v", rec.Code, http.StatusServiceUnavailable)
	}

	var response version.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.LatestVersion != "v9.9.9" {
		t.Errorf("Response latestVersion = %q; want the last known %q", response.LatestVersion, "v9.9.9")
	}
	if response.UpdateError == "" {
		t.Error("Response updateError is empty; the failed check is invisible")
	}
}

// ClearCache forces the POST to go back to GitHub instead of answering from the
// one-hour cache.
func TestHandleVersionCheckClearsCache(t *testing.T) {
	fake := newFakeGitHub(t, releaseJSON("v9.9.9"))
	srv := newVersionTestServer(t, fake)

	srv.handleVersion(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/version", nil))
	if got := fake.requests(); got != 1 {
		t.Fatalf("GitHub requests after GET = %d; want 1", got)
	}

	fake.respond(http.StatusOK, releaseJSON("v9.9.10"))
	rec := httptest.NewRecorder()
	srv.handleVersionCheck(rec, httptest.NewRequest(http.MethodPost, "/version/check", nil))

	if got := fake.requests(); got != 2 {
		t.Errorf("GitHub requests after POST = %d; want 2 (cache cleared)", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("handleVersionCheck() status = %v, want %v", rec.Code, http.StatusOK)
	}

	var response version.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.LatestVersion != "v9.9.10" {
		t.Errorf("Response latestVersion = %q; want the refetched %q", response.LatestVersion, "v9.9.10")
	}
}

// BenchmarkHandleVersion measures the performance of version endpoint.
func BenchmarkHandleVersion(b *testing.B) {
	fake := &fakeGitHub{status: http.StatusOK, body: releaseJSON("v9.9.9")}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fake.body)
	}))
	defer fake.server.Close()
	srv := &Server{versionManager: version.New(version.WithAPIBase(fake.server.URL))}
	req := httptest.NewRequest(http.MethodGet, "/version", nil)

	// Warm the cache so the benchmark measures the handler, not GitHub.
	srv.handleVersion(httptest.NewRecorder(), req)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		srv.handleVersion(rec, req)
	}
}

// TestVersionResponseFields verifies expected fields in response.
func TestVersionResponseFields(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	srv.handleVersion(rec, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Every field the UI reads is present on a successful check.
	for _, field := range []string{"version", "goVersion", "latestVersion", "updateAvailable", "releaseUrl", "releaseNotes", "installCommand"} {
		if _, ok := response[field]; !ok {
			t.Errorf("Response missing required field: %s", field)
		}
	}
}

// TestHandleVersionCheckPOSTMethod verifies the check endpoint works correctly.
func TestHandleVersionCheckPOSTMethod(t *testing.T) {
	srv := newVersionTestServer(t, newFakeGitHub(t, releaseJSON("v9.9.9")))

	req := httptest.NewRequest(http.MethodPost, "/version/check", nil)
	rec := httptest.NewRecorder()

	srv.handleVersionCheck(rec, req)

	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("handleVersionCheck() Content-Type = %v, want application/json", contentType)
	}
}
