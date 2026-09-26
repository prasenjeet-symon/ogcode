package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// generateTitle uses the LLM to generate a short title from the session's first
// request, updating the session title worker-side. This is the worker-side
// analogue of the server's generateTitle (internal/server/session_routes.go:447)
// but uses the worker env's provider so remote sessions get titles even though
// their DB never touches the master.
//
// The session row is created by startAgent with the prompt as a placeholder
// title, so a title is generated once per session (the first message). It runs
// in a goroutine — failures are logged but never block the loop from starting.
func (e *env) generateTitle(ctx context.Context, sess *session.Session) {
	if e == nil || e.runner == nil {
		return
	}
	firstMessage := sess.Title // createSessionRows seeded the title with the prompt
	if len(firstMessage) > 500 {
		firstMessage = firstMessage[:500]
	}

	// Resolve the provider for the session's model, falling back to the env's
	// default provider (the same way startAgent runs the loop).
	var p provider.Provider
	if sess.Model != "" {
		p = e.runner.Registry.ResolveProviderFor(sess.Model, sess.Provider)
	}
	if p == nil {
		p = e.runner.DefaultProvider
	}
	if p == nil {
		slog.Warn("generateTitle: no provider available, skipping title generation")
		return
	}

	// Use a fast model for title generation to minimize latency and cost.
	// Falls back to the session model if no fast alternative is available.
	titleModel := sess.Model
	for _, m := range p.Models() {
		id := strings.ToLower(m.ID)
		if strings.Contains(id, "haiku") || strings.Contains(id, "mini") || strings.Contains(id, "flash") {
			titleModel = m.ID
			break
		}
	}

	titleCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	promptContent, _ := json.Marshal(
		"Generate a very short title (5-8 words max) for a conversation that starts with this message. " +
			"Respond with ONLY the title, no quotes, no punctuation at the end, no explanation. " +
			"The title should capture the main topic or intent.\n\nMessage: " + firstMessage,
	)

	req := provider.StreamRequest{
		Model:    titleModel,
		System:   []string{"You generate concise, descriptive titles. Respond with only the title text, nothing else."},
		Messages: []provider.ModelMessage{{Role: "user", Content: promptContent}},
	}

	ch, err := p.StreamChat(titleCtx, req)
	if err != nil {
		slog.Warn("generateTitle: stream failed", "err", err)
		return
	}

	sessionID := sess.ID
	var title strings.Builder
	var usage *provider.TokenUsage
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			title.WriteString(evt.Text)
		}
		if evt.Type == provider.EventUsage {
			usage = evt.Usage
		}
		if evt.Type == provider.EventError {
			slog.Warn("generateTitle: stream error", "err", evt.Error)
			return
		}
	}
	// Record what the title call spent: like the server's title generation, the
	// risk check and compaction, it streams usage the main-turn accounting never
	// sees. Recorded regardless of whether the title is kept below.
	if e.runner.Store != nil && usage != nil {
		if err := e.runner.Store.AddUtilityUsage(sessionID, session.TokenCounts{
			Input:      usage.InputTokens,
			Output:     usage.OutputTokens,
			Reasoning:  usage.ReasoningTokens,
			CacheRead:  usage.CacheReadTokens,
			CacheWrite: usage.CacheWriteTokens,
		}); err != nil {
			slog.Warn("record utility usage", "err", err)
		}
	}

	generated := strings.TrimSpace(title.String())
	generated = strings.Trim(generated, "\"'`")
	if generated == "" {
		slog.Warn("generateTitle: empty title generated, skipping")
		return
	}
	if len(generated) > 100 {
		generated = generated[:100] + "…"
	}

	// Re-fetch to avoid clobbering a title changed since the row was created; the
	// placeholder is still the prompt, so only update when it is.
	sess, err = e.runner.Store.Get(sess.ID)
	if err != nil || sess == nil {
		slog.Warn("generateTitle: session not found", "err", err)
		return
	}
	// Only update if the title is still the placeholder we seeded.
	if sess.Title == firstMessage || sess.Title == "" {
		sess.Title = generated
		sess.UpdatedAt = session.Now()
		if err := e.runner.Store.Update(sess); err != nil {
			slog.Error("generateTitle: failed to update session title", "err", err)
			return
		}
		slog.Info("generateTitle: updated session title", "session", sess.ID, "title", generated)
	}
}
