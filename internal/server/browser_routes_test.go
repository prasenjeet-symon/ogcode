package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prasenjeet-symon/ogcode/internal/browser"
)

var browserTokenShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

const browserTestOrigin = "chrome-extension://ogcode-test"

func browserWaitStatus(t *testing.T, url string, want bool) browser.Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last browser.Status
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/api/browser/status")
		if err == nil {
			var st browser.Status
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			last = st
			if st.Connected == want {
				return st
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("browser status never became connected=%v (last: %#v)", want, last)
	return last
}

// browserRequest drives h with a request from a loopback peer — the address a
// real extension or tunnel client connects from. httptest.NewRequest stamps a
// non-loopback RemoteAddr, which the pair and bridge gates refuse.
func browserRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:52103"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBrowserRoutesWithoutABridge(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	rec := boardRequest(t, h, "GET", "/api/browser/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status without a bridge = %d, want 200 (it should answer unpaired)", rec.Code)
	}
	var st browser.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode status body %q: %v", rec.Body.String(), err)
	}
	if st.Connected {
		t.Error("no bridge is wired but status reports connected")
	}

	if rec := boardRequest(t, h, "GET", "/api/browser/pair", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("pair without a bridge = %d, want 503", rec.Code)
	}
	if rec := boardRequest(t, h, "GET", "/api/browser/bridge?token=x", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("bridge without a wire = %d, want 503", rec.Code)
	}
}

func TestBrowserPairReturnsThePairingURL(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()

	rec := browserRequest(t, h, "GET", "/api/browser/pair", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pair = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode pair body %q: %v", rec.Body.String(), err)
	}
	if !browserTokenShape.MatchString(got["token"]) {
		t.Errorf("token = %q, want 64 lowercase hex chars", got["token"])
	}
	want := "ws://example.com/api/browser/bridge?token=" + got["token"]
	if got["url"] != want {
		t.Errorf("url = %q, want %q (httptest default host, no TLS)", got["url"], want)
	}
}

// browserBoot mimics what Serve does for the bridge: load the persisted
// token, mint + save when there is none or it is malformed.
func browserBoot(t *testing.T, dir string) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.dir = dir
	srv.browserBridge = browser.NewWithToken(loadBrowserPairToken(dir))
	saveBrowserPairToken(dir, srv.browserBridge.Token())
	return srv
}

func TestBrowserPairTokenSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first := browserBoot(t, dir).browserBridge.Token()
	if !browserTokenShape.MatchString(first) {
		t.Fatalf("first boot minted %q, want 64 lowercase hex chars", first)
	}
	if restored := browserBoot(t, dir).browserBridge.Token(); restored != first {
		t.Fatalf("the next boot of the same dir served %q, want %q — an extension paired to the first boot would 401 after the restart", restored, first)
	}

	// A rotate is explicit only, and it too survives the next boot.
	srv := browserBoot(t, dir)
	rotated := srv.browserBridge.RotateToken()
	saveBrowserPairToken(dir, rotated)
	if again := browserBoot(t, dir).browserBridge.Token(); again != rotated {
		t.Fatalf("a boot after a rotate served %q, want the rotated %q", again, rotated)
	}

	// A corrupted file falls back to mint-and-persist rather than pairing.
	if err := os.WriteFile(filepath.Join(dir, ".ogcode", browserPairTokenFile), []byte("zzz"), 0o600); err != nil {
		t.Fatalf("corrupt the token file: %v", err)
	}
	if after := browserBoot(t, dir).browserBridge.Token(); !browserTokenShape.MatchString(after) || after == rotated {
		t.Fatalf("a boot over a corrupted token file served %q, want a fresh token", after)
	}
}

func TestBrowserPairRotatePersistsTheToken(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()

	if rec := browserRequest(t, h, "POST", "/api/browser/pair", ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d, want 200", rec.Code)
	}
	data, err := os.ReadFile(filepath.Join(srv.dir, ".ogcode", browserPairTokenFile))
	if err != nil {
		t.Fatalf("rotate did not persist the token: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != srv.browserBridge.Token() {
		t.Errorf("token file holds %q, want the live token %q", got, srv.browserBridge.Token())
	}
}

func TestBrowserPairRotateChangesTheToken(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()

	first := browserRequest(t, h, "GET", "/api/browser/pair", "")
	var one map[string]string
	_ = json.Unmarshal(first.Body.Bytes(), &one)

	if rec := browserRequest(t, h, "POST", "/api/browser/pair", ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d, want 200", rec.Code)
	}

	second := browserRequest(t, h, "GET", "/api/browser/pair", "")
	var two map[string]string
	_ = json.Unmarshal(second.Body.Bytes(), &two)

	if !browserTokenShape.MatchString(two["token"]) {
		t.Errorf("rotated token = %q, want 64 lowercase hex chars", two["token"])
	}
	if one["token"] == two["token"] {
		t.Error("POST /api/browser/pair did not rotate the token")
	}
}

func TestBrowserBridgeGates(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()
	token := srv.browserBridge.Token()

	req := httptest.NewRequest("GET", "/api/browser/bridge?token=wrong", nil)
	req.Header.Set("Origin", browserTestOrigin)
	req.RemoteAddr = "127.0.0.1:52103" // a loopback peer gets to the token gate
	direct := httptest.NewRecorder()
	h.ServeHTTP(direct, req)
	if direct.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", direct.Code)
	}

	// The gate judges the connection's peer, not the Host the request
	// carries: a LAN client is refused even when it spoofs Host: localhost.
	req = httptest.NewRequest("GET", "/api/browser/bridge?token="+token, nil)
	req.Host = "localhost"
	req.RemoteAddr = "192.0.2.1:1234"
	direct = httptest.NewRecorder()
	h.ServeHTTP(direct, req)
	if direct.Code != http.StatusForbidden {
		t.Fatalf("spoofed localhost host from a LAN peer = %d, want 403", direct.Code)
	}

	// The handshake gates also hold on a live listener: a plain GET with a good
	// token is not a websocket and must not be accepted.
	live := httptest.NewServer(h)
	defer live.Close()
	resp, err := http.Get(live.URL + "/api/browser/bridge?token=" + token)
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("plain GET with a good token = %d, want a 4xx rejection", resp.StatusCode)
	}

	// A browser origin outside the allowlist is rejected, token or not.
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(live.URL, "http")+"/api/browser/bridge?token="+token, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "test done")
		t.Fatal("dial with a foreign origin succeeded")
	}
	if dialResp != nil && dialResp.StatusCode == http.StatusSwitchingProtocols {
		t.Errorf("foreign origin switched protocols (status %d)", dialResp.StatusCode)
	}
}

func TestBrowserBridgePairsAnExtensionEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	live := httptest.NewServer(srv.routes())
	defer live.Close()

	token := srv.browserBridge.Token()
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(live.URL, "http")+"/api/browser/bridge?token="+token, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{browserTestOrigin}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		t.Fatalf("dial bridge: %v", err)
	}

	ioCtx := context.Background()
	tabs, err := json.Marshal([]browser.TabInfo{{ID: 3, URL: "https://example.test/", Title: "Example", Granted: true}})
	if err != nil {
		t.Fatalf("marshal tabs: %v", err)
	}
	hello := fmt.Sprintf(`{"type":"hello","version":"1","tabs":%s}`, tabs)
	if err := conn.Write(ioCtx, websocket.MessageText, []byte(hello)); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	st := browserWaitStatus(t, live.URL, true)
	if st.Version != "1" {
		t.Errorf("version = %q, want 1, from the hello frame", st.Version)
	}
	if len(st.Tabs) != 1 || st.Tabs[0].ID != 3 || st.Tabs[0].URL != "https://example.test/" {
		t.Errorf("tabs = %#v, want one granted tab 3 https://example.test/", st.Tabs)
	}

	if err := conn.Close(websocket.StatusNormalClosure, "bye"); err != nil {
		t.Logf("close: %v", err)
	}
	after := browserWaitStatus(t, live.URL, false)
	if len(after.Tabs) != 0 {
		t.Errorf("tabs after the disconnect = %#v, want none", after.Tabs)
	}
}

func TestBrowserPairGateIsPeerBased(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()

	for _, method := range []string{"GET", "POST"} {
		// The pair endpoints hand out the pair token, so a spoofed Host must
		// not carry a LAN peer past the gate — the peer address decides.
		req := httptest.NewRequest(method, "/api/browser/pair", nil)
		req.Host = "localhost"
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s pair from 192.0.2.1 with Host: localhost = %d body %s, want 403", method, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "token") {
			t.Fatalf("%s pair body %q leaked token material to a foreign peer", method, rec.Body.String())
		}

		rec = browserRequest(t, h, method, "/api/browser/pair", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s pair from loopback = %d body %s, want 200", method, rec.Code, rec.Body.String())
		}
	}
}

func TestIsLoopbackPeer(t *testing.T) {
	remotePeers := map[string]bool{
		"":                false, // no peer address at all: fail closed
		"127.0.0.1:51234": true,
		"[::1]:8080":      true,
		"::1":             true,  // an address without a port is still judged
		"localhost:51234": false, // a name is no peer check — resolve nothing
		"192.0.2.1:1234":  false,
		"10.10.2.3:443":   false,
		"example.com:80":  false,
		"fe80::1:51234":   false,
		"[fe80::1]:51234": false,
	}
	for remote, want := range remotePeers {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		if got := isLoopbackPeer(req); got != want {
			t.Errorf("isLoopbackPeer(RemoteAddr %q) = %v, want %v", remote, got, want)
		}
	}
}

func TestBrowserPairAdvertisesWssBehindATlsEdge(t *testing.T) {
	srv := newTestServer(t)
	srv.browserBridge = browser.New()
	h := srv.routes()

	// A TLS-terminating tunnel hands the request to the server over plain
	// HTTP; the pairing URL must advertise the wss:// edge the extension
	// actually dials, not the ws:// hop in between.
	edged := httptest.NewRequest("GET", "/api/browser/pair", nil)
	edged.RemoteAddr = "127.0.0.1:52103"
	edged.Host = "tunnel.example.com"
	edged.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, edged)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair behind the edge = %d body %s, want 200", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode pair body %q: %v", rec.Body.String(), err)
	}
	if !strings.HasPrefix(got["url"], "wss://") {
		t.Fatalf("url = %q, want a wss:// edge scheme", got["url"])
	}

	want := map[string]string{"http": "ws://", "": "ws://", "https": "wss://"}
	for proto, scheme := range want {
		hop := httptest.NewRequest("GET", "/api/browser/pair", nil)
		hop.RemoteAddr = "127.0.0.1:52103"
		if proto == "" {
			hop.Header.Del("X-Forwarded-Proto")
		} else {
			hop.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, hop)
		if rec.Code != http.StatusOK {
			t.Fatalf("pair with X-Forwarded-Proto %q = %d, want 200", proto, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode pair body %q: %v", rec.Body.String(), err)
		}
		if !strings.HasPrefix(body["url"], scheme) {
			t.Errorf("X-Forwarded-Proto %q: url = %q, want %q", proto, body["url"], scheme)
		}
	}
}
