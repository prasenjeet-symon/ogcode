package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// A local Ollama instance routes a model to the hosted backend only when the
// name marks it as cloud, and the marker takes two different forms depending on
// whether the catalog name already carries a tag:
//
//	gpt-oss:120b  (tagged)  -> gpt-oss:120b-cloud   suffix on the tag
//	minimax-m3    (bare)    -> minimax-m3:cloud     "cloud" becomes the tag
//
// Using the wrong form returns 404. Both were verified against a live instance.
// An endpoint that IS ollama.com wants the catalog name unchanged.
const (
	ollamaCloudSuffix = "-cloud"
	ollamaCloudTag    = ":cloud"
)

// ollamaShowTimeout bounds each /api/show lookup. The context window is
// nice-to-have metadata, so the whole lookup pass must stay cheap: entries are
// visited in parallel with a fan-out of ollamaShowConcurrency, and an entry
// that answers slowly or not at all simply carries no window.
const (
	ollamaShowTimeout     = 3 * time.Second
	ollamaShowConcurrency = 8
	ollamaShowMaxLookups  = 40
)

type ollamaShowResponse struct {
	ModelInfo map[string]any `json:"model_info"`
	// Capabilities lists what the model can do ("completion", "tools",
	// "thinking", "vision"); Thinking, from Ollama 0.34.3 on, the exact values
	// `think` takes for it. Both are absent from an older or partial answer.
	Capabilities []string        `json:"capabilities"`
	Thinking     *ollamaThinking `json:"thinking"`
	Error        string          `json:"error"`
}

// ollamaThinking is /api/show's thinking metadata: every value `think` takes
// for the model — false, true, or named levels — and the one it uses when none
// is sent. Ollama's cloud rejects a value a model does not list, so these, not
// the catalogue, are what a picker for an Ollama-served model offers.
type ollamaThinking struct {
	Values  []any `json:"values"`
	Default any   `json:"default"`
}

// ollamaThinkLevel maps one `think` value onto ogcode's vocabulary: false is
// off, true is on, a named level keeps its name. A value ogcode has no name for
// is "" and is skipped.
func ollamaThinkLevel(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return EffortOn
		}
		return EffortNone
	case string:
		if t != EffortOn && IsEffort(t) {
			return t
		}
	}
	return ""
}

// ollamaEffortsFromShow reads the effort levels a model takes from its
// /api/show answer. known reports whether the answer settles the question:
//
//   - With thinking metadata, the listed values are the levels, lowest first.
//     A single value — never thinks, or always thinks at one depth — is kept as
//     it is, so the catalogue cannot offer a choice the host does not have.
//   - Without metadata, a model whose capabilities lack "thinking" never thinks
//     ([none]). One that has it is left to the catalogue, except on a local
//     daemon, whose template-based thinking models all take think true/false.
//   - Anything else is unknown, and the catalogue answers.
func ollamaEffortsFromShow(resp *ollamaShowResponse, model string, local bool) (levels []string, def string, known bool) {
	if resp.Thinking != nil {
		for _, v := range resp.Thinking.Values {
			if l := ollamaThinkLevel(v); l != "" && !slices.Contains(levels, l) {
				levels = append(levels, l)
			}
		}
		// "on" belongs only to an on/off model; beside named levels it adds
		// nothing a level does not already say.
		if len(levels) > 2 || (len(levels) == 2 && !slices.Contains(levels, EffortNone)) {
			levels = slices.DeleteFunc(levels, func(l string) bool { return l == EffortOn })
		}
		slices.SortFunc(levels, func(a, b string) int {
			return slices.Index(effortOrder, a) - slices.Index(effortOrder, b)
		})
		if d := ollamaThinkLevel(resp.Thinking.Default); slices.Contains(levels, d) {
			def = d
		}
		return levels, def, len(levels) > 0
	}
	if len(resp.Capabilities) == 0 {
		return nil, "", false
	}
	if !slices.Contains(resp.Capabilities, "thinking") {
		return []string{EffortNone}, EffortNone, true
	}
	if m, ok := LookupCatalogModel(model); ok && len(m.Efforts) > 0 {
		return nil, "", false
	}
	if local {
		return slices.Clone(onOffEfforts), "", true
	}
	return nil, "", false
}

// ollamaContextWindowFromShow extracts the context length from an /api/show
// response. The key is "<family>.context_length", so any key ending in
// ".context_length" wins; an int64 is the JSON shape for a numeric literal.
// Returns 0 when absent (the caller treats 0 as unknown, as everywhere else).
func ollamaContextWindowFromShow(resp *ollamaShowResponse) int {
	for k, v := range resp.ModelInfo {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		if n, ok := v.(float64); ok && n > 0 {
			return int(n)
		}
	}
	return 0
}

// ollamaShow asks showURL to describe one model. It returns nil when the
// lookup fails for any reason — including "model not found", which arrives as
// a 200 with an error field. prepare, when set, adds the endpoint's headers.
func ollamaShow(ctx context.Context, client *http.Client, showURL, model string, prepare func(*http.Request)) *ollamaShowResponse {
	ctx, cancel := context.WithTimeout(ctx, ollamaShowTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, showURL, bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	if prepare != nil {
		prepare(req)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var parsed ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || parsed.Error != "" {
		return nil
	}
	return &parsed
}

// ollamaShowFill describes models through showURL and records what it learns:
// the effort levels each takes and, when withWindow, its context window — which
// is a cloud model's real window, but on a local daemon only the weights'
// maximum, not the num_ctx the runtime will actually allot. The pass is bounded —
// per-request timeout, bounded concurrency, and a cap on lookups per fetch — so a
// slow or unreachable /api/show can never hold up provider construction.
func ollamaShowFill(ctx context.Context, showURL string, models []ModelInfo, nameOf func(ModelInfo) string, prepare func(*http.Request), withWindow, local bool) {
	if len(models) == 0 {
		return
	}
	// Every name is looked up once even when the list carries both the bare
	// and the tagged form of the same hosted model.
	names := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		n := nameOf(m)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) > ollamaShowMaxLookups {
		names = names[:ollamaShowMaxLookups]
	}

	answers := make([]*ollamaShowResponse, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, ollamaShowConcurrency)
	client := &http.Client{Timeout: ollamaShowTimeout}
	for i, n := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			answers[i] = ollamaShow(ctx, client, showURL, name, prepare)
		}(i, n)
	}
	wg.Wait()

	byName := make(map[string]*ollamaShowResponse, len(names))
	for i, n := range names {
		if answers[i] != nil {
			byName[n] = answers[i]
		}
	}
	for i := range models {
		resp := byName[nameOf(models[i])]
		if resp == nil {
			continue
		}
		if w := ollamaContextWindowFromShow(resp); withWindow && w > 0 {
			models[i].ContextWindow = w
		}
		if levels, def, known := ollamaEffortsFromShow(resp, models[i].ID, local); known {
			models[i].Efforts, models[i].DefaultEffort = levels, def
		}
	}
}

// ollamaNativeBase is the root of an Ollama endpoint's native API, which sits
// beside the OpenAI-compatible /v1 the provider is configured with.
func ollamaNativeBase(baseURL string) string {
	return strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
}
