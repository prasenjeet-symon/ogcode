// Package nativehost implements the Chrome Native Messaging host the companion
// browser extension pairs through: Chrome launches `ogcode native-host` with
// the port's stdin/stdout bound, and everything the extension needs to find
// this machine's running ogcode servers — an origin, a websocket URL and a
// pair token per server — is one "pair" request away.
//
// The wire protocol is the one Chrome defines for native messaging: every
// message, request and reply alike, is a 4-byte length followed by that many
// bytes of UTF-8 JSON. Chrome's spec says the length is written in the
// machine's native byte order, and every platform Chrome ships on is
// little-endian (x86, ARM64, WebAssembly), so binary.LittleEndian matches
// native order everywhere while keeping the framing platform-independent.
//
// Only framed messages may ever reach the host's stdout — Chrome reads that
// pipe as a stream of frames, and one stray line breaks every reply after it —
// so diagnostics go to stderr through slog and never touch out.
package nativehost

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/portmap"
	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// NativeHostName is the native-messaging host's name: it names both the
// manifest file Chrome looks up ({Name}.json) and the
// chrome.runtime.connectNative call the extension makes.
const NativeHostName = "com.ogcode"

// WebStoreExtensionID is the companion extension's published Chrome Web Store
// id. Store installs run under this id, so every native-messaging manifest
// written at boot allowlists it and a store install pairs itself without a
// flag. (Unpacked repo installs keep their path-derived id alongside it.)
const WebStoreExtensionID = "kacnfgnhmmnhdpokflkiemdehhjhkidp"

// maxMessageBytes bounds one framed message. Real requests are a few dozen
// bytes and the pair reply a few hundred; the cap exists so a broken frame
// cannot name an absurd length and have the reader allocate for it.
const maxMessageBytes = 32 << 20

// request is the shape this host reads out of any message from Chrome: just
// the type. A flash-class model occasionally drops the field entirely on a
// pair intent, so a request with no type is read as "pair".
type request struct {
	Type string `json:"type"`
}

// pairEntry describes one reachable local server in the reply's servers list.
type pairEntry struct {
	Origin  string `json:"origin"`
	URL     string `json:"url"`
	Token   string `json:"token"`
	Port    int    `json:"port"`
	Project string `json:"project"`
}

// pairReplyBody is the top-level shape of a pair reply, including the empty
// one: it carries an ok of true and an empty servers list rather than an
// error, which is how the extension is told to fall back to manual pairing.
type pairReplyBody struct {
	OK      bool        `json:"ok"`
	Version string      `json:"version"`
	Servers []pairEntry `json:"servers"`
}

// prober turns a recorded port into the pairing material the server on that
// port serves at GET /api/browser/pair, saying whether the port answered
// something usable at all. The default one speaks HTTP over loopback; tests
// stub the whole function, keeping unit tests off the network entirely.
type prober func(port int) (pair, bool)

// pair is the substance of a server's pair response: the token and the ready
// websocket URL, verbatim from the server.
type pair struct {
	token string
	url   string
}

// probeTimeout bounds each server's pair GET. The port registry can carry
// stale entries for servers long gone — those dial and fail — but a half-dead
// listener must not stall the reply Chrome is waiting on.
const probeTimeout = 750 * time.Millisecond

// probeWorkers bounds the concurrent pair probing. Each probe is one short
// loopback GET, so a small group is plenty; the cap exists to keep a long
// registry from opening one socket per recorded port on the machine Chrome
// just asked to pair against.
const probeWorkers = 8

// pairBodyLimit caps how much of a pair response is read; the payload is two
// small strings.
const pairBodyLimit = 64 << 10

// browserBridgePath is the endpoint the pair URL must name to count.
const browserBridgePath = "/api/browser/bridge"

// Run serves the native-messaging protocol over in/out with the production
// prober: every recorded port is probed over loopback HTTP at pair time.
func Run(in io.Reader, out io.Writer) error {
	return runWith(in, out, httpProber(&http.Client{Timeout: probeTimeout}, nil))
}

// httpProber builds a prober that GETs http://127.0.0.1:<port>/api/browser/pair
// through client. base rewrites the probed origin so tests can point the same
// code at an httptest server; production passes nil, meaning the plain
// loopback origin.
func httpProber(client *http.Client, base func(port int) string) prober {
	return func(port int) (pair, bool) {
		origin := fmt.Sprintf("http://127.0.0.1:%d", port)
		if base != nil {
			origin = base(port)
		}
		resp, err := client.Get(origin + "/api/browser/pair")
		if err != nil {
			slog.Debug("native host: pair probe failed", "port", port, "err", err)
			return pair{}, false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// A server without the bridge wired, rotating its token, or
			// refusing the request — none of those is pairable.
			slog.Debug("native host: pair probe refused", "port", port, "status", resp.StatusCode)
			return pair{}, false
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, pairBodyLimit))
		if err != nil {
			slog.Debug("native host: pair probe body", "port", port, "err", err)
			return pair{}, false
		}
		token, url, ok := pairFromBody(body)
		if !ok {
			slog.Debug("native host: pair probe response unusable", "port", port)
			return pair{}, false
		}
		return pair{token: token, url: url}, true
	}
}

// pairFromBody parses the body of GET /api/browser/pair: an object with a
// non-empty token and a non-empty url naming the bridge path. A refusal shape
// ("error" with no token) fails the same way any other unusable body does.
func pairFromBody(body []byte) (token, url string, ok bool) {
	var payload struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", false
	}
	if payload.Token == "" || payload.URL == "" || !strings.Contains(payload.URL, browserBridgePath) {
		return "", "", false
	}
	return payload.Token, payload.URL, true
}

// registryFor lists every recorded server — canonicalized project dir → port —
// from ~/.ogcode/ports.json. A package var so tests inject a fixture registry
// instead of the real machine's registry file.
var registryFor = portmap.All

// runWith is Run's injection seam: probe supplies the pairing material for a
// recorded port. The loop reads until Chrome closes stdin — the port
// disconnect is how the host process is told to end — and answers exactly one
// framed message per request it can read.
func runWith(in io.Reader, out io.Writer, probe prober) error {
	for {
		body, err := readMessage(in)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// The port went away: Chrome's normal goodbye, not a failure.
				return nil
			}
			return err
		}
		reply, err := json.Marshal(dispatch(body, probe))
		if err != nil {
			// Our own reply shapes always marshal; this cannot fire, but the
			// loop must keep answering even if it did.
			reply = []byte(`{"ok":false,"error":"failed to encode the reply"}`)
		}
		if err := writeMessage(out, reply); err != nil {
			return err // stdout broke: Chrome is gone, nothing further matters
		}
	}
}

// dispatch turns one request message into its reply value. Unknown and
// undecodable requests each get an ok:false reply and the loop carries on —
// a wedged host would drag Chrome's extension down with it, and Chrome has
// no channel for the host to restart.
func dispatch(body []byte, probe prober) any {
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		slog.Debug("native host: undecodable request", "err", err)
		return map[string]any{"ok": false, "error": "undecodable request"}
	}
	typ := req.Type
	if typ == "" {
		// No type at all is treated as a pair request: the flash-class models
		// behind the extension's calls occasionally omit the field.
		typ = "pair"
	}
	switch typ {
	case "pair":
		return buildPairReply(probe)
	default:
		slog.Debug("native host: unknown request type", "type", typ)
		return map[string]any{"ok": false, "error": fmt.Sprintf("unknown request type %q", typ)}
	}
}

// buildPairReply answers a pair request with every local server the port
// registry currently knows that answers a pair GET. Servers that never
// answer — stale registry entries for servers long gone — are simply absent,
// and an empty list is still ok:true, which is the manual-pairing signal.
func buildPairReply(probe prober) any {
	reply := pairReplyBody{OK: true, Version: version.Version, Servers: []pairEntry{}}

	type recorded struct {
		project string
		port    int
	}
	// One parse: everything below walks the same registry snapshot, so a
	// ports.json rewrite mid-call cannot splice two inconsistent iterates.
	registry := registryFor()
	entries := make([]recorded, 0, len(registry))
	for dir, port := range registry {
		entries = append(entries, recorded{project: dir, port: port})
	}
	// Sort by port, breaking ties on the project so the same reply shape comes
	// out of the same registry however the map iterates.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].port != entries[j].port {
			return entries[i].port < entries[j].port
		}
		return entries[i].project < entries[j].project
	})

	served := map[int]bool{}
	candidates := make([]recorded, 0, len(entries))
	for _, e := range entries {
		if served[e.port] {
			// Two registry entries can share a port (a project moved on);
			// only one server can actually be bound there. Dropped before
			// any probing starts, as before.
			continue
		}
		served[e.port] = true
		candidates = append(candidates, e)
	}

	// Probe with a bounded worker group: each stale entry pays its full
	// probeTimeout, and the serial walk made the reply Chrome is already
	// waiting for cost that per dead port. Results land on their candidate's
	// index, so the reply below keeps the port-then-project order however
	// the probes finish.
	pairs := make([]pair, len(candidates))
	usable := make([]bool, len(candidates))
	workers := len(candidates)
	if workers > probeWorkers {
		workers = probeWorkers
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				at := next.Add(1) - 1
				if at >= int64(len(candidates)) {
					return
				}
				pairs[at], usable[at] = probe(candidates[at].port)
			}
		}()
	}
	wg.Wait()

	for i, e := range candidates {
		if !usable[i] {
			continue
		}
		reply.Servers = append(reply.Servers, pairEntry{
			Origin:  fmt.Sprintf("http://127.0.0.1:%d", e.port),
			URL:     pairs[i].url,
			Token:   pairs[i].token,
			Port:    e.port,
			Project: e.project,
		})
	}
	return reply
}

// readMessage reads one framed message from r: four little-endian length
// bytes, then that many bytes of UTF-8 JSON. See the package comment for why
// little-endian is the native order here.
func readMessage(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(head[:])
	if n > maxMessageBytes {
		return nil, fmt.Errorf("native host: framed length %d is past the %d cap", n, maxMessageBytes)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeMessage writes one framed message to out as a single Write: the
// little-endian length and the UTF-8 JSON body, so nothing but the framed
// bytes Chrome's reader expects ever reaches the native-messaging pipe.
func writeMessage(out io.Writer, body []byte) error {
	frame := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	_, err := out.Write(frame)
	return err
}
