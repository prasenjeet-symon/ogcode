package tool

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

// fakeTool is a minimal ToolDef for registry tests.
type fakeTool struct{ id string }

func (f fakeTool) ID() string                  { return f.id }
func (f fakeTool) Description() string         { return "fake" }
func (f fakeTool) Parameters() json.RawMessage { return json.RawMessage("{}") }
func (f fakeTool) Execute(context.Context, json.RawMessage, Context) (Result, error) {
	return Result{}, nil
}

func TestRegistry_ForAgent_Glob(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{"bash"})
	r.Register(fakeTool{"read"})
	r.Register(fakeTool{"mcp_github_create_issue"})
	r.Register(fakeTool{"mcp_github_list_repos"})
	r.Register(fakeTool{"mcp_slack_send"})

	// Exact ids still work.
	got := ids(r.ForAgent([]string{"bash", "read"}))
	want := []string{"bash", "read"}
	if !equal(got, want) {
		t.Errorf("exact: got %v want %v", got, want)
	}

	// "mcp_*" expands to every id starting with mcp_.
	got = ids(r.ForAgent([]string{"mcp_*"}))
	want = []string{"mcp_github_create_issue", "mcp_github_list_repos", "mcp_slack_send"}
	if !equal(got, want) {
		t.Errorf("mcp_*: got %v want %v", got, want)
	}

	// Overlapping patterns dedupe.
	got = ids(r.ForAgent([]string{"mcp_github_*", "mcp_*"}))
	if !equal(got, want) {
		t.Errorf("overlap dedupe: got %v want %v", got, want)
	}

	// Unknown plain id is dropped silently (preserves prior exact-match behavior).
	got = ids(r.ForAgent([]string{"nonexistent"}))
	if len(got) != 0 {
		t.Errorf("unknown plain id: got %v want []", got)
	}
}

func ids(tools []ToolDef) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.ID()
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRegistry_ConcurrentRegisterAndRead pins the lazy-connect safety
// invariant: MCP tools are Register'd from a background Connect goroutine while
// the agent loop reads via ForAgent/Get each step. Without the registry mutex
// this is a map data race (Go's runtime catches it under -race). Run with
// -race to catch a regression.
func TestRegistry_ConcurrentRegisterAndRead(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			r.Register(fakeTool{"mcp_srv_tool" + itoa(i)})
		}
	}()
	for i := 0; i < 200; i++ {
		_ = r.ForAgent([]string{"mcp_*"})
		_ = r.Get("mcp_srv_tool0")
		_ = r.List()
	}
	<-done
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
