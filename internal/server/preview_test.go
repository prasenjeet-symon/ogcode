package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
)

// previewPort extracts the loopback port an httptest backend bound, so a test
// can address it as a preview subdomain (the proxy always dials 127.0.0.1, and
// httptest binds there too).
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

// previewHostURL is the URL a browser opens for a live service: the service's
// port as a subdomain of the preview domain, carrying the test server's own
// port. In a browser <port>.preview.localhost resolves to loopback; in the test
// it is reached through previewClient below.
func previewHostURL(t *testing.T, srv *Server, port int) string {
	t.Helper()
	return "http://" + strconv.Itoa(port) + "." + agent.PreviewDomain() + ":" + itoa(srv.Port()) + "/"
}

// previewClient dials 127.0.0.1:<serverPort> whatever host its URL names, so a
// request addressed to <port>.preview.localhost — which the Go resolver does
// not know, unlike a browser — still reaches the test server. The transport
// sends the URL's own Host, which is exactly what previewHostDispatch reads.
func previewClient(serverPort int) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", itoa(serverPort)))
			},
		},
	}
}

// noRedirectClient is an http.Client that returns the first response instead of
// following a redirect, so a test can inspect the 301 itself.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// startPreviewServer runs a real loopback server for a preview test and stops
// it when the test ends.
func startPreviewServer(t *testing.T) *Server {
	t.Helper()
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	})
	waitUp(t, srv)
	return srv
}

// publishPreviewPort publishes port through the API the Preview page uses, so
// the proxy will serve it. Going through HTTP rather than the store keeps the
// test off the server goroutine's fields.
func publishPreviewPort(t *testing.T, srv *Server, port int) {
	t.Helper()
	resp, err := http.Post("http://127.0.0.1:"+itoa(srv.Port())+"/api/preview/ports",
		"application/json", strings.NewReader(`{"port":`+strconv.Itoa(port)+`}`))
	if err != nil {
		t.Fatalf("publish %d: %v", port, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("publish %d: status %d, body %q", port, resp.StatusCode, body)
	}
}

// unpublishPreviewPort takes port off the proxy's allowlist through the API.
func unpublishPreviewPort(t *testing.T, srv *Server, port int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, "http://127.0.0.1:"+itoa(srv.Port())+"/api/preview/ports/"+strconv.Itoa(port), nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unpublish %d: %v", port, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unpublish %d: status %d, want 204", port, resp.StatusCode)
	}
}

// TestServe_PreviewProxy pins the feature: a live local service on a loopback
// port (stubbed here by an httptest backend) is served at its own origin root
// through ogcode — http://<port>.preview.localhost:<server-port>/ — so an app
// that bootstraps off window.location.pathname sees "/" and starts normally,
// with the path and query forwarded unchanged.
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

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, atoi(t, port))

	client := previewClient(srv.Port())
	base := previewHostURL(t, srv, atoi(t, port))

	t.Run("serves the service root at its own origin", func(t *testing.T) {
		resp, err := client.Get(base)
		if err != nil {
			t.Fatalf("GET %s: %v", base, err)
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
		resp, err := client.Get(base + "app.js")
		if err != nil {
			t.Fatalf("GET %sapp.js: %v", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "app bundle") {
			t.Fatalf("status = %d body = %q, want the asset body", resp.StatusCode, body)
		}
	})

	t.Run("carries the query string through", func(t *testing.T) {
		resp, err := client.Get(base + "echo?seek=42")
		if err != nil {
			t.Fatalf("GET %secho: %v", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "q=seek=42") {
			t.Fatalf("status = %d body = %q, want the query forwarded", resp.StatusCode, body)
		}
	})

	t.Run("404 from the backend passes through", func(t *testing.T) {
		resp, err := client.Get(base + "no/such/path")
		if err != nil {
			t.Fatalf("GET %sno/such/path: %v", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 passthrough", resp.StatusCode)
		}
	})

	// The legacy /preview/<port>/… form survives as a redirect shim onto the
	// service's own hostname, carrying the extra path (escaping intact) and the
	// query over. Temporary, because the target depends on the preview domain
	// and the origin used — a cached 301 would outlive either changing.
	t.Run("the legacy path form redirects to the service host", func(t *testing.T) {
		resp, err := noRedirectClient().Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview/" + port + "/foo/a%2Fb?x=1")
		if err != nil {
			t.Fatalf("GET legacy path: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want 307", resp.StatusCode)
		}
		want := "http://" + port + "." + agent.PreviewDomain() + ":" + itoa(srv.Port()) + "/foo/a%2Fb?x=1"
		if loc := resp.Header.Get("Location"); loc != want {
			t.Fatalf("Location = %q, want %q", loc, want)
		}
	})

	// A legacy link to a port that is not published lands on the Preview page
	// with the port named, where it can be added — its hostname would only
	// answer 403.
	t.Run("the legacy path form sends an unpublished port to the page", func(t *testing.T) {
		resp, err := noRedirectClient().Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview/4999/")
		if err != nil {
			t.Fatalf("GET legacy path: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/preview?port=4999" {
			t.Fatalf("Location = %q, want /preview?port=4999", loc)
		}
	})

	// The port-less /preview must NOT be matched by the redirect wildcard — it
	// is the SPA route where the preview page lives. If chi's /preview/* ever
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
		resp, err := noRedirectClient().Get("http://127.0.0.1:" + itoa(srv.Port()) + "/preview/")
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

// TestServe_PreviewStripsFrameGuards pins that a service which forbids framing
// (X-Frame-Options or a CSP frame-ancestors directive) still renders inside the
// preview's iframe: the proxy strips those headers on the way through, because
// the target is the user's own loopback process.
func TestServe_PreviewStripsFrameGuards(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src *")
		w.Header().Add("Content-Security-Policy-Report-Only", "frame-ancestors example.com")
		_, _ = io.WriteString(w, "<title>guarded app</title>")
	}))
	defer backend.Close()
	port := previewPort(t, backend.URL)

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, atoi(t, port))

	resp, err := previewClient(srv.Port()).Get(previewHostURL(t, srv, atoi(t, port)))
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Frame-Options"); v != "" {
		t.Fatalf("X-Frame-Options = %q, want stripped", v)
	}
	for _, csp := range resp.Header.Values("Content-Security-Policy") {
		if strings.Contains(strings.ToLower(csp), "frame-ancestors") {
			t.Fatalf("CSP still carries frame-ancestors: %q", csp)
		}
		if !strings.Contains(csp, "default-src 'self'") {
			t.Fatalf("CSP lost unrelated directives: %q", csp)
		}
	}
	for _, csp := range resp.Header.Values("Content-Security-Policy-Report-Only") {
		if strings.Contains(strings.ToLower(csp), "frame-ancestors") {
			t.Fatalf("report-only CSP still carries frame-ancestors: %q", csp)
		}
	}
}

// TestStripFrameAncestors pins the directive removal directly: frame-ancestors
// is dropped (anywhere, any case), everything else in the policy survives.
func TestStripFrameAncestors(t *testing.T) {
	h := make(http.Header)
	h.Add("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src *")
	h.Add("Content-Security-Policy", "script-src 'self'")
	stripFrameAncestors(h)

	var joined string
	for _, v := range h.Values("Content-Security-Policy") {
		joined += v
	}
	if strings.Contains(strings.ToLower(joined), "frame-ancestors") {
		t.Fatalf("frame-ancestors survived: %q", joined)
	}
	if !strings.Contains(joined, "default-src 'self'") || !strings.Contains(joined, "img-src *") || !strings.Contains(joined, "script-src 'self'") {
		t.Fatalf("unrelated directives lost: %q", joined)
	}

	// A policy that was only frame-ancestors collapses to nothing.
	h2 := make(http.Header)
	h2.Set("Content-Security-Policy", "frame-ancestors 'none'")
	stripFrameAncestors(h2)
	if got := h2.Get("Content-Security-Policy"); got != "" {
		t.Fatalf("CSP = %q, want empty after dropping the only directive", got)
	}
}

// TestServe_PreviewBadPort pins that a non-numeric port answers a clear 400
// rather than being interpreted as a host, and never reaches the backend.
func TestServe_PreviewBadPort(t *testing.T) {
	srv := startPreviewServer(t)

	base := "http://127.0.0.1:" + itoa(srv.Port())
	for _, path := range []string{"/preview/xyz/", "/preview/0/", "/preview/99999/", "/preview/+3000/", "/preview/03000/"} {
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

// TestServe_PreviewDown is the nothing-is-listening case for a published port:
// the proxy must fail with a 502 (not the SPA fallback, which would answer 200 text/html and leave
// the preview page thinking the service is up).
func TestServe_PreviewDown(t *testing.T) {
	// Grab a port, then free it so nothing answers there.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadPort := previewPort(t, backend.URL)
	backend.Close()

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, atoi(t, deadPort))

	resp, err := previewClient(srv.Port()).Get(previewHostURL(t, srv, atoi(t, deadPort)))
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
		// One port, one spelling: Atoi would take both of these as 3000.
		{"+3000", 0, "", false},
		{"03000", 0, "", false},
		{"3000/a%2Fb", 3000, "/a%2Fb", true},
	}
	for _, c := range cases {
		port, path, ok := parsePreviewPath(c.in)
		if ok != c.ok || port != c.port || path != c.path {
			t.Errorf("parsePreviewPath(%q) = (%d, %q, %v), want (%d, %q, %v)",
				c.in, port, path, ok, c.port, c.path, c.ok)
		}
	}
}

// TestParsePreviewHost pins the host-form dispatch directly: the label to the
// left of the preview domain is a loopback port, and only a canonical in-range
// number counts. Everything under the domain is reported as such — it must
// never reach ogcode's own routes — while a host outside it falls through to the
// normal router.
func TestParsePreviewHost(t *testing.T) {
	const suffix = "preview.localhost"
	cases := []struct {
		host     string
		port     int
		ok       bool
		inDomain bool
	}{
		{"3000.preview.localhost", 3000, true, true},
		{"3000.preview.localhost:7799", 3000, true, true},
		{"8080.Preview.Localhost", 8080, true, true},
		{"1.preview.localhost", 1, true, true},
		{"65535.preview.localhost", 65535, true, true},
		// A fully-qualified name with its root dot is the same host.
		{"3000.preview.localhost.", 3000, true, true},
		{"3000.preview.localhost.:7799", 3000, true, true},
		// Under the domain but not a preview: refused, never ogcode's own UI.
		{"0.preview.localhost", 0, false, true},
		{"65536.preview.localhost", 0, false, true},
		{"x.preview.localhost", 0, false, true},
		{"preview.localhost", 0, false, true},
		{"preview.localhost:7799", 0, false, true},
		{"3000.a.preview.localhost", 0, false, true},
		// One port, one spelling: Atoi would take both of these as 3000.
		{"+3000.preview.localhost", 0, false, true},
		{"03000.preview.localhost", 0, false, true},
		// Outside the domain: the normal router.
		{"3000.other", 0, false, false},
		{"3000.preview.localhost.evil", 0, false, false},
		{"evil-preview.localhost", 0, false, false},
		{"localhost:7799", 0, false, false},
		{"127.0.0.1:7799", 0, false, false},
	}
	for _, c := range cases {
		port, ok, inDomain := parsePreviewHost(c.host, suffix)
		if ok != c.ok || port != c.port || inDomain != c.inDomain {
			t.Errorf("parsePreviewHost(%q, %q) = (%d, %v, %v), want (%d, %v, %v)",
				c.host, suffix, port, ok, inDomain, c.port, c.ok, c.inDomain)
		}
	}
	// An empty suffix never matches, so a server with no preview domain treats
	// no host as a preview rather than matching everything.
	if _, ok, inDomain := parsePreviewHost("3000.preview.localhost", ""); ok || inDomain {
		t.Error("empty suffix matched a preview host")
	}
}

// TestServe_PreviewDomainNeverReachesOgcode pins that every name under the
// preview domain is the previews' alone: a malformed or non-numeric label, or
// the bare domain, answers 404 instead of falling through to ogcode's own UI
// and API — which an operator may guard on the main host only.
func TestServe_PreviewDomainNeverReachesOgcode(t *testing.T) {
	srv := startPreviewServer(t)
	for _, host := range []string{
		"03000." + agent.PreviewDomain(),
		"admin." + agent.PreviewDomain(),
		"3000.a." + agent.PreviewDomain(),
		agent.PreviewDomain(),
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+itoa(srv.Port())+"/api/path", nil)
		req.Host = host + ":" + itoa(srv.Port())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET via %s: %v", host, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "\"directory\"") {
			t.Errorf("%s: status %d body %q, want a 404 that is not ogcode's API", host, resp.StatusCode, body)
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

// TestServe_PreviewServesOnlyPublishedPorts pins the proxy's allowlist. A
// loopback port is often private precisely because it is loopback-only — a
// database console, a debugger, another ogcode — and a preview hostname is
// meant to be shared, so the hostname serves exactly the ports someone chose to
// show: 403 (and the service never dialed) until the port is published, 200
// while it is, 403 again once it is taken off.
func TestServe_PreviewServesOnlyPublishedPorts(t *testing.T) {
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>private console</title>")
	}))
	defer backend.Close()
	port := atoi(t, previewPort(t, backend.URL))

	srv := startPreviewServer(t)
	client := previewClient(srv.Port())
	get := func() (int, string) {
		t.Helper()
		resp, err := client.Get(previewHostURL(t, srv, port))
		if err != nil {
			t.Fatalf("GET preview: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := get(); code != http.StatusForbidden || !strings.Contains(body, "not a published preview") {
		t.Fatalf("unpublished: status %d body %q, want 403 naming the rule", code, body)
	}
	if hits.Load() != 0 {
		t.Fatal("the proxy dialed an unpublished port")
	}

	publishPreviewPort(t, srv, port)
	if code, body := get(); code != http.StatusOK || !strings.Contains(body, "private console") {
		t.Fatalf("published: status %d body %q, want the service", code, body)
	}

	unpublishPreviewPort(t, srv, port)
	before := hits.Load()
	if code, _ := get(); code != http.StatusForbidden {
		t.Fatalf("after unpublish: status %d, want 403", code)
	}
	if hits.Load() != before {
		t.Fatal("the proxy dialed a port after it was unpublished")
	}
}

// TestServe_PreviewNeverServesItsOwnPort pins that the server's own port is
// refused at its preview hostname even if something recorded it: proxied, it
// would be a second door into ogcode's whole API on a hostname that may sit
// outside the authentication guarding the main one.
func TestServe_PreviewNeverServesItsOwnPort(t *testing.T) {
	srv := startPreviewServer(t)

	// The API refuses to publish it...
	resp, err := http.Post("http://127.0.0.1:"+itoa(srv.Port())+"/api/preview/ports",
		"application/json", strings.NewReader(`{"port":`+itoa(srv.Port())+`}`))
	if err != nil {
		t.Fatalf("publish self: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("publish self: status %d, want 400", resp.StatusCode)
	}

	// ...and the proxy refuses it regardless: an /api path on the self preview
	// host must not reach the API.
	resp, err = previewClient(srv.Port()).Get(previewHostURL(t, srv, srv.Port()) + "api/path")
	if err != nil {
		t.Fatalf("GET self preview: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self preview: status %d body %q, want 403", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "\"directory\"") {
		t.Fatalf("self preview answered with the API: %q", body)
	}
}

// TestServe_PreviewBypassesOgcodeCORS pins that a preview host is a transparent
// pass-through, not an ogcode route: an OPTIONS preflight reaches the service
// (ogcode's CORS middleware would otherwise answer it itself), the service's own
// Access-Control-Allow-Origin arrives alone (a second "*" would make browsers
// reject it), and a service that sets none gets none — ogcode's "*" would open
// every loopback service to reads from any website.
func TestServe_PreviewBypassesOgcodeCORS(t *testing.T) {
	var preflights atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodOptions:
			preflights.Add(1)
			w.Header().Set("Access-Control-Allow-Origin", "http://app.example")
			w.Header().Set("Access-Control-Allow-Headers", "content-type")
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/cors":
			w.Header().Set("Access-Control-Allow-Origin", "http://app.example")
			_, _ = io.WriteString(w, "ok")
		default:
			_, _ = io.WriteString(w, "no cors")
		}
	}))
	defer backend.Close()
	port := atoi(t, previewPort(t, backend.URL))

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, port)
	client := previewClient(srv.Port())
	base := previewHostURL(t, srv, port)

	req, _ := http.NewRequest(http.MethodOptions, base+"api", nil)
	req.Header.Set("Origin", "http://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	resp.Body.Close()
	if preflights.Load() != 1 {
		t.Fatalf("preflight reached the service %d times, want 1", preflights.Load())
	}
	if got := resp.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "http://app.example" {
		t.Fatalf("preflight ACAO = %q, want only the service's own", got)
	}

	for path, want := range map[string][]string{"cors": {"http://app.example"}, "plain": nil} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		got := resp.Header.Values("Access-Control-Allow-Origin")
		if len(got) != len(want) || (len(want) == 1 && got[0] != want[0]) {
			t.Fatalf("GET %s ACAO = %q, want %q", path, got, want)
		}
	}
}

// TestServe_PreviewForwardedHeaders pins what the service learns about the
// browser-facing request: the preview host (to build absolute URLs), the scheme
// the browser used — https when a TLS-terminating proxy in front says so, not
// the plain http of the last hop, which would make an app emit mixed content or
// redirect-loop — and the client chain with the peer this server saw appended.
func TestServe_PreviewForwardedHeaders(t *testing.T) {
	type seen struct{ host, xfHost, xfProto, xfFor string }
	got := make(chan seen, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Host, r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For")}
	}))
	defer backend.Close()
	port := atoi(t, previewPort(t, backend.URL))

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, port)

	req, _ := http.NewRequest(http.MethodGet, previewHostURL(t, srv, port), nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	resp, err := previewClient(srv.Port()).Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	s := <-got

	if s.host != "127.0.0.1:"+strconv.Itoa(port) {
		t.Errorf("upstream Host = %q, want the loopback target (what dev-server host checks accept)", s.host)
	}
	if want := strconv.Itoa(port) + "." + agent.PreviewDomain() + ":" + itoa(srv.Port()); s.xfHost != want {
		t.Errorf("X-Forwarded-Host = %q, want %q", s.xfHost, want)
	}
	if s.xfProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https (the scheme the browser used)", s.xfProto)
	}
	if s.xfFor != "203.0.113.7, 127.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want the inbound chain plus the real peer", s.xfFor)
	}
}

// TestServe_PreviewRewritesLoopbackRedirects pins the Location mapping: an app
// that builds absolute URLs from the Host it sees (127.0.0.1:<port>) redirects
// to its loopback origin, which from the browser's side is the viewer's OWN
// machine — so such a Location is mapped back onto the preview host. Relative
// redirects, other ports and other hosts pass through untouched.
func TestServe_PreviewRewritesLoopbackRedirects(t *testing.T) {
	var port int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := map[string]string{
			"/ip":       "http://127.0.0.1:" + strconv.Itoa(port) + "/en/?a=1",
			"/name":     "http://localhost:" + strconv.Itoa(port) + "/login",
			"/relative": "/en/",
			"/other":    "http://127.0.0.1:1/elsewhere",
			"/foreign":  "https://accounts.example.com/auth",
		}[r.URL.Path]
		w.Header().Set("Location", loc)
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()
	port = atoi(t, previewPort(t, backend.URL))

	srv := startPreviewServer(t)
	publishPreviewPort(t, srv, port)
	origin := strings.TrimSuffix(previewHostURL(t, srv, port), "/")

	client := previewClient(srv.Port())
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for path, want := range map[string]string{
		"/ip":       origin + "/en/?a=1",
		"/name":     origin + "/login",
		"/relative": "/en/",
		"/other":    "http://127.0.0.1:1/elsewhere",
		"/foreign":  "https://accounts.example.com/auth",
	} {
		resp, err := client.Get(origin + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("%s: Location = %q, want %q", path, got, want)
		}
	}
}

// TestServe_PreviewRefusesConnect pins that a preview host is an HTTP origin,
// not a tunnel: CONNECT is refused rather than handed to the reverse proxy.
func TestServe_PreviewRefusesConnect(t *testing.T) {
	srv := startPreviewServer(t)
	conn, err := net.Dial("tcp", "127.0.0.1:"+itoa(srv.Port()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	host := "3000." + agent.PreviewDomain() + ":" + itoa(srv.Port())
	if _, err := io.WriteString(conn, "CONNECT "+host+" HTTP/1.1\r\nHost: "+host+"\r\n\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("CONNECT status = %d, want 405", resp.StatusCode)
	}
}
