package worker

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// recoverFixtures opens a throwaway session DB/Store plus a LoopRunner with only
// the Store and Bus wired — ReconcileSession touches nothing else, so the
// fixture needs nothing more.
func recoverFixtures(t *testing.T) (*session.Session, *session.Store, *agent.LoopRunner) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ogcode.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	store := session.NewStore(database)
	runner := &agent.LoopRunner{
		Store: store,
		Bus:   bus.New(1024),
	}
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "proj",
		Directory:   "/tmp/proj",
		Title:       "t",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess, store, runner
}

// addPromptAndAssistant writes a user prompt then an assistant turn, optionally
// carrying a dangling (unanswered) tool call.
func addPromptAndAssistant(t *testing.T, store *session.Store, sess *session.Session, typef string, danglingCall bool, finish *string) {
	t.Helper()
	userMsg := &session.MessageInfo{
		ID: session.NewMessageID(), SessionID: sess.ID,
		Role: session.RoleUser, CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	asstID := session.NewMessageID()
	asst := &session.MessageInfo{
		ID: asstID, SessionID: sess.ID, Role: session.RoleAssistant,
		ParentID: &userMsg.ID, Finish: finish, CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(asst); err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}
	if danglingCall {
		data, _ := json.Marshal(session.ToolPartData{
			Tool: "bash", CallID: "call-1",
			State: session.ToolState{Status: session.ToolPending, Input: json.RawMessage(`{"command":"ls"}`)},
		})
		if err := store.CreatePart(&session.Part{
			ID: session.NewPartID(), MessageID: asstID, SessionID: sess.ID,
			Type: session.PartTool, Data: data, CreatedAt: session.Now(), UpdatedAt: session.Now(),
		}); err != nil {
			t.Fatalf("create tool part: %v", err)
		}
	}
}

// pendingToolCalls counts tool_use parts still sitting in a pending/running
// state — i.e. calls the recovery failed to close.
func pendingToolCalls(t *testing.T, store *session.Store, id session.SessionID) int {
	t.Helper()
	messages, err := store.GetMessages(id, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	n := 0
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var d session.ToolPartData
			if json.Unmarshal(p.Data, &d) == nil && d.State.Status == session.ToolPending {
				n++
			}
		}
	}
	return n
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRecoverInterruptedSessions_MarksDanglingTurnResumable pins that boot
// recovery closes a dangling tool call left by a crash and claims the turn so
// the session is valid to resume.
func TestRecoverInterruptedSessions_MarksDanglingTurnResumable(t *testing.T) {
	sess, store, runner := recoverFixtures(t)
	// A turn that started a tool call and never finished (no finish reason, no
	// interruption record) is the crash shape: the unpaired call makes the next
	// request invalid until repaired.
	addPromptAndAssistant(t, store, sess, "build", true, nil)

	recoverInterruptedSessions(store, runner, discardLogger())

	if n := pendingToolCalls(t, store, sess.ID); n != 0 {
		t.Fatalf("dangling tool call left pending after recovery, want closed: %d", n)
	}

	// Recovery must have claimed the turn: the last assistant message now has an
	// interruption record (Resumable), so ReconcileSession reports a target.
	target, err := runner.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target == nil {
		t.Fatal("session not resumable after recovery of a dangling tool call")
	}
	if target.Interrupted == nil || !target.Interrupted.Resumable {
		t.Fatal("recovered turn is not marked resumable")
	}
}

// TestRecoverInterruptedSessions_LeavesFinishedSessionUntouched pins that a
// session which ended naturally is not claimed: recover must not fabricate an
// interruption where the model finished cleanly.
func TestRecoverInterruptedSessions_LeavesFinishedSessionUntouched(t *testing.T) {
	sess, store, runner := recoverFixtures(t)
	endTurn := "end_turn"
	addPromptAndAssistant(t, store, sess, "build", false, &endTurn)

	recoverInterruptedSessions(store, runner, discardLogger())

	target, err := runner.ReconcileSession(sess.ID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if target != nil {
		t.Fatalf("finished session became resumable, want untouched (got target %q)", target.ID)
	}
}

// TestRecoverInterruptedSessions_SkipsNonResumableTypes pins that only
// build/plan/"" sessions are repaired: a crashed task session is left dangling
// (cheap to restart, so recover stays out of it).
func TestRecoverInterruptedSessions_SkipsNonResumableTypes(t *testing.T) {
	sess, store, runner := recoverFixtures(t)
	// Override the type to one recover must skip.
	sess.SessionType = "task"
	if err := store.Update(sess); err != nil {
		t.Fatalf("update session type: %v", err)
	}
	addPromptAndAssistant(t, store, sess, "task", true, nil)

	recoverInterruptedSessions(store, runner, discardLogger())

	if n := pendingToolCalls(t, store, sess.ID); n == 0 {
		t.Fatal("recover closed a tool call in a non-resumable session type; want it left alone")
	}
}
