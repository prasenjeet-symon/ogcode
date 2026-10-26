package nativehost

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// frameMessage builds one wire frame: a four-byte little-endian length and
// the JSON payload after it.
func frameMessage(t *testing.T, payload string) []byte {
	t.Helper()
	head := make([]byte, 4)
	binary.LittleEndian.PutUint32(head, uint32(len(payload)))
	return append(head, payload...)
}

// readFrame decodes one reply frame off r, returning the body's JSON object.
func readFrame(t *testing.T, r io.Reader) ([]byte, error) {
	t.Helper()
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	body := make([]byte, binary.LittleEndian.Uint32(head[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func decodeReply(t *testing.T, body []byte) pairReplyBody {
	t.Helper()
	var reply pairReplyBody
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatalf("reply body is not a pair reply: %v", err)
	}
	return reply
}

// setRegistry injects a fixture registry (project dir → port) in place of
// ~/.ogcode/ports.json for the duration of the test.
func setRegistry(t *testing.T, entries map[string]int) {
	t.Helper()
	original := registryFor
	registryFor = func() map[string]int { return entries }
	t.Cleanup(func() { registryFor = original })
}

func TestPairReplyCarriesEveryReachableServerSortedByPort(t *testing.T) {
	setRegistry(t, map[string]int{
		"/servers/a": 9101, // reachable
		"/servers/b": 9100, // reachable
		"/servers/c": 9102, // unparseable response
		"/servers/d": 9103, // {"error": ...} and no token
	})
	probe := func(port int) (pair, bool) {
		switch port {
		case 9100, 9101:
			return pair{
				token: fmt.Sprintf("tok-%d", port),
				url:   fmt.Sprintf("ws://127.0.0.1:%d/api/browser/bridge?token=tok-%d", port, port),
			}, true
		default:
			return pair{}, false
		}
	}

	in := bytes.NewReader(frameMessage(t, `{"type":"pair"}`))
	var out bytes.Buffer
	if err := runWith(in, &out, probe); err != nil {
		t.Fatalf("runWith: %v", err)
	}

	// The wire shape: exactly one frame, length prefix little-endian over the
	// body's byte count, so Chrome's framer can parse it back.
	head := out.Bytes()[:4]
	wantHead := make([]byte, 4)
	binary.LittleEndian.PutUint32(wantHead, uint32(out.Len()-4))
	if !bytes.Equal(head, wantHead) {
		t.Fatalf("reply length prefix = % x, want % x", head, wantHead)
	}
	if n := replyCount(t, out.Bytes()); n != 1 {
		t.Fatalf("got %d replies, want exactly one frame", n)
	}

	pairBody, err := readFrame(t, bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("reading the pair reply: %v", err)
	}
	reply := decodeReply(t, pairBody)
	if !reply.OK {
		t.Fatal("a pair reply with reachable servers must be ok")
	}
	if reply.Version != version.Version {
		t.Fatalf("reply version = %q, want %q", reply.Version, version.Version)
	}
	want := []pairEntry{
		{Origin: "http://127.0.0.1:9100", URL: "ws://127.0.0.1:9100/api/browser/bridge?token=tok-9100", Token: "tok-9100", Port: 9100, Project: "/servers/b"},
		{Origin: "http://127.0.0.1:9101", URL: "ws://127.0.0.1:9101/api/browser/bridge?token=tok-9101", Token: "tok-9101", Port: 9101, Project: "/servers/a"},
	}
	for i, w := range want {
		if i >= len(reply.Servers) {
			t.Fatalf("reply has %d servers, want %d", len(reply.Servers), len(want))
		}
		if got := reply.Servers[i]; got != w {
			t.Errorf("server[%d] = %+v, want %+v", i, got, w)
		}
	}
	if len(reply.Servers) != len(want) {
		t.Fatalf("reply has %d servers, want %d", len(reply.Servers), len(want))
	}
}

// The probe group runs the ports at once within the bound — a reply after N
// stale ports must not cost N serial probe timeouts.
func TestPairReplyProbesConcurrentlyWithinABound(t *testing.T) {
	registry := make(map[string]int, 20)
	for i := 0; i < 20; i++ {
		registry[fmt.Sprintf("/stale/%02d", i)] = 9300 + i
	}
	setRegistry(t, registry)

	held := make(chan struct{})
	entered := make(chan int, 20)
	var once sync.Once
	open := func() { once.Do(func() { close(held) }) }
	t.Cleanup(open)
	probe := func(port int) (pair, bool) {
		entered <- port
		<-held
		return pair{}, false
	}

	in := bytes.NewReader(frameMessage(t, `{"type":"pair"}`))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runWith(in, &out, probe)
	}()

	// Serial probing enters one port at a time and never fills the bound.
	deadline := time.Now().Add(3 * time.Second)
	for len(entered) < 8 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if len(entered) < 8 {
		t.Fatalf("%d probes entered before the deadline, want 8 running at once — the probes look serial", len(entered))
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(entered); got > 8 {
		t.Fatalf("%d probes entered at once, want the bound of 8", got)
	}
	open()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWith: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the pair reply never finished after the probes were released")
	}
	pairBody, err := readFrame(t, bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("reading the pair reply: %v", err)
	}
	reply := decodeReply(t, pairBody)
	if !reply.OK || len(reply.Servers) != 0 {
		t.Fatalf("pair reply = %+v, want ok with every port stale", reply)
	}
}

// With concurrent probes the completions arrive out of registry order; the
// reply must still list the servers sorted by port.
func TestPairReplyKeepsPortOrderWhenProbesFinishOutOfOrder(t *testing.T) {
	registry := make(map[string]int, 12)
	for i := 0; i < 12; i++ {
		registry[fmt.Sprintf("/slow/%02d", i)] = 9400 + i
	}
	setRegistry(t, registry)

	// The lowest ports answer last, so probe completion order runs opposite
	// to registry order.
	probe := func(port int) (pair, bool) {
		time.Sleep(time.Duration(9412-port) * 3 * time.Millisecond)
		return pair{
			token: fmt.Sprintf("tok-%d", port),
			url:   fmt.Sprintf("ws://127.0.0.1:%d/api/browser/bridge?token=tok-%d", port, port),
		}, true
	}

	in := bytes.NewReader(frameMessage(t, `{"type":"pair"}`))
	var out bytes.Buffer
	if err := runWith(in, &out, probe); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	pairBody, err := readFrame(t, bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("reading the pair reply: %v", err)
	}
	reply := decodeReply(t, pairBody)
	if len(reply.Servers) != 12 {
		t.Fatalf("reply has %d servers, want 12", len(reply.Servers))
	}
	for i, got := range reply.Servers {
		port := 9400 + i
		want := pairEntry{
			Origin:  fmt.Sprintf("http://127.0.0.1:%d", port),
			URL:     fmt.Sprintf("ws://127.0.0.1:%d/api/browser/bridge?token=tok-%d", port, port),
			Token:   fmt.Sprintf("tok-%d", port),
			Port:    port,
			Project: fmt.Sprintf("/slow/%02d", i),
		}
		if got != want {
			t.Errorf("server[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestUnknownAndUndecodableRequestsKeepTheLoopAlive(t *testing.T) {
	setRegistry(t, map[string]int{})
	noopProbe := func(int) (pair, bool) { return pair{}, false }

	payloads := []string{
		`{"type":"pair"}`,     // ok:true, empty servers
		`{"type":"takeover"}`, // unknown type: ok:false, the loop stays up
		`not json at all`,     // undecodable: ok:false, the loop stays up
		`{"project":"/work"}`, // no type: read as a pair request
		`{"type":"pair"}`,     // still answering after the garbage
	}
	var in bytes.Buffer
	for _, p := range payloads {
		in.Write(frameMessage(t, p))
	}
	var out bytes.Buffer
	if err := runWith(bytes.NewReader(in.Bytes()), &out, noopProbe); err != nil {
		t.Fatalf("runWith: %v", err)
	}

	r := bytes.NewReader(out.Bytes())
	replies := make([]pairReplyBody, 0, len(payloads))
	for {
		body, err := readFrame(t, r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading reply %d: %v", len(replies), err)
		}
		replies = append(replies, decodeReply(t, body))
	}
	if len(replies) != len(payloads) {
		t.Fatalf("got %d replies, want %d", len(replies), len(payloads))
	}
	if !replies[0].OK || len(replies[0].Servers) != 0 {
		t.Fatalf("first reply = ok:%v %d servers, want ok:true and an empty empty-servers list", replies[0].OK, len(replies[0].Servers))
	}
	if replies[1].OK || !bytes.Contains(rawBodyAt(t, out.Bytes(), 1), []byte("takeover")) {
		t.Fatalf("unknown type reply = %+v, want ok:false naming the type", replies[1])
	}
	if replies[2].OK {
		t.Fatalf("undecodable request answered ok")
	}
	if !replies[3].OK {
		t.Fatalf("a type-less request is a pair request; got %+v", replies[3])
	}
	if !replies[4].OK {
		t.Fatalf("the loop did not survive the garbage: %+v", replies[4])
	}
	if replies[1].OK {
		t.Fatalf("unknown type must not be ok")
	}
}

// rawBodyAt returns the JSON body of reply number n in out's stream.
func rawBodyAt(t *testing.T, stream []byte, n int) []byte {
	t.Helper()
	r := bytes.NewReader(stream)
	for i := 0; ; i++ {
		body, err := readFrame(t, r)
		if err != nil {
			t.Fatalf("reading reply %d: %v", i, err)
		}
		if i == n {
			return body
		}
	}
}

func TestEOFEndsTheHostCleanly(t *testing.T) {
	setRegistry(t, map[string]int{"": 9595})
	probe := func(int) (pair, bool) { return pair{}, false }

	in := bytes.NewReader(frameMessage(t, `{"type":"pair"}`))
	var out bytes.Buffer
	if err := runWith(in, &out, probe); err != nil {
		t.Fatalf("runWith after the stream ends: %v, want a clean nil exit", err)
	}
	if n := replyCount(t, out.Bytes()); n != 1 {
		t.Fatalf("got %d replies for one request, want 1", n)
	}
}

func TestAbsurdLengthPrefixFailsLoudlyWithoutPanicking(t *testing.T) {
	// 0xffffffff as the length prefix: a broken or hostile frame.
	probe := func(int) (pair, bool) { return pair{}, false }

	in := bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff})
	var out bytes.Buffer
	if err := runWith(in, &out, probe); err == nil {
		t.Fatal("an absurd length prefix must fail the run, not panic or hang")
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %d bytes before failing: % x", out.Len(), out.Bytes())
	}
}

func TestTruncatedBodyEndsCleanlyAsAShutPort(t *testing.T) {
	probe := func(int) (pair, bool) { return pair{}, false }

	// Length 4 claimed, 1 byte delivered: Chrome died mid-message.
	in := bytes.NewReader([]byte{4, 0, 0, 0, '{'})
	var out bytes.Buffer
	if err := runWith(in, &out, probe); err != nil {
		t.Fatalf("runWith: %v, want a clean nil exit", err)
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %d bytes", out.Len())
	}
}

func replyCount(t *testing.T, stream []byte) int {
	t.Helper()
	r := bytes.NewReader(stream)
	n := 0
	for {
		if _, err := readFrame(t, r); err != nil {
			return n
		}
		n++
	}
}

func TestPairFromBodyAcceptsAGoodBodyAndRefusesTheRest(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"good", `{"token":"tok","url":"ws://127.0.0.1:9595/api/browser/bridge?token=tok"}`, true},
		{"unparseable", "not json", false},
		{"error field with no token", `{"error":"browser takeover is not wired into this server"}`, false},
		{"empty token", `{"token":"","url":"ws://127.0.0.1:9595/api/browser/bridge"}`, false},
		{"url naming something else", `{"token":"tok","url":"ws://127.0.0.1:9595/other"}`, false},
		{"array body", `[]`, false},
	}
	for _, c := range cases {
		token, url, ok := pairFromBody([]byte(c.body))
		if ok != c.want {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.want)
			continue
		}
		if ok && (token != "tok" || url == "") {
			t.Errorf("%s: token %q url %q, want the body's values", c.name, token, url)
		}
	}
}

func TestHTTPProberProbesThePairEndpoint(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/browser/pair" {
			t.Errorf("probed %q, want /api/browser/pair", r.URL.Path)
		}
		fmt.Fprint(w, `{"token":"tok","url":"ws://127.0.0.1:9595/api/browser/bridge?token=tok"}`)
	}))
	t.Cleanup(good.Close)
	unparseable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>no</html>")
	}))
	t.Cleanup(unparseable.Close)
	nonOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"browser takeover is not wired into this server"}`)
	}))
	t.Cleanup(nonOK.Close)

	// Each fake port maps to one httptest server, so the same origin rewrite
	// covers all four probe outcomes.
	base := func(port int) string {
		switch port {
		case 5010:
			return good.URL
		case 5011:
			return unparseable.URL
		case 5012:
			return nonOK.URL
		case 5013:
			return "http://127.0.0.1:1" // nothing listens there
		default:
			return ""
		}
	}
	prober := httpProber(good.Client(), base)

	got, ok := prober(5010)
	if !ok || got.token != "tok" || got.url != "ws://127.0.0.1:9595/api/browser/bridge?token=tok" {
		t.Fatalf("prober(5010) = %+v %v, want the body's token and url", got, ok)
	}
	for _, port := range []int{5011, 5012, 5013} {
		if _, ok := prober(port); ok {
			t.Errorf("prober(%d) reported a usable pair, want false", port)
		}
	}
}
