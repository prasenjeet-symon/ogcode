package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
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
