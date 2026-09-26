package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// previewPort extracts the loopback port an httptest backend bound, so a test
// can address it through /preview/<port>/ (the proxy always dials 127.0.0.1,
// and httptest binds there too).
func previewPort(t *testing.T, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse backend URL %q: %v", serverURL, err)
	}
	_, port, ok := strings.Cut(u.Host, ":")
	if !ok || port == "" {
		t.Fatalf("backend URL %q carries no port", serverURL)
	}
	return port
}

// TestServe_PreviewProxy pins the feature: a live local service on a loopback
// port (stubbed here by an httptest backend) is readable through ogcode's own
// origin at /preview/<port>/, with the prefix stripped and the rest of the path
// (and query) forwarded unchanged.
func TestServe_PreviewProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>the player</title><script src=\"app.js\"></script>")
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, "// app bundle")
		case "/echo":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "q="+r.URL.RawQuery)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	port := previewPort(t, backend.URL)

	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	}()
	waitUp(t, srv)

	base := "http://127.0.0.1:" + itoa(srv.Port()) + "/preview/" + port

	t.Run("forwards the root with the prefix stripped", func(t *testing.T) {
		resp, err := http.Get(base + "/")
		if err != nil {
			t.Fatalf("GET %s/: %v", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %q", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "the player") {
			t.Fatalf("body = %q, want the backend index", body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("Content-Type = %q, want text/html", ct)
		}
	})

	t.Run("forwards assets deeper in the path", func(t *testing.T) {
		resp, err := http.Get(base + "/app.js")
		if err != nil {
			t.Fatalf("GET %s/app.js: %v", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "app bundle") {
			t.Fatalf("status = %d body = %q, want the asset body", resp.StatusCode, body)
		}
	})

	t.Run("carries the query string through", func(t *testing.T) {
		resp, err := http.Get(base + "/echo?seek=42")
		if err != nil {
			t.Fatalf("GET %s/echo: %v", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "q=seek=42") {
			t.Fatalf("status = %d body = %q, want the query forwarded", resp.StatusCode, body)
		}
	})

	t.Run("redirects the bare prefix to a trailing slash", func(t *testing.T) {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(base)
		if err != nil {
			t.Fatalf("GET %s: %v", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/preview/"+port+"/" {
			t.Fatalf("Location = %q, want %q", loc, "/preview/"+port+"/")
		}
	})

	t.Run("404 from the backend passes through", func(t *testing.T) {
		resp, err := http.Get(base + "/no/such/path")
		if err != nil {
			t.Fatalf("GET %s/no/such/path: %v", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 passthrough", resp.StatusCode)
		}
	})

	// The port-less /preview must NOT be matched by the proxy wildcard — it is
	// the SPA route where the preview page lives. If chi's /preview/* ever
	// starts matching the bare path, that page becomes unreachable.
	t.Run("bare prefix falls through to the SPA", func(t *testing.T) {
		resp, err := http.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview")
		if err != nil {
			t.Fatalf("GET /preview: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 from the SPA fallback", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("Content-Type = %q, want text/html", ct)
		}
	})

	// /preview/ (wildcard, nothing after the slash) is not a target either —
	// it redirects to the port-less page rather than answering 400.
	t.Run("empty target redirects to the page", func(t *testing.T) {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview/")
		if err != nil {
			t.Fatalf("GET /preview/: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/preview" {
			t.Fatalf("Location = %q, want /preview", loc)
		}
	})
}

// TestServe_PreviewBadPort pins that a non-numeric port answers a clear 400
// rather than being interpreted as a host, and never reaches the backend.
func TestServe_PreviewBadPort(t *testing.T) {
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	}()
	waitUp(t, srv)

	base := "http://127.0.0.1:" + itoa(srv.Port())
	for _, path := range []string{"/preview/xyz/", "/preview/0/", "/preview/99999/"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400; body = %q", path, resp.StatusCode, body)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
			t.Fatalf("%s Content-Type = %q, want a plain-text error", path, resp.Header.Get("Content-Type"))
		}
	}
}

// TestServe_PreviewDown is the nothing-is-listening case: the proxy must fail
// with a 502 (not the SPA fallback, which would answer 200 text/html and leave
// the preview page thinking the service is up).
func TestServe_PreviewDown(t *testing.T) {
	// Grab a port, then free it so nothing answers there.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadPort := previewPort(t, backend.URL)
	backend.Close()

	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	}()
	waitUp(t, srv)

	resp, err := http.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview/" + deadPort + "/")
	if err != nil {
		t.Fatalf("GET preview against a dead port: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "no service is reachable") {
		t.Fatalf("body = %q, want the preview error text", body)
	}
	if strings.Contains(string(body), "<!doctype") {
		t.Fatalf("body = %q, looks like the SPA fallback", body)
	}
}

// TestServe_PreviewStatus covers GET /api/preview/status?port=N, the endpoint
// the preview page polls to choose between embedding the service and showing
// the nothing-there hint.
func TestServe_PreviewStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	port := previewPort(t, backend.URL)

	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	}()
	waitUp(t, srv)

	base := "http://127.0.0.1:" + itoa(srv.Port())

	t.Run("up with target", func(t *testing.T) {
		resp, err := http.Get(base + "/api/preview/status?port=" + port)
		if err != nil {
			t.Fatalf("GET /api/preview/status: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d; body = %q", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"up":true`) {
			t.Fatalf("body = %q, want up true while the backend answers", body)
		}
		if !strings.Contains(string(body), `"target":"http://127.0.0.1:`+port+`"`) {
			t.Fatalf("body = %q, want the loopback target echoed", body)
		}
	})

	t.Run("down after the backend dies", func(t *testing.T) {
		backend.Close()
		if previewReachable(atoi(t, port)) {
			t.Fatal("previewReachable = true, want false once the backend is closed")
		}
		resp, err := http.Get(base + "/api/preview/status?port=" + port)
		if err != nil {
			t.Fatalf("GET /api/preview/status: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 with up false; body = %q", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"up":false`) {
			t.Fatalf("body = %q, want up false", body)
		}
	})

	t.Run("rejects a missing or malformed port", func(t *testing.T) {
		for _, q := range []string{"", "?port=", "?port=xyz", "?port=0", "?port=70000"} {
			resp, err := http.Get(base + "/api/preview/status" + q)
			if err != nil {
				t.Fatalf("GET /api/preview/status%s: %v", q, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%q status = %d, want 400; body = %q", q, resp.StatusCode, body)
			}
		}
	})
}

// TestParsePreviewPath pins the port/path split directly, including the cases
// the HTTP tests cannot easily reach (an empty tail, a non-numeric segment at
// the extreme bounds).
func TestParsePreviewPath(t *testing.T) {
	cases := []struct {
		in   string
		port int
		path string
		ok   bool
	}{
		{"3000", 3000, "/", true},
		{"3000/", 3000, "/", true},
		{"3000/foo/bar", 3000, "/foo/bar", true},
		{"8080/index.html", 8080, "/index.html", true},
		{"1", 1, "/", true},
		{"65535", 65535, "/", true},
		{"0", 0, "", false},
		{"65536", 0, "", false},
		{"-1", 0, "", false},
		{"xyz", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		port, path, ok := parsePreviewPath(c.in)
		if ok != c.ok || port != c.port || path != c.path {
			t.Errorf("parsePreviewPath(%q) = (%d, %q, %v), want (%d, %q, %v)",
				c.in, port, path, ok, c.port, c.path, c.ok)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("atoi(%q): not a number", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
