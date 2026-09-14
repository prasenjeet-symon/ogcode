package master_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

const testSecret = "pair-secret"

func newTestServer(t *testing.T) (*master.Server, string) {
	t.Helper()
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	// h2c so Connect bidi streaming works over the httptest cleartext listener.
	hs := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(hs.Close)
	return srv, hs.URL
}

// h2cClient dials the test server with prior-knowledge HTTP/2 over cleartext.
func h2cClient(baseURL string) controlplanev1connect.ControlPlaneServiceClient {
	hc := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}
	return controlplanev1connect.NewControlPlaneServiceClient(hc, baseURL)
}

func TestRegisterWrongSecretRejected(t *testing.T) {
	_, url := newTestServer(t)
	client := h2cClient(url)
	_, err := client.Register(context.Background(), connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: "nope",
		WorkerName:    "w",
	}))
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("want Unauthenticated, got %v (err=%v)", got, err)
	}
}

// A worker that presents a stable id must be registered under exactly that id
// (so its panel subdomain survives reconnects), and a malformed id must be
// rejected rather than silently replaced.
func TestRegisterHonorsStableWorkerID(t *testing.T) {
	_, url := newTestServer(t)
	client := h2cClient(url)
	ctx := context.Background()

	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret,
		WorkerName:    "laptop",
		WorkerId:      "my-laptop-01",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := reg.Msg.GetWorkerId(); got != "my-laptop-01" {
		t.Fatalf("stable id not honored: got %q want %q", got, "my-laptop-01")
	}

	// Re-registering with the same id must keep the id stable (not mint a new one).
	reg2, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret,
		WorkerName:    "laptop",
		WorkerId:      "my-laptop-01",
	}))
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if got := reg2.Msg.GetWorkerId(); got != "my-laptop-01" {
		t.Fatalf("stable id changed across re-register: got %q", got)
	}

	// Malformed ids are rejected, not silently replaced.
	for _, bad := range []string{"UPPER", "-lead", "trail-", "has space", "a/b", ""} {
		if bad == "" {
			continue // empty means "mint a fresh id", covered elsewhere
		}
		_, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
			PairingSecret: testSecret,
			WorkerName:    "laptop",
			WorkerId:      bad,
		}))
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Fatalf("worker_id %q: want InvalidArgument, got %v (err=%v)", bad, got, err)
		}
	}
}

func TestRegisterAndHeartbeat(t *testing.T) {
	_, url := newTestServer(t)
	client := h2cClient(url)
	ctx := context.Background()

	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret,
		WorkerName:    "laptop",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if reg.Msg.GetWorkerId() == "" || reg.Msg.GetWorkerToken() == "" {
		t.Fatalf("empty register response: %+v", reg.Msg)
	}

	hb, err := client.Heartbeat(ctx, connect.NewRequest(&cpv1.HeartbeatRequest{
		WorkerToken: reg.Msg.GetWorkerToken(),
	}))
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !hb.Msg.GetOk() {
		t.Fatal("heartbeat not ok")
	}

	_, err = client.Heartbeat(ctx, connect.NewRequest(&cpv1.HeartbeatRequest{WorkerToken: "bogus"}))
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("bogus heartbeat: want Unauthenticated, got %v", got)
	}
}

// fakeWorker connects, sends Hello, and runs a command loop that answers
// ListWorkspaces/StartAgent and, on StartAgent, relays a burst of session events.
func TestWorkerStreamRoundTrip(t *testing.T) {
	srv, url := newTestServer(t)
	client := h2cClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: "laptop",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	workerID := reg.Msg.GetWorkerId()
	token := reg.Msg.GetWorkerToken()

	stream := client.WorkerStream(ctx)
	if err := stream.Send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: token}},
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	const eventBurst = 5
	// Fake worker command loop.
	go func() {
		for {
			cmd, err := stream.Receive()
			if err != nil {
				return
			}
			switch c := cmd.GetCommand().(type) {
			case *cpv1.MasterToWorker_ListWorkspaces:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{
						RequestId: cmd.GetRequestId(),
						Ok:        true,
						Workspaces: []*cpv1.Workspace{
							{Path: "/home/me/proj", Name: "proj", Branch: "main", Present: true},
						},
					},
				}})
			case *cpv1.MasterToWorker_StartAgent:
				// Ack the command.
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
				// Relay a burst of session events for this session.
				for i := 0; i < eventBurst; i++ {
					_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_SessionEvent{
						SessionEvent: &cpv1.SessionEvent{
							SessionId:  c.StartAgent.GetSessionId(),
							Type:       "message.part.delta",
							Properties: []byte(`{"n":` + string(rune('0'+i)) + `}`),
						},
					}})
				}
			default:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			}
		}
	}()

	// Wait for the master to receive Hello and mark the worker online.
	waitFor(t, 3*time.Second, func() bool {
		info, ok := srv.Registry().Get(workerID)
		return ok && info.Status == registry.StatusOnline
	}, "worker never came online")

	// Subscribe BEFORE starting the agent so we capture its event burst.
	events := srv.Bus().SubscribeAll()

	// ListWorkspaces round-trips through the stream.
	ws, err := srv.ListWorkspaces(ctx, workerID)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(ws) != 1 || ws[0].Name != "proj" {
		t.Fatalf("workspaces: %+v", ws)
	}

	// StartRemoteAgent mints a global session id and routes it.
	sessionID, err := srv.StartRemoteAgent(ctx, workerID, master.StartSpec{
		Workspace: "/home/me/proj", Prompt: "hello", ViewportWidth: 80, ViewportHeight: 24,
	})
	if err != nil {
		t.Fatalf("StartRemoteAgent: %v", err)
	}
	if sessionID == "" {
		t.Fatal("empty session id")
	}
	if got, ok := srv.Registry().WorkerForSession(sessionID); !ok || got != workerID {
		t.Fatalf("session not routed: %q,%v", got, ok)
	}

	// The relayed session events must arrive on the master bus with FRESH,
	// gap-free master seqs (1..N) — the contract the panel's resync depends on.
	for want := int64(1); want <= eventBurst; want++ {
		select {
		case ev := <-events:
			if ev.Seq != want {
				t.Fatalf("relayed seq: got %d want %d", ev.Seq, want)
			}
			if ev.Type != "message.part.delta" {
				t.Fatalf("relayed type: %q", ev.Type)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for relayed event seq %d", want)
		}
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// fakeWorkerWith registers a fake worker (stable id = wsName) whose command
// loop answers ListWorkspaces with that worker's workspace and acks
// StartAgent with an event burst; it returns the worker id once the master has
// marked it online.
func fakeWorkerWith(t *testing.T, ctx context.Context, client controlplanev1connect.ControlPlaneServiceClient, wsName string) string {
	t.Helper()

	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: wsName, WorkerId: wsName,
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stream := client.WorkerStream(ctx)
	if err := stream.Send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: reg.Msg.GetWorkerToken()}},
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	go func() {
		for {
			cmd, err := stream.Receive()
			if err != nil {
				return
			}
			switch c := cmd.GetCommand().(type) {
			case *cpv1.MasterToWorker_ListWorkspaces:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{
						RequestId:  cmd.GetRequestId(),
						Ok:         true,
						Workspaces: []*cpv1.Workspace{{Path: "/ws/" + wsName, Name: wsName, Branch: "main", Present: true}},
					},
				}})
			case *cpv1.MasterToWorker_StartAgent:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_SessionEvent{
					SessionEvent: &cpv1.SessionEvent{SessionId: c.StartAgent.GetSessionId(), Type: "message.part.delta"},
				}})
			default:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			}
		}
	}()
	return reg.Msg.GetWorkerId()
}

// TestWorkerStreamRoundTrip_ConcurrentWorkers pins Phase G gap 2: two workers
// streaming at once, with ListWorkspaces and StartRemoteAgent issued for both
// CONCURRENTLY, must each be answered by the right worker and route to the
// right worker. The pending-command table in master.Call is keyed per
// request_id, so a result from worker B can never complete a call against
// worker A — this test holds that invariant under -race.
func TestWorkerStreamRoundTrip_ConcurrentWorkers(t *testing.T) {
	srv, url := newTestServer(t)
	client := h2cClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	alpha := fakeWorkerWith(t, ctx, client, "alpha")
	beta := fakeWorkerWith(t, ctx, client, "beta")
	for _, id := range []string{alpha, beta} {
		waitFor(t, 3*time.Second, func() bool {
			info, ok := srv.Registry().Get(id)
			return ok && info.Status == registry.StatusOnline
		}, "worker "+id+" never came online")
	}

	// Interleave the two workers' calls on the shared client.
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for _, id := range []string{alpha, beta} {
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			ws, err := srv.ListWorkspaces(ctx, id)
			if err != nil {
				results <- fmt.Errorf("ListWorkspaces(%s): %w", id, err)
				return
			}
			if len(ws) != 1 || ws[0].Name != id {
				results <- fmt.Errorf("ListWorkspaces(%s) = %+v, want workspace named %q", id, ws, id)
			}
		}(id)
		go func(id string) {
			defer wg.Done()
			sid, err := srv.StartRemoteAgent(ctx, id, master.StartSpec{
				Workspace: "/ws/" + id, Prompt: "hi", ViewportWidth: 80, ViewportHeight: 24,
			})
			if err != nil {
				results <- fmt.Errorf("StartRemoteAgent(%s): %w", id, err)
				return
			}
			if got, ok := srv.Registry().WorkerForSession(sid); !ok || got != id {
				results <- fmt.Errorf("session %s routed to %q,%v, want %q", sid, got, ok, id)
			}
		}(id)
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
}

// TestTunnel_FirstFrameRejects pins Phase G gap 4 at the wire boundary: the
// Tunnel RPC's first frame IS the auth check. A bogus token and a known but
// EXPIRED token are both refused with Unauthenticated before any yamux session
// is registered — no tunnel exists to proxy UI requests through.
func TestTunnel_FirstFrameRejects(t *testing.T) {
	srv, url := newTestServer(t)
	client := h2cClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: "laptop",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	validToken := reg.Msg.GetWorkerToken()

	// Force the token expired via the registry's replace path (the only way a
	// token expires in-process short of sleeping out the TTL): re-Add the same
	// worker id with an expired token, exactly as Restore-after-restart would.
	srv.Registry().Add(reg.Msg.GetWorkerId(), "laptop", nil, nil,
		pairing.Token{Value: validToken, ExpiresAt: time.Now().Add(-time.Minute)}, time.Now())

	t.Run("unknown token", func(t *testing.T) {
		stream := client.Tunnel(ctx)
		if err := stream.Send(&cpv1.TunnelChunk{Data: []byte("bogus-token")}); err != nil {
			t.Fatalf("send first frame: %v", err)
		}
		_, err := stream.Receive()
		if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
			t.Fatalf("bogus token: want Unauthenticated, got %v (err=%v)", got, err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		stream := client.Tunnel(ctx)
		if err := stream.Send(&cpv1.TunnelChunk{Data: []byte(validToken)}); err != nil {
			t.Fatalf("send first frame: %v", err)
		}
		_, err := stream.Receive()
		if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
			t.Fatalf("expired token: want Unauthenticated, got %v (err=%v)", got, err)
		}
	})

	// The rejection happens before tunnels.set, so no tunnel is registered and
	// the worker's UI subdomain stays unroutable — nothing was proxied.
}
