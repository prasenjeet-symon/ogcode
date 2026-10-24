package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// nextQuestionRequested waits for the next question.requested event on ch and
// decodes its payload — the loop publishes the Request struct itself, so both
// the direct and the mirrored copy arrive in that shape.
func nextQuestionRequested(t *testing.T, ch <-chan bus.Event) question.Request {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Type != "question.requested" {
			t.Fatalf("event = %q, want question.requested", ev.Type)
		}
		var req question.Request
		if err := json.Unmarshal(ev.Properties, &req); err != nil {
			t.Fatalf("decode %s: %v", ev.Type, err)
		}
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("no question.requested event was published")
		return question.Request{}
	}
}

// nextQuestionReplied waits for the next question.replied event on ch and
// decodes its properties as the string map the cancellation path publishes.
func nextQuestionReplied(t *testing.T, ch <-chan bus.Event) map[string]string {
	t.Helper()
	for {
		select {
		case ev := <-ch:
			if ev.Type != "question.replied" {
				continue
			}
			var p map[string]string
			if err := json.Unmarshal(ev.Properties, &p); err != nil {
				t.Fatalf("decode %s: %v", ev.Type, err)
			}
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("no question.replied event was published")
			return nil
		}
	}
}

// askStorageBatch is one batch to put to the user.
func askStorageBatch() []question.Question {
	return []question.Question{{
		Header: "Storage", Question: "Which store?",
		Options: []question.Option{{Label: "Postgres"}, {Label: "SQLite"}},
	}}
}

// A teammate's ask_user batch has to reach the session that spawned it, exactly
// like its permission prompts: the human is watching that session, and the
// teammate's own page is not somewhere they are told to look. This drives the
// real AskUser so the mirror cannot be dropped from the wiring without failing
// here.
func TestTeammateQuestionMirrorsToTheParentSession(t *testing.T) {
	sessID := session.SessionID("sess-team")
	lr := &LoopRunner{Bus: bus.New(16), Questions: question.NewManager()}
	lr.ParentSessionOf = func(id session.SessionID) (session.SessionID, string) {
		if id != sessID {
			t.Errorf("ParentSessionOf(%q), want %q", id, sessID)
		}
		return "sess-parent", "build-7"
	}
	events := lr.Bus.SubscribeAll()

	done := make(chan struct{})
	var reply question.Reply
	var askErr error
	go func() {
		defer close(done)
		reply, askErr = lr.AskUser(context.Background(), string(sessID), askStorageBatch())
	}()

	seen := map[string]question.Request{}
	for len(seen) < 2 {
		req := nextQuestionRequested(t, events)
		seen[req.SessionID] = req
	}
	own, ok := seen[string(sessID)]
	if !ok {
		t.Fatalf("no own question.requested filed under %q: %v", string(sessID), seen)
	}
	parent, ok := seen["sess-parent"]
	if !ok {
		t.Fatalf("no mirrored question.requested filed under sess-parent: %v", seen)
	}
	if own.ID == "" || own.ID != parent.ID {
		t.Fatalf("ids %q / %q, want one shared question id", own.ID, parent.ID)
	}
	if got := own.OwnerSessionID; got != "" {
		t.Errorf("own event ownerSessionId = %q, want empty", got)
	}
	if got := parent.OwnerSessionID; got != string(sessID) {
		t.Fatalf("mirrored ownerSessionId = %q, want %q", got, sessID)
	}
	if got := parent.Teammate; got != "build-7" {
		t.Fatalf("mirrored teammate = %q, want build-7", got)
	}
	if len(parent.Questions) != 1 || parent.Questions[0].Header != "Storage" {
		t.Fatalf("mirrored questions = %+v, want the full batch", parent.Questions)
	}

	// The parent's copy is answerable: the reply is keyed by question id, so
	// resolving the single pending request unblocks the teammate.
	pending := lr.Questions.PendingForSession(string(sessID))
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want the one batch", pending)
	}
	if pending[0].Teammate != "build-7" {
		t.Fatalf("stored teammate = %q, want build-7", pending[0].Teammate)
	}
	answer := question.Reply{Answers: []question.Answer{{Selected: []string{"Postgres"}}}}
	if !lr.Questions.Reply(pending[0].ID, answer) {
		t.Fatal("Reply refused the pending batch")
	}
	<-done
	if askErr != nil {
		t.Fatalf("AskUser returned %v", askErr)
	}
	if len(reply.Answers) != 1 || reply.Answers[0].Selected[0] != "Postgres" {
		t.Fatalf("the blocked loop got %+v, want the posted answers", reply)
	}
}

// Cancelling the wait must clear the parent's copy too — a mirrored dialog that
// outlives its loop sits there answering nothing.
func TestTeammateQuestionCancellationClearsTheParentQueue(t *testing.T) {
	sessID := session.SessionID("sess-team")
	lr := &LoopRunner{Bus: bus.New(16), Questions: question.NewManager()}
	lr.ParentSessionOf = func(session.SessionID) (session.SessionID, string) {
		return "sess-parent", "build-7"
	}
	events := lr.Bus.SubscribeAll()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var askErr error
	go func() {
		defer close(done)
		_, askErr = lr.AskUser(ctx, string(sessID), askStorageBatch())
	}()

	// Drain the two question.requested events, then cancel.
	for i := 0; i < 2; i++ {
		if req := nextQuestionRequested(t, events); req.ID == "" {
			t.Fatalf("question.requested %d carried no id", i)
		}
	}
	cancel()
	<-done
	if askErr != context.Canceled {
		t.Fatalf("AskUser error = %v, want context.Canceled", askErr)
	}

	replied := map[string]string{}
	for len(replied) < 2 {
		p := nextQuestionReplied(t, events)
		if p["questionId"] == "" {
			t.Fatalf("question.replied carried no id: %v", p)
		}
		replied[p["sessionId"]] = p["response"]
	}
	if replied["sess-parent"] != "cancelled" || replied[string(sessID)] != "cancelled" {
		t.Fatalf("replied = %v, want cancelled in both sessions", replied)
	}

	// The pending batch is gone with it: neither queue can list a dead dialog.
	if got := lr.Questions.PendingForSession(string(sessID)); len(got) != 0 {
		t.Fatalf("pending = %+v, want none after cancellation", got)
	}
}

// A session that nobody spawned must not publish a second, mirror event.
func TestMirrorQuestionIsSilentWithoutAParent(t *testing.T) {
	b := bus.New(8)
	events := b.SubscribeAll()
	lr := &LoopRunner{Bus: b}
	lr.mirrorQuestion("question.requested", "", "sess-1", question.Request{ID: "q1"}, "")
	lr.mirrorQuestion("question.replied", "sess-1", "sess-1", question.Request{ID: "q1"}, "cancelled")

	select {
	case ev := <-events:
		t.Fatalf("unexpected %s event: %s", ev.Type, ev.Properties)
	case <-time.After(50 * time.Millisecond):
	}
}
