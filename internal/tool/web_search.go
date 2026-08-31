package tool

import (
	"context"
	"encoding/json"
	"fmt"
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
				"type": "number",
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
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Limit <= 0 || input.Limit > 15 {
		input.Limit = 8
	}

	results, err := t.Bridge.Search(ctx, input.Query, input.Limit)
	if err != nil {
		return Result{Output: fmt.Sprintf("Search failed: %s", err)}, nil
	}
	if len(results) == 0 {
		return Result{Title: input.Query, Output: "No results found."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", input.Query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return Result{
		Title:  input.Query,
		Output: sb.String(),
	}, nil
}
