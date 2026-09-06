package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/search"
)

// WebSearchTool searches the web via the configured search backend and returns a markdown list of results.
type WebSearchTool struct {
	Bridge search.Backend
}

func (WebSearchTool) ID() string { return "web_search" }
func (WebSearchTool) Description() string {
	return "Search the web for information. Returns titles, URLs, and snippets for the top results. Call multiple times in parallel for different sub-queries."
}
func (WebSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query"
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of results to return (default 8, max 15)"
			}
		},
		"required": ["query"]
	}`)
}

func (t WebSearchTool) Execute(ctx context.Context, args json.RawMessage, _ Context) (Result, error) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Limit <= 0 || input.Limit > 15 {
		input.Limit = 8
	}

	results, err := t.Bridge.Search(ctx, input.Query, input.Limit)
	if err != nil {
		// The bridge name is who was asked first; the server log carries the
		// per-hop story when a fallback chain is wired.
		slog.Info("web_search: search failed", "provider", t.Bridge.Name(), "query", input.Query, "err", err)
		return Result{Output: fmt.Sprintf("Search failed: %s", err)}, nil
	}
	if len(results) == 0 {
		slog.Info("web_search: no results", "provider", t.Bridge.Name(), "query", input.Query)
		return Result{Title: input.Query, Output: "No results found."}, nil
	}

	// The stamp on the results is who actually answered — with a fallback chain
	// wired that can differ from the configured provider, and surfacing that
	// difference is the point.
	provider := results[0].Provider
	if provider == "" {
		provider = t.Bridge.Name()
	}
	slog.Info("web_search: results served", "provider", provider, "query", input.Query, "count", len(results))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\nProvider: %s\n\n", input.Query, provider))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return Result{
		Title:  input.Query,
		Output: sb.String(),
	}, nil
}
