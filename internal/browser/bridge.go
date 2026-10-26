// Package browser holds the server half of the browser takeover bridge: a
// WebSocket endpoint the ogcode companion browser extension connects to, and
// the request/response plumbing that turns an agent's tool call into one
// operation executed in the user's real Chrome tab.
//
// The protocol is documented in browser-extension/PROTOCOL.md and mirrored by
// the extension in browser-extension/background.js. Every message is one JSON
// object: hello (extension announcements), ping, op (server -> extension) and
// result (extension -> server). Nothing else is accepted.
package browser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// opTimeout bounds one operation the bridge sends to the extension. Navigate
// waits for a page load and a screenshot takes a PNG round trip, so both get a
// wider budget than the rest.
const (
	opTimeout     = 30 * time.Second
	slowOpTimeout = 60 * time.Second
	tokenBytes    = 32

	// maxResultFrameBytes bounds one inbound result frame. A screenshot reply
	// is a base64 PNG — comfortably past coder/websocket's 32 KiB default —
	// so the bridge must raise its own read limit. Matches the 4 MiB
	// request-body budget used elsewhere in the product.
	maxResultFrameBytes = 4 << 20
)

// ErrNotConnected is returned when no extension holds the bridge: no takeover
// is possible and the caller should steer the user to pairing.
var ErrNotConnected = errors.New("browser extension is not connected to this server")

// Op names understood by the extension. The server does not parse them
// further; a new op only needs matching support on both sides.
const (
	OpNavigate   = "navigate"
	OpSnapshot   = "snapshot"
	OpContent    = "content"
	OpReadText   = "read_text"
	OpClick      = "click"
	OpType       = "type"
	OpKey        = "key"
	OpScroll     = "scroll"
	OpScreenshot = "screenshot"
	OpNetwork    = "network"
	OpConsole    = "console"
)

// Dispatcher is what a browser tool calls: the live bridge, or a test double.
type Dispatcher interface {
	// Status reports the current connection and the tabs it announced.
	Status() Status
	// Dispatch runs one named operation and returns its data payload.
	Dispatch(ctx context.Context, op string, tab *int64, args map[string]any) (map[string]any, error)
}

// TabInfo is one tab the extension announced in hello.
type TabInfo struct {
	ID      int64  `json:"id"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Granted bool   `json:"granted"`
}

// Status is what GET /api/browser/status and the browser_status tool report.
type Status struct {
	Connected bool      `json:"connected"`
	Version   string    `json:"version,omitempty"`
	Tabs      []TabInfo `json:"tabs"`
}

// GrantedCount counts the granted tabs the latest hello announced — the one
// number the takeover steer in the system prompt turns on.
func (s Status) GrantedCount() int {
	n := 0
	for i := range s.Tabs {
		if s.Tabs[i].Granted {
			n++
		}
	}
	return n
}

// opFrame is an operation the server sends to the extension.
type opFrame struct {
	Type string         `json:"type"`
	ID   string         `json:"id"`
	Op   string         `json:"op"`
	Tab  *int64         `json:"tab,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// resultFrame is the extension's reply to one op.
type resultFrame struct {
	Type string         `json:"type"`
	ID   string         `json:"id"`
	Ok   bool           `json:"ok"`
	Data map[string]any `json:"data,omitempty"`
	Err  string         `json:"error,omitempty"`
}

// helloFrame announces the extension and its granted tabs.
type helloFrame struct {
	Type    string    `json:"type"`
	Version any       `json:"version"`
	Tabs    []TabInfo `json:"tabs"`
}

// versionString normalises the hello version: the extension sends a bare JSON
// number (1) and tests quote it as a string; both must land printable in
// Status.Version instead of dropping the hello as malformed.
func versionString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// pendingOps carries one in-flight op to the goroutine waiting for it.
type pendingOps map[string]chan resultFrame

// Bridge is the server half of the websocket: it holds the single connected
// extension, relays operations to it and routes the results back. One Bridge
// serves every session in the process — the takeover browser is the user's,
// not a session's.
type Bridge struct {
	mu      sync.Mutex
	token   []byte
	conn    *websocket.Conn
	version string
	tabs    []TabInfo
	pending pendingOps
	writeMu sync.Mutex // one writer at a time; Conn rejects concurrent writes
	opSeq   uint64
}

// New returns a bridge with a fresh pairing token.
func New() *Bridge {
	return &Bridge{
		token:   newToken(),
		pending: pendingOps{},
	}
}

// NewWithToken returns a bridge pinned to the hex pairing token hexToken. It is
// how a server that persists the token across restarts keeps the same secret,
// so an already-paired extension never sees a 401 after the server reboots.
// An empty or malformed token falls back to a fresh one, and the caller should
// persist whatever Token() ends up serving.
func NewWithToken(hexToken string) *Bridge {
	if raw, err := hex.DecodeString(hexToken); err == nil && len(raw) == tokenBytes {
		return &Bridge{
			token:   raw,
			pending: pendingOps{},
		}
	}
	return New()
}

// newToken returns pairing-token bytes. The token is what a browser anywhere
// on the machine would need to pair, so it comes from crypto/rand.
func newToken() []byte {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on the supported platforms; a fallback
		// would weaken pairing, so panic rather than hand out a guessable
		// token.
		panic(fmt.Sprintf("browser: read pairing token: %v", err))
	}
	return b
}

// Token returns the hex pairing token the extension must present.
func (b *Bridge) Token() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return hex.EncodeToString(b.token)
}

// MatchesToken reports whether a presented token equals the live one in
// constant time.
func (b *Bridge) MatchesToken(presented string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	want := hex.EncodeToString(b.token)
	return subtle.ConstantTimeCompare([]byte(want), []byte(presented)) == 1
}

// RotateToken replaces the pairing token. An already-connected extension keeps
// control until its socket drops; the old URL simply stops accepting new
// connections.
func (b *Bridge) RotateToken() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.token = newToken()
	return hex.EncodeToString(b.token)
}

// Status implements Dispatcher.
func (b *Bridge) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := Status{Connected: b.conn != nil, Version: b.version}
	st.Tabs = append([]TabInfo(nil), b.tabs...)
	return st
}

// AcceptOptions is the origin policy the bridge handshake enforces.
//
// A Chrome extension service worker presents Origin: chrome-extension://<id>,
// which can never match our host, so the handshake allowlists exactly that
// scheme. A web page that tries to pair cannot forge it — and the pairing
// token on top of that is still checked server-side.
//
// coder/websocket matches a pattern against the bare origin host unless the
// pattern contains "://" — so without the explicit scheme the id alone would
// be compared and every legitimate extension would be rejected.
func AcceptOptions() *websocket.AcceptOptions {
	return &websocket.AcceptOptions{
		OriginPatterns: []string{"chrome-extension://*"},
	}
}

// Serve takes over an accepted connection: it becomes the bridge's extension
// link until the socket drops, replacing any previous one. It returns when the
// connection closes (cleanly, in error, or because the server is shutting
// down); the caller closes the conn once Serve returns.
func (b *Bridge) Serve(ctx context.Context, conn *websocket.Conn) {
	// A re-pair replaces the old connection outright: one browser, one link.
	// The old link's in-flight ops can never be answered by the new one, so
	// they fail immediately.
	oldConn, stale := b.swapConn(conn)
	if oldConn != nil {
		_ = oldConn.Close(websocket.StatusNormalClosure, "replaced by a new pairing")
	}
	b.failPending(stale, ErrNotConnected)
	defer b.dropConn(conn)

	// Result frames are unbounded by the protocol — a screenshot reply is a
	// base64 PNG, easily hundreds of kilobytes — and coder/websocket's default
	// read limit is 32 KiB, which fails every one of them with
	// "message too big: read limited at 32769 bytes" and kills the link. The
	// cap matches the request-body budget (see ogxMaxRequestBytes).
	conn.SetReadLimit(maxResultFrameBytes)

	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !isNormalWSClose(err) {
				slog.Warn("browser bridge connection ended", "err", err)
			}
			return
		}
		if msgType != websocket.MessageText {
			continue
		}
		b.handleFrame(ctx, conn, data)
	}
}

// swapConn installs conn as the current link and returns the link it replaced
// together with that link's in-flight operations.
func (b *Bridge) swapConn(conn *websocket.Conn) (*websocket.Conn, pendingOps) {
	b.mu.Lock()
	defer b.mu.Unlock()
	old := b.conn
	stale := b.pending
	b.conn = conn
	b.pending = pendingOps{}
	return old, stale
}

// dropConn clears the link when it is still the current one, and fails every
// in-flight op: the extension that owed them is gone.
func (b *Bridge) dropConn(conn *websocket.Conn) {
	b.mu.Lock()
	if b.conn != conn {
		// This link was already replaced; its state was handed off when the
		// new pairing swapped in, so there is nothing left to clear.
		b.mu.Unlock()
		return
	}
	b.conn = nil
	b.version = ""
	b.tabs = nil
	// The stale set is taken and the field zeroed under the same lock
	// swapConn hands a re-pair off with — only, with the link dead there is
	// no successor to owe the results, so the waiters learn ErrNotConnected
	// now instead of at their timers' ends.
	stale := b.pending
	b.pending = pendingOps{}
	b.mu.Unlock()
	b.failPending(stale, ErrNotConnected)
}

// handleFrame dispatches one incoming frame by its type.
func (b *Bridge) handleFrame(ctx context.Context, conn *websocket.Conn, data []byte) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || probe.Type == "" {
		slog.Warn("browser bridge dropped an unframed message", "bytes", len(data))
		return
	}
	switch probe.Type {
	case "hello":
		var hello helloFrame
		if err := json.Unmarshal(data, &hello); err != nil {
			slog.Warn("browser bridge dropped a malformed hello", "err", err)
			return
		}
		b.mu.Lock()
		b.version = versionString(hello.Version)
		b.tabs = hello.Tabs
		b.mu.Unlock()
		slog.Info("browser extension paired", "version", hello.Version, "tabs", len(hello.Tabs))
	case "ping":
		// A liveness probe from the extension; answer so it can tell a
		// half-open socket from a live one.
		_ = b.write(ctx, conn, []byte(`{"type":"pong"}`))
	case "result":
		var res resultFrame
		if err := json.Unmarshal(data, &res); err != nil || res.ID == "" {
			slog.Warn("browser bridge dropped a malformed result", "err", err)
			return
		}
		b.resolve(res.ID, res)
	default:
		slog.Warn("browser bridge ignored an unknown frame type", "type", probe.Type)
	}
}

// resolve delivers one result to whoever dispatched the matching op.
func (b *Bridge) resolve(id string, res resultFrame) {
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	ch <- res
	close(ch)
}

// failPending fails every in-flight op in ops.
func (b *Bridge) failPending(ops pendingOps, err error) {
	for _, ch := range ops {
		ch <- resultFrame{Err: err.Error()}
		close(ch)
	}
}

// Dispatch implements Dispatcher: it frames the op, sends it to the extension
// and waits for the matching result, bounded by the op's own budget and by
// ctx. The result channel is registered under the state mutex before the
// frame is written, so a fast reply can never lose the race against
// registration.
func (b *Bridge) Dispatch(ctx context.Context, op string, tab *int64, args map[string]any) (map[string]any, error) {
	b.mu.Lock()
	if b.conn == nil {
		b.mu.Unlock()
		return nil, ErrNotConnected
	}
	b.opSeq++
	id := fmt.Sprintf("op_%d_%s", b.opSeq, randSuffix())
	ch := make(chan resultFrame, 1)
	b.pending[id] = ch
	conn := b.conn
	b.mu.Unlock()

	frame, err := json.Marshal(opFrame{Type: "op", ID: id, Op: op, Tab: tab, Args: args})
	if err == nil {
		err = b.write(ctx, conn, frame)
	}
	if err != nil {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("browser: send %s: %w", op, err)
	}

	budget := opTimeout
	if op == OpNavigate || op == OpScreenshot {
		budget = slowOpTimeout
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()

	select {
	case res := <-ch:
		if res.Err != "" {
			// failPending ships an error as a plain string, so a pending op
			// failed by a re-pair must surface the sentinel itself or callers
			// lose the errors.Is contract.
			if res.Err == ErrNotConnected.Error() {
				return nil, ErrNotConnected
			}
			return nil, fmt.Errorf("browser: %s", res.Err)
		}
		if res.Data == nil {
			res.Data = map[string]any{}
		}
		return res.Data, nil
	case <-timer.C:
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("browser: %s timed out after %s — the tab may be busy or still navigating", op, budget)
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}

// write sends one text frame. Writes are serialized — a Conn allows exactly
// one concurrent writer, and dispatches (or the ping/pong answers) can race.
func (b *Bridge) write(ctx context.Context, conn *websocket.Conn, data []byte) error {
	if conn == nil {
		return ErrNotConnected
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func isNormalWSClose(err error) bool {
	return errors.Is(err, context.Canceled) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway
}

// randSuffix is a lowercase-hex discriminator so op ids cannot collide across
// restarts or hand-crafted frames.
func randSuffix() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
