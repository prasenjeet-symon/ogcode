package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestServe_ScrcpyProxy pins the feature: every server instance forwards
// /scrcpy/* to the separately-run ws-scrcpy app (stubbed here by an httptest
// backend standing in for 127.0.0.1:8000), stripping the /scrcpy prefix, and
// answers 502 (not the SPA fallback) when ws-scrcpy is down.
func TestServe_ScrcpyProxy(t *testing.T) {
	// Stand in for ws-scrcpy: its SPA index plus an asset. The real app serves
	// bundle.js, main.css and the scrcpy-server jar at its root.
	var hits int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>WS scrcpy</title><script src=\"bundle.js\"></script>")
		case "/bundle.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, "// ws-scrcpy bundle")
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("OGCODE_SCRCPY_TARGET", backend.URL)
	// -count>1 reruns this test in the same process: drop any proxy a previous
	// test built so this run rebuilds against OUR stub.
	resetScrcpyProxyForTest()

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

	t.Run("forwards index with prefix stripped", func(t *testing.T) {
		resp, err := http.Get(base + "/scrcpy/")
		if err != nil {
			t.Fatalf("GET /scrcpy/: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %q", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "WS scrcpy") {
			t.Fatalf("body = %q, want the ws-scrcpy SPA index", body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("Content-Type = %q, want text/html", ct)
		}
	})

	t.Run("forwards assets deeper in the path", func(t *testing.T) {
		resp, err := http.Get(base + "/scrcpy/bundle.js")
		if err != nil {
			t.Fatalf("GET /scrcpy/bundle.js: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "ws-scrcpy bundle") {
			t.Fatalf("status = %d body = %q, want the asset body", resp.StatusCode, body)
		}
	})

	t.Run("redirects bare path to trailing slash", func(t *testing.T) {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(base + "/scrcpy")
		if err != nil {
			t.Fatalf("GET /scrcpy: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/scrcpy/" {
			t.Fatalf("Location = %q, want /scrcpy/", loc)
		}
	})

	t.Run("404 from backend passes through", func(t *testing.T) {
		resp, err := http.Get(base + "/scrcpy/no/such/path")
		if err != nil {
			t.Fatalf("GET /scrcpy/no/such/path: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 passthrough", resp.StatusCode)
		}
	})

	t.Run("reachable check dials the backend", func(t *testing.T) {
		if !srv.scrcpyReachable() {
			t.Fatal("scrcpyReachable = false, want true while the stub answers")
		}
	})

	if hits == 0 {
		t.Fatal("backend never reached")
	}
}

// TestServe_ScrcpyDown is the ws-scrcpy-not-running case: the proxy must fail
// with a 502 whose body names the fix, and must never fall through to the SPA
// fallback (which would answer 200 text/html and confuse the panel).
func TestServe_ScrcpyDown(t *testing.T) {
	// A port with nothing listening: dialing fails fast.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	backendURL := backend.URL
	backend.Close() // free the port so nothing answers on it

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

	// Point the proxy target at the dead port. The proxy singleton is built
	// on the first request, and it reads the env override at build time, so
	// setting it (and resetting the once) before the first hit routes this
	// test's traffic there.
	setenvScrappyTargetForTest(t, backendURL)

	resp, err := http.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/scrcpy/")
	if err != nil {
		t.Fatalf("GET /scrcpy/ against a dead target: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "ws-scrcpy is not reachable") {
		t.Fatalf("body = %q, want the ws-scrcpy error text", body)
	}
	if strings.Contains(string(body), "<!doctype") {
		t.Fatalf("body = %q, looks like the SPA fallback", body)
	}
}

// TestServe_ScrcpyStatus covers GET /api/scrcpy/status, the endpoint the device
// panel polls to choose between embedding the stream and showing the start
// hint. Up must mirror scrcpyReachable and target must echo the configured
// destination — the panel renders "not reachable at <target>" from it.
func TestServe_ScrcpyStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer backend.Close()
	t.Setenv("OGCODE_SCRCPY_TARGET", backend.URL)
	resetScrcpyProxyForTest()

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
		resp, err := http.Get(base + "/api/scrcpy/status")
		if err != nil {
			t.Fatalf("GET /api/scrcpy/status: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d; body = %q", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"up":true`) {
			t.Fatalf("body = %q, want up true while the stub answers", body)
		}
		if !strings.Contains(string(body), `"target":"`+backend.URL+`"`) {
			t.Fatalf("body = %q, want the configured target echoed", body)
		}
	})

	t.Run("down after the target dies", func(t *testing.T) {
		backend.Close()
		if srv.scrcpyReachable() {
			t.Fatal("scrcpyReachable = true, want false once the target is closed")
		}
		resp, err := http.Get(base + "/api/scrcpy/status")
		if err != nil {
			t.Fatalf("GET /api/scrcpy/status: %v", err)
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
}

// setenvScrappyTargetForTest swaps the proxy target. The package-level
// singleton is built once per process, so a test that must point it elsewhere
// resets it and the env override read by the next build.
func setenvScrappyTargetForTest(t *testing.T, target string) {
	t.Helper()
	t.Setenv("OGCODE_SCRCPY_TARGET", target)
	resetScrcpyProxyForTest()
}

func resetScrcpyProxyForTest() {
	scrcpyProxyOnce = sync.Once{}
	scrcpyProxy = nil
}

// TestParseAdbDevices covers the `adb devices -l` reader: the header, the
// daemon chatter (the `*` lines), offline and unauthorized states, the model
// prop with its underscores, and lines that carry no state.
func TestParseAdbDevices(t *testing.T) {
	out := `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
emulator-5554   device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64xa transport_id:1
emulator-5556   offline transport_id:2
10.0.0.8:5555   unauthorized transport_id:3
deadbeef        device usb:1-2 product:raven model:Pixel_6_Pro device:raven transport_id:4
emulator-5554   device product:duplicate model:ignored transport_id:9

`
	got := parseAdbDevices(out)
	if len(got) != 4 {
		t.Fatalf("got %d devices (%v), want 4", len(got), got)
	}
	bySerial := map[string]scrcpyDevice{}
	for _, d := range got {
		bySerial[d.Serial] = d
	}
	if d := bySerial["emulator-5554"]; d.State != "device" || d.Model != "sdk gphone64 arm64" {
		t.Fatalf("emulator-5554 = %+v, want state device + model \"sdk gphone64 arm64\"", d)
	}
	if d := bySerial["emulator-5556"]; d.State != "offline" || d.Model != "" {
		t.Fatalf("emulator-5556 = %+v, want offline with no model", d)
	}
	if d := bySerial["10.0.0.8:5555"]; d.State != "unauthorized" {
		t.Fatalf("10.0.0.8:5555 = %+v, want unauthorized", d)
	}
	if d := bySerial["deadbeef"]; d.Model != "Pixel 6 Pro" {
		t.Fatalf("deadbeef = %+v, want model \"Pixel 6 Pro\"", d)
	}
}

// TestParseAdbDevices_Empty pins the no-devices case: the header alone parses
// to an empty (non-nil-ness irrelevant) list, not an error.
func TestParseAdbDevices_Empty(t *testing.T) {
	if got := parseAdbDevices("List of devices attached\n"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestServe_ScrcpyDevices covers GET /api/scrcpy/devices end to end with a
// fake adb on PATH (CI has no adb and no emulator): the handler must list the
// fake devices, and an adb that fails must degrade to an empty list — not a
// 500 — because "adb is broken" renders as "no devices" in the picker.
func TestServe_ScrcpyDevices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a sh script; CI runs this on linux/mac")
	}
	scratch := t.TempDir()
	// Point ANDROID_HOME at a platform-tools layout adbBinary() resolves.
	tools := filepath.Join(scratch, "platform-tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeADB := filepath.Join(tools, "adb")
	script := "#!/bin/sh\ncat <<'EOF'\nList of devices attached\nemulator-5554   device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64xa\nEOF\n"
	if err := os.WriteFile(fakeADB, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", scratch)

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

	resp, err := http.Get(base + "/api/scrcpy/devices")
	if err != nil {
		t.Fatalf("GET /api/scrcpy/devices: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"serial":"emulator-5554"`) ||
		!strings.Contains(string(body), `"state":"device"`) ||
		!strings.Contains(string(body), `"model":"sdk gphone64 arm64"`) {
		t.Fatalf("body = %q, want the fake adb's device with model underscores turned to spaces", body)
	}

	// Now make adb fail (not executable) and confirm the endpoint stays 200
	// with an empty list instead of erroring.
	if err := os.Chmod(fakeADB, 0o644); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(base + "/api/scrcpy/devices")
	if err != nil {
		t.Fatalf("GET /api/scrcpy/devices after breaking adb: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d after breaking adb; body = %q", resp2.StatusCode, body2)
	}
	if !strings.Contains(string(body2), `"devices":[]`) {
		t.Fatalf("body = %q, want an empty devices list when adb fails", body2)
	}
}
