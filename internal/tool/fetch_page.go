package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/search"
)

// FetchPageTool retrieves the text content of a URL via the configured search backend.
type FetchPageTool struct {
	Bridge search.Backend
}

func (FetchPageTool) ID() string { return "fetch_page" }
func (FetchPageTool) Description() string {
	return "Fetch a URL and return its readable text content. Call multiple times in parallel for different URLs."
}
func (FetchPageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "The URL to fetch"
			}
		},
		"required": ["url"]
	}`)
}

func (t FetchPageTool) Execute(ctx context.Context, args json.RawMessage, _ Context) (Result, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.URL == "" {
		return Result{Output: "url is required"}, nil
	}

	page, err := t.Bridge.FetchPage(ctx, input.URL)
	if err != nil {
		return Result{Output: fmt.Sprintf("Fetch failed for %s: %s", input.URL, err)}, nil
	}

	output := fmt.Sprintf("# %s\nURL: %s\n\n%s", page.Title, page.URL, page.Text)
	if page.Truncated {
		output += "\n\n[content truncated at 14,000 characters]"
	}

	return Result{
		Title:  page.Title,
		Output: output,
	}, nil
}
