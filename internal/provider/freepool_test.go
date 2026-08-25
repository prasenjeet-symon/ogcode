package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleFreePoolJSON() string {
	return `{
		"version": 1,
		"providers": {
			"cerebras": {
				"collection": "Cerebras",
				"baseURL": "https://api.cerebras.ai/v1",
				"keys": ["csk_ccc"],
				"defaultModel": "llama-3.3-70b"
			},
			"github_models": {
				"collection": "GitHub Models",
				"baseURL": "https://models.inference.ai.azure.com",
				"keys": ["ghp_ddd"],
				"defaultModel": "gpt-4o-mini"
			}
		}
	}`
}

func TestFreePoolFileParsing(t *testing.T) {
	var file freePoolFile
	if err := json.Unmarshal([]byte(sampleFreePoolJSON()), &file); err != nil {
		t.Fatalf("parse free pool JSON: %v", err)
	}
	if file.Version != 1 {
		t.Fatalf("version = %d, want 1", file.Version)
	}
	if len(file.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(file.Providers))
	}
	cerebras, ok := file.Providers["cerebras"]
	if !ok {
		t.Fatal("missing cerebras provider")
	}
	if len(cerebras.Keys) != 1 {
		t.Fatalf("cerebras keys = %d, want 1", len(cerebras.Keys))
	}
}

func TestFreePoolCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "free-keys.json")
	data := []byte(sampleFreePoolJSON())
	if err := writeFreePoolCache(path, data); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	defs, err := readFreePoolCache(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("defs = %d, want 2", len(defs))
	}
}

func TestFreePoolCacheMissingIsNotError(t *testing.T) {
	defs, err := readFreePoolCache(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing cache should not be an error, got %v", err)
	}
	if defs != nil {
		t.Fatalf("missing cache should return nil defs, got %v", defs)
	}
}

func TestPickFreeKey(t *testing.T) {
	if got := pickFreeKey(nil); got != "" {
		t.Fatalf("pickFreeKey(nil) = %q, want empty", got)
	}
	if got := pickFreeKey([]string{"only"}); got != "only" {
		t.Fatalf("pickFreeKey(single) = %q, want only", got)
	}
	// Random pick should always return one of the keys.
	keys := []string{"a", "b", "c", "d"}
	for i := 0; i < 100; i++ {
		got := pickFreeKey(keys)
		if !contains(keys, got) {
			t.Fatalf("pickFreeKey returned %q which is not in the pool", got)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestNewFreePoolProvider(t *testing.T) {
	def := FreeProviderDef{
		Collection:   "Cerebras",
		BaseURL:      "https://api.cerebras.ai/v1",
		Keys:         []string{"csk_test"},
		DefaultModel: "llama-3.3-70b",
	}
	p, err := NewFreePoolProvider(def)
	if err != nil {
		t.Fatalf("NewFreePoolProvider: %v", err)
	}
	if p.ID() != "ogcode-cerebras" {
		t.Fatalf("ID = %q, want ogcode-cerebras", p.ID())
	}
	if p.BaseURL() != def.BaseURL {
		t.Fatalf("BaseURL = %q, want %q", p.BaseURL(), def.BaseURL)
	}
	// All free-pool models group under the shared "ogcode" collection, even
	// though the provider id still derives from the pool entry's collection.
	if p.collection != freePoolCollection {
		t.Fatalf("collection = %q, want %q", p.collection, freePoolCollection)
	}
	// Keys are masked by FreeProviderList but the provider itself has the key.
}

func TestNewFreePoolProviderNoKeys(t *testing.T) {
	def := FreeProviderDef{
		Collection: "Cerebras",
		BaseURL:    "https://api.cerebras.ai/v1",
		Keys:       nil,
	}
	_, err := NewFreePoolProvider(def)
	if err != errFreePoolNoKeys {
		t.Fatalf("expected errFreePoolNoKeys, got %v", err)
	}
}

func TestFreeProviderListMasksKeys(t *testing.T) {
	p := freePoolInstance()
	p.mu.Lock()
	p.defs = map[string]FreeProviderDef{
		"cerebras": {Collection: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", Keys: []string{"secret"}, DefaultModel: "llama-3.3-70b"},
	}
	p.loaded = true
	// Use a recent time so the pool is considered fresh.
	p.fetchedAt = time.Now()
	p.mu.Unlock()

	list := FreeProviderList()
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if list[0].Keys != nil {
		t.Fatalf("FreeProviderList must mask keys, got %v", list[0].Keys)
	}
}

func TestFreeProviderIDsPriority(t *testing.T) {
	defs := map[string]FreeProviderDef{
		"nvidia":     {},
		"cerebras":   {},
		"openrouter": {},
		"sambanova":  {},
	}
	ids := FreeProviderIDs(defs)
	// OpenRouter must come first (it's the recommended default).
	if len(ids) < 4 || ids[0] != "ogcode-openrouter" {
		t.Fatalf("FreeProviderIDs = %v, want ogcode-openrouter first", ids)
	}
}

func TestCurateFreePoolModelsOpenRouter(t *testing.T) {
	fetched := []ModelInfo{
		{ID: "qwen/qwen3-coder:free"},
		{ID: "anthropic/claude-opus-4"}, // paid — must be dropped
		{ID: "meta-llama/llama-3.3-70b-instruct:free"},
		{ID: "openai/gpt-4o"}, // paid — must be dropped
	}
	out := curateFreePoolModels(fetched, "https://openrouter.ai/api/v1")
	if len(out) != 2 {
		t.Fatalf("OpenRouter curation should keep only :free models, got %d (%v)", len(out), out)
	}
	for _, m := range out {
		if !strings.HasSuffix(m.ID, ":free") {
			t.Fatalf("non-free model survived OpenRouter curation: %q", m.ID)
		}
		if !m.ActiveByDefault {
			t.Fatalf("free model %q must be enabled by default", m.ID)
		}
	}
}

func TestCurateFreePoolModelsNonOpenRouterEnablesAll(t *testing.T) {
	// Cerebras (and other free-tier providers) are already free-only, so nothing
	// is filtered — but every model must be enabled so the picker isn't empty.
	fetched := []ModelInfo{{ID: "llama-3.3-70b"}, {ID: "llama-3.1-8b-instant"}}
	out := curateFreePoolModels(fetched, "https://api.cerebras.ai/v1")
	if len(out) != 2 {
		t.Fatalf("non-OpenRouter curation must keep all models, got %d", len(out))
	}
	for _, m := range out {
		if !m.ActiveByDefault {
			t.Fatalf("model %q must be enabled by default", m.ID)
		}
	}
}

func TestLoadFreePoolFromNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleFreePoolJSON()))
	}))
	defer srv.Close()

	t.Setenv("OGCODE_FREE_KEYS_URL", srv.URL)
	t.Setenv("OGCODE_EMBED_MODEL_DIR", t.TempDir())

	defs, err := loadFreePool(t.Context())
	if err != nil {
		t.Fatalf("loadFreePool: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("defs = %d, want 2", len(defs))
	}
}

func TestLoadFreePoolFallsBackToCache(t *testing.T) {
	// Point the URL at an unreachable server; the cache should be used.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("OGCODE_FREE_KEYS_URL", srv.URL)
	t.Setenv("OGCODE_EMBED_MODEL_DIR", dir)

	// Seed the cache.
	if err := writeFreePoolCache(filepath.Join(dir, "free-keys.json"), []byte(sampleFreePoolJSON())); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	defs, err := loadFreePool(t.Context())
	if err != nil {
		t.Fatalf("loadFreePool: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("defs = %d, want 2", len(defs))
	}
}

func TestLoadFreePoolNoNetworkNoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("OGCODE_FREE_KEYS_URL", srv.URL)
	t.Setenv("OGCODE_EMBED_MODEL_DIR", dir)

	defs, err := loadFreePool(t.Context())
	if err != nil {
		t.Fatalf("loadFreePool: %v", err)
	}
	if defs != nil {
		t.Fatalf("defs = %v, want nil when no network and no cache", defs)
	}
}

// TestFreePoolCachePermissions verifies the cache file is written with
// restrictive permissions (0600) so pool keys are not world-readable.
func TestFreePoolCachePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "free-keys.json")
	if err := writeFreePoolCache(path, []byte(sampleFreePoolJSON())); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache perms = %o, want 600", info.Mode().Perm())
	}
}
