package master_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
)

// TestSessionView_UnknownSessionRendersNotFound pins that an id the master has
// no routing for renders the friendly not-found page (404) rather than an
// error or an empty monitor.
func TestSessionView_UnknownSessionRendersNotFound(t *testing.T) {
	_, hs := newSessionPanelServer(t, "ws-view")

	resp := sessionsGet(t, hs, "/sessions/ses_unknown")
	defer resp.Body.Close()
	assertBody(t, resp, http.StatusNotFound,
		"No such session", "ses_unknown")
}

// TestSessionView_MalformedPathsAreNotFound pins that path shapes outside
// "<ses id>" and "<ses id>/events" (empty id, nested segments) do not reach
// the monitor or the SSE endpoint.
func TestSessionView_MalformedPathsAreNotFound(t *testing.T) {
	_, hs := newSessionPanelServer(t, "ws-view")

	for _, path := range []string{"/sessions/", "/sessions/x/../../etc", "/sessions/a/b"} {
		resp := sessionsGet(t, hs, path)
		defer resp.Body.Close()
		assertBody(t, resp, http.StatusNotFound, "No such session")
	}
}

// TestSessionView_RoutedSessionRendersMonitor pins the routed monitor: worker
// identity, the tunnel link to the worktree UI, the SSE feed path and the
// script wiring.
func TestSessionView_RoutedSessionRendersMonitor(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "ws-view")

	// Route a session without starting one: StartRemoteAgent mints its own id,
	// and a direct route keeps the id stable for the assertions below.
	srv.Registry().RouteSession("ses_fixed", "ws-view")

	body := assertBody(t, sessionsGet(t, hs, "/sessions/ses_fixed"), http.StatusOK,
		"ses_fixed", "ws-view", "/sessions/ses_fixed/events", "new EventSource")

	// The tunnel registry has no tunnel for this harness worker, so the page
	// offers the legacy bare-subdomain link built from the request host —
	// which in tests carries the httptest port (r.Host = host:port).
	want := "http://ws-view." + hs.Listener.Addr().String() + "/"
	if !strings.Contains(body, want) {
		t.Errorf("body missing bare worker link %q (body=%s)", want, body)
	}
}

// TestSessionView_GateUnauthenticatedGetsLogin pins that the monitor is an
// operator-gated console surface like the rest of the apex console: an
// unauthenticated browser sees the login page, not session state.
func TestSessionView_GateUnauthenticatedGetsLogin(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	resp := getWithCookies(t, hs.URL+"/sessions/ses_any", nil)
	defer resp.Body.Close()
	assertBody(t, resp, http.StatusOK, "Sign in", "Username")
}

// TestSessionView_GateAuthenticatedSeesMonitor pins that a logged-in operator
// gets through the gate to the monitor page.
func TestSessionView_GateAuthenticatedSeesMonitor(t *testing.T) {
	srv, hs, _ := newAccountPanelServer(t, "alice", "s3cret")
	srv.Registry().RouteSession("ses_gated", "w-any")

	resp := getWithCookies(t, hs.URL+"/sessions/ses_gated", loginCookie(t, hs, "alice", "s3cret"))
	defer resp.Body.Close()
	// The worker is unknown to this registry; the page still renders with the
	// id and the feed path.
	assertBody(t, resp, http.StatusOK, "ses_gated", "/sessions/ses_gated/events")
}

// sseFrame is one JSON data frame of the session event stream.
type sseFrame struct {
	Type       string          `json:"type"`
	Seq        int64           `json:"seq"`
	Properties json.RawMessage `json:"properties"`
}

// readSSEFrames reads frames from a text/event-stream body until want have
// arrived, skipping comment and framing lines.
func readSSEFrames(t *testing.T, br *bufio.Reader, want int) []sseFrame {
	t.Helper()
	frames := make([]sseFrame, 0, want)
	deadline := time.Now().Add(5 * time.Second)
	for len(frames) < want {
		if time.Now().After(deadline) {
			t.Fatalf("stream produced %d frames after 5s, want %d (got %+v)", len(frames), want, frames)
		}
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream after %d frames: %v", len(frames), err)
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f sseFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
			t.Fatalf("bad frame %q: %v", line, err)
		}
		frames = append(frames, f)
	}
	return frames
}

// TestSessionEvents_StreamsOnlyMatchingSession pins the SSE filter: a worker
// relays interleaved events for two sessions; the browser stream for one of
// them must carry exactly its own events, with master seqs preserved for gap
// detection and the session.created payload re-keyed to "sessionId".
func TestSessionEvents_StreamsOnlyMatchingSession(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "ws-events")
	const target = "ses_mine"

	srv.Registry().RouteSession(target, "ws-events")

	// A fake worker whose ONLY job is to relay an interleaved burst of session
	// events once triggered — the master republishes relayed SessionEvents onto
	// the bus without validating them, so no real session needs to exist.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := h2cClient(hs.URL)
	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: "ws-events-relay", WorkerId: "ws-events-relay",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stream := client.WorkerStream(ctx)
	if err := stream.Send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: reg.Msg.GetWorkerToken()}}}); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	// session.created keeps the worker's verbatim payload shape (the session
	// object's id field is "id" — the master must re-key it to "sessionId").
	targetID := target
	burst := []*cpv1.SessionEvent{
		{SessionId: "ses_other", Type: "message.part.updated", Properties: []byte(`{"sessionId":"ses_other","partId":"prt_1"}`)},
		{SessionId: targetID, Type: "session.created", Properties: []byte(`{"id":"` + targetID + `","title":"fix bug","directory":"/home/me/proj"}`)},
		{SessionId: targetID, Type: "message.part.updated", Properties: []byte(`{"sessionId":"` + targetID + `","partId":"prt_2"}`)},
		{SessionId: targetID, Type: "loop.done", Properties: []byte(`{"sessionId":"` + targetID + `","reason":"stop"}`)},
		{SessionId: "ses_other", Type: "loop.done", Properties: []byte(`{"sessionId":"ses_other","reason":"error"}`)},
	}
	trigger := make(chan struct{})
	go func() {
		<-trigger
		for _, ev := range burst {
			_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_SessionEvent{SessionEvent: ev}})
		}
	}()

	// Open the browser stream BEFORE the burst so nothing is missed, and read
	// the ": hello" comment that proves the stream is wired before any event.
	resp, err := http.Get(hs.URL + "/sessions/" + target + "/events")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("SSE status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}
	br := bufio.NewReader(resp.Body)
	hello, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read stream head: %v", err)
	}
	if hello != ": hello\n" {
		t.Fatalf("stream head = %q, want the hello comment", hello)
	}

	// Fire the interleaved burst.
	close(trigger)

	frames := readSSEFrames(t, br, 3)
	var types []string
	for _, f := range frames {
		types = append(types, f.Type)
	}
	wantTypes := []string{"session.created", "message.part.updated", "loop.done"}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("streamed types = %v, want %v (ses_other events must be filtered)", types, wantTypes)
	}
	lastSeq := int64(0)
	for i, f := range frames {
		if f.Seq <= lastSeq {
			t.Errorf("frame %d (%s): seq %d not increasing (previous %d)", i, f.Type, f.Seq, lastSeq)
		}
		lastSeq = f.Seq
		var p map[string]any
		if err := json.Unmarshal(f.Properties, &p); err != nil {
			t.Fatalf("frame %d (%s): bad properties %s: %v", i, f.Type, f.Properties, err)
		}
		if got := p["sessionId"]; got != target {
			t.Errorf("frame %d (%s): sessionId = %v, want %q", i, f.Type, got, target)
		}
	}
	// The session.created payload was re-keyed without losing its fields.
	if !strings.Contains(string(frames[0].Properties), `"title":"fix bug"`) {
		t.Errorf("session.created payload lost its original fields: %s", frames[0].Properties)
	}
}

// TestSessionEvents_UnroutedSessionRejected pins that the SSE endpoint, like
// the page, refuses a session id the master holds no routing for.
func TestSessionEvents_UnroutedSessionRejected(t *testing.T) {
	_, hs := newSessionPanelServer(t, "ws-events")

	resp := sessionsGet(t, hs, "/sessions/ses_ghost/events")
	defer resp.Body.Close()
	assertBody(t, resp, http.StatusNotFound, "no such session")
}

// TestSessionView_EventsStreamGated pins that the SSE endpoint sits behind the
// operator gate too: an unauthenticated request never reaches the bus stream.
func TestSessionView_EventsStreamGated(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	resp := getWithCookies(t, hs.URL+"/sessions/ses_any/events", nil)
	defer resp.Body.Close()
	assertBody(t, resp, http.StatusOK, "Sign in", "Username")
}

// Compile-time pin: the view model lives in the master package next to the
// other console handlers.
var _ = master.StartSpec{}
