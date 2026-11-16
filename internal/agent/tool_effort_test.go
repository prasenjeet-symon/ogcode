package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// toolEffortProvider answers the first step with one analyze_image call and
// the next with a final text answer. It takes low and high only, so a stored
// level outside that set shows whether the loop resolved the effort before
// handing it to the tool.
type toolEffortProvider struct {
	mu   sync.Mutex
	args string
	call int
}

func (p *toolEffortProvider) ID() string { return "mock" }
func (p *toolEffortProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}
func (p *toolEffortProvider) EffortSpec(string) provider.EffortSpec {
	return provider.EffortSpec{Levels: []string{provider.EffortLow, provider.EffortHigh}}
}
func (p *toolEffortProvider) StreamChat(_ context.Context, _ provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.call++
	call := p.call
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		toolRound := call == 1
		if toolRound {
			toolArgs, _ := json.Marshal(map[string]string{"path": p.args})
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: "call_1", ToolName: "analyze_image"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: "call_1", ToolInput: toolArgs}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: "call_1"}
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

// toolEffortRecorder stands in for the vision pipeline and records the effort
// analyze_image passed it, which is what the loop's own effort reaches the
// one-shot call as.
type toolEffortRecorder struct {
	mu      sync.Mutex
	efforts []string
}

func (r *toolEffortRecorder) run(_ context.Context, _ []byte, _, _, _, _, _, effort string) (string, error) {
	r.mu.Lock()
	r.efforts = append(r.efforts, effort)
	r.mu.Unlock()
	return "a 1x1 pixel", nil
}

func (r *toolEffortRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.efforts...)
}

// pngPixel is a valid 1x1 PNG, so the tool decodes and forwards it exactly as
// it would a screenshot.
const pngPixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// newToolEffortRunner wires a turn that calls the REAL analyze_image tool (with
// its pipeline replaced by a recorder) so a test can watch what the loop hands
// the tool instead of what a hand-built tool.Context carries.
func newToolEffortRunner(t *testing.T) (*LoopRunner, *toolEffortRecorder) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	image := filepath.Join(t.TempDir(), "shot.png")
	raw, err := base64.StdEncoding.DecodeString(pngPixel)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if err := os.WriteFile(image, raw, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	mock := &toolEffortProvider{args: image}
	reg := provider.NewRegistry()
	reg.Register(mock)
	rec := &toolEffortRecorder{}
	tools := tool.NewRegistry()
	tools.Register(tool.AnalyzeImageTool{Run: rec.run})
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 5,
	}
	return lr, rec
}

// TestToolContextCarriesTheRunEffort pins the loop-side half of analyze_image's
// effort: tool.Context.Effort is the depth the run resolved for its model, so
// the tool inherits the session's choice without being told, and a level the
// model does not take arrives empty rather than as a request it would fail.
func TestToolContextCarriesTheRunEffort(t *testing.T) {
	t.Run("the session's level reaches the tool", func(t *testing.T) {
		lr, rec := newToolEffortRunner(t)
		runEffortTurn(t, lr, provider.EffortHigh)
		got := rec.recorded()
		if len(got) == 0 {
			t.Fatal("analyze_image never reached the pipeline")
		}
		for _, e := range got {
			if e != provider.EffortHigh {
				t.Fatalf("analyze_image effort = %q, want high", e)
			}
		}
	})

	t.Run("a level the model lacks reaches it empty", func(t *testing.T) {
		lr, rec := newToolEffortRunner(t)
		runEffortTurn(t, lr, provider.EffortMax)
		got := rec.recorded()
		if len(got) == 0 {
			t.Fatal("analyze_image never reached the pipeline")
		}
		for _, e := range got {
			if e != "" {
				t.Fatalf("analyze_image effort = %q, want the default (empty)", e)
			}
		}
	})
}
