package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// resumeFixture builds a session with a user prompt already in it and returns
// the store, the session and the prompt's ID.
func resumeFixture(t *testing.T) (*LoopRunner, *session.Session, session.MessageID) {
	t.Helper()
	lr := newTestLoopRunner(t)
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "proj",
		Directory:   "/tmp/proj",
		Title:       "t",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := lr.Store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{
		ID: session.NewMessageID(), SessionID: sess.ID,
		Role: session.RoleUser, CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	return lr, sess, userMsg.ID
}

// addAssistant writes an assistant turn, optionally interrupted, with the given
// tool calls. A call with answered=true also gets a tool-result message.
func addAssistant(t *testing.T, lr *LoopRunner, sess *session.Session, parent session.MessageID,
	interrupted *session.Interruption, finish *string, calls map[string]bool) session.MessageID {
	t.Helper()

	id := session.NewMessageID()
	msg := &session.MessageInfo{
		ID: id, SessionID: sess.ID, Role: session.RoleAssistant,
		ParentID: &parent, Finish: finish, Interrupted: interrupted, CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(msg); err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}

	var answered []string
	for callID, isAnswered := range calls {
		data, _ := json.Marshal(session.ToolPartData{
			Tool: "bash", CallID: callID,
			State: session.ToolState{Status: session.ToolPending, Input: json.RawMessage(`{"command":"ls"}`)},
		})
		part := &session.Part{
			ID: session.NewPartID(), MessageID: id, SessionID: sess.ID,
			Type: session.PartTool, Data: data, CreatedAt: session.Now(), UpdatedAt: session.Now(),
		}
		if err := lr.Store.CreatePart(part); err != nil {
			t.Fatalf("create tool part: %v", err)
		}
		if isAnswered {
			answered = append(answered, callID)
		}
	}
	if len(answered) > 0 {
		resultMsg := &session.MessageInfo{
			ID: session.NewMessageID(), SessionID: sess.ID,
			Role: session.RoleUser, ParentID: &id, CreatedAt: session.Now(),
		}
		if err := lr.Store.CreateMessage(resultMsg); err != nil {
			t.Fatalf("create result msg: %v", err)
		}
		for _, callID := range answered {
			out := "ok"
			data, _ := json.Marshal(session.ToolPartData{
				Tool: "bash", CallID: callID,
				State: session.ToolState{Status: session.ToolCompleted, Output: &out},
			})
			part := &session.Part{
				ID: session.NewPartID(), MessageID: resultMsg.ID, SessionID: sess.ID,
				Type: session.PartTool, Data: data, CreatedAt: session.Now(), UpdatedAt: session.Now(),
			}
			if err := lr.Store.CreatePart(part); err != nil {
				t.Fatalf("create result part: %v", err)
			}
		}
	}
	return id
}

// unansweredCalls counts tool_use blocks in the session with no matching result.
// This is the invariant the provider enforces: every tool_use must be answered,
// or the whole request is rejected.
func unansweredCalls(t *testing.T, lr *LoopRunner, sessionID session.SessionID) int {
	t.Helper()
	messages, err := lr.Store.GetMessages(sessionID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	calls, results := map[string]bool{}, map[string]bool{}
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var d session.ToolPartData
			if json.Unmarshal(p.Data, &d) != nil || d.CallID == "" {
				continue
			}
			if m.Info.Role == session.RoleAssistant {
				calls[d.CallID] = true
			} else {
				results[d.CallID] = true
			}
		}
	}
	n := 0
	for id := range calls {
		if !results[id] {
			n++
		}
	}
	return n
}

func errFinish() *string { f := "error"; return &f }

// The invariant everything else rests on: a turn that died between emitting a
// tool call and writing its result leaves a tool_use nothing answers, and both
// major APIs reject that request outright. Until it is closed, the session
// cannot be continued at all — not by a resume and not by the user typing.
func TestReconcile_ClosesDanglingToolCalls(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	addAssistant(t, lr, sess, prompt, crashedInterruption(), errFinish(),
		map[string]bool{"call_done": true, "call_dangling": false})

	if got := unansweredCalls(t, lr, sess.ID); got != 1 {
		t.Fatalf("fixture should start with 1 unanswered call, got %d", got)
	}

	if _, err := lr.ReconcileSession(sess.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := unansweredCalls(t, lr, sess.ID); got != 0 {
		t.Errorf("%d tool calls still unanswered after reconcile; the next request would be rejected", got)
	}
}

// Reconciling is reached from the error path, from startup, and from the resume
// request itself. Running it more than once must not keep appending results for
// calls it already closed.
func TestReconcile_IsIdempotent(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	addAssistant(t, lr, sess, prompt, crashedInterruption(), errFinish(),
		map[string]bool{"call_dangling": false})

	for i := 0; i < 3; i++ {
		if _, err := lr.ReconcileSession(sess.ID); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}

	messages, _ := lr.Store.GetMessages(sess.ID, "", 100)
	results := 0
	for _, m := range messages {
		if m.Info.Role != session.RoleUser {
			continue
		}
		for _, p := range m.Parts {
			if p.Type == session.PartTool {
				results++
			}
		}
	}
	if results != 1 {
		t.Errorf("got %d tool results for one dangling call; reconcile is appending on every run", results)
	}
}

// A process killed mid-stream writes nothing on the way out. The turn it leaves
// has no finish reason at all, and nothing else in the system will ever claim
// it — so startup recovery has to.
func TestReconcile_ClaimsTurnsLeftUnfinishedByACrash(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	addAssistant(t, lr, sess, prompt, nil, nil, nil)

	target, err := lr.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target == nil {
		t.Fatal("a turn with no finish reason was not offered as resumable")
	}
	if target.Interrupted.Reason != session.InterruptCrashed {
		t.Errorf("reason = %q, want crashed", target.Interrupted.Reason)
	}
	if target.Finish == nil || *target.Finish != "error" {
		t.Error("the turn was left without a finish reason, so the UI would show it as still running")
	}
}

// A turn that finished normally is not an interruption, and offering to resume
// it would invite the user to re-run something that already worked.
func TestReconcile_LeavesCompletedTurnsAlone(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	stop := "stop"
	addAssistant(t, lr, sess, prompt, nil, &stop, nil)

	target, err := lr.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target != nil {
		t.Error("a normally-finished turn was offered as resumable")
	}
}

// The heart of the design. A turn that got some tool calls done before dying
// must keep them: the results are on disk, and some of those tools had real
// side effects. Deleting the turn would discard the record and invite the model
// to run them a second time — which for a shell command is not a repeat.
func TestPrepareResume_KeepsCompletedToolWork(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	interrupted := addAssistant(t, lr, sess, prompt, crashedInterruption(), errFinish(),
		map[string]bool{"call_done": true, "call_dangling": false})

	ok, err := lr.PrepareResume(sess.ID)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !ok {
		t.Fatal("PrepareResume reported nothing to resume")
	}

	kept, err := lr.Store.GetMessage(interrupted)
	if err != nil || kept == nil {
		t.Fatalf("the interrupted turn was deleted, taking its completed tool results with it")
	}
	if got := unansweredCalls(t, lr, sess.ID); got != 0 {
		t.Errorf("%d calls left unanswered, so the resumed request would be rejected", got)
	}

	// The last message must not be a finished assistant turn, or the loop would
	// see it and stop on the first iteration without doing anything.
	messages, _ := lr.Store.GetMessages(sess.ID, "", 100)
	if shouldBreak(messages) {
		t.Error("the resumed loop would break immediately instead of continuing")
	}
}

// A turn that produced only text leaves a fragment stopping mid-word. Re-sending
// it asks the model to continue from its own truncated output rather than to
// take the step again, and nothing outside the message was done — so it goes.
func TestPrepareResume_DropsATextOnlyFragment(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	interrupted := addAssistant(t, lr, sess, prompt, crashedInterruption(), errFinish(), nil)

	textData, _ := json.Marshal(session.TextPartData{Text: "I'll start by reading the confi"})
	part := &session.Part{
		ID: session.NewPartID(), MessageID: interrupted, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := lr.Store.CreatePart(part); err != nil {
		t.Fatalf("create text part: %v", err)
	}

	ok, err := lr.PrepareResume(sess.ID)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !ok {
		t.Fatal("PrepareResume reported nothing to resume")
	}

	gone, err := lr.Store.GetMessage(interrupted)
	if err == nil && gone != nil {
		t.Error("the truncated turn survived; the model would be asked to continue mid-word")
	}
	messages, _ := lr.Store.GetMessages(sess.ID, "", 100)
	if shouldBreak(messages) {
		t.Error("the resumed loop would break immediately instead of continuing")
	}
}

func TestPrepareResume_NothingToDoOnAHealthySession(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	stop := "stop"
	addAssistant(t, lr, sess, prompt, nil, &stop, nil)

	ok, err := lr.PrepareResume(sess.ID)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if ok {
		t.Error("a healthy session was reported as resumable")
	}
}

// The classification decides whether a Resume button appears at all. Offering
// one for a request the provider will reject identically next time wastes the
// user's time; withholding one for a rate limit strands a session that only
// needed waiting.
func TestClassifyInterruption(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		reason    session.InterruptReason
		resumable bool
	}{
		{"rate limit", &provider.APIError{StatusCode: http.StatusTooManyRequests}, session.InterruptRateLimit, true},
		{"server error", &provider.APIError{StatusCode: http.StatusBadGateway}, session.InterruptServerError, true},
		{"overloaded", &provider.APIError{StatusCode: 529}, session.InterruptServerError, true},
		{"bad key", &provider.APIError{StatusCode: http.StatusUnauthorized}, session.InterruptAuth, true},
		{"no balance", &provider.APIError{StatusCode: http.StatusPaymentRequired}, session.InterruptAuth, true},
		{"malformed request", &provider.APIError{StatusCode: http.StatusBadRequest, Body: "unknown field"}, session.InterruptFatal, false},
		{"dropped connection", errors.New("read tcp: connection reset by peer"), session.InterruptNetwork, true},
		{"unrecognised", errors.New("something nobody predicted"), session.InterruptServerError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyInterruption(tc.err, 3)
			if got == nil {
				t.Fatal("no interruption recorded")
			}
			if got.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.reason)
			}
			if got.Resumable != tc.resumable {
				t.Errorf("resumable = %v, want %v", got.Resumable, tc.resumable)
			}
			if got.Detail == "" {
				t.Error("no detail, so the UI has nothing to tell the user")
			}
			if got.Step != 3 {
				t.Errorf("step = %d, want 3", got.Step)
			}
		})
	}
}

// A rate limit that names its window is the one case where the UI can say when
// to come back rather than just that something failed.
func TestClassifyInterruption_CarriesTheRetryWindow(t *testing.T) {
	got := classifyInterruption(&provider.APIError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: 90 * time.Second,
	}, 1)

	if got.RetryAfter == 0 {
		t.Fatal("Retry-After was dropped; the UI cannot say when to resume")
	}
	if wait := time.Until(time.Unix(got.RetryAfter, 0)); wait < 80*time.Second || wait > 100*time.Second {
		t.Errorf("retry window = %v, want about 90s", wait)
	}
	if got.Detail == "" || !containsAny(got.Detail, "1m30s", "90s") {
		t.Errorf("detail = %q, want it to name the wait", got.Detail)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// The shape found in a real workspace, and the reason no Resume button appeared
// for it: four build sessions whose last turn recorded finish "tool_calls",
// ran a bash call to completion, and never wrote the paired result message. The
// loop died in the gap. Nothing about that turn is missing a finish reason, so a
// claim keyed on the absence of one walks straight past it — while the session
// itself cannot be continued at all, because the unpaired call makes the next
// request invalid.
func TestReconcile_ClaimsATurnStrandedOnToolCalls(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	toolCalls := "tool_calls"
	id := addAssistant(t, lr, sess, prompt, nil, &toolCalls, map[string]bool{"call_ran": false})

	target, err := lr.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target == nil {
		t.Fatal("a turn stranded on tool_calls was not offered as resumable")
	}
	if target.ID != id {
		t.Errorf("claimed the wrong turn: %s", target.ID)
	}
	if target.Interrupted.Reason != session.InterruptStalled {
		t.Errorf("reason = %q, want stalled", target.Interrupted.Reason)
	}
	if got := unansweredCalls(t, lr, sess.ID); got != 0 {
		t.Errorf("%d calls still unanswered; the session stays unusable", got)
	}
}

// A failure recorded before any of this existed carries a finish reason and no
// interruption record. Keying the claim on the reason is what makes those
// sessions reachable rather than permanently buttonless.
func TestReconcile_ClaimsLegacyFailures(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	addAssistant(t, lr, sess, prompt, nil, errFinish(), nil)

	target, err := lr.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target == nil {
		t.Fatal("a turn that failed before this feature existed was not offered as resumable")
	}
	if !target.Interrupted.Resumable {
		t.Error("legacy failure marked unresumable")
	}
}

// The two endings the model or the user actually chose. Offering to resume
// either would invite re-running something that already concluded.
func TestReconcile_LeavesChosenEndingsAlone(t *testing.T) {
	for _, finish := range []string{"stop", "end_turn", "aborted"} {
		t.Run(finish, func(t *testing.T) {
			lr, sess, prompt := resumeFixture(t)
			f := finish
			addAssistant(t, lr, sess, prompt, nil, &f, nil)

			target, err := lr.ReconcileSession(sess.ID)
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if target != nil {
				t.Errorf("finish %q was claimed as an interruption", finish)
			}
		})
	}
}

// A tool that ran to completion before the loop died has its output sitting on
// the assistant part already. Only the pairing was missing — overwriting the
// result with "interrupted" would discard real work and tell the model
// something false about what happened.
func TestReconcile_KeepsOutputFromToolsThatFinished(t *testing.T) {
	lr, sess, prompt := resumeFixture(t)
	toolCalls := "tool_calls"
	id := addAssistant(t, lr, sess, prompt, nil, &toolCalls, nil)

	out := "total 4\ndrwxr-xr-x  x"
	data, _ := json.Marshal(session.ToolPartData{
		Tool: "bash", CallID: "call_ran",
		State: session.ToolState{
			Status: session.ToolCompleted,
			Input:  json.RawMessage(`{"command":"ls -la"}`),
			Output: &out,
		},
	})
	part := &session.Part{
		ID: session.NewPartID(), MessageID: id, SessionID: sess.ID,
		Type: session.PartTool, Data: data, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := lr.Store.CreatePart(part); err != nil {
		t.Fatalf("create tool part: %v", err)
	}

	if _, err := lr.ReconcileSession(sess.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	messages, _ := lr.Store.GetMessages(sess.ID, "", 100)
	var result *session.ToolPartData
	for _, m := range messages {
		if m.Info.Role != session.RoleUser {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var d session.ToolPartData
			if json.Unmarshal(p.Data, &d) == nil && d.CallID == "call_ran" {
				result = &d
			}
		}
	}
	if result == nil {
		t.Fatal("no tool result was written for the completed call")
	}
	if result.State.Status != session.ToolCompleted {
		t.Errorf("status = %q, want completed — the tool ran", result.State.Status)
	}
	if result.State.Output == nil || *result.State.Output != out {
		t.Errorf("output = %v, want the command's real output preserved", result.State.Output)
	}
}
