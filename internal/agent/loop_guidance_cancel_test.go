package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// newTestLoopRunner spins up a LoopRunner backed by a real temp-file SQLite
// store and an in-memory bus — enough to exercise the DB-mutating helpers.
func newTestLoopRunner(t *testing.T) *LoopRunner {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &LoopRunner{
		Store: session.NewStore(database),
		Bus:   bus.New(0),
	}
}

// TestCancelPartialToolCalls_PairsToolUseWithResult reproduces the mid-loop
// guidance scenario where the LLM stream is cancelled while the model is still
// emitting tool calls. The assistant message ends up carrying dangling tool_use
// blocks; without a matching tool_result the next provider request 400s. The
// fix creates a paired error tool-result message. This test asserts that every
// assistant tool_use produced by convertMessages has a matching tool_result.
func TestCancelPartialToolCalls_PairsToolUseWithResult(t *testing.T) {
	lr := newTestLoopRunner(t)
	store := lr.Store

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

	// A user prompt, then an assistant message with two tool-call parts that were
	// only partially streamed before guidance cancelled the stream — no
	// tool-result message follows (the dangling-tool_use bug state).
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	assistantID := session.NewMessageID()
	assistantMsg := &session.MessageInfo{ID: assistantID, SessionID: sess.ID, Role: session.RoleAssistant, ParentID: &userMsg.ID, CreatedAt: session.Now()}
	if err := store.CreateMessage(assistantMsg); err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}

	calls := []pendingToolCall{
		{CallID: "call_a", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
		{CallID: "call_b", Name: "read", Input: json.RawMessage(`{"path":"x.go"}`)}, // partial args are fine
	}
	for i := range calls {
		partData, _ := json.Marshal(session.ToolPartData{
			Tool:   calls[i].Name,
			CallID: calls[i].CallID,
			State:  session.ToolState{Status: session.ToolPending, Input: calls[i].Input},
		})
		part := &session.Part{
			ID:        session.NewPartID(),
			MessageID: assistantID,
			SessionID: sess.ID,
			Type:      session.PartTool,
			Data:      partData,
			CreatedAt: session.Now(),
			UpdatedAt: session.Now(),
		}
		if err := store.CreatePart(part); err != nil {
			t.Fatalf("create tool part: %v", err)
		}
		calls[i].PartID = part.ID
	}

	// Sanity check: before the fix the history has unpaired tool_use blocks.
	before, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if uses, results := toolUseIDs(convertMessages(before, false, "claude-opus-4-6")), toolResultIDs(convertMessages(before, false, "claude-opus-4-6")); len(uses) == 0 || len(results) != 0 {
		t.Fatalf("precondition: expected dangling tool_use and no results, got uses=%v results=%v", uses, results)
	}

	// Apply the fix.
	lr.cancelPartialToolCalls(sess.ID, assistantID, calls)

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}

	// Every assistant tool_use must now be paired with a tool_result.
	model := convertMessages(msgs, false, "claude-opus-4-6")
	uses := toolUseIDs(model)
	results := toolResultIDs(model)
	if len(uses) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %d (%v)", len(uses), uses)
	}
	for _, id := range uses {
		if !results[id] {
			t.Errorf("tool_use %q has no matching tool_result — history would 400 on the next request", id)
		}
	}

	// The tool parts on the assistant message must be marked cancelled so the UI
	// stops showing them as running.
	for _, m := range msgs {
		if m.Info.Role != session.RoleAssistant {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var td session.ToolPartData
			if err := json.Unmarshal(p.Data, &td); err != nil {
				t.Fatalf("unmarshal tool part: %v", err)
			}
			if td.State.Status != session.ToolError {
				t.Errorf("expected cancelled tool part status %q, got %q", session.ToolError, td.State.Status)
			}
			if td.State.Error == nil || *td.State.Error == "" {
				t.Errorf("expected cancellation error message on tool part %q", td.CallID)
			}
		}
	}
}

// TestCancelPartialToolCalls_SanitizesInvalidJSONArgs verifies that a tool call
// interrupted mid-arguments (leaving partial, invalid JSON) is coerced to a
// valid empty object before it is re-sent as tool_use arguments on the resumed
// request. Sending invalid JSON arguments can make strict OpenAI-compatible
// endpoints reject or stall the request.
func TestCancelPartialToolCalls_SanitizesInvalidJSONArgs(t *testing.T) {
	lr := newTestLoopRunner(t)
	store := lr.Store

	sess := &session.Session{ID: session.NewSessionID(), ProjectID: "p", Directory: "/tmp/p", Title: "t", SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now()}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	assistantID := session.NewMessageID()
	if err := store.CreateMessage(&session.MessageInfo{ID: assistantID, SessionID: sess.ID, Role: session.RoleAssistant, CreatedAt: session.Now()}); err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}

	// The persisted part always holds valid JSON: the loop coerces unparseable
	// arguments to {} before writing, precisely so one bad tool call cannot
	// leave an unreadable record behind. The partial, invalid input lives where
	// it actually accumulates — in the in-memory pendingToolCall below — and
	// that is what the cancel path has to sanitize before it is replayed.
	//
	// The setup used to marshal the invalid input straight into the part. That
	// marshal fails (json.RawMessage is validated on the way out) and the error
	// was discarded, so the part was stored empty and the assertion below was
	// satisfied by a tool_use with no id and no name — the very shape that makes
	// Anthropic reject the whole conversation.
	partData, err := json.Marshal(session.ToolPartData{
		Tool:   "write",
		CallID: "call_partial",
		State:  session.ToolState{Status: session.ToolPending, Input: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("marshal tool part: %v", err)
	}
	part := &session.Part{ID: session.NewPartID(), MessageID: assistantID, SessionID: sess.ID, Type: session.PartTool, Data: partData, CreatedAt: session.Now(), UpdatedAt: session.Now()}
	if err := store.CreatePart(part); err != nil {
		t.Fatalf("create tool part: %v", err)
	}

	// Partial input must be invalid JSON for this test to be meaningful.
	if json.Valid([]byte(`{"path":"a.js","content":"const x`)) {
		t.Fatal("test setup: expected the partial input to be invalid JSON")
	}

	lr.cancelPartialToolCalls(sess.ID, assistantID, []pendingToolCall{
		{CallID: "call_partial", Name: "write", Input: json.RawMessage(`{"path":"a.js","content":"const x`), PartID: part.ID},
	})

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}

	// The tool_use arguments in the converted (provider-bound) request must now
	// be valid JSON.
	model := convertMessages(msgs, false, "claude-opus-4-6")
	sawToolUse := false
	for _, m := range model {
		if m.Role != "assistant" || m.ToolCalls == nil {
			continue
		}
		var calls []struct {
			Function struct {
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(m.ToolCalls, &calls); err != nil {
			t.Fatalf("unmarshal tool_calls: %v", err)
		}
		for _, c := range calls {
			sawToolUse = true
			if !json.Valid([]byte(c.Function.Arguments)) {
				t.Errorf("resumed tool_use arguments are not valid JSON: %q", c.Function.Arguments)
			}
		}
	}
	if !sawToolUse {
		t.Fatal("expected a tool_use in the converted messages")
	}
}

// TestCancelPartialToolCalls_NoParts is a guard for the vanished-parts edge:
// when none of the referenced parts resolve, no (empty, invalid) tool-result
// message should be created.
func TestCancelPartialToolCalls_NoParts(t *testing.T) {
	lr := newTestLoopRunner(t)
	sess := &session.Session{ID: session.NewSessionID(), ProjectID: "p", Directory: "/tmp/p", Title: "t", SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now()}
	if err := lr.Store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	assistantID := session.NewMessageID()
	assistantMsg := &session.MessageInfo{ID: assistantID, SessionID: sess.ID, Role: session.RoleAssistant, CreatedAt: session.Now()}
	if err := lr.Store.CreateMessage(assistantMsg); err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}

	// Reference a part ID that was never persisted.
	lr.cancelPartialToolCalls(sess.ID, assistantID, []pendingToolCall{{CallID: "ghost", Name: "bash", PartID: session.NewPartID()}})

	msgs, err := lr.Store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected only the assistant message (no empty tool-result message), got %d messages", len(msgs))
	}
}

func toolUseIDs(msgs []provider.ModelMessage) []string {
	var ids []string
	for _, m := range msgs {
		if m.Role != "assistant" || m.ToolCalls == nil {
			continue
		}
		var calls []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(m.ToolCalls, &calls) == nil {
			for _, c := range calls {
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

func toolResultIDs(msgs []provider.ModelMessage) map[string]bool {
	set := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			set[m.ToolCallID] = true
		}
	}
	return set
}
