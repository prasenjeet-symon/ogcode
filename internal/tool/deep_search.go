package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// DeepSearchFunc is a function that runs a child search-agent session and returns
// the synthesised answer. Implemented by agent.LoopRunner.RunSearchSession and
// wired in from server.go to avoid the tool→agent import cycle.
type DeepSearchFunc func(ctx context.Context, query, dir, model, provider string) (string, error)

// deepSearchTimeout bounds the total time a deep search can run. This prevents
// a misbehaving search agent from burning tokens forever. The search agent is
// designed to complete in ~2 LLM rounds (search + fetch + synthesise), but
// realistic timings are: ~15s LLM inference × 3 rounds + ~10s searches + ~30s
// parallel fetches (bridged, may queue) = ~85s on a good day. Slow models (Claude
// Opus, o3) or queued bridge requests can push this well past 90s. 180s gives
// comfortable headroom while still preventing runaway sessions.
const deepSearchTimeout = 180 * time.Second

// DeepSearchTool lets any agent delegate a research query to the SearchAgent.
// It creates an ephemeral child session, runs the full search loop, and returns
// the synthesised text as the tool result.
type DeepSearchTool struct {
	Run DeepSearchFunc
	// Cache stores the synthesised answer so an identical question on the same
	// model and provider is served from disk instead of repeating the pipeline's
	// two LLM round trips. nil disables caching entirely.
	Cache *webcache.Cache
}

// deepSearchCacheDate is the answer cache's granularity in the key and in the
// envelope: a deep search costs two LLM round trips, so a same-day answer is
// reused.
const deepSearchCacheDate = "2006-01-02"

// deepSearchNow is the clock the answer cache keys off. A package var so the
// midnight-rollover test can stand on both banks of a day boundary without
// waiting for one.
var deepSearchNow = time.Now

// deepSearchEnvelope is the JSON body stored in the answer cache. It carries
// the researched date next to the synthesis so a hit can say how old it is.
type deepSearchEnvelope struct {
	Answer     string `json:"answer"`
	Researched string `json:"researched"`
}

// deepSearchCachedFooter renders the single trailing line a cached answer
// carries, so a synthesis served from cache is never mistaken for a fresh one.
func deepSearchCachedFooter(researched string) string {
	when := researched
	if d, err := time.Parse(deepSearchCacheDate, researched); err == nil {
		when = d.Format("2 Jan 2006")
	}
	if when == "" {
		return "\n\n_Served from a cached answer; pass fresh=true to research again._"
	}
	return "\n\n_Served from a cached answer researched " + when + "; pass fresh=true to research again._"
}

// deepSearchMetadata identifies a completed search to the agent loop, which
// records the question in the conversation's session memory: a later turn
// should know this research already happened instead of paying for the
// pipeline again. Cached tells a replayed answer from a fresh one.
func deepSearchMetadata(query string, cached bool) map[string]any {
	return map[string]any{"deepSearchQuery": query, "deepSearchCached": cached}
}

// DeepSearchRecord reads that marker back. ok is false for any result the tool
// did not stamp — a pipeline error, an empty answer, another tool's result — so
// the loop remembers only the research this conversation actually paid for.
func DeepSearchRecord(res Result) (query string, cached, ok bool) {
	query, _ = res.Metadata["deepSearchQuery"].(string)
	if query == "" {
		return "", false, false
	}
	cached, _ = res.Metadata["deepSearchCached"].(bool)
	return query, cached, true
}

func (DeepSearchTool) ID() string { return "deep_search" }
func (DeepSearchTool) Description() string {
	return "Delegate a research query to the deep-search pipeline. A single web search of the query verbatim is run, results pooled and ranked, the top pages fetched in parallel, and a synthesised answer with a sources list returned. Use for any question requiring current web information. While the answer cache is enabled, repeating an identical query with the same context returns the stored answer immediately instead of running the pipeline again, so re-asking a question this conversation already researched is cheap and fast; the answer says when it was served from cache and how to research it again."
}
func (DeepSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The research question or topic to investigate"
			},
			"context": {
				"type": "string",
				"description": "Optional: extra context to help the search agent focus (e.g. language, framework, constraints)"
			},
			"fresh": {
				"type": "boolean",
				"description": "Ignore any cached answer and research the question again from scratch — a full pipeline run, only worth it when the cached findings are out of date"
			}
		},
		"required": ["query"]
	}`)
}

func (t DeepSearchTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Query   string `json:"query"`
		Context string `json:"context"`
		Fresh   bool   `json:"fresh"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Query == "" {
		return Result{Output: "query is required"}, nil
	}

	fullQuery := input.Query
	if input.Context != "" {
		fullQuery = input.Query + "\n\nAdditional context: " + input.Context
	}

	// The key carries the model and provider as well as the question: a model id
	// can be served by more than one provider, and answers differ by model, so
	// neither belongs to another's cache entry. `now` is captured once so the key
	// and the envelope's researched date agree.
	now := deepSearchNow()
	researched := now.Format(deepSearchCacheDate)
	answerKey := webcache.Key(webcache.KindAnswer, fullQuery, tctx.Model, tctx.Provider, researched)
	caching := t.Cache != nil && t.Cache.Enabled()

	if caching && !input.Fresh {
		if body, ok := t.Cache.Get(webcache.KindAnswer, answerKey); ok {
			var env deepSearchEnvelope
			if err := json.Unmarshal(body, &env); err != nil {
				// A body we cannot read is a miss, not an error: re-research and
				// overwrite it.
				slog.Warn("deep_search: unreadable cached answer", "err", err)
			} else if env.Answer != "" {
				return Result{
					Title:    input.Query,
					Output:   env.Answer + deepSearchCachedFooter(env.Researched),
					Metadata: deepSearchMetadata(input.Query, true),
				}, nil
			}
		}
	}

	// Bound the search session with its own timeout so it doesn't inherit an
	// unbounded context from the parent loop. A well-structured search completes
	// in ~2 tool-call rounds (~30-60s for LLM + ~5s for search/fetch), so 90s
	// is generous.
	searchCtx, cancel := context.WithTimeout(ctx, deepSearchTimeout)
	defer cancel()

	answer, err := t.Run(searchCtx, fullQuery, tctx.SessionDir, tctx.Model, tctx.Provider)
	if err != nil {
		return Result{Output: fmt.Sprintf("Search agent error: %s", err)}, nil
	}

	// An error is never cached, and neither is an empty answer. A cache failure
	// is logged and swallowed: the tool's job is the answer, not the cache.
	if caching && answer != "" {
		if body, err := json.Marshal(deepSearchEnvelope{Answer: answer, Researched: researched}); err != nil {
			slog.Warn("deep_search: marshal answer for cache", "err", err)
		} else if err := t.Cache.Put(webcache.KindAnswer, answerKey, body); err != nil {
			slog.Warn("deep_search: cache answer", "err", err)
		}
	}

	result := Result{Title: input.Query, Output: answer}
	if answer != "" {
		// A search that produced nothing is not research the next turn should
		// be told about, so it carries no marker.
		result.Metadata = deepSearchMetadata(input.Query, false)
	}
	return result, nil
}
