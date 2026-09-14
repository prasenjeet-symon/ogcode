package master

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
)

// TestRenderApex_WorktreeRoutes pins the Phase-3 console links: a worker with
// live per-worktree tunnels renders one link per "<workerID>-<route>" subdomain
// (and no bare link), while a worker without tunnels keeps the legacy bare link.
func TestRenderApex_WorktreeRoutes(t *testing.T) {
	srv := newTunnelTestServer(t)

	now := time.Now()
	tok, _ := pairing.New(testRouteSecret, time.Minute).Mint(now)
	srv.Registry().Add("abc123", "laptop", nil, nil, tok, now)
	srv.Registry().Add("def456", "desktop", nil, nil, tok, now)

	// Only abc123 has worktree tunnels attached.
	srv.tunnels.set("abc123", "treemain", deadYamuxPair(t))
	srv.tunnels.set("abc123", "feature-fix-auth", deadYamuxPair(t))

	rec := httptest.NewRecorder()
	srv.renderApex(rec, httptest.NewRequest(http.MethodGet, "http://panel.example/", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("renderApex = %d, body %s", rec.Code, body)
	}

	host := "panel.example"
	// abc123: one link per route subdomain, no bare link.
	for _, want := range []string{
		"http://abc123-treemain." + host + "/",
		"http://abc123-feature-fix-auth." + host + "/",
		"Open treemain UI",
		"Open feature-fix-auth UI",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("console missing %q", want)
		}
	}
	if strings.Contains(body, "http://abc123."+host+"/") {
		t.Errorf("worker with routes must not render a bare link (body=%s)", body)
	}
	// def456: no tunnels → legacy bare link.
	if !strings.Contains(body, "http://def456."+host+"/") {
		t.Errorf("console missing legacy bare link for def456 (body=%s)", body)
	}
	if !strings.Contains(body, "Open worker UI") {
		t.Errorf("console missing legacy open label for def456 (body=%s)", body)
	}
}

// TestTunnelRegistry_ClosedEntryFallsThrough pins that a dead session entry does
// not make the console enumerate a route... (routes() lists labels regardless of
// session health — pinned by TestRenderApex_WorktreeRoutes); here we pin the
// proxy side instead: an entry whose session was replaced is gone from get().
func TestTunnelRegistry_ReplaceDropsOldSession(t *testing.T) {
	srv := newTunnelTestServer(t)

	first := deadYamuxPair(t)
	srv.tunnels.set("abc123", "treemain", first)

	replacement := deadYamuxPair(t)
	srv.tunnels.set("abc123", "treemain", replacement)

	if sess, ok := srv.tunnels.get("abc123-treemain"); !ok || sess != replacement {
		t.Fatalf("get after replace = (_, %v), want the replacement session", ok)
	}
	// The stale teardown must not clobber the fresh reconnect.
	srv.tunnels.del("abc123", "treemain", first)
	if _, ok := srv.tunnels.get("abc123-treemain"); !ok {
		t.Fatal("stale del removed the replacement session")
	}
	srv.tunnels.del("abc123", "treemain", replacement)
	if _, ok := srv.tunnels.get("abc123-treemain"); ok {
		t.Fatal("del did not remove the matching session")
	}
}
