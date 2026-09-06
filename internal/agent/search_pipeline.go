package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/search"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Deep-research pipeline tuning. searchResultCount / searchMaxCandidates /
// searchSynthMaxTokens are internal plumbing; fetch-top-K and per-page chars are
// user-configurable (settings screen → searchTuning).
const (
	searchResultCount    = 20   // results requested for the query from the bridge
	searchMaxCandidates  = 24   // cap on unique results fed to the ranker
	searchSynthMaxTokens = 4096 // output budget for the final synthesis call
)

// searchTuning holds the user-configurable knobs for one deep-research run.
type searchTuning struct {
	fetchTopK int // number of ranked URLs fetched in full
	pageChars int // per-page character cap fed into synthesis
}

// tuning resolves the current knobs from SearchParams (fresh from the config DB,
// so changes apply without a restart), falling back to defaults when unset.
func (lr *LoopRunner) tuning() searchTuning {
	t := searchTuning{
		fetchTopK: session.DefaultSearchFetchTopK,
		pageChars: session.DefaultSearchPageChars,
	}
	if lr.SearchParams != nil {
		c := lr.SearchParams()
		// GetSearchConfig already clamps, but guard here too for non-DB callers.
		if c.FetchTopK > 0 {
			t.fetchTopK = c.FetchTopK
		}
		if c.PageChars > 0 {
			t.pageChars = c.PageChars
		}
	}
	return t
}

// RunSearchSession runs the deep-research pipeline for a query and returns the
// synthesised markdown answer. It is called by tool.DeepSearchTool via the
// tool.DeepSearchFunc contract.
//
// Unlike a free-form agent loop, this is a deterministic 4-stage pipeline —
// search → rank → fetch → synthesise — with exactly two LLM calls. The query is
// searched verbatim: the calling agent already phrases a focused research
// question, so expanding it into sub-queries cost a blocking LLM round trip up
// front for little gain. Breadth now comes from asking the bridge for more
// results on the one query instead.
// It never depends on the (session-inherited) model emitting parallel tool
// calls or converging on its own: the searches and fetches are orchestrated in
// parallel on the Go side, and the final stage is always a plain synthesis, so
// the result can never come back empty the way the old tool-calling loop did on
// weaker models. The model is still inherited from the caller (dir is accepted
// for signature compatibility but unused — the pipeline needs no working dir).
func (lr *LoopRunner) RunSearchSession(ctx context.Context, query, dir, model string) (string, error) {
	_ = dir
	if lr.SearchBridge == nil {
		return "", fmt.Errorf("search bridge is not available")
	}

	p, model := lr.resolveSearchProvider(model)
	if p == nil {
		return "", fmt.Errorf("no LLM provider available for deep search")
	}

	tune := lr.tuning()
	today := time.Now().Format("Monday, 2 January 2006")
	slog.Info("deep search: start", "query", truncateText(query, 80), "model", model,
		"fetchTopK", tune.fetchTopK, "pageChars", tune.pageChars)

	// Stage 1 — search the query verbatim and collect unique results.
	candidates := lr.searchGather(ctx, query)
	if len(candidates) == 0 {
		return "No web results were found for this query. The search provider may be rate-limited or blocking automated access — try again shortly or rephrase the query.", nil
	}
	slog.Info("deep search: candidates pooled", "count", len(candidates))

	// Stage 2 — let the model rank the results and pick the best URLs.
	picks := lr.searchRank(ctx, p, model, query, candidates, tune)
	slog.Info("deep search: ranked picks", "count", len(picks))

	// Stage 3 — fetch the chosen pages in parallel.
	pages := lr.searchFetch(ctx, picks)
	slog.Info("deep search: pages fetched", "count", len(pages))

	// Stage 4 — synthesise a final answer from whatever we managed to fetch.
	answer, err := lr.searchSynthesize(ctx, p, model, query, today, candidates, pages, tune)
	if err != nil {
		return "", fmt.Errorf("deep search synthesis: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "The deep search could not synthesise an answer from the fetched pages. Try rephrasing the query.", nil
	}

	// Guarantee a Sources section even if the model omitted one.
	return appendSourcesIfMissing(answer, picks, pages), nil
}

// resolveSearchProvider picks the provider for the (inherited) model, mirroring
// resolveRunModel: prefer the model's provider, fall back to the registry
// default and then the immutable startup default. When model is empty it adopts
// the default provider's first model so the pipeline always has something to run.
func (lr *LoopRunner) resolveSearchProvider(model string) (provider.Provider, string) {
	if model == "" {
		dp := lr.Registry.Default()
		if dp == nil {
			dp = lr.DefaultProvider
		}
		if dp != nil {
			if models := dp.Models(); len(models) > 0 {
				model = models[0].ID
			}
		}
	}
	var p provider.Provider
	if model != "" {
		p = lr.Registry.ResolveProvider(model)
	}
	if p == nil {
		if dp := lr.Registry.Default(); dp != nil {
			p = dp
		} else {
			p = lr.DefaultProvider
		}
	}
	return p, model
}

// searchGather runs the query verbatim against the bridge and returns the
// unique, non-junk results in rank order, capped at searchMaxCandidates.
func (lr *LoopRunner) searchGather(ctx context.Context, query string) []search.SearchResult {
	items, err := lr.SearchBridge.Search(ctx, query, searchResultCount)
	if err != nil {
		slog.Warn("deep search: search failed", "query", query, "err", err)
		return nil
	}

	seen := make(map[string]bool)
	var pooled []search.SearchResult
	for _, r := range items {
		key := urlKey(r.URL)
		if key == "" || seen[key] || isJunkResultHost(r.URL) {
			continue
		}
		seen[key] = true
		pooled = append(pooled, r)
		if len(pooled) >= searchMaxCandidates {
			break
		}
	}
	return pooled
}

// searchRank presents the numbered pooled candidates to the model and asks it
// to pick the best ones by number. Choosing by number (rather than by URL) is
// robust for weak local models, which reliably echo a small integer but often
// mangle or hallucinate a full URL. A URL is still accepted if the model writes
// one, but only when it matches a real candidate. On anything unusable it falls
// back to the top search results so the pipeline always makes progress.
func (lr *LoopRunner) searchRank(ctx context.Context, p provider.Provider, model, query string, candidates []search.SearchResult, tune searchTuning) []search.SearchResult {
	var sb strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&sb, "%d. %s — %s\n   %s\n", i+1, c.Title, domainOf(c.URL), truncateText(oneLine(c.Snippet), 160))
	}
	system := "You are a research source selector. From a numbered list of search results you pick the few most relevant, authoritative ones worth reading in full."
	user := fmt.Sprintf(`Research question:
%s

Candidate sources:
%s
Pick the %d best sources to read in full to answer the question. Prefer official documentation, primary sources, and authoritative sites over SEO aggregators. Output only their numbers, one per line, most relevant first — just the numbers, nothing else.`, query, sb.String(), tune.fetchTopK)

	out, err := oneShotLLM(ctx, p, model, system, user, 0)
	if err != nil {
		slog.Warn("deep search: rank failed, using top results", "err", err)
		return topResults(candidates, tune.fetchTopK)
	}

	byURL := make(map[string]search.SearchResult, len(candidates))
	for _, c := range candidates {
		byURL[urlKey(c.URL)] = c
	}
	var picks []search.SearchResult
	seen := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		c, ok := pickFromLine(line, candidates, byURL)
		if !ok {
			continue
		}
		key := urlKey(c.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		picks = append(picks, c)
		if len(picks) >= tune.fetchTopK {
			break
		}
	}
	if len(picks) == 0 {
		slog.Warn("deep search: ranker returned nothing usable, using top results")
		return topResults(candidates, tune.fetchTopK)
	}
	return picks
}

// pickFromLine resolves one line of ranker output to a candidate. It prefers an
// explicit URL (accepted only if it matches a candidate — rejecting hallucinated
// URLs); otherwise it reads the line's first integer as a 1-based candidate index.
func pickFromLine(line string, candidates []search.SearchResult, byURL map[string]search.SearchResult) (search.SearchResult, bool) {
	if u := extractFirstURL(line); u != "" {
		if c, ok := byURL[urlKey(u)]; ok {
			return c, true
		}
		return search.SearchResult{}, false
	}
	if n, ok := firstInt(line); ok && n >= 1 && n <= len(candidates) {
		return candidates[n-1], true
	}
	return search.SearchResult{}, false
}

// firstInt returns the first run of decimal digits in s as an int.
func firstInt(s string) (int, bool) {
	i := 0
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	if i >= len(s) {
		return 0, false
	}
	n := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		n = n*10 + int(s[i]-'0')
		i++
	}
	return n, true
}

// isJunkResultHost reports whether a result URL is a search-engine or bare
// portal homepage that carries no article content worth fetching.
func isJunkResultHost(raw string) bool {
	h := strings.ToLower(domainOf(raw))
	if strings.HasPrefix(h, "google.") || strings.Contains(h, ".google.") {
		return true
	}
	switch h {
	case "bing.com", "duckduckgo.com", "search.yahoo.com":
		return true
	}
	return false
}

// searchFetch fetches the chosen pages concurrently, dropping any that fail.
func (lr *LoopRunner) searchFetch(ctx context.Context, picks []search.SearchResult) []search.PageContent {
	pages := make([]search.PageContent, len(picks))
	ok := make([]bool, len(picks))
	var wg sync.WaitGroup
	for i, c := range picks {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()
			// A panic here runs on this goroutine, where nothing above it can
			// recover, and an unrecovered panic in any goroutine takes the whole
			// process down — so one malformed page would kill every session the
			// server is serving, not just this search. A panicking fetch is
			// dropped exactly like a failing one: ok[idx] stays false and the
			// caller synthesises from the remaining pages, or from snippets if
			// none survived. The stack is logged so the bug stays diagnosable.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("deep search: fetch panicked",
						"url", u, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			page, err := lr.SearchBridge.FetchPage(ctx, u)
			if err != nil {
				slog.Warn("deep search: fetch failed", "url", u, "err", err)
				return
			}
			pages[idx] = page
			ok[idx] = true
		}(i, c.URL)
	}
	wg.Wait()

	var out []search.PageContent
	for i := range pages {
		if ok[i] {
			out = append(out, pages[i])
		}
	}
	return out
}

// searchSynthesize writes the final markdown answer from the fetched pages. If
// every fetch failed it falls back to synthesising from the search snippets, so
// the pipeline still returns something useful rather than an empty result.
func (lr *LoopRunner) searchSynthesize(ctx context.Context, p provider.Provider, model, query, today string, candidates []search.SearchResult, pages []search.PageContent, tune searchTuning) (string, error) {
	var sb strings.Builder
	if len(pages) > 0 {
		for i, pg := range pages {
			body := pg.Text
			if len(body) > tune.pageChars {
				body = body[:tune.pageChars]
			}
			fmt.Fprintf(&sb, "\n--- Source %d: %s (%s) ---\n%s\n", i+1, pg.Title, pg.URL, body)
		}
	} else {
		for i, c := range topResults(candidates, tune.fetchTopK) {
			fmt.Fprintf(&sb, "\n--- Result %d: %s (%s) ---\n%s\n", i+1, c.Title, c.URL, oneLine(c.Snippet))
		}
	}

	system := `You are a deep research agent. Synthesise the provided source material into a single, comprehensive, well-cited markdown answer.
- Start with a clear H1 title, then use H2/H3 sections.
- Be specific and concrete: name exact versions, APIs, and tradeoffs.
- Cite claims inline using the source URLs.
- End with a "## Sources" section listing every URL you used as numbered links. This section is mandatory.
Output only the markdown answer — no preamble.`
	user := fmt.Sprintf(`Today is %s.

Research question:
%s

Source material:
%s
Write the final answer now as plain markdown.`, today, query, sb.String())

	return oneShotLLM(ctx, p, model, system, user, searchSynthMaxTokens)
}

// oneShotLLM makes a single tool-free LLM call and returns the collected text.
// It falls back to the reasoning stream when the text stream is empty (some
// thinking models emit their whole answer as reasoning). maxTokens of 0 leaves
// the provider default in place.
func oneShotLLM(ctx context.Context, p provider.Provider, model, system, user string, maxTokens int) (string, error) {
	userJSON, err := json.Marshal(user)
	if err != nil {
		return "", err
	}
	req := provider.StreamRequest{
		Model:     model,
		System:    []string{system},
		Messages:  []provider.ModelMessage{{Role: "user", Content: userJSON}},
		MaxTokens: maxTokens,
	}
	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		return "", err
	}
	var text, reasoning strings.Builder
	var streamErr string
	for evt := range ch {
		switch evt.Type {
		case provider.EventTextDelta:
			text.WriteString(evt.Text)
		case provider.EventReasoning:
			reasoning.WriteString(evt.Text)
		case provider.EventError:
			streamErr = evt.Error
		}
	}
	// A provider can emit useful-looking partial text before the connection or
	// idle watchdog fails. Treat that as a failed call rather than presenting a
	// truncated answer as a successful research result; the caller can then use
	// its fallback or report the failure clearly.
	if streamErr != "" {
		return "", fmt.Errorf("%s", streamErr)
	}
	out := strings.TrimSpace(text.String())
	if out == "" {
		out = strings.TrimSpace(reasoning.String())
	}
	return out, nil
}

// extractFirstURL returns the first http(s) token in s, trimmed of surrounding
// markdown/punctuation, or "" if none is present.
func extractFirstURL(s string) string {
	idx := strings.Index(s, "http")
	if idx < 0 {
		return ""
	}
	fields := strings.FieldsFunc(s[idx:], func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ')' || r == ']' || r == '>' || r == '"' || r == '`' || r == '<'
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimRight(fields[0], ".,;")
}

// urlKey normalises a URL for equality/dedup: trimmed and without a trailing slash.
func urlKey(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// domainOf returns the registrable-ish host (without a leading "www.") for display.
func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// oneLine collapses all runs of whitespace (including newlines) into single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// topResults returns the first n results (or all of them if fewer).
func topResults(items []search.SearchResult, n int) []search.SearchResult {
	if len(items) > n {
		return items[:n]
	}
	return items
}

// appendSourcesIfMissing adds a Sources section built from the fetched pages
// (preferred, they were actually read) or the ranked picks, unless the answer
// already contains one.
func appendSourcesIfMissing(answer string, picks []search.SearchResult, pages []search.PageContent) string {
	if hasSourcesSection(answer) {
		return answer
	}
	seen := make(map[string]bool)
	var sources []sourceEntry
	for _, pg := range pages {
		key := urlKey(pg.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		sources = append(sources, sourceEntry{URL: pg.URL, Title: pg.Title})
	}
	if len(sources) == 0 {
		for _, c := range picks {
			key := urlKey(c.URL)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			sources = append(sources, sourceEntry{URL: c.URL, Title: c.Title})
		}
	}
	if len(sources) == 0 {
		return answer
	}
	return answer + "\n\n## Sources\n\n" + formatSources(sources)
}
