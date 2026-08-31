package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func toolPart(role session.MessageRole, callID, tool string, out string) session.Part {
	data, _ := json.Marshal(session.ToolPartData{
		Tool:   tool,
		CallID: callID,
		State: session.ToolState{
			Status: session.ToolCompleted,
			Input:  json.RawMessage(`{}`),
			Output: &out,
		},
	})
	return session.Part{ID: session.NewPartID(), Type: session.PartTool, Data: data}
}

func msgWith(role session.MessageRole, parts ...session.Part) *session.MessageWithParts {
	return &session.MessageWithParts{
		Info:  session.MessageInfo{ID: session.NewMessageID(), Role: role},
		Parts: parts,
	}
}

// A tool part with no call id cannot be paired on any provider: OpenAI rejects
// an empty tool_call_id, and Anthropic rejects both a tool_use with no id and a
// tool_result that answers nothing. Sending it produced a 400 on every
// subsequent request in the session — the history is rebuilt identically each
// time, so retrying and resuming both reproduce it exactly.
func TestConvertMessages_DropsUnpairableToolParts(t *testing.T) {
	msgs := []*session.MessageWithParts{
		msgWith(session.RoleUser, session.Part{
			ID: session.NewPartID(), Type: session.PartText,
			Data: json.RawMessage(`{"text":"do the thing"}`),
		}),
		msgWith(session.RoleAssistant,
			toolPart(session.RoleAssistant, "call_ok", "grep", ""),
			toolPart(session.RoleAssistant, "", "broken", ""),
		),
		msgWith(session.RoleUser,
			toolPart(session.RoleUser, "call_ok", "grep", "found it"),
			toolPart(session.RoleUser, "", "broken", "orphan output"),
		),
	}

	out := convertMessages(msgs, false, "test-model")

	var toolCallIDs, resultIDs []string
	for _, m := range out {
		if m.ToolCalls != nil {
			var calls []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(m.ToolCalls, &calls); err != nil {
				t.Fatalf("tool_calls not JSON: %v", err)
			}
			for _, c := range calls {
				toolCallIDs = append(toolCallIDs, c.ID)
			}
		}
		if m.Role == "tool" {
			resultIDs = append(resultIDs, m.ToolCallID)
		}
	}

	for _, id := range toolCallIDs {
		if id == "" {
			t.Error("a tool_use with an empty id reached the request; Anthropic rejects it and OpenAI rejects its result")
		}
	}
	for _, id := range resultIDs {
		if id == "" {
			t.Error("a tool result with an empty tool_call_id reached the request")
		}
	}

	// The valid pair must survive intact — dropping the malformed one must not
	// take the good call's answer with it, or the model sees an unanswered call.
	if len(toolCallIDs) != 1 || toolCallIDs[0] != "call_ok" {
		t.Errorf("tool calls = %v, want exactly [call_ok]", toolCallIDs)
	}
	if len(resultIDs) != 1 || resultIDs[0] != "call_ok" {
		t.Errorf("tool results = %v, want exactly [call_ok]", resultIDs)
	}

	// Every call still has its answer, which is the invariant both APIs enforce.
	answered := map[string]bool{}
	for _, id := range resultIDs {
		answered[id] = true
	}
	for _, id := range toolCallIDs {
		if !answered[id] {
			t.Errorf("tool_use %q left unanswered by the drop", id)
		}
	}

	// And the orphan's output must not leak in as prose.
	for _, m := range out {
		if m.Content != nil && strings.Contains(string(m.Content), "orphan output") {
			t.Error("the dropped result's output was re-emitted as message content")
		}
	}
}

// An assistant turn whose only tool call is malformed must not emit an empty
// tool_calls array — both APIs reject that too.
func TestConvertMessages_NoEmptyToolCallsArray(t *testing.T) {
	msgs := []*session.MessageWithParts{
		msgWith(session.RoleAssistant, toolPart(session.RoleAssistant, "", "broken", "")),
	}
	for _, m := range convertMessages(msgs, false, "test-model") {
		if m.ToolCalls != nil && strings.TrimSpace(string(m.ToolCalls)) == "[]" {
			t.Error("emitted an empty tool_calls array")
		}
		if m.Role == "tool" && m.ToolCallID == "" {
			t.Error("emitted a tool result with no id")
		}
	}
}

// Tool-call arguments arrive from the provider and are not always valid JSON —
// a proxy that forwards a truncated delta as a complete call produces arguments
// that stop mid-string. Marshalling those into a part validates them and fails,
// and an unchecked failure there writes a part with no data at all, taking the
// call id with it. That one part then breaks the session: the UI shows it as
// malformed, and every rebuilt request carries a tool result Anthropic rejects.
func TestValidToolInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"truncated object", `{"pattern":"foo`, `{}`},
		{"empty", ``, `{}`},
		{"not an object", `"just a string"`, `{}`},
		{"array", `[1,2]`, `{}`},
		{"bare garbage", `undefined`, `{}`},
		{"valid object preserved", `{"pattern":"foo","path":"a.js"}`, `{"pattern":"foo","path":"a.js"}`},
		{"valid empty object preserved", `{}`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validToolInput(json.RawMessage(c.in))
			if string(got) != c.want {
				t.Errorf("validToolInput(%q) = %q, want %q", c.in, got, c.want)
			}
			// Whatever comes back must survive the round trip that broke before:
			// marshalling it into a part and reading the part back.
			b, err := json.Marshal(session.ToolPartData{
				Tool: "grep", CallID: "call_1",
				State: session.ToolState{Status: session.ToolPending, Input: got},
			})
			if err != nil {
				t.Fatalf("marshalling a part with this input fails (%v) — the part would be stored empty", err)
			}
			var back session.ToolPartData
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("part does not read back: %v", err)
			}
			if back.CallID != "call_1" {
				t.Errorf("call id lost in the round trip: %q", back.CallID)
			}
		})
	}
}

// The shape already sitting in sessions that hit this bug: a part whose data
// never marshalled, so nothing at all was stored. It has to become harmless at
// request-build time, because there is no migration — the row is already on
// disk, and the session has to start working again without one.
func TestConvertMessages_UnreadablePartDoesNotPoisonTheRequest(t *testing.T) {
	unreadable := session.Part{ID: session.NewPartID(), Type: session.PartTool, Data: nil}
	msgs := []*session.MessageWithParts{
		msgWith(session.RoleUser, session.Part{
			ID: session.NewPartID(), Type: session.PartText,
			Data: json.RawMessage(`{"text":"do the thing"}`),
		}),
		msgWith(session.RoleAssistant, unreadable),
		msgWith(session.RoleUser, session.Part{ID: session.NewPartID(), Type: session.PartTool, Data: json.RawMessage(`{}`)}),
	}

	for _, m := range convertMessages(msgs, false, "claude-opus-4-6") {
		if m.Role == "tool" {
			t.Errorf("an unreadable part still produced a tool result (id %q)", m.ToolCallID)
		}
		if m.ToolCalls != nil {
			t.Errorf("an unreadable part still produced tool_calls: %s", m.ToolCalls)
		}
		switch m.Role {
		case "user", "assistant", "system", "tool":
		default:
			t.Errorf("unexpected role %q", m.Role)
		}
	}
}
