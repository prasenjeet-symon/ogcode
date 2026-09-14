package registry

import (
	"testing"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
)

type fakeConn struct {
	sent []*cpv1.MasterToWorker
}

func (f *fakeConn) Send(cmd *cpv1.MasterToWorker) error {
	f.sent = append(f.sent, cmd)
	return nil
}

func tok(v string) pairing.Token {
	return pairing.Token{Value: v, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestAddAndTokenIndex(t *testing.T) {
	r := New()
	now := time.Now()
	r.Add("w1", "laptop", []string{"git"}, []Workspace{{Path: "/a", Name: "a"}}, tok("t1"), now)

	id, ok := r.WorkerIDForToken("t1")
	if !ok || id != "w1" {
		t.Fatalf("WorkerIDForToken: got %q,%v", id, ok)
	}
	info, ok := r.Get("w1")
	if !ok || info.Name != "laptop" || info.Status != StatusOffline {
		t.Fatalf("Get: %+v ok=%v", info, ok)
	}
	if len(info.Workspaces) != 1 || info.Workspaces[0].Path != "/a" {
		t.Fatalf("workspaces: %+v", info.Workspaces)
	}
}

func TestUpdateTokenReindexes(t *testing.T) {
	r := New()
	r.Add("w1", "n", nil, nil, tok("old"), time.Now())
	if !r.UpdateToken("w1", tok("new")) {
		t.Fatal("UpdateToken returned false")
	}
	if _, ok := r.WorkerIDForToken("old"); ok {
		t.Fatal("old token must be removed from index")
	}
	if id, ok := r.WorkerIDForToken("new"); !ok || id != "w1" {
		t.Fatalf("new token index: %q,%v", id, ok)
	}
}

func TestAttachSendDetach(t *testing.T) {
	r := New()
	r.Add("w1", "n", nil, nil, tok("t"), time.Now())
	fc := &fakeConn{}
	if err := r.Attach("w1", fc, time.Now()); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Get("w1"); info.Status != StatusOnline {
		t.Fatalf("status after attach: %s", info.Status)
	}
	cmd := &cpv1.MasterToWorker{RequestId: "r1"}
	if err := r.SendCommand("w1", cmd); err != nil {
		t.Fatal(err)
	}
	if len(fc.sent) != 1 || fc.sent[0].GetRequestId() != "r1" {
		t.Fatalf("sent: %+v", fc.sent)
	}
	r.Detach("w1", fc)
	if info, _ := r.Get("w1"); info.Status != StatusOffline {
		t.Fatalf("status after detach: %s", info.Status)
	}
	if err := r.SendCommand("w1", cmd); err == nil {
		t.Fatal("SendCommand to offline worker must error")
	}
}

func TestDetachIgnoresStaleConn(t *testing.T) {
	r := New()
	r.Add("w1", "n", nil, nil, tok("t"), time.Now())
	old := &fakeConn{}
	_ = r.Attach("w1", old, time.Now())
	fresh := &fakeConn{}
	_ = r.Attach("w1", fresh, time.Now()) // reconnect

	// A late Detach from the OLD stream must not clobber the fresh conn.
	r.Detach("w1", old)
	if err := r.SendCommand("w1", &cpv1.MasterToWorker{}); err != nil {
		t.Fatalf("fresh conn should still be attached: %v", err)
	}
	if len(fresh.sent) != 1 {
		t.Fatalf("expected send to fresh conn, got %d", len(fresh.sent))
	}
}

func TestSessionRouting(t *testing.T) {
	r := New()
	r.RouteSession("s1", "w1")
	r.RouteSession("s2", "w1")
	r.RouteSession("s3", "w2")

	if id, ok := r.WorkerForSession("s1"); !ok || id != "w1" {
		t.Fatalf("WorkerForSession(s1): %q,%v", id, ok)
	}
	if got := len(r.SessionsForWorker("w1")); got != 2 {
		t.Fatalf("SessionsForWorker(w1): got %d want 2", got)
	}
	r.UnrouteSession("s1")
	if _, ok := r.WorkerForSession("s1"); ok {
		t.Fatal("s1 should be unrouted")
	}
}

func TestReapStale(t *testing.T) {
	r := New()
	now := time.Now()
	r.Add("w1", "n", nil, nil, tok("t"), now)
	r.RouteSession("s1", "w1")
	r.RouteSession("s2", "w1")

	// Not stale yet.
	dead, orphans := r.ReapStale(30*time.Second, now.Add(10*time.Second))
	if len(dead) != 0 || len(orphans) != 0 {
		t.Fatalf("premature reap: dead=%v orphans=%v", dead, orphans)
	}
	// Now stale.
	dead, orphans = r.ReapStale(30*time.Second, now.Add(time.Hour))
	if len(dead) != 1 || dead[0] != "w1" {
		t.Fatalf("dead: %v", dead)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans: %v", orphans)
	}
	if info, _ := r.Get("w1"); info.Status != StatusDead {
		t.Fatalf("status: %s", info.Status)
	}
	// Idempotent: a dead worker is not reaped twice.
	dead, _ = r.ReapStale(30*time.Second, now.Add(2*time.Hour))
	if len(dead) != 0 {
		t.Fatalf("dead worker reaped twice: %v", dead)
	}
}
