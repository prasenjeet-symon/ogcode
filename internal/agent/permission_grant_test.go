package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// newPermissionLoopRunner builds the minimum LoopRunner requestPermission
// needs: a store holding one session and a permission manager over a fresh
// global DB. No model or provider is involved — the grant path is pure rules.
func newPermissionLoopRunner(t *testing.T, sessPermission string) (*LoopRunner, session.SessionID, *permission.Manager) {
	t.Helper()
	projectDB, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close() })
	globalDB, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { globalDB.Close() })

	store := session.NewStore(projectDB)
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-model", SessionType: "build",
		Permission: sessPermission,
		CreatedAt:  session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	perm := permission.NewManager(permission.NewStore(globalDB))
	lr := &LoopRunner{Store: store, Bus: bus.New(16), Permissions: perm, Dir: sess.Directory}
	return lr, sess.ID, perm
}

func bashCall(t *testing.T, command string) pendingToolCall {
	t.Helper()
	raw, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: command})
	if err != nil {
		t.Fatalf("marshal command %q: %v", command, err)
	}
	return pendingToolCall{CallID: "c1", Name: "bash", Input: raw, Ready: true}
}

// answerNextPermission waits for a pending request on sessID and replies with
// the given response, then returns. It runs until the deadline so the caller
// can drive requestPermission concurrently.
func answerNextPermission(t *testing.T, perm *permission.Manager, sessID session.SessionID, response string, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		if pending := perm.PendingForSession(string(sessID)); len(pending) > 0 {
			perm.Reply(pending[0].ID, response)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// An "always" reply must grant the EXACT command the user approved and store it
// so the same command no longer prompts — in this session and in every other.
// This drives the reply path end-to-end through requestPermission.
func TestAlwaysReplyStoresAnExactGrant(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, "ask")

	done := make(chan struct{})
	var action permission.Action
	var replyErr error
	go func() {
		defer close(done)
		action, replyErr = lr.requestPermission(context.Background(), sessID, bashCall(t, "echo hello"), "m", "p")
	}()
	answerNextPermission(t, perm, sessID, "always", done)
	<-done

	if replyErr != nil {
		t.Fatalf("requestPermission: %v", replyErr)
	}
	if action != permission.Allow {
		t.Fatalf("always reply resolved to %q, want allow", action)
	}

	// The exact command is granted, and only it.
	if got := perm.Ruleset(string(sessID)).Evaluate("bash", "echo hello"); got != permission.Allow {
		t.Fatalf("approved command = %q, want allow", got)
	}
	if got := perm.Ruleset("some-other-session").Evaluate("bash", "echo hello"); got != permission.Allow {
		t.Fatalf("grant not visible in another session = %q, want allow", got)
	}
	if got := perm.Ruleset("some-other-session").Evaluate("bash", "rm -rf /"); got != permission.Ask {
		t.Fatalf("unrelated command = %q, want ask", got)
	}
}

// A "once" reply must NOT be stored: the next identical call still prompts.
func TestOnceReplyLeavesNothingStored(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, "ask")

	done := make(chan struct{})
	var action permission.Action
	var replyErr error
	go func() {
		defer close(done)
		action, replyErr = lr.requestPermission(context.Background(), sessID, bashCall(t, "echo hello"), "m", "p")
	}()
	answerNextPermission(t, perm, sessID, "once", done)
	<-done

	if replyErr != nil || action != permission.Allow {
		t.Fatalf("once reply = %q err %v, want allow", action, replyErr)
	}
	if got := perm.Ruleset(string(sessID)).Evaluate("bash", "echo hello"); got != permission.Ask {
		t.Fatalf("once left a stored grant: %q, want ask", got)
	}
}

// The already-granted path must short-circuit before any prompt is raised —
// the whole point of persisting the grant.
func TestStoredGrantSkipsThePrompt(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, "ask")
	if err := perm.SetDefaultMode(permission.ModeAsk); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	perm.AddRule("earlier-session", permission.Rule{Permission: "bash", Pattern: "echo hello", Action: permission.Allow})

	// No goroutine to answer: if the prompt were raised this would block until
	// the context deadline. The test proves it never is.
	action, err := lr.requestPermission(context.Background(), sessID, bashCall(t, "echo hello"), "m", "p")
	if err != nil {
		t.Fatalf("requestPermission: %v", err)
	}
	if action != permission.Allow {
		t.Fatalf("stored grant resolved to %q, want allow", action)
	}
	if got := perm.PendingForSession(string(sessID)); len(got) != 0 {
		t.Fatalf("a prompt was raised despite the stored grant: %+v", got)
	}
}

// In Yolo mode every prompted call runs, with no prompt and no LLM risk check
// (the runner has no model gateway, so a classification attempt would fail).
func TestYoloModeAllowsWithoutPrompting(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, permission.ModeYolo)

	// A command the rules classify as clearly consequential — in Auto this would
	// raise a prompt rather than reach here.
	action, err := lr.requestPermission(context.Background(), sessID, bashCall(t, "rm -rf ./build"), "m", "p")
	if err != nil {
		t.Fatalf("requestPermission: %v", err)
	}
	if action != permission.Allow {
		t.Fatalf("yolo mode resolved to %q, want allow", action)
	}
	if got := perm.PendingForSession(string(sessID)); len(got) != 0 {
		t.Fatalf("yolo mode raised a prompt: %+v", got)
	}
}

// An explicit Deny rule still denies in Yolo mode — the mode removes asking,
// not the configured refusals.
func TestYoloModeStillHonoursDeny(t *testing.T) {
	lr, sessID, perm := newPermissionLoopRunner(t, permission.ModeYolo)
	perm.EnsureRules(string(sessID), permission.Ruleset{
		{Permission: "bash", Pattern: "echo hi", Action: permission.Deny},
	})

	action, err := lr.requestPermission(context.Background(), sessID, bashCall(t, "echo hi"), "m", "p")
	if err != nil {
		t.Fatalf("requestPermission: %v", err)
	}
	if action != permission.Deny {
		t.Fatalf("denied command in yolo mode resolved to %q, want deny", action)
	}
}
