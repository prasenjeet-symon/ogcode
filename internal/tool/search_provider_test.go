package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/search"
)

// stubSearchBackend answers from fixed results and records the last query,
// standing in for the SwitchableBackend the server wires in.
type stubSearchBackend struct {
	results []search.SearchResult
	page    search.PageContent
	err     error

	lastQuery string
}

func (s *stubSearchBackend) Name() string { return "stub" }

func (s *stubSearchBackend) Search(_ context.Context, query string, _ int) ([]search.SearchResult, error) {
	s.lastQuery = query
	return s.results, s.err
}

func (s *stubSearchBackend) FetchPage(_ context.Context, _ string) (search.PageContent, error) {
	return s.page, s.err
}

func tavilyResult() search.SearchResult {
	return search.SearchResult{Title: "T", URL: "https://example.com", Snippet: "s", Provider: search.ProviderTavily}
}

// The provider header is the attribution this change exists for: the stamp the
// backend left on the data, shown in the tool output the model and user both
// read.
func TestWebSearchTool_ShowsProviderFromResults(t *testing.T) {
	b := &stubSearchBackend{results: []search.SearchResult{tavilyResult()}}

	res, err := WebSearchTool{Bridge: b}.Execute(context.Background(), json.RawMessage(`{"query":"test"}`), Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "Provider: tavily") {
		t.Errorf("output missing provider header:\n%s", res.Output)
	}
}

// A fallback rescue is visible as such — the label the fallback stamps, not
// the configured provider's bare name.
func TestWebSearchTool_ShowsFallbackAttribution(t *testing.T) {
	b := &stubSearchBackend{results: []search.SearchResult{{
		Title: "T", URL: "https://example.com", Snippet: "s", Provider: "native (tavily fallback)",
	}}}

	res, err := WebSearchTool{Bridge: b}.Execute(context.Background(), json.RawMessage(`{"query":"test"}`), Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "Provider: native (tavily fallback)") {
		t.Errorf("output missing fallback attribution:\n%s", res.Output)
	}
}

// A backend that stamps nothing (an out-of-tree Backend implementation) must
// not render an empty "Provider:" line — the tool falls back to the bridge's
// own Name.
func TestWebSearchTool_FallsBackToBridgeName(t *testing.T) {
	b := &stubSearchBackend{results: []search.SearchResult{{Title: "T", URL: "https://example.com", Snippet: "s"}}}

	res, err := WebSearchTool{Bridge: b}.Execute(context.Background(), json.RawMessage(`{"query":"test"}`), Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "Provider: stub") {
		t.Errorf("output missing bridge-name attribution:\n%s", res.Output)
	}
}

// captureSlog swaps the default logger for one writing into buf, returning a
// restore func for the test's defer.
func captureSlog(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	return func() { slog.SetDefault(prev) }
}

// The per-call log line carries the same attribution for the server log.
func TestWebSearchTool_LogsProviderPerCall(t *testing.T) {
	var logged bytes.Buffer
	restore := captureSlog(&logged)
	defer restore()

	b := &stubSearchBackend{results: []search.SearchResult{tavilyResult()}}
	tool := WebSearchTool{Bridge: b}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`), Context{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(logged.String(), "provider=tavily") {
		t.Errorf("log missing provider field:\n%s", logged.String())
	}
}

// fetch_page mirrors the search tool's contract: page stamp in the output,
// bridge name as the unstamped fallback, attribution on failure too.
func TestFetchPageTool_ShowsProvider(t *testing.T) {
	b := &stubSearchBackend{page: search.PageContent{URL: "https://example.com", Title: "Example", Text: "body", Provider: search.ProviderTavily}}

	res, err := FetchPageTool{Bridge: b}.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com"}`), Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "Provider: tavily") {
		t.Errorf("output missing provider header:\n%s", res.Output)
	}
}

func TestFetchPageTool_FallsBackToBridgeName(t *testing.T) {
	b := &stubSearchBackend{page: search.PageContent{URL: "https://example.com", Title: "Example", Text: "body"}}

	res, err := FetchPageTool{Bridge: b}.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com"}`), Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "Provider: stub") {
		t.Errorf("output missing bridge-name attribution:\n%s", res.Output)
	}
}

func TestFetchPageTool_LogsProviderOnFailure(t *testing.T) {
	var logged bytes.Buffer
	restore := captureSlog(&logged)
	defer restore()

	b := &stubSearchBackend{err: context.DeadlineExceeded}
	tool := FetchPageTool{Bridge: b}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com"}`), Context{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(logged.String(), "provider=stub") {
		t.Errorf("log missing provider field:\n%s", logged.String())
	}
}
