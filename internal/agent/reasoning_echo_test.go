package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// A host such as DeepSeek takes each assistant message's own reasoning back as
// plain text. Only that model's reasoning goes, only from assistant messages,
// and only for a provider that sends it.
func TestConvertMessagesEchoesTheModelsOwnReasoning(t *testing.T) {
	reasoning := func(text, model string) session.Part {
		data, _ := json.Marshal(session.ReasoningPartData{Text: text, Model: model})
		return session.Part{Type: session.PartReasoning, Data: data}
	}
	text := func(s string) session.Part {
		data, _ := json.Marshal(session.TextPartData{Text: s})
		return session.Part{Type: session.PartText, Data: data}
	}
	turn := func(model string) []*session.MessageWithParts {
		return []*session.MessageWithParts{
			{Info: session.MessageInfo{Role: session.RoleUser}, Parts: []session.Part{text("fix the bug")}},
			{Info: session.MessageInfo{Role: session.RoleAssistant}, Parts: []session.Part{
				reasoning("read the file first", model),
				{Type: session.PartTool, Data: json.RawMessage(`{"tool":"read","callId":"call_1","state":{"status":"completed","input":{}}}`)},
			}},
			{Info: session.MessageInfo{Role: session.RoleUser}, Parts: []session.Part{
				{Type: session.PartTool, Data: json.RawMessage(`{"tool":"read","callId":"call_1","state":{"status":"completed","input":{},"output":"package main"}}`)},
			}},
			{Info: session.MessageInfo{Role: session.RoleAssistant}, Parts: []session.Part{
				reasoning("it is the nil check", model), text("fixed"),
			}},
		}
	}
	echoed := func(msgs []provider.ModelMessage) []string {
		var out []string
		for _, m := range msgs {
			out = append(out, m.ReasoningText)
		}
		return out
	}

	got := convertMessages(turn("deepseek-flash"), false, "deepseek-flash", true)
	if want := []string{"", "read the file first", "", "it is the nil check"}; !slices.Equal(echoed(got), want) {
		t.Errorf("reasoning text = %q, want %q", echoed(got), want)
	}
	for i, m := range got {
		if len(m.ReasoningParts) != 0 {
			t.Errorf("message %d: unsigned reasoning became a thinking block: %+v", i, m.ReasoningParts)
		}
	}

	none := []string{"", "", "", ""}
	if got := echoed(convertMessages(turn("deepseek-flash"), false, "deepseek-flash", false)); !slices.Equal(got, none) {
		t.Errorf("reasoning attached for a provider that does not send it: %q", got)
	}
	if got := echoed(convertMessages(turn("deepseek-flash"), false, "deepseek-v4-pro", true)); !slices.Equal(got, none) {
		t.Errorf("another model's reasoning attached: %q", got)
	}
	if got := echoed(convertMessages(turn(""), false, "deepseek-flash", true)); !slices.Equal(got, none) {
		t.Errorf("reasoning of unknown origin attached: %q", got)
	}
}

// The reasoning a provider sends counts toward the request's size, so
// proactive compaction sees it.
func TestEstimateRequestTokensCountsEchoedReasoning(t *testing.T) {
	msg := provider.ModelMessage{Role: "assistant", Content: json.RawMessage(`"done"`)}
	without := estimateRequestTokens(provider.StreamRequest{Messages: []provider.ModelMessage{msg}})
	msg.ReasoningText = "first read the handler, then check where the error is swallowed"
	with := estimateRequestTokens(provider.StreamRequest{Messages: []provider.ModelMessage{msg}})
	if with <= without {
		t.Errorf("estimate with reasoning %d, without %d: the reasoning was not counted", with, without)
	}
}

// echoingProvider scripts a three-step turn — reason and call grep, twice, then
// answer — and records the history each agent step was sent. echo is what it
// reports as provider.EchoesReasoning.
type echoingProvider struct {
	echo bool

	mu    sync.Mutex
	steps int
	sent  [][]provider.ModelMessage
}

func (m *echoingProvider) ID() string { return "mock-echo" }
func (m *echoingProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-echo-model", ProviderID: "mock-echo"}}
}
func (m *echoingProvider) EchoesReasoning() bool { return m.echo }

func (m *echoingProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	step := 0
	if req.Thinking { // an agent step, not a utility call
		m.mu.Lock()
		m.steps++
		step = m.steps
		m.sent = append(m.sent, slices.Clone(req.Messages))
		m.mu.Unlock()
	}
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if step == 1 || step == 2 {
			// Reasoning arrives in deltas, as it streams.
			ch <- provider.StreamEvent{Type: provider.EventReasoning, Text: fmt.Sprintf("step %d: ", step)}
			ch <- provider.StreamEvent{Type: provider.EventReasoning, Text: "look for foo"}
			id := fmt.Sprintf("call_%d", step)
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: id, ToolName: "grep", ToolInput: json.RawMessage(`{"pattern":"foo"}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: id}
			fr := "tool_calls"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
			return
		}
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

func runEchoTurn(t *testing.T, echo bool) *echoingProvider {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-echo-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	mock := &echoingProvider{echo: echo}
	reg := provider.NewRegistry()
	reg.Register(mock)
	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 6,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(), Title: "t",
		Model: "mock-echo-model", Provider: "mock-echo", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := lr.Store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	user := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := lr.Store.CreateMessage(user); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	text, _ := json.Marshal(session.TextPartData{Text: "find foo"})
	if err := lr.Store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: user.ID, SessionID: sess.ID, Type: session.PartText,
		Data: text, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) != 3 {
		t.Fatalf("the turn ran %d agent steps, want 3", len(mock.sent))
	}
	return mock
}

// assistantReasoning lists the reasoning text on each assistant message sent.
func assistantReasoning(msgs []provider.ModelMessage) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == "assistant" {
			out = append(out, m.ReasoningText)
		}
	}
	return out
}

// The whole path: reasoning streamed in one step reaches the provider on the
// assistant message of every later step — in the same bytes each time, so the
// resent history stays a cache hit — and only for a provider that sends it.
func TestRunLoopEchoesReasoningToAProviderThatSendsIt(t *testing.T) {
	t.Run("a provider that sends it", func(t *testing.T) {
		m := runEchoTurn(t, true)
		if got, want := assistantReasoning(m.sent[1]), []string{"step 1: look for foo"}; !slices.Equal(got, want) {
			t.Errorf("step 2 assistant reasoning = %q, want %q", got, want)
		}
		if got, want := assistantReasoning(m.sent[2]), []string{"step 1: look for foo", "step 2: look for foo"}; !slices.Equal(got, want) {
			t.Errorf("step 3 assistant reasoning = %q, want %q", got, want)
		}
		if prev := m.sent[1]; len(m.sent[2]) <= len(prev) || !reflect.DeepEqual(m.sent[2][:len(prev)], prev) {
			t.Errorf("step 3 did not resend step 2's history unchanged:\nstep 2: %+v\nstep 3: %+v", prev, m.sent[2])
		}
	})
	t.Run("a provider that does not", func(t *testing.T) {
		m := runEchoTurn(t, false)
		for i, msgs := range m.sent {
			for _, r := range assistantReasoning(msgs) {
				if r != "" {
					t.Errorf("step %d: reasoning %q attached for a provider that drops it", i+1, r)
				}
			}
		}
	})
}
