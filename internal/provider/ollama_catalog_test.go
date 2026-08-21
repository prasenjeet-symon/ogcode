package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fakeCatalog = `{"models":[
  {"name":"big:400b","size":400000000000},
  {"name":"nosize","size":0},
  {"name":"small:20b","size":13800000000},
  {"name":"mid:120b","size":65300000000}
]}`

func catalogServer(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_CATALOG_URL", srv.URL)
}

func TestCloudModelID(t *testing.T) {
	// The wrong form 404s on a live instance, so both cases are pinned here.
	cases := map[string]string{
		"gpt-oss:120b":           "gpt-oss:120b-cloud", // tagged: suffix on the tag
		"minimax-m3":             "minimax-m3:cloud",   // bare: cloud becomes the tag
		"deepseek-v4-flash:0731": "deepseek-v4-flash:0731-cloud",
		"kimi-k2.7-code":         "kimi-k2.7-code:cloud",
		"already:120b-cloud":     "already:120b-cloud", // idempotent
		"already:cloud":          "already:cloud",
	}
	for in, want := range cases {
		if got := cloudModelID(in); got != want {
			t.Errorf("cloudModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchOllamaCloudCatalogNamesForLocalEndpoint(t *testing.T) {
	catalogServer(t, fakeCatalog, http.StatusOK)

	got, err := FetchOllamaCloudCatalog(context.Background(), "http://localhost:8090/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d models, want 4", len(got))
	}
	want := map[string]string{
		"small:20b": "small:20b-cloud",
		"mid:120b":  "mid:120b-cloud",
		"big:400b":  "big:400b-cloud",
		"nosize":    "nosize:cloud", // bare name takes the tag form
	}
	for _, m := range got {
		if want[m.Name] != m.ID {
			t.Errorf("name %q produced id %q, want %q", m.Name, m.ID, want[m.Name])
		}
		if m.ProviderID != "ollama" {
			t.Errorf("id %q: provider %q, want ollama", m.ID, m.ProviderID)
		}
		if m.ActiveByDefault {
			t.Errorf("id %q should not be active by default", m.ID)
		}
	}
}

func TestFetchOllamaCloudCatalogBareNamesForDirectEndpoint(t *testing.T) {
	catalogServer(t, fakeCatalog, http.StatusOK)

	// ollama.com speaks the catalog's own naming — no suffix.
	got, err := FetchOllamaCloudCatalog(context.Background(), "https://ollama.com/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, m := range got {
		if m.ID != m.Name {
			t.Errorf("got id %q for name %q, want them equal", m.ID, m.Name)
		}
	}
}

func TestFetchOllamaCloudCatalogSortsSmallestFirst(t *testing.T) {
	catalogServer(t, fakeCatalog, http.StatusOK)

	got, err := FetchOllamaCloudCatalog(context.Background(), "http://localhost:8090/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"small:20b", "mid:120b", "big:400b", "nosize"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q (order: %v)", i, got[i].Name, w, names(got))
		}
	}
}

func TestFetchOllamaCloudCatalogErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		catalogServer(t, "", http.StatusInternalServerError)
		if _, err := FetchOllamaCloudCatalog(context.Background(), "http://localhost:8090/v1"); err == nil {
			t.Error("expected an error for a non-200 response")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		catalogServer(t, "{not json", http.StatusOK)
		if _, err := FetchOllamaCloudCatalog(context.Background(), "http://localhost:8090/v1"); err == nil {
			t.Error("expected an error for malformed JSON")
		}
	})
}

func TestOllamaCatalogEnabled(t *testing.T) {
	t.Run("on by default", func(t *testing.T) {
		if !ollamaCatalogEnabled() {
			t.Error("expected the catalog to be enabled by default")
		}
	})
	for _, off := range []string{"false", "FALSE", "0"} {
		t.Run("disabled by "+off, func(t *testing.T) {
			t.Setenv("OLLAMA_CLOUD_CATALOG", off)
			if ollamaCatalogEnabled() {
				t.Errorf("OLLAMA_CLOUD_CATALOG=%q should disable the catalog", off)
			}
		})
	}
}

func TestMergeOllamaModelsLocalWins(t *testing.T) {
	local := []ModelInfo{
		{ID: "gpt-oss:120b-cloud", Name: "pulled", ActiveByDefault: true},
	}
	catalog := []ModelInfo{
		{ID: "gpt-oss:120b-cloud", Name: "catalog", ActiveByDefault: false},
		{ID: "gemma4:31b-cloud", Name: "gemma4:31b"},
	}

	got := mergeOllamaModels(local, catalog)
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2 (order: %v)", len(got), names(got))
	}
	if got[0].Name != "pulled" || !got[0].ActiveByDefault {
		t.Errorf("the locally pulled entry should win: got %+v", got[0])
	}
	if got[1].ID != "gemma4:31b-cloud" {
		t.Errorf("got %q, want the catalog-only model appended", got[1].ID)
	}
}

func TestResolveDefaultModel(t *testing.T) {
	list := []ModelInfo{
		{ID: "disabled:1"},
		{ID: "enabled:1", ActiveByDefault: true},
	}

	t.Run("explicit configuration is never overridden", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3", modelExplicit: true}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "qwen3" {
			t.Errorf("got %q, want the explicitly configured qwen3", got)
		}
	})

	t.Run("guess not served falls to first enabled", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "enabled:1" {
			t.Errorf("got %q, want enabled:1", got)
		}
	})

	t.Run("guess that is served is kept", func(t *testing.T) {
		p := &OpenAIProvider{model: "disabled:1"}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "disabled:1" {
			t.Errorf("got %q, want the configured model kept", got)
		}
	})

	t.Run("nothing enabled falls to first model", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel([]ModelInfo{{ID: "only:1"}})
		if got := p.defaultModel(); got != "only:1" {
			t.Errorf("got %q, want only:1", got)
		}
	})

	t.Run("empty list keeps the placeholder", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel(nil)
		if got := p.defaultModel(); got != "qwen3" {
			t.Errorf("got %q, want the placeholder retained", got)
		}
	})
}

func names(list []ModelInfo) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.Name
	}
	return out
}
