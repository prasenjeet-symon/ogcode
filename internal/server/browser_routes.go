package server

import (
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"
	"github.com/prasenjeet-symon/ogcode/internal/browser"
)

// browserBridgePath is where the websocket lives; the pair endpoint builds
// candidate URLs against it.
const browserBridgePath = "/api/browser/bridge"

// browserPairTokenFile is the name inside the project's .ogcode directory the
// pairing token survives restarts in. The token identifies this server to the
// companion extension, which stores one origin + token in chrome.storage — so
// keeping it stable across boots lets an already-paired extension reconnect on
// its own instead of 401ing in its retry loop after every restart.
const browserPairTokenFile = "browser-pair-token"

// handleBrowserStatus reports the takeover bridge: whether an extension is
// connected, its version and the tabs it announced. A server without the
// bridge wired answers a clean disconnected shape rather than a 500, the way
// the other nil-tolerated surfaces do.
func (s *Server) handleBrowserStatus(w http.ResponseWriter, _ *http.Request) {
	if s.browserBridge == nil {
		writeJSON(w, http.StatusOK, browser.Status{})
		return
	}
	writeJSON(w, http.StatusOK, s.browserBridge.Status())
}

// handleBrowserPairGet hands out the pairing material: the token and a ready
// ws:// URL the extension's side panel can be pointed at. The token only
// enables the bridge on this machine — loopback is checked again at the
// handshake — so it may be shown in the UI. The endpoint answers this machine
// only: serve binds all interfaces, so a loopback peer is a local user or a
// tunnel client, and anything off-box is refused before the token is read.
func (s *Server) handleBrowserPairGet(w http.ResponseWriter, r *http.Request) {
	if s.browserBridge == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "browser takeover is not wired into this server",
		})
		return
	}
	if !isLoopbackPeer(r) {
		refuseForeignPeer(w, "the pair endpoint")
		return
	}
	writeJSON(w, http.StatusOK, s.browserPairResponse(r))
}

// handleBrowserPairRotate is the same payload after rotating the token: an
// old URL stops accepting new pairings immediately. It is gated like the
// pair GET — it hands out the just-rotated token too.
func (s *Server) handleBrowserPairRotate(w http.ResponseWriter, r *http.Request) {
	if s.browserBridge == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "browser takeover is not wired into this server",
		})
		return
	}
	if !isLoopbackPeer(r) {
		refuseForeignPeer(w, "the pair endpoint")
		return
	}
	// The rotated token is persisted too: a rotate is only ever explicit, and
	// the new value must still be the one the next boot serves.
	saveBrowserPairToken(s.dir, s.browserBridge.RotateToken())
	writeJSON(w, http.StatusOK, s.browserPairResponse(r))
}

// loadBrowserPairToken reads the pairing token persisted by an earlier boot of
// this server, returning "" when there is none or it is unreadable — the bridge
// then mints a fresh one, which New() re-persists at boot.
func loadBrowserPairToken(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".ogcode", browserPairTokenFile))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("browser: reading saved pair token", "err", err)
		}
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveBrowserPairToken persists a pairing token best-effort: a lost save only
// costs one re-pair, so a failure warns instead of failing the boot or the
// rotate it rides on.
func saveBrowserPairToken(dir, token string) {
	path := filepath.Join(dir, ".ogcode", browserPairTokenFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Warn("browser: saving pair token", "err", err)
		return
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		slog.Warn("browser: saving pair token", "err", err)
	}
}

// browserPairResponse renders the pair payload against the request's own
// host, so a tunneled or custom-hosted server hands back a URL that actually
// reaches it. The scheme reads the same signal the rest of the server does —
// requestScheme, so a TLS-terminating tunnel advertises wss:// instead of a
// ws:// the edge can never accept — and the token is read once, so a
// rotation racing the request can never make the token field and the URL
// disagree.
func (s *Server) browserPairResponse(r *http.Request) map[string]string {
	scheme := "ws"
	if requestScheme(r) == "https" {
		scheme = "wss"
	}
	token := s.browserBridge.Token()
	return map[string]string{
		"token": token,
		"url":   scheme + "://" + r.Host + browserBridgePath + "?token=" + token,
	}
}

// handleBrowserBridge upgrades the request to the takeover websocket and
// hands the connection to the bridge for the rest of its life.
//
// Three checks gate the handshake, each for a different exposure: the request
// must come from this machine — judged on the connection's peer address,
// which a client cannot forge, rather than on the Host header it chooses to
// send (the pair endpoints gate the same way, since they hand out the
// token), the presented token must match in constant time, and the websocket
// origin must be a chrome-extension page — a web page cannot forge that
// scheme, and coder/websocket rejects a cross-origin request unless a
// pattern allowlists it.
func (s *Server) handleBrowserBridge(w http.ResponseWriter, r *http.Request) {
	if s.browserBridge == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "browser takeover is not wired into this server",
		})
		return
	}

	if !isLoopbackPeer(r) {
		refuseForeignPeer(w, "the browser bridge")
		return
	}

	if !s.browserBridge.MatchesToken(r.URL.Query().Get("token")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "pairing token missing or wrong — fetch a fresh one from GET /api/browser/pair",
		})
		return
	}

	conn, err := websocket.Accept(w, r, browser.AcceptOptions())
	if err != nil {
		// Accept already wrote the rejection (a non-websocket request, a
		// hostile origin); nothing else to say.
		return
	}
	defer conn.Close(websocket.StatusInternalError, "bridge handler returned")

	s.browserBridge.Serve(r.Context(), conn)
}

// isLoopbackPeer reports whether the request's connection peer names this
// machine: the loopback v4 range, ::1, or a bracketed IPv6 literal. Unlike
// the Host header, the peer address comes off the accepted socket and cannot
// be typed in — so the pair token and the bridge are only served to requests
// that reach this machine, however the request frames itself.
func isLoopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// A peer address without a port keeps its whole value; a parseable
		// bare address still answers the gate, and anything unparseable
		// falls through to a refusal below.
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// refuseForeignPeer is the consistent refusal for the peer gate: plain 403
// with the surface named, whether the request reached the bridge, the pair
// GET or the pair rotate.
func refuseForeignPeer(w http.ResponseWriter, surface string) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": surface + " answers this machine only",
	})
}
