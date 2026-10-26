package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testBridgePath = "/api/browser/bridge"

const testOrigin = "chrome-extension://ogcode-test"

var tokenShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// bridgeHandler mounts the bridge exactly the server route does.
func bridgeHandler(b *Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, AcceptOptions())
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusInternalError, "handler done")
		b.Serve(r.Context(), conn)
	}
}

// extClient plays the extension: it dials, forwards op frames to the test and
// answers any op the replies map has a canned payload for.
type extClient struct {
	conn *websocket.Conn
	ops  chan opFrame
}

func dialExt(t *testing.T, serverURL string, b *Bridge, replies map[string]map[string]any) *extClient {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(serverURL, "http")+testBridgePath+"?token="+b.Token(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{testOrigin}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		t.Fatalf("dial bridge: %v", err)
	}
	c := &extClient{conn: conn, ops: make(chan opFrame, 16)}
	go c.readLoop(replies)
	return c
}

func (c *extClient) readLoop(replies map[string]map[string]any) {
	ctx := context.Background()
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var f opFrame
		if json.Unmarshal(data, &f) != nil || f.Type != "op" {
			continue
		}
		select {
		case c.ops <- f:
		default:
		}
		if reply, ok := replies[f.Op]; ok {
			buf, err := json.Marshal(resultFrame{Type: "result", ID: f.ID, Ok: true, Data: reply})
			if err != nil {
				continue
			}
			_ = c.conn.Write(ctx, websocket.MessageText, buf)
		}
	}
}

func (c *extClient) hello(t *testing.T, version string, tabs []TabInfo) {
	t.Helper()
	buf, err := json.Marshal(helloFrame{Type: "hello", Version: version, Tabs: tabs})
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if err := c.conn.Write(context.Background(), websocket.MessageText, buf); err != nil {
		t.Fatalf("write hello: %v", err)
	}
}

func waitConnState(t *testing.T, b *Bridge, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.Status().Connected == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("bridge connected never became %v (status: %#v)", want, b.Status())
}

func TestDispatchWithoutAConnectionFailsFast(t *testing.T) {
	b := New()
	if b.Status().Connected {
		t.Fatal("a fresh bridge reports connected")
	}
	_, err := b.Dispatch(context.Background(), OpSnapshot, nil, nil)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestBridgeDispatchServesOpAndResult(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	c := dialExt(t, srv.URL, b, map[string]map[string]any{
		OpSnapshot: {"text": "hello from the page"},
		OpNavigate: {"ok": true},
	})
	c.hello(t, "1.2.3", []TabInfo{{ID: 11, URL: "https://example.test/", Title: "Example", Granted: true}})
	// The hello frame is processed asynchronously; Connected may flip before
	// the version and tabs have landed. Poll the full hello payload, not the
	// connected bit.
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := b.Status()
		if st.Version == "1.2.3" && len(st.Tabs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridge status never carried the hello payload (status: %#v)", st)
		}
		time.Sleep(5 * time.Millisecond)
	}

	st := b.Status()
	if st.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", st.Version)
	}
	if len(st.Tabs) != 1 || st.Tabs[0].ID != 11 || st.Tabs[0].URL != "https://example.test/" {
		t.Errorf("tabs = %#v, want one tab 11 https://example.test/", st.Tabs)
	}

	res, err := b.Dispatch(context.Background(), OpSnapshot, nil, nil)
	if err != nil {
		t.Fatalf("dispatch snapshot: %v", err)
	}
	if res["text"] != "hello from the page" {
		t.Errorf("res = %#v, want text hello from the page", res)
	}
	select {
	case f := <-c.ops:
		if f.Type != "op" || f.Op != OpSnapshot {
			t.Errorf("op frame = %#v, want op snapshot", f)
		}
		if !strings.HasPrefix(f.ID, "op_") {
			t.Errorf("op id = %q, want an op_ prefix", f.ID)
		}
		if f.Tab != nil {
			t.Errorf("tab = %v on a whole-window op, want nil", *f.Tab)
		}
	default:
		t.Fatal("extension never saw the snapshot op")
	}

	n := int64(7)
	if _, err := b.Dispatch(context.Background(), OpNavigate, &n, map[string]any{"url": "https://two.test/"}); err != nil {
		t.Fatalf("dispatch navigate: %v", err)
	}
	f := <-c.ops
	if f.Op != OpNavigate || f.Tab == nil || *f.Tab != 7 {
		t.Errorf("navigate frame = %#v, want op navigate tab 7", f)
	}
	if f.Args["url"] != "https://two.test/" {
		t.Errorf("args = %#v, want url https://two.test/", f.Args)
	}
}

// TestBridgeAcceptsAResultFarPastTheOldDefaultLimit pins the read limit Serve
// raises on every new link: a screenshot reply is a base64 PNG the 32 KiB
// default refused with "message too big: read limited at 32769 bytes", and
// that failure killed the whole link. A result frame well past it must be
// delivered whole.
func TestBridgeAcceptsAResultFarPastTheOldDefaultLimit(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	big := strings.Repeat("iVBORw0KGgo", 10_000) // ~110 KB of base64-ish payload
	c := dialExt(t, srv.URL, b, map[string]map[string]any{
		OpSnapshot: {
			"format":  "png",
			"base64":  big,
			"payload": strings.Repeat("x", 64_000), // two oversized JSON strings per frame
		},
	})
	c.hello(t, "1.0.0", nil)
	waitConnState(t, b, true)

	res, err := b.Dispatch(context.Background(), OpSnapshot, nil, nil)
	if err != nil {
		t.Fatalf("dispatch snapshot: %v", err)
	}
	if got, _ := res["base64"].(string); got != big {
		t.Errorf("base64 payload length = %d, want the full %d bytes", len(got), len(big))
	}
	waitConnState(t, b, true)
}

func TestBridgeHelloAcceptsANumericVersion(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	c := dialExt(t, srv.URL, b, nil)
	// background.js sends version as a bare JSON number (PROTOCOL.md); the
	// bridge must land it in Status.Version, not drop the hello as malformed.
	raw := []byte(`{"type":"hello","version":1,"tabs":[{"id":3,"url":"https://n.test/","title":"Numeric","granted":true}]}`)
	if err := c.conn.Write(context.Background(), websocket.MessageText, raw); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := b.Status()
		if st.Version == "1" && len(st.Tabs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never carried the numeric hello (status: %#v)", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBridgeAnswersPing(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+testBridgePath+"?token="+b.Token(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{testOrigin}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil || m["type"] != "pong" {
		t.Fatalf("reply = %s, want a pong frame", data)
	}
}

func TestBridgeRePairFailsPendingOps(t *testing.T) {
	b := New()
	srvA := httptest.NewServer(bridgeHandler(b))
	defer srvA.Close()

	cA := dialExt(t, srvA.URL, b, nil) // no canned replies: the op stays pending
	cA.hello(t, "1.0", nil)
	waitConnState(t, b, true)

	result := make(chan error, 1)
	go func() {
		_, err := b.Dispatch(context.Background(), OpReadText, nil, map[string]any{"maxChars": 200})
		result <- err
	}()

	select {
	case f := <-cA.ops:
		if f.Op != OpReadText {
			t.Errorf("op = %q, want %q", f.Op, OpReadText)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("extension A never saw the pending op")
	}

	srvB := httptest.NewServer(bridgeHandler(b))
	defer srvB.Close()
	cB := dialExt(t, srvB.URL, b, nil)
	cB.hello(t, "2.0", nil)

	deadline := time.Now().Add(2 * time.Second)
	for b.Status().Version != "2.0" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.Status().Version; got != "2.0" {
		t.Fatalf("version = %q, want 2.0 after the re-pair", got)
	}

	select {
	case err := <-result:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("pending dispatch err = %v, want ErrNotConnected", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pending dispatch was not failed by the re-pair")
	}
}

// Same contract as a re-pair, with nothing that succeeds it: when the socket
// dies (a closed browser), every in-flight Dispatch must fail with
// ErrNotConnected at once instead of waiting out its op timer on a link that
// cannot answer.
func TestBridgeDisconnectFailsPendingOpsImmediately(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	c := dialExt(t, srv.URL, b, nil) // no canned replies: the op stays pending
	c.hello(t, "1.0", nil)
	waitConnState(t, b, true)

	result := make(chan error, 1)
	go func() {
		_, err := b.Dispatch(context.Background(), OpReadText, nil, map[string]any{"maxChars": 200})
		result <- err
	}()

	select {
	case f := <-c.ops:
		if f.Op != OpReadText {
			t.Errorf("op = %q, want %q", f.Op, OpReadText)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("extension never saw the pending op")
	}

	// The extension's side goes away with the op still owed — no re-pair
	// comes to hand the pending set off to.
	if err := c.conn.Close(websocket.StatusNormalClosure, "extension vanished"); err != nil {
		t.Logf("close: %v", err)
	}
	waitConnState(t, b, false)

	start := time.Now()
	select {
	case err := <-result:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("pending dispatch err = %v, want ErrNotConnected", err)
		}
		if waited := time.Since(start); waited > 5*time.Second {
			t.Fatalf("the pending dispatch was failed %s after the disconnect, want immediately — it waited out its op timer", waited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pending dispatch was not failed by the disconnect")
	}
}

func TestDispatchHonoursACancelledContext(t *testing.T) {
	b := New()
	srv := httptest.NewServer(bridgeHandler(b))
	defer srv.Close()

	c := dialExt(t, srv.URL, b, nil)
	c.hello(t, "1.0", nil)
	waitConnState(t, b, true)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := b.Dispatch(ctx, OpSnapshot, nil, nil)
	if err == nil {
		t.Fatal("dispatch succeeded although the extension never answered")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("dispatch took %s — the caller's context was ignored", elapsed)
	}
}

func TestNewWithTokenPinsThePersistedToken(t *testing.T) {
	b := New()
	// Reusing a real token's shape keeps the pin honest: 32 bytes, hex.
	pinned := NewWithToken(b.Token())
	if pinned.Token() != b.Token() {
		t.Fatalf("NewWithToken served %q, want the token it was given %q", pinned.Token(), b.Token())
	}
	if !pinned.MatchesToken(b.Token()) {
		t.Error("a bridge pinned to a token refuses the handshake that presents it")
	}
}

func TestNewWithTokenFallsBackToFreshOnBadInput(t *testing.T) {
	for _, bad := range []string{"", "not-hex", strings.Repeat("ab", 31)} {
		b := NewWithToken(bad)
		if !tokenShape.MatchString(b.Token()) {
			t.Errorf("NewWithToken(%q) served %q, want a fresh 64-hex token", bad, b.Token())
		}
		if b.MatchesToken(bad) {
			t.Errorf("NewWithToken(%q) accepted the bad input as a pairing token", bad)
		}
	}
}

func TestBridgeTokenMatchesAndRotates(t *testing.T) {
	b := New()
	token := b.Token()
	if !tokenShape.MatchString(token) {
		t.Fatalf("token = %q, want 64 lowercase hex chars", token)
	}

	if !b.MatchesToken(token) {
		t.Error("the bridge does not match its own token")
	}
	if b.MatchesToken("") || b.MatchesToken(strings.Repeat("0", 63)) || b.MatchesToken(strings.ToUpper(token)) {
		t.Error("the bridge accepted a wrong token")
	}

	rotated := b.RotateToken()
	if rotated == token {
		t.Error("RotateToken returned the same token")
	}
	if !tokenShape.MatchString(rotated) {
		t.Errorf("rotated token = %q, want 64 lowercase hex chars", rotated)
	}
	if b.MatchesToken(token) {
		t.Error("the old token still matches after rotation")
	}
	if !b.MatchesToken(rotated) {
		t.Error("the rotated token does not match")
	}
}
