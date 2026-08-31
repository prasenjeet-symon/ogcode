//go:build darwin

package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// renderProbePage is a page whose visible content does not exist in the markup
// that was served: a script replaces it shortly after load. Reading `source`
// returns the placeholder; reading the rendered DOM returns the replacement.
//
// It is served from a local file rather than the network so the test measures
// exactly one thing — which of the two the backend read — with no site's
// behaviour, latency or bot policy mixed in.
const renderProbePage = `<!doctype html>
<html><head><title>render probe</title></head>
<body><div id="target">PLACEHOLDER-FROM-SOURCE</div>
<script>setTimeout(function () {
  document.getElementById("target").textContent = "REPLACED-BY-SCRIPT";
}, 250);</script>
</body></html>`

// This pins the capability the readPage path exists for, and it is written to
// be informative either way: where Safari permits JavaScript from Apple Events
// it asserts the rendered DOM came back, and where it does not it skips with
// the reason rather than failing. A machine without the setting is not a broken
// machine — it is the default one, and the fallback it takes is the behaviour
// that predates readPage.
func TestLiveSafariReadsRenderedDOM(t *testing.T) {
	b := requireLiveSafari(t)

	dir := t.TempDir()
	// World-readable: Safari runs as the user but out of its own container, and
	// a 0600 file in a temp directory is not always reachable from it.
	path := filepath.Join(dir, "render-probe.html")
	if err := os.WriteFile(path, []byte(renderProbePage), 0o644); err != nil {
		t.Fatalf("write probe page: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod temp dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	html, err := b.loadHTML(ctx, "file://"+path, "")
	if err != nil {
		t.Fatalf("load probe page: %v", err)
	}

	switch {
	case strings.Contains(html, "REPLACED-BY-SCRIPT"):
		t.Log("rendered DOM was read: client-side content is visible to fetch_page")
	case strings.Contains(html, "PLACEHOLDER-FROM-SOURCE"):
		if b.javascriptAvailable() {
			t.Error("read the served markup even though JavaScript was not refused — " +
				"readPage should have preferred the rendered DOM")
		}
		t.Skip("Safari is not permitting JavaScript from Apple Events; the served markup was used, " +
			"which is the documented fallback")
	default:
		t.Fatalf("probe page came back as neither variant: %.200q", html)
	}
}

// The dwell is what keeps the read from happening at the same instant after
// every load. It is small, so this only checks it is present and varies rather
// than asserting a distribution.
func TestSafariDwellVaries(t *testing.T) {
	seen := make(map[time.Duration]bool)
	for i := 0; i < 40; i++ {
		d := safariDwell()
		if d < safariDwellMin || d >= safariDwellMin+safariDwellJitter {
			t.Fatalf("dwell %v is outside [%v, %v)", d, safariDwellMin, safariDwellMin+safariDwellJitter)
		}
		seen[d] = true
	}
	if len(seen) < 5 {
		t.Errorf("dwell took only %d distinct values across 40 draws — it is not being randomised", len(seen))
	}
}

// A refusal must expire. The setting behind it is a checkbox someone can tick
// while ogcode is running, and a permanent "no" would mean they tick it and
// nothing changes until they restart.
func TestSafariJavaScriptRefusalExpires(t *testing.T) {
	b := NewSafariBackend()
	if !b.javascriptAvailable() {
		t.Fatal("a fresh backend should attempt the DOM read")
	}

	b.jsMu.Lock()
	b.jsKnown, b.jsOK, b.jsCheckedAt = true, false, time.Now()
	b.jsMu.Unlock()
	if b.javascriptAvailable() {
		t.Error("a fresh refusal should be honoured")
	}

	b.jsMu.Lock()
	b.jsCheckedAt = time.Now().Add(-safariJSRecheck - time.Second)
	b.jsMu.Unlock()
	if !b.javascriptAvailable() {
		t.Error("a refusal older than the recheck window should be retried")
	}
}

// A probe that was killed — a cancelled turn, or osascript hitting its own
// deadline — is not the user declining. Recording it as one would disable the
// rendered-DOM path, and with it Google, for minutes after a single Ctrl-C.
// This is not hypothetical: it is what the first version of the probe did, and
// a live run caught it.
func TestSafariJavaScriptProbeIgnoresInterruptions(t *testing.T) {
	b := NewSafariBackend()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.probeJavaScript(ctx, "1") {
		t.Error("a probe on a dead context should not report success")
	}

	b.jsMu.Lock()
	known := b.jsKnown
	b.jsMu.Unlock()
	if known {
		t.Error("an interrupted probe recorded a verdict; it must leave the question open")
	}
	if !b.javascriptAvailable() {
		t.Error("an interrupted probe disabled the rendered-DOM path")
	}
}

func TestIsTransientOSAError(t *testing.T) {
	interrupted := []string{
		"osascript: signal: killed",
		"osascript: context deadline exceeded",
		"safari: poll: context canceled",
	}
	for _, msg := range interrupted {
		if !isTransientOSAError(errors.New(msg)) {
			t.Errorf("%q should be treated as an interruption", msg)
		}
	}
	// A real refusal must still be recorded, or the hint never appears and the
	// backend retries a permission it has already been denied on every load.
	answers := []string{
		"osascript: Safari got an error: AppleEvent handler failed. (-10000)",
		"osascript: Not authorized to send Apple events to Safari. (-1743)",
	}
	for _, msg := range answers {
		if isTransientOSAError(errors.New(msg)) {
			t.Errorf("%q should be treated as an answer, not an interruption", msg)
		}
	}
}
