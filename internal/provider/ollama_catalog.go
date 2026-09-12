package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// OllamaCloudCatalogURL lists the models hosted on Ollama Cloud.
//
// This endpoint is UNDOCUMENTED: it is the same /api/tags path a local Ollama
// instance serves for its own models, served by ollama.com for the hosted
// catalog. It is public and needs no authentication. Because it is
// undocumented, a failure here is normal operating condition, never an error
// worth surfacing — callers must fall back to the static lists.
const OllamaCloudCatalogURL = "https://ollama.com/api/tags"

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

// cloudModelID converts a catalog name into the id a local or proxied instance
// resolves to the hosted model.
func cloudModelID(name string) string {
	if !strings.Contains(name, ":") {
		return name + ollamaCloudTag
	}
	if strings.HasSuffix(name, ollamaCloudSuffix) || strings.HasSuffix(name, ollamaCloudTag) {
		return name
	}
	return name + ollamaCloudSuffix
}

// ollamaCatalogTimeout bounds the fetch. The catalog is a nice-to-have on top
// of the real model list, so it must never hold up provider construction.
const ollamaCatalogTimeout = 5 * time.Second

// ollamaShowTimeout bounds each /api/show lookup. The context window is
// nice-to-have metadata, so the whole lookup pass must stay well under the
// catalog timeout: entries are visited in parallel with a fan-out of
// ollamaShowConcurrency, and an entry that answers slowly or not at all simply
// carries no window.
const (
	ollamaShowTimeout     = 3 * time.Second
	ollamaShowConcurrency = 8
	ollamaShowMaxLookups  = 40
)

// ollamaCatalogDefaultActive is how many catalog models to enable when the
// instance has nothing pulled — enough to be usable, few enough not to flood
// the picker with 100GB+ models.
const ollamaCatalogDefaultActive = 3

// ollamaCatalogCollection groups these models in the UI, separating "exists in
// the cloud" from "pulled on this instance".
const ollamaCatalogCollection = "Ollama Cloud"

type ollamaCatalogEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type ollamaCatalogResponse struct {
	Models []ollamaCatalogEntry `json:"models"`
}

// ollamaCatalogURL returns the catalog endpoint, honouring OLLAMA_CATALOG_URL
// for testing and for mirrors.
func ollamaCatalogURL() string {
	if u := strings.TrimSpace(os.Getenv("OLLAMA_CATALOG_URL")); u != "" {
		return u
	}
	return OllamaCloudCatalogURL
}

// ollamaShowURL returns the /api/show endpoint, honouring OLLAMA_SHOW_URL for
// testing. Deriving it from OLLAMA_CATALOG_URL alone would misroute /api/show
// to a fake /api/tags mirror, so the two endpoints are overridden separately;
// when only the catalog URL is overridden the real ollama.com /api/show is
// still correct for a mirror that serves catalog data verbatim.
func ollamaShowURL() string {
	if u := strings.TrimSpace(os.Getenv("OLLAMA_SHOW_URL")); u != "" {
		return u
	}
	return ollamaCloudShowURL
}

// OllamaCloudShowURL describes one hosted model: capabilities, details, and a
// model_info map keyed by "<architecture>.<field>" — architecture is the
// family name (glm5_next, deepseek4, minimax-m3 …), so the context length key
// differs per model and is found by suffix match.
const ollamaCloudShowURL = "https://ollama.com/api/show"

type ollamaShowResponse struct {
	ModelInfo map[string]any `json:"model_info"`
	Error     string         `json:"error"`
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

// ollamaShowContextWindow queries the catalog host's /api/show for one model
// and returns its context length, or 0 when the lookup fails for any reason —
// including "model not found", which arrives as a 200 with an error field.
func ollamaShowContextWindow(ctx context.Context, client *http.Client, model string) int {
	ctx, cancel := context.WithTimeout(ctx, ollamaShowTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaShowURL(), bytes.NewReader(payload))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var parsed ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0
	}
	return ollamaContextWindowFromShow(&parsed)
}

// ollamaCatalogShowNames returns the catalog-side name to look up for m: the
// catalog lists the hosted model by its own name, which is what /api/show
// knows — the "-cloud"/":cloud" suffix in the model id is only the routing
// marker a local or proxied instance needs.
func ollamaCatalogShowName(m ModelInfo) string {
	name := strings.TrimSuffix(m.ID, ollamaCloudSuffix)
	name = strings.TrimSuffix(name, ollamaCloudTag)
	if !strings.Contains(name, ":") {
		// A bare id means the catalog name had no tag either, so stripping the
		// suffix removed nothing — the name is the id itself.
		return name
	}
	return name
}

// ollamaShowContextWindows fills in ContextWindow for the models the catalog
// host can describe. Failures are silent: an entry the host cannot describe
// simply keeps a window of 0 (unknown). The pass is bounded — per-request
// timeout, bounded concurrency, and a cap on lookups per fetch — so a slow or
// unreachable /api/show can never hold up provider construction.
func ollamaShowContextWindows(ctx context.Context, models []ModelInfo) {
	if len(models) == 0 {
		return
	}
	// Every catalog name is looked up once even when the catalog lists both
	// the bare and the tagged form of the same hosted model.
	names := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		n := ollamaCatalogShowName(m)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) > ollamaShowMaxLookups {
		names = names[:ollamaShowMaxLookups]
	}

	windows := make([]int, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, ollamaShowConcurrency)
	client := &http.Client{Timeout: ollamaShowTimeout}
	for i, n := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			windows[i] = ollamaShowContextWindow(ctx, client, name)
		}(i, n)
	}
	wg.Wait()

	byName := make(map[string]int, len(names))
	for i, n := range names {
		byName[n] = windows[i]
	}
	for i := range models {
		if w := byName[ollamaCatalogShowName(models[i])]; w > 0 {
			models[i].ContextWindow = w
		}
	}
}

// ollamaCatalogEnabled reports whether the cloud catalog should be merged into
// the model list. Set OLLAMA_CLOUD_CATALOG=false to keep the picker limited to
// models actually pulled on the instance.
func ollamaCatalogEnabled() bool {
	v := strings.TrimSpace(os.Getenv("OLLAMA_CLOUD_CATALOG"))
	return !strings.EqualFold(v, "false") && v != "0"
}

// FetchOllamaCloudCatalog returns the models hosted on Ollama Cloud, named for
// the endpoint they will be requested through: bare names when baseURL is
// ollama.com itself, "-cloud"-suffixed when going through a local or proxied
// instance.
//
// Cloud models do not need to be pulled — a signed-in instance resolves them
// remotely on first use — so every entry is immediately usable. That is the
// difference from a locally-listed model, which must exist on disk.
//
// The result is sorted smallest-first (entries with no size reported sort
// last), so a caller that needs to pick a cheap default can take the head of
// the list without carrying size data of its own.
func FetchOllamaCloudCatalog(ctx context.Context, baseURL string) ([]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, ollamaCatalogTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaCatalogURL(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama catalog: status %d", resp.StatusCode)
	}

	var parsed ollamaCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	entries := make([]ollamaCatalogEntry, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		if strings.TrimSpace(m.Name) != "" {
			entries = append(entries, m)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].Size, entries[j].Size
		// Some catalog entries report size 0 (incomplete metadata). Sort them
		// last rather than letting them masquerade as the cheapest option.
		if (a == 0) != (b == 0) {
			return b == 0
		}
		return a < b
	})

	// A base URL that already points at ollama.com speaks the catalog's own
	// naming; anything else is a local or proxied instance that needs the
	// suffix to route the request onward.
	direct := isCloudURL(baseURL)

	out := make([]ModelInfo, 0, len(entries))
	for _, m := range entries {
		name := strings.TrimSpace(m.Name)
		id := name
		if !direct {
			id = cloudModelID(name)
		}
		out = append(out, ModelInfo{
			ID:              id,
			Name:            name,
			ProviderID:      "ollama",
			ActiveByDefault: false,
			Collection:      ollamaCatalogCollection,
		})
	}

	// The /api/tags catalog carries no context data; each model's real window
	// comes from the host's /api/show. Without it every cloud model would
	// report a window of 0 and the compaction threshold would fall back to a
	// fixed 128k cap — far past what some of these models accept, so an
	// overflow surfaced as repeated reactive compactions instead of one
	// proactive one ahead of time. Failures here are silent (window stays 0).
	ollamaShowContextWindows(ctx, out)
	return out, nil
}

// mergeOllamaModels combines the models an instance actually reports with the
// cloud catalog. Local entries win on conflict: a pulled model is proof it
// exists on that instance, and it carries whatever metadata the instance
// reported.
func mergeOllamaModels(local, catalog []ModelInfo) []ModelInfo {
	seen := make(map[string]struct{}, len(local))
	out := make([]ModelInfo, 0, len(local)+len(catalog))
	for _, m := range local {
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	for _, m := range catalog {
		if _, dup := seen[m.ID]; dup {
			continue
		}
		out = append(out, m)
	}
	return out
}
