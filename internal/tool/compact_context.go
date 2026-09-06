package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// minCompactSummaryChars rejects a summary too short to be carrying anything.
// The agent is about to drop every earlier message in the turn in exchange for
// this text; a one-liner means the work that came before is simply lost.
const minCompactSummaryChars = 80

// CompactContextTool lets the agent reclaim its own context mid-turn on
// endpoints that do not cache a repeated prefix, where every step re-pays full
// price for the entire accumulated history.
//
// The tool itself only validates. The agent loop does the work: seeing this
// call, it records a watermark at the assistant message that made it, and from
// the next step onward assembles the request as the summary plus everything
// after that watermark. Nothing is deleted — the session store keeps every
// message, so history and the UI are unaffected — only the model-facing slice
// narrows.
type CompactContextTool struct{}

func NewCompactContextTool() CompactContextTool { return CompactContextTool{} }

func (CompactContextTool) ID() string { return "compact_context" }

func (CompactContextTool) Description() string {
	return "Replace everything you have done so far in this turn with a summary you write, freeing the context it occupies. Use it when earlier steps have served their purpose — large files you have already extracted what you need from, searches whose answer you have recorded — and re-sending them on every remaining step is pure cost. Your summary becomes the only record of that work for the rest of the turn, so it must carry every conclusion, file path, and decision you still need. Anything you leave out is gone and must be rediscovered from scratch."
}

func (CompactContextTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["summary"],
		"properties": {
			"summary": {
				"type": "string",
				"description": "A complete account of the turn so far, written for a reader who cannot see any of it. Include: what the task is and what remains, what you established and where (exact file paths and line ranges), decisions made and approaches already ruled out, and any exact values — names, flags, config, commands — you would otherwise have to look up again. Omit raw file contents you have already drawn your conclusions from."
			}
		}
	}`)
}

// ParseCompactContextArgs extracts and validates the summary from a raw tool
// call. Exported so the agent loop reads the argument through exactly the same
// parser the tool validated it with, rather than a second, drifting copy.
func ParseCompactContextArgs(raw json.RawMessage) (string, error) {
	var params struct {
		Summary string `json:"summary"`
	}
	if err := DecodeArgs(raw, &params); err != nil {
		return "", err
	}
	summary := strings.TrimSpace(params.Summary)
	if summary == "" {
		return "", errors.New("summary is required")
	}
	if len(summary) < minCompactSummaryChars {
		return "", errors.New("summary is too short to stand in for the work it replaces")
	}
	return summary, nil
}

func (CompactContextTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	summary, err := ParseCompactContextArgs(args)
	if err != nil {
		// A validation failure must leave the context untouched. Returning the
		// reason as output (rather than an error) lets the agent correct itself
		// on the next step instead of failing the turn.
		return Result{
			Title:  "Compact Context",
			Output: "Context was NOT compacted: " + err.Error() + ". Nothing was dropped. Call compact_context again with a summary that fully stands in for the work so far, or carry on without compacting.",
		}, nil
	}

	return Result{
		Title: "Compact Context",
		// "compacted" is the agent loop's signal to record a watermark. It is set
		// only on this success path, so a rejected summary can never cause the
		// loop to drop context for a compaction that did not happen.
		Metadata: map[string]any{"compacted": true, "summaryChars": len(summary)},
		Output:   "Context compacted. Every message before this call has left your context and been replaced by your summary — from here on, that summary is the only record of the turn so far. Continue the task; do not re-read what you already summarized unless the summary genuinely lacks something you now need.",
	}, nil
}
