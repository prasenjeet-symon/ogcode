package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// mechanicalStepsProvider answers the first len(roundTools) StreamChat calls
// with one tool call per name in that round's list, and a final text answer
// after that, recording StepMechanical from every request so a test can pin
// the loop's notion of a mechanical continuation.
type mechanicalStepsProvider struct {
	mu           sync.Mutex
	roundTools   [][]string
	textWithCall bool
	calls        int
	steps        []bool
}

func (m *mechanicalStepsProvider) ID() string { return "mock" }
func (m *mechanicalStepsProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *mechanicalStepsProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.steps = append(m.steps, req.StepMechanical)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		toolRound := call <= len(m.roundTools)
		if toolRound && m.textWithCall {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "partial"}
		}
		if toolRound {
			for k, name := range m.roundTools[call-1] {
				callID := fmt.Sprintf("call_%d_%d", call, k)
				ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: name}
				ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(`{}`)}
				ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
			}
		} else {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "final answer"}
		}
		fr := "tool_use"
		if !toolRound {
			fr = "stop"
		}
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// mechanicalNoopTool stands in for any tool the mechanical-flag tests do not
// want to run for real: the flag is computed from the call names before
// execution, and the stub keeps the executed transcript clean.
type mechanicalNoopTool struct{ id string }

func (t mechanicalNoopTool) ID() string          { return t.id }
func (t mechanicalNoopTool) Description() string { return "noop " + t.id + " (test)" }
func (t mechanicalNoopTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t mechanicalNoopTool) Execute(_ context.Context, _ json.RawMessage, _ tool.Context) (tool.Result, error) {
	return tool.Result{Title: t.id, Output: "ok"}, nil
}

// TestRunLoop_StepMechanicalFollowsToolOnlySteps pins the loop-side wiring of
// StreamRequest.StepMechanical: the first step of a turn is never mechanical,
// a step that follows WORK-tool calls without a final text is, and a step
// that follows a text-final answer is not. applyEffort holds a mechanical
// step on a flash-class model to its lightest level (provider side,
// openai_effort.go).
func TestRunLoop_StepMechanicalFollowsToolOnlySteps(t *testing.T) {
	t.Run("a work-tool-only round marks the next step mechanical", func(t *testing.T) {
		lr, mock := newMechanicalRunner(t, 1, false, "bash")
		runEffortTurn(t, lr, "")
		got := mock.recordedSteps()
		if len(got) < 2 {
			t.Fatalf("steps = %v, want two (tool round then text)", got)
		}
		if got[0] {
			t.Errorf("first step of the turn StepMechanical = true, want false")
		}
		if !got[1] {
			t.Errorf("step after a bash-only round StepMechanical = false, want true")
		}
		for i, s := range got[2:] {
			if s {
				t.Errorf("step after the text final (index %d) StepMechanical = true", i+2)
			}
		}
	})

	t.Run("text beside the tool call keeps the next step planning", func(t *testing.T) {
		lr, mock := newMechanicalRunner(t, 1, true, "write")
		runEffortTurn(t, lr, "")
		got := mock.recordedSteps()
		if len(got) < 2 {
			t.Fatalf("steps = %v, want two", got)
		}
		if got[1] {
			t.Errorf("step after a text+tool round StepMechanical = true, want false")
		}
	})
}

// TestRunLoop_ExplorationOnlyRoundsKeepTheNextStepPlanning pins the
// exploration exemption baked into nextStepMechanical: a tool-only round
// whose every call was a read-only planning or exploration tool
// (codebase_map, file_map, read, grep, glob, update_intent, compact_context,
// ask_user, plus the document, recall and syntax readers) shipped no work
// forward — the model spent the round orienting,
// and the step that follows usually has to think about what it just learned —
// so that step is NOT held to the lightest effort. Rounds that did real work
// keep marking the next step mechanical.
//
// Before the exemption every tool-only round was mechanical, which pinned
// reasoning_effort:"none" onto the step that plans the real work — the exact
// step the effort budget exists for.
func TestRunLoop_StepMechanicalExemptsExplorationRounds(t *testing.T) {
	for _, name := range []string{"update_intent", "read", "grep", "glob", "codebase_map", "file_map", "compact_context", "ask_user", "check_syntax", "read_pdf_page", "pdf_index", "docx_index", "read_docx_page", "memory_recall", "project_memory_recall"} {
		t.Run(name+"-only round leaves the next step planning", func(t *testing.T) {
			lr, mock := newMechanicalRunner(t, 1, false, name)
			runEffortTurn(t, lr, "")
			got := mock.recordedSteps()
			if len(got) < 2 {
				t.Fatalf("steps = %v, want two (tool round then text)", got)
			}
			if got[1] {
				t.Errorf("step after a %s-only round StepMechanical = true, want false", name)
			}
		})
	}

	t.Run("a mixed round with one work call still marks mechanical", func(t *testing.T) {
		lr, mock := newMechanicalRoundsRunner(t, false, []string{"update_intent", "write"})
		runEffortTurn(t, lr, "")
		got := mock.recordedSteps()
		if len(got) < 2 {
			t.Fatalf("steps = %v, want two (tool round then text)", got)
		}
		if !got[1] {
			t.Errorf("step after a mixed update_intent+write round StepMechanical = false, want true")
		}
	})
}

func newMechanicalRunner(t *testing.T, rounds int, textWithCall bool, toolName string) (*LoopRunner, *mechanicalStepsProvider) {
	t.Helper()
	names := make([][]string, rounds)
	for i := range names {
		names[i] = []string{toolName}
	}
	return newMechanicalRoundsRunner(t, textWithCall, names...)
}

// newMechanicalRoundsRunner drives a turn through RunLoop where round i
// answers with one tool call per name in rounds[i] and the turn ends with a
// text answer, registering a no-op stub for every tool named along the way.
func newMechanicalRoundsRunner(t *testing.T, textWithCall bool, rounds ...[]string) (*LoopRunner, *mechanicalStepsProvider) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	mock := &mechanicalStepsProvider{roundTools: rounds, textWithCall: textWithCall}
	reg := provider.NewRegistry()
	reg.Register(mock)
	tools := tool.NewRegistry()
	registered := map[string]bool{}
	for _, round := range rounds {
		for _, name := range round {
			if registered[name] {
				continue
			}
			registered[name] = true
			tools.Register(mechanicalNoopTool{id: name})
		}
	}
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 5,
	}
	return lr, mock
}

func (m *mechanicalStepsProvider) recordedSteps() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.steps...)
}
