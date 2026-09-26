package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestParsePreviewListeners covers the `lsof -Fn` reader: the localhost and
// wildcard binds are kept, a LAN-only bind is dropped (the proxy dials the
// literal 127.0.0.1, so showing it would offer a tile that cannot load),
// duplicates collapse, and the result is sorted.
func TestParsePreviewListeners(t *testing.T) {
	out := `p395
f8
n*:7000
f9
n*:7000
p507
f11
n127.0.0.1:20241
p514
f18
n127.0.0.1:8765
p900
f20
n192.168.1.5:5000
p901
f21
n[::1]:9229
p902
f22
n0.0.0.0:3000
`
	got := parsePreviewListeners(out)
	want := []int{3000, 7000, 8765, 20241}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (sorted, localhost/wildcard only)", got, want)
		}
	}
}

// TestParsePreviewListeners_Empty pins the lsof-failed case: no output parses to
// no ports, not an error — discovery is best-effort and the manual add still
// works without it.
func TestParsePreviewListeners_Empty(t *testing.T) {
	if got := parsePreviewListeners(""); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestParseRequestedPorts covers the ?ports= reader: comma-separated, trimmed,
// deduped, with a bad entry dropped rather than failing the request.
func TestParseRequestedPorts(t *testing.T) {
	got := parseRequestedPorts("3000, 8080,3000,xyz,0,70000,")
	want := []int{3000, 8080}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestPreviewCandidatesFrom pins that the server's own port and every requested
// port are removed from the discovered list, so a port is auto-detected or
// explicitly requested — never both.
func TestPreviewCandidatesFrom(t *testing.T) {
	got := previewCandidatesFrom([]int{3000, 8080, 9699, 5173}, []int{8080}, 9699)
	want := []int{3000, 5173}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestCleanTitle pins the <title> label: entities decoded, whitespace
// collapsed, and a long title clipped with an ellipsis.
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  My   App  ", "My App"},
		{"Tom &amp; Jerry", "Tom & Jerry"},
		{"line\n  two", "line two"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("x", 200)
	got := cleanTitle(long)
	if r := []rune(got); len(r) > 81 || !strings.HasSuffix(got, "…") {
		t.Errorf("cleanTitle(long) = %q (len %d), want it clipped to ~80 runes + ellipsis", got, len(r))
	}
}

// TestProbePreviewService covers the probe against a real backend: an HTML page
// is up+HTML with its title read, a plain-text answer is up but not HTML (so
// discovery drops it), and a dead port is not up at all.
func TestProbePreviewService(t *testing.T) {
	htmlBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><head><title>the player</title></head><body>hi</body></html>")
	}))
	defer htmlBackend.Close()

	t.Run("html page is up and titled", func(t *testing.T) {
		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, htmlBackend.URL)))
		if !up || !isHTML {
			t.Fatalf("up=%v isHTML=%v, want both true", up, isHTML)
		}
		if title != "the player" {
			t.Fatalf("title = %q, want %q", title, "the player")
		}
	})

	t.Run("plain text is up but not html", func(t *testing.T) {
		txt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "Ollama is running")
		}))
		defer txt.Close()
		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, txt.URL)))
		if !up || isHTML {
			t.Fatalf("up=%v isHTML=%v, want up true and isHTML false", up, isHTML)
		}
		if title != "" {
			t.Fatalf("title = %q, want empty for a non-HTML answer", title)
		}
	})

	t.Run("500 is not up", func(t *testing.T) {
		broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer broken.Close()
		if _, up, _ := probePreviewService(atoi(t, previewPort(t, broken.URL))); up {
			t.Fatal("up = true, want false for a 5xx")
		}
	})

	t.Run("dead port is not up", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		port := atoi(t, previewPort(t, dead.URL))
		dead.Close()
		if _, up, _ := probePreviewService(port); up {
			t.Fatal("up = true, want false for a closed port")
		}
	})
}

// TestGatherPreviewServices pins the grid rule: a discovered candidate is kept
// only when it answers with HTML, while a requested port is listed whether or
// not it answers, so a hand-added port or a deep link that is down still shows
// a tile instead of silently vanishing.
func TestGatherPreviewServices(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>dashboard</title>")
	}))
	defer app.Close()
	appPort := atoi(t, previewPort(t, app.URL))

	// A discovered listener that answers plain text — an Ollama-style port the
	// grid must NOT surface as an app.
	notAnApp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not html")
	}))
	defer notAnApp.Close()
	notAnAppPort := atoi(t, previewPort(t, notAnApp.URL))

	// A requested port that is down must still be listed.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadPort := atoi(t, previewPort(t, dead.URL))
	dead.Close()

	services := gatherPreviewServices([]int{appPort, notAnAppPort}, []int{deadPort})

	byPort := map[int]previewService{}
	for _, s := range services {
		byPort[s.Port] = s
	}

	if got, ok := byPort[appPort]; !ok || !got.Up || got.Source != "auto" || got.Title != "dashboard" {
		t.Fatalf("app port = %+v (present %v), want auto/up/dashboard", got, ok)
	}
	if _, ok := byPort[notAnAppPort]; ok {
		t.Fatalf("plain-text listener surfaced as an app: %+v", byPort[notAnAppPort])
	}
	if got, ok := byPort[deadPort]; !ok || got.Up || got.Source != "manual" {
		t.Fatalf("down requested port = %+v (present %v), want manual/down", got, ok)
	}
	// Sorted by port.
	for i := 1; i < len(services); i++ {
		if services[i-1].Port > services[i].Port {
			t.Fatalf("services not sorted by port: %+v", services)
		}
	}
}

// TestServe_PreviewServices covers GET /api/preview/services end to end with a
// fake lsof on PATH (CI may have none): the handler must discover the HTML app
// the fake lsof reports, drop the non-loopback bind, and list a requested port
// that is down.
func TestServe_PreviewServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake lsof is a sh script; CI runs this on linux/mac")
	}

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>my app</title>")
	}))
	defer app.Close()
	appPort := previewPort(t, app.URL)

	// A LAN-only bind the parser must drop, plus a duplicate and a wildcard bind
	// on the same app port (lsof prints one line per open socket).
	scratch := t.TempDir()
	fakeLsof := filepath.Join(scratch, "lsof")
	script := "#!/bin/sh\ncat <<'EOF'\n" +
		"p100\nf4\nn127.0.0.1:" + appPort + "\n" +
		"p100\nf5\nn*:" + appPort + "\n" +
		"p200\nf6\nn192.168.1.9:5555\n" +
		"EOF\n"
	if err := os.WriteFile(fakeLsof, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scratch+string(os.PathListSeparator)+os.Getenv("PATH"))

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

	// A requested port that nothing answers on: it must still appear.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadPort := previewPort(t, dead.URL)
	dead.Close()

	resp, err := http.Get(base + "/api/preview/services?ports=" + deadPort)
	if err != nil {
		t.Fatalf("GET /api/preview/services: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %q", resp.StatusCode, body)
	}
	s := string(body)
	if !strings.Contains(s, `"port":`+appPort) || !strings.Contains(s, `"title":"my app"`) || !strings.Contains(s, `"source":"auto"`) {
		t.Fatalf("body = %q, want the discovered HTML app with its title", body)
	}
	if !strings.Contains(s, `"port":`+deadPort) || !strings.Contains(s, `"source":"manual"`) || !strings.Contains(s, `"up":false`) {
		t.Fatalf("body = %q, want the down requested port listed as manual", body)
	}
	if strings.Contains(s, `"port":5555`) {
		t.Fatalf("body = %q, want the LAN-only bind dropped", body)
	}

	// A broken/absent lsof degrades to an empty auto list (still 200), not a
	// 500. Simulated by pointing PATH at a directory with no lsof at all — the
	// Windows/absent case — because a bare-name lookup that finds a
	// non-executable fake would simply fall through to the real lsof on PATH.
	t.Setenv("PATH", t.TempDir())
	resp2, err := http.Get(base + "/api/preview/services")
	if err != nil {
		t.Fatalf("GET /api/preview/services after breaking lsof: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d after breaking lsof; body = %q", resp2.StatusCode, body2)
	}
	if !strings.Contains(string(body2), `"services":[]`) {
		t.Fatalf("body = %q, want an empty service list when lsof fails", body2)
	}
}

// TestPreviewListeners_RealLsof is a smoke test against the machine's real
// lsof, when there is one: it pins that the output actually parses rather than
// trusting the fixture. Skipped where lsof is absent.
func TestPreviewListeners_RealLsof(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed here")
	}
	ports := previewListeners()
	// The result is machine-dependent, so assert only sanity: sorted, in range.
	for i, p := range ports {
		if p < 1 || p > 65535 {
			t.Fatalf("port %d out of range", p)
		}
		if i > 0 && ports[i-1] > p {
			t.Fatalf("ports not sorted: %v", ports)
		}
	}
}

// TestSplitListenAddress pins the address split, including the bracketed IPv6
// form (the port is after the last colon).
func TestSplitListenAddress(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port string
		ok   bool
	}{
		{"127.0.0.1:3000", "127.0.0.1", "3000", true},
		{"*:7000", "*", "7000", true},
		{"[::1]:9229", "[::1]", "9229", true},
		{"3000", "", "", false},
	}
	for _, c := range cases {
		host, port, ok := splitListenAddress(c.in)
		if ok != c.ok || host != c.host || port != c.port {
			t.Errorf("splitListenAddress(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, host, port, ok, c.host, c.port, c.ok)
		}
	}
}

// TestLoopbackReachable pins which binds the proxy can actually dial: the
// proxy's target host is the literal 127.0.0.1, so wildcard binds reach it and
// a LAN-only or IPv6-loopback bind does not.
func TestLoopbackReachable(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "*", "0.0.0.0"} {
		if !loopbackReachable(host) {
			t.Errorf("loopbackReachable(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"192.168.1.5", "[::1]", "::1", "10.0.0.8"} {
		if loopbackReachable(host) {
			t.Errorf("loopbackReachable(%q) = true, want false (unreachable at 127.0.0.1)", host)
		}
	}
}

// TestPreviewListenersOutput_Shape pins that the lsof command is invoked as the
// parser expects: `-Fn` field output on a real lsof yields `n<address>` lines.
// Skipped where lsof is absent.
func TestPreviewListenersOutput_Shape(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed here")
	}
	out, err := previewListenersOutput()
	if err != nil {
		// lsof exiting non-zero with no listeners is acceptable; the parse of
		// whatever it printed is what this checks.
		t.Logf("lsof returned %v; parsing output anyway", err)
	}
	if out == "" {
		t.Skip("lsof printed nothing on this machine")
	}
	if !strings.Contains(out, "n") {
		t.Fatalf("lsof output has no field lines: %q", out)
	}
	// The parser must not panic or produce garbage on the real output.
	for _, p := range parsePreviewListeners(out) {
		if u, err := url.Parse("http://127.0.0.1:" + itoa(p)); err != nil || u.Port() == "" {
			t.Fatalf("parsed an unusable port %d", p)
		}
	}
}
