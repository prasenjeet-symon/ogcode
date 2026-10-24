package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// nextPermissionEvent waits for the next permission event on ch.
func nextPermissionEvent(t *testing.T, ch <-chan bus.Event) (string, map[string]string) {
	t.Helper()
	select {
	case ev := <-ch:
		var p map[string]string
		if err := json.Unmarshal(ev.Properties, &p); err != nil {
			t.Fatalf("decode %s: %v", ev.Type, err)
		}
		return ev.Type, p
	case <-time.After(5 * time.Second):
		t.Fatal("no permission event was published")
		return "", nil
	}
}

// A teammate's prompt has to reach the session that spawned it: the human is
// watching that one, and a teammate's own page is not somewhere they are told to
// look. This drives the real requestPermission so the mirror cannot be dropped
// from the wiring without failing here.
func TestTeammatePermissionIsMirroredToTheParentSession(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, "ask")
	lr.ParentSessionOf = func(id session.SessionID) (session.SessionID, string) {
		if id != sessID {
			t.Errorf("ParentSessionOf(%q), want %q", id, sessID)
		}
		return "sess-parent", "build-7"
	}
	events := lr.Bus.SubscribeAll()

	done := make(chan struct{})
	var action permission.Action
	go func() {
		defer close(done)
		action, _ = lr.requestPermission(context.Background(), sessID, bashCall(t, "echo hello"), "m", "p")
	}()

	seen := map[string]map[string]string{}
	for len(seen) < 2 {
		event, p := nextPermissionEvent(t, events)
		if event != "permission.requested" {
			t.Fatalf("event = %q, want permission.requested", event)
		}
		seen[p["sessionId"]] = p
	}
	own, parent := seen[string(sessID)], seen["sess-parent"]
	if own == nil || parent == nil {
		t.Fatalf("events filed under %v, want the teammate and its parent", seen)
	}
	if own["permissionId"] == "" || own["permissionId"] != parent["permissionId"] {
		t.Fatalf("ids %q / %q, want one shared permission id", own["permissionId"], parent["permissionId"])
	}
	if got := parent["ownerSessionId"]; got != string(sessID) {
		t.Fatalf("mirrored ownerSessionId = %q, want %q", got, sessID)
	}
	if got := parent["teammate"]; got != "build-7" {
		t.Fatalf("mirrored teammate = %q, want build-7", got)
	}
	if got := parent["pattern"]; got != "echo hello" {
		t.Fatalf("mirrored pattern = %q, want echo hello", got)
	}

	// The parent's copy is answerable: the reply is keyed by permission id, so
	// resolving the single pending request unblocks the teammate.
	pending := perm.PendingForSession(string(sessID))
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want the one request", pending)
	}
	if pending[0].Teammate != "build-7" {
		t.Fatalf("stored teammate = %q, want build-7", pending[0].Teammate)
	}
	perm.Reply(pending[0].ID, "once")
	<-done
	if action != permission.Allow {
		t.Fatalf("reply resolved to %q, want allow", action)
	}
}

// Cancelling the wait must clear the parent's copy too — a mirrored prompt that
// outlives its loop sits there answering nothing.
func TestTeammatePermissionCancellationClearsTheParentQueue(t *testing.T) {
	lr, sessID, _ := newPermissionLoopRunner(t, "ask")
	lr.ParentSessionOf = func(session.SessionID) (session.SessionID, string) {
		return "sess-parent", "build-7"
	}
	events := lr.Bus.SubscribeAll()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		lr.requestPermission(ctx, sessID, bashCall(t, "echo hello"), "m", "p")
	}()

	// Drain the two permission.requested events, then cancel.
	for i := 0; i < 2; i++ {
		if event, _ := nextPermissionEvent(t, events); event != "permission.requested" {
			t.Fatalf("event = %q, want permission.requested", event)
		}
	}
	cancel()

	replied := map[string]string{}
	for len(replied) < 2 {
		event, p := nextPermissionEvent(t, events)
		if event != "permission.replied" {
			continue
		}
		replied[p["sessionId"]] = p["response"]
	}
	if replied["sess-parent"] != "cancelled" || replied[string(sessID)] != "cancelled" {
		t.Fatalf("replied = %v, want cancelled in both sessions", replied)
	}
	<-done
}

// A session that nobody spawned must not publish a second, mirror event.
func TestMirrorPermissionIsSilentWithoutAParent(t *testing.T) {
	b := bus.New(8)
	events := b.SubscribeAll()
	lr := &LoopRunner{Bus: b}
	lr.mirrorPermission("permission.requested", "", "sess-1", permission.Request{ID: "p1"}, "")
	lr.mirrorPermission("permission.requested", "sess-1", "sess-1", permission.Request{ID: "p1"}, "")

	select {
	case ev := <-events:
		t.Fatalf("unexpected %s event: %s", ev.Type, ev.Properties)
	case <-time.After(50 * time.Millisecond):
	}
}
