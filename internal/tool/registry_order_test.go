package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Tool definitions are part of the cached prompt prefix on every provider ogcode
// supports, so the order ForAgent returns them in must be a function of the
// input, not of Go's map iteration.
//
// This is deliberately asserted on the RAW order. The sibling test in
// registry_glob_test.go compares through the ids() helper, which sorts before
// comparing — so it validates glob membership and is blind to ordering, which is
// how a randomized tool array reached the wire on every step unnoticed.
//
// Why it matters, concretely: Anthropic orders its cached prefix
// tools → system → messages and pins a cache_control breakpoint to the LAST
// tool, so a reshuffle both moves the breakpoint and invalidates the system
// block and the entire message history behind it. OpenAI documents reordering
// as breaking the prefix as well.
func TestForAgentGlobOrderIsDeterministic(t *testing.T) {
	r := NewRegistry()
	// Enough matching tools that map-iteration randomness is overwhelming rather
	// than incidental: with one or two entries a map range often looks stable.
	for i := 0; i < 40; i++ {
		r.Register(fakeTool{fmt.Sprintf("mcp_server%02d_tool", i)})
	}
	r.Register(fakeTool{"bash"})
	r.Register(fakeTool{"read"})

	order := func() string {
		out := []string{}
		for _, td := range r.ForAgent([]string{"bash", "read", "mcp_*"}) {
			out = append(out, td.ID())
		}
		return strings.Join(out, ",")
	}

	first := order()
	for i := 0; i < 50; i++ {
		if got := order(); got != first {
			t.Fatalf("ForAgent returned a different order on call %d.\n first: %s\n  got: %s", i+2, first, got)
		}
	}

	// Explicit ids keep the caller's order; the glob expands in sorted order at
	// the position the pattern appeared.
	if !strings.HasPrefix(first, "bash,read,mcp_server00_tool,mcp_server01_tool,") {
		t.Errorf("unexpected shape — explicit ids should lead, glob should follow sorted:\n%s", first)
	}
}

// A glob whose matches are already sorted must not be reordered by the dedupe
// path when a narrower pattern precedes a wider one.
func TestForAgentOverlappingGlobsKeepDeterministicOrder(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"mcp_a_one", "mcp_a_two", "mcp_b_one", "mcp_c_one"} {
		r.Register(fakeTool{id})
	}

	order := func() string {
		out := []string{}
		for _, td := range r.ForAgent([]string{"mcp_a_*", "mcp_*"}) {
			out = append(out, td.ID())
		}
		return strings.Join(out, ",")
	}
	first := order()
	for i := 0; i < 50; i++ {
		if got := order(); got != first {
			t.Fatalf("overlapping globs reordered on call %d:\n first: %s\n   got: %s", i+2, first, got)
		}
	}
	// mcp_a_* resolves first and claims its two; mcp_* then adds the rest, sorted.
	if want := "mcp_a_one,mcp_a_two,mcp_b_one,mcp_c_one"; first != want {
		t.Errorf("got %q, want %q", first, want)
	}
}

// Generation is what lets a turn hold a resolved toolset and cheaply notice it
// went stale, instead of choosing between re-resolving every step (which churns
// the cached prompt prefix) and never re-resolving (which leaves a long turn
// blind to a server that connected after it started).
func TestRegistryGeneration(t *testing.T) {
	r := NewRegistry()
	start := r.Generation()

	r.Register(zzGenTool{"a"})
	afterAdd := r.Generation()
	if afterAdd == start {
		t.Fatal("Register did not bump the generation")
	}

	// A Remove that deletes nothing must NOT bump: Remove is called
	// speculatively with ids that may not be registered, and a bump there would
	// make every in-flight turn re-resolve — and take a cache miss — for nothing.
	r.Remove("never-registered")
	if r.Generation() != afterAdd {
		t.Error("Remove of an unknown id bumped the generation")
	}

	r.Remove("a")
	if r.Generation() == afterAdd {
		t.Error("Remove of a real id did not bump the generation")
	}

	// Re-registering an id bumps: ToolDef is an interface, so a replaced
	// definition is not detectable by comparison, and erring toward one extra
	// re-resolve is the cheap direction.
	before := r.Generation()
	r.Register(zzGenTool{"b"})
	r.Register(zzGenTool{"b"})
	if r.Generation() <= before+1 {
		t.Error("re-registering the same id should still bump")
	}
}

type zzGenTool struct{ id string }

func (z zzGenTool) ID() string                  { return z.id }
func (z zzGenTool) Description() string         { return "d" }
func (z zzGenTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (z zzGenTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	return Result{}, nil
}
