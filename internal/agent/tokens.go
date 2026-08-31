package agent

import "github.com/prasenjeet-symon/ogcode/internal/provider"

// Token budgeting for proactive compaction. We can't run the model's exact
// tokenizer locally (Anthropic's is closed; OpenAI's needs a large vendored
// vocab, and this app is deliberately dependency-light and offline-first), and
// after the first step the provider reports the exact input-token count anyway
// — which effectiveRequestTokens prefers. The estimate below only has to be a
// good, slightly-conservative approximation for the first request and for
// freshly-added tool output not yet reflected in any reported count.
const (
	// compactionReserveTokens is held back from the context window for the
	// upcoming response plus a safety margin.
	compactionReserveTokens = 20000
	// fallbackMaxRequestTokens is the token budget used when the model's context
	// window is unknown (e.g. dynamically-fetched Ollama models). ~128k keeps the
	// prior behaviour (the old 500KB byte cap ÷ 4) for those models.
	fallbackMaxRequestTokens = 128000
	// minOutputTokens is the floor for a request's output budget. It matches the
	// Anthropic provider's own floor, so clamping for a nearly-full context window
	// can never leave the model less room than it had before budgeting existed.
	minOutputTokens = 4096
	// outputBudgetMargin is held back when sizing the output budget against the
	// context window. Providers reject a request whose input plus max_tokens
	// exceeds the window, and our input figure is an estimate, so the budget has
	// to leave slack for that estimate being low.
	outputBudgetMargin = 8000
	// imageTokenEstimate is a flat per-image token cost. Images are billed by
	// pixel area, not base64 length, so counting the (often megabyte-scale) base64
	// string as text massively over-counts. A large Anthropic image tops out around
	// ~1600 tokens; use that as a conservative flat estimate.
	imageTokenEstimate = 1600
)

// estimateTokens approximates the number of tokens a string will use. It blends
// two signals:
//
//   - byteEstimate (len/4): the classic "~4 characters per token" rule of thumb.
//     Accurate for prose, but under-counts punctuation-dense code/JSON by up to
//     ~3x because it ignores how BPE splits structural characters.
//   - segments: each maximal alphanumeric run, plus each individual punctuation/
//     symbol/non-ASCII rune, approximates roughly one BPE token. Accurate for
//     code/JSON, slightly high for prose.
//
// Averaging the two tracks real tokenizer counts within ~20% across both regimes
// and, crucially, never collapses to the byte estimate's 2-3x under-count on
// dense JSON — the case that must not slip past the compaction trigger.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	byteEstimate := len(s) / 4
	segments := 0
	inWord := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			if !inWord {
				segments++
				inWord = true
			}
		case r == ' ' || r == '\t' || r == '\r':
			// Whitespace merges into an adjacent token — no segment of its own.
			inWord = false
		default:
			// Newlines, punctuation, symbols, and every non-ASCII rune (CJK, emoji)
			// each count as roughly one token.
			inWord = false
			segments++
		}
	}
	est := (byteEstimate + segments + 1) / 2 // +1 rounds the average up (conservative)
	if est < 1 {
		est = 1
	}
	return est
}

// estimateRequestTokens estimates the total input tokens a request will consume:
// system prompts, every message field, images, reasoning blocks, and — unlike the
// old byte estimate — the tool definitions, which are serialized into every
// request and can be large (JSON schemas).
func estimateRequestTokens(req provider.StreamRequest) int {
	tokens := 0
	for _, s := range req.System {
		tokens += estimateTokens(s)
	}
	for _, m := range req.Messages {
		tokens++ // role + message framing overhead
		if m.Content != nil {
			tokens += estimateTokens(string(m.Content))
		}
		if m.ToolCalls != nil {
			tokens += estimateTokens(string(m.ToolCalls))
		}
		if m.ToolCallID != "" {
			tokens += estimateTokens(m.ToolCallID)
		}
		if m.Name != "" {
			tokens += estimateTokens(m.Name)
		}
		tokens += len(m.Images) * imageTokenEstimate
		for _, rp := range m.ReasoningParts {
			tokens += estimateTokens(rp.Text)
			tokens += estimateTokens(rp.Signature)
			tokens += estimateTokens(rp.RedactedData)
		}
	}
	// Tool definitions are sent on every request and were previously ignored.
	for _, t := range req.Tools {
		tokens += estimateTokens(t.Name)
		tokens += estimateTokens(t.Description)
		tokens += estimateTokens(string(t.Parameters))
	}
	return tokens
}

// effectiveRequestTokens returns the token count to compare against the
// compaction threshold. It prefers the provider-reported input token count from
// the previous step (reportedInputTokens = Input+CacheRead+CacheWrite, or 0 when
// no step has completed yet) when it is larger — that count is exact — and
// otherwise uses the local estimate, which catches the newest tool output not yet
// reflected in any reported count. The larger of the two errs toward compacting
// early rather than overflowing.
func effectiveRequestTokens(estimatedTokens, reportedInputTokens int) int {
	if reportedInputTokens > estimatedTokens {
		return reportedInputTokens
	}
	return estimatedTokens
}

// compactionThresholdTokens returns the request-size threshold (in tokens) above
// which the loop compacts before sending. With a known context window it is
// (contextWindow − reserve); the reserve is clamped to at most half the window so
// tiny models still get a usable budget. With an unknown window it falls back to
// a fixed token cap.
func compactionThresholdTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return fallbackMaxRequestTokens
	}
	reserve := compactionReserveTokens
	if reserve > contextWindow/2 {
		reserve = contextWindow / 2
	}
	return contextWindow - reserve
}

// outputTokenBudget sizes a request's max_tokens. Without one, a provider that
// applies a small default (Anthropic floors at 4096) truncates any long response
// mid-way — a large file written in a single tool call comes back cut off, with
// its arguments no longer valid JSON.
//
// The budget is the model's own ceiling, reduced to what the context window has
// left: input tokens and max_tokens are charged against the same window, so a
// nearly-full request must ask for less output or be rejected outright. A model
// with no known ceiling gets 0, meaning "send no limit and let the provider
// decide" — guessing one upward would break every request to that model.
func outputTokenBudget(modelMaxOutput, contextWindow, requestTokens int) int {
	if modelMaxOutput <= 0 {
		return 0
	}
	budget := modelMaxOutput
	if contextWindow > 0 {
		if room := contextWindow - requestTokens - outputBudgetMargin; room < budget {
			budget = room
		}
	}
	if budget < minOutputTokens {
		budget = minOutputTokens
	}
	return budget
}
