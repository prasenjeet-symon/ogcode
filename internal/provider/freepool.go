package provider

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FreeProviderDef describes one OpenAI-compatible free-tier provider sourced
// from the shared community key pool. The pool is a JSON file hosted on a
// public GitHub repo so keys can be rotated centrally without a binary release.
type FreeProviderDef struct {
	Collection   string   `json:"collection"`   // grouping label ("Groq", "Cerebras", …)
	BaseURL      string   `json:"baseURL"`      // OpenAI-compatible API base URL
	Keys         []string `json:"keys"`         // pool of API keys (round-robin / random)
	DefaultModel string   `json:"defaultModel"` // suggested default model ID
}

// freePoolFile is the on-disk JSON schema fetched from the GitHub repo.
type freePoolFile struct {
	Version   int                       `json:"version"`
	Providers map[string]FreeProviderDef `json:"providers"`
}

// DefaultFreePoolURL is the public raw GitHub URL for the community key pool.
// It is served from the ogcode repo itself (repo-root keys.json on the default
// branch) so the pool lives alongside the code. It can be overridden via the
// OGCODE_FREE_KEYS_URL env var so forks or self-hosters can point at their own
// key pool (and so tests can point at a local server).
const DefaultFreePoolURL = "https://raw.githubusercontent.com/prasenjeet-symon/ogcode/main/keys.json"

// freePoolTTL is how long the cached key pool is considered fresh before a
// background refresh is attempted. The cache is always used for instant
// startup; the refresh is best-effort and never blocks.
const freePoolTTL = 24 * time.Hour

// freePoolTimeout caps the network fetch so a slow or unreachable repo never
// blocks server startup.
const freePoolTimeout = 8 * time.Second

// FreePoolTimeout is the exported form for callers outside the provider package.
const FreePoolTimeout = freePoolTimeout

// freePool holds the loaded key pool and manages its refresh lifecycle.
// It is a process-wide singleton fetched lazily so the first call (server
// startup) loads the pool and subsequent calls reuse it.
type freePool struct {
	mu       sync.RWMutex
	loaded   bool
	defs     map[string]FreeProviderDef
	fetchedAt time.Time
}

var (
	freePoolOnce sync.Once
	pool         *freePool
)

func freePoolInstance() *freePool {
	freePoolOnce.Do(func() {
		pool = &freePool{}
	})
	return pool
}

// freePoolCacheDir returns the directory for the cached key pool JSON. It
// honours the OGCODE_EMBED_MODEL_DIR override (shared cache root) and falls
// back to ~/.ogcode/.
func freePoolCachePath() string {
	dir := os.Getenv("OGCODE_EMBED_MODEL_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		dir = filepath.Join(home, ".ogcode")
	}
	return filepath.Join(dir, "free-keys.json")
}

// freePoolURL resolves the effective fetch URL (env override > default).
func freePoolURL() string {
	if u := os.Getenv("OGCODE_FREE_KEYS_URL"); u != "" {
		return u
	}
	return DefaultFreePoolURL
}

// FetchFreePool loads the community free-tier key pool. It is safe to call
// repeatedly — the first call fetches (or reads the cache); subsequent calls
// return the in-memory copy. The pool is refreshed in the background after
// the cache TTL expires, but a stale cache is always returned immediately so
// startup is never blocked on the network.
//
// Returns nil when neither a fetch nor a cache is available (graceful
// degradation: the caller treats this as "no free providers").
func FetchFreePool(ctx context.Context) (map[string]FreeProviderDef, error) {
	p := freePoolInstance()

	// Fast path: return the in-memory copy if it's still fresh.
	p.mu.RLock()
	if p.loaded && time.Since(p.fetchedAt) < freePoolTTL {
		defs := p.defs
		p.mu.RUnlock()
		return defs, nil
	}
	p.mu.RUnlock()

	// Slow path: load from cache or network. Use a write lock so only one
	// goroutine performs the load; others wait and reuse the result.
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring the write lock (another goroutine may have
	// loaded while we waited).
	if p.loaded && time.Since(p.fetchedAt) < freePoolTTL {
		return p.defs, nil
	}

	defs, err := loadFreePool(ctx)
	if err != nil {
		// If we already have a stale in-memory copy, keep using it rather than
		// wiping out the providers on a transient network error.
		if p.loaded && len(p.defs) > 0 {
			slog.Warn("free pool: refresh failed, keeping stale copy", "err", err)
			return p.defs, nil
		}
		return nil, err
	}

	p.defs = defs
	p.loaded = true
	p.fetchedAt = time.Now()
	return defs, nil
}

// loadFreePool fetches the key pool from the network, falling back to the
// on-disk cache when the fetch fails. If the network fetch succeeds the cache
// is (re)written so subsequent offline launches still work.
func loadFreePool(ctx context.Context) (map[string]FreeProviderDef, error) {
	url := freePoolURL()
	cachePath := freePoolCachePath()

	// Try the network first (with a bounded timeout).
	fetchCtx, cancel := context.WithTimeout(ctx, freePoolTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, "GET", url, nil)
	if err != nil {
		return readFreePoolCache(cachePath)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ogcode")

	client := &http.Client{Timeout: freePoolTimeout}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("free pool: network fetch failed, falling back to cache", "url", url, "err", err)
		return readFreePoolCache(cachePath)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("free pool: non-200 response, falling back to cache", "url", url, "status", resp.StatusCode)
		return readFreePoolCache(cachePath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return readFreePoolCache(cachePath)
	}

	var file freePoolFile
	if err := json.Unmarshal(body, &file); err != nil {
		slog.Warn("free pool: invalid JSON, falling back to cache", "err", err)
		return readFreePoolCache(cachePath)
	}
	if len(file.Providers) == 0 {
		slog.Warn("free pool: empty providers list, falling back to cache")
		return readFreePoolCache(cachePath)
	}

	// Persist the cache so offline launches work.
	if err := writeFreePoolCache(cachePath, body); err != nil {
		slog.Warn("free pool: failed to write cache (non-fatal)", "err", err)
	}

	slog.Info("free pool: loaded from network", "providers", len(file.Providers), "url", url)
	return file.Providers, nil
}

// readFreePoolCache reads the on-disk cached key pool. Returns nil (no error)
// when the cache does not exist — this is a normal cold-start-without-network
// state, not an error condition.
func readFreePoolCache(path string) (map[string]FreeProviderDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file freePoolFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	slog.Info("free pool: loaded from cache", "providers", len(file.Providers), "path", path)
	return file.Providers, nil
}

// writeFreePoolCache atomically writes the raw pool JSON to the cache path.
func writeFreePoolCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pickFreeKey selects a key from the pool. Uses a random pick for simple load
// balancing across the pool. Returns "" when the pool is empty.
func pickFreeKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	if len(keys) == 1 {
		return keys[0]
	}
	return keys[rand.Intn(len(keys))]
}

// freePoolCollection is the single UI grouping label applied to every model
// served from the community free-tier pool, regardless of the upstream provider
// (Groq, OpenRouter, …). Grouping them all under one dedicated "ogcode"
// collection keeps the free models visually separate from — and non-conflicting
// with — a user's own configured providers (OpenAI/Anthropic/OpenRouter/Ollama/
// Groq/…).
const freePoolCollection = "ogcode"

// NewFreePoolProvider creates an OpenAI-compatible Provider instance for a
// free-tier entry from the key pool. The provider ID is keyed by the pool's
// collection (e.g. "ogcode-groq") so multiple free providers coexist in the
// registry as separately selectable instances — but every model they serve is
// tagged with the shared freePoolCollection ("ogcode") label so they all group
// together in the UI, apart from the user's own providers.
func NewFreePoolProvider(def FreeProviderDef) (*OpenAIProvider, error) {
	key := pickFreeKey(def.Keys)
	if key == "" {
		return nil, errFreePoolNoKeys
	}
	id := "ogcode-" + strings.ToLower(def.Collection)
	model := def.DefaultModel
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}
	return &OpenAIProvider{
		id:         id,
		apiKey:     key,
		model:      model,
		baseURL:    def.BaseURL,
		collection: freePoolCollection,
		freePool:   true,
	}, nil
}

// FreeProviderIDs returns the registry IDs for all free-pool providers in a
// stable priority order (Groq first — the recommended default free provider).
func FreeProviderIDs(defs map[string]FreeProviderDef) []string {
	// Stable priority: OpenRouter, Groq, Cerebras, SambaNova, GitHub Models,
	// NVIDIA, then any others alphabetically.
	priority := []string{"openrouter", "groq", "cerebras", "sambanova", "github_models", "nvidia"}
	seen := make(map[string]bool)
	var out []string
	for _, k := range priority {
		if _, ok := defs[k]; ok {
			out = append(out, "ogcode-"+k)
			seen[k] = true
		}
	}
	for k := range defs {
		if !seen[k] {
			out = append(out, "ogcode-"+k)
		}
	}
	return out
}

// ResetFreePoolForTest clears the singleton free pool state. Test-only — used
// to isolate server/provider tests from the global pool so they don't pick up
// providers loaded by the freepool unit tests in the same process.
func ResetFreePoolForTest() {
	p := freePoolInstance()
	p.mu.Lock()
	p.defs = nil
	p.loaded = false
	p.fetchedAt = time.Time{}
	p.mu.Unlock()
}
var errFreePoolNoKeys = freePoolError("free pool entry has no keys")

type freePoolError string

func (e freePoolError) Error() string { return string(e) }

// HasFreeProviders reports whether the free pool is available (loaded and
// non-empty). Used by the onboarding gate to decide whether to skip the
// credential wizard.
func HasFreeProviders() bool {
	p := freePoolInstance()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.loaded && len(p.defs) > 0
}

// FreeProviderList returns the active free-pool provider collection names in
// priority order, for the UI. Empty when the pool is not available.
func FreeProviderList() []FreeProviderDef {
	p := freePoolInstance()
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.defs) == 0 {
		return nil
	}
	ids := FreeProviderIDs(p.defs)
	out := make([]FreeProviderDef, 0, len(ids))
	for _, id := range ids {
		id := id
		// Strip the "ogcode-" prefix to get the key.
		k := strings.TrimPrefix(id, "ogcode-")
		if def, ok := p.defs[k]; ok {
			// Mask the keys — the UI only needs to know the provider exists.
			def.Keys = nil
			out = append(out, def)
		}
	}
	return out
}