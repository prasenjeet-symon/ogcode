package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// deepSearchTestSchema is the web_cache table from migration
// internal/db/074_web_cache.sql. webcache.Open deliberately does not migrate, so
// a bare sql.DB needs the table laid down the way a migrated database has it.
const deepSearchTestSchema = `
CREATE TABLE IF NOT EXISTS web_cache (
    kind       TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    body       BLOB    NOT NULL,
    bytes      INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (kind, key)
);
CREATE INDEX IF NOT EXISTS idx_web_cache_expires ON web_cache (expires_at);`

// deepSearchTestCache opens a real answer cache on a temp SQLite file — the
// same shape the server wires in, so the tests exercise the shipping
// implementation rather than a stand-in.
func deepSearchTestCache(t *testing.T) *webcache.Cache {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(deepSearchTestSchema); err != nil {
		t.Fatalf("create web_cache: %v", err)
	}
	cache, err := webcache.Open(db, webcache.Options{
		Enabled:   true,
		TTL:       map[webcache.Kind]time.Duration{webcache.KindAnswer: time.Hour},
		L1Entries: 16,
		L1Bytes:   1 << 20,
		L2Rows:    64,
		L2Bytes:   1 << 20,
	})
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	return cache
}

// deepSearchCounter stands in for agent.LoopRunner.RunSearchSession: it counts
// invocations and answers with the call number, so a cache hit (Run never
// called) is distinguishable from a real research round trip.
type deepSearchCounter struct {
	mu    sync.Mutex
	calls int
	// errs holds one error per call, indexed from 1; a nil or missing entry
	// means the call succeeds.
	errs []error
	// empty makes every successful call return an empty answer.
	empty bool
}

func (c *deepSearchCounter) run(_ context.Context, _, _, _, _ string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if n := c.calls; n <= len(c.errs) && c.errs[n-1] != nil {
		return "", c.errs[n-1]
	}
	if c.empty {
		return "", nil
	}
	return fmt.Sprintf("researched answer #%d", c.calls), nil
}

func (c *deepSearchCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// deepSearchInput is one Execute call: the arguments plus the ToolContext
// fields that ride into the cache key.
type deepSearchInput struct {
	query    string
	context  string
	model    string
	provider string
	fresh    bool
}

func (in deepSearchInput) args(t *testing.T) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(struct {
		Query   string `json:"query"`
		Context string `json:"context"`
		Fresh   bool   `json:"fresh"`
	}{Query: in.query, Context: in.context, Fresh: in.fresh})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

func (in deepSearchInput) toolContext(dir string) Context {
	return Context{Model: in.model, Provider: in.provider, SessionDir: dir}
}

// deepSearchKey rebuilds the answer cache key the way Execute documents it, so
// a test can probe the cache directly.
func deepSearchKey(in deepSearchInput, day string) string {
	fullQuery := in.query
	if in.context != "" {
		fullQuery = in.query + "\n\nAdditional context: " + in.context
	}
	return webcache.Key(webcache.KindAnswer, fullQuery, in.model, in.provider, day)
}

func TestDeepSearchToolAnswerCache(t *testing.T) {
	base := deepSearchInput{query: "what is ogcode", model: "ogx-glm-5.3-flash", provider: "ogx"}
	withContext := base
	withContext.context = "Go, CLI"
	otherModel := base
	otherModel.model = "ogx-deepseek-v4.1-flash"
	otherProvider := base
	otherProvider.provider = "openai"
	fresh := base
	fresh.fresh = true

	tests := []struct {
		name  string
		cache bool
		first deepSearchInput
		next  deepSearchInput
		// wantCalls is how many times Run must have been invoked after both
		// calls.
		wantCalls int
		// wantCached is true when the second result must be the first answer
		// served from cache, footer and all.
		wantCached bool
	}{
		{
			name:       "identical call is served from cache",
			cache:      true,
			first:      base,
			next:       base,
			wantCalls:  1,
			wantCached: true,
		},
		{
			name:       "fresh re-researches",
			cache:      true,
			first:      base,
			next:       fresh,
			wantCalls:  2,
			wantCached: false,
		},
		{
			name:       "changed context misses",
			cache:      true,
			first:      base,
			next:       withContext,
			wantCalls:  2,
			wantCached: false,
		},
		{
			name:       "changed model misses",
			cache:      true,
			first:      base,
			next:       otherModel,
			wantCalls:  2,
			wantCached: false,
		},
		{
			name:       "changed provider misses",
			cache:      true,
			first:      base,
			next:       otherProvider,
			wantCalls:  2,
			wantCached: false,
		},
		{
			name:       "nil cache behaves as before",
			cache:      false,
			first:      base,
			next:       base,
			wantCalls:  2,
			wantCached: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			counter := &deepSearchCounter{}
			tool := DeepSearchTool{Run: counter.run}
			if tc.cache {
				tool.Cache = deepSearchTestCache(t)
			}
			dir := t.TempDir()

			first, err := tool.Execute(context.Background(), tc.first.args(t), tc.first.toolContext(dir))
			if err != nil {
				t.Fatalf("first Execute: %v", err)
			}
			if first.Output != "researched answer #1" {
				t.Fatalf("first Output = %q, want the fresh answer with no footer", first.Output)
			}
			if first.Title != tc.first.query {
				t.Errorf("first Title = %q, want %q", first.Title, tc.first.query)
			}

			next, err := tool.Execute(context.Background(), tc.next.args(t), tc.next.toolContext(dir))
			if err != nil {
				t.Fatalf("second Execute: %v", err)
			}
			if got := counter.count(); got != tc.wantCalls {
				t.Fatalf("Run calls = %d, want %d", got, tc.wantCalls)
			}
			if next.Title != tc.next.query {
				t.Errorf("second Title = %q, want %q", next.Title, tc.next.query)
			}

			if !tc.wantCached {
				if next.Output != "researched answer #2" {
					t.Errorf("second Output = %q, want a fresh answer", next.Output)
				}
				if strings.Contains(next.Output, "cached answer") {
					t.Errorf("second Output = %q, must not claim to be cached", next.Output)
				}
				return
			}

			if !strings.HasPrefix(next.Output, first.Output) {
				t.Errorf("second Output = %q, want the cached first answer %q", next.Output, first.Output)
			}
			if !strings.Contains(next.Output, "Served from a cached answer researched") {
				t.Errorf("second Output = %q, want the cached-answer footer", next.Output)
			}
			if !strings.Contains(next.Output, "fresh=true") {
				t.Errorf("second Output = %q, want the footer to name fresh=true", next.Output)
			}
			if lines := strings.Count(next.Output, "\n\n"); lines != 1 {
				t.Errorf("footer must be one trailing line, got %d separators in %q", lines, next.Output)
			}
		})
	}
}

// TestDeepSearchToolAnswerCacheRollsAtMidnight pins the day boundary in the
// answer key: an answer researched late one night must be a miss the next
// morning, so the cache never serves stale research across a day line. The
// clock is a package var exactly for this test.
func TestDeepSearchToolAnswerCacheRollsAtMidnight(t *testing.T) {
	cache := deepSearchTestCache(t)
	counter := &deepSearchCounter{}
	tool := DeepSearchTool{Run: counter.run, Cache: cache}
	in := deepSearchInput{query: "ogcode web cache plan", model: "m", provider: "p"}

	before := time.Date(2026, 11, 2, 23, 59, 30, 0, time.UTC)
	after := before.Add(time.Minute)
	deepSearchNow = func() time.Time { return before }
	defer func() { deepSearchNow = time.Now }()

	first, err := tool.Execute(context.Background(), in.args(t), in.toolContext(t.TempDir()))
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if first.Output != "researched answer #1" {
		t.Fatalf("first Output = %q, want the fresh answer", first.Output)
	}

	deepSearchNow = func() time.Time { return after }
	second, err := tool.Execute(context.Background(), in.args(t), in.toolContext(t.TempDir()))
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if got := counter.count(); got != 2 {
		t.Fatalf("Run calls = %d, want 2 — the pre-midnight answer must miss after midnight", got)
	}
	if second.Output != "researched answer #2" || strings.Contains(second.Output, "cached answer") {
		t.Errorf("second Output = %q, want a fresh research with no cached footer", second.Output)
	}

	// Both days keep their own row: the pre-midnight answer is untouched and
	// the new research is stored under the new day.
	for _, at := range []time.Time{before, after} {
		day := at.Format(deepSearchCacheDate)
		body, ok := cache.Get(webcache.KindAnswer, deepSearchKey(in, day))
		if !ok {
			t.Fatalf("answer missing under day %q", day)
		}
		var env deepSearchEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.Researched != day {
			t.Errorf("envelope researched = %q, want %q", env.Researched, day)
		}
	}
}

// TestDeepSearchToolAnswerCacheStoresEnvelope probes the stored body directly:
// the key is the full query plus model, provider and day, and the envelope
// carries the answer with the date it was researched.
func TestDeepSearchToolAnswerCacheStoresEnvelope(t *testing.T) {
	cache := deepSearchTestCache(t)
	counter := &deepSearchCounter{}
	tool := DeepSearchTool{Run: counter.run, Cache: cache}
	in := deepSearchInput{query: "what is ogcode", context: "Go, CLI", model: "m", provider: "p"}

	day := time.Now().Format(deepSearchCacheDate)
	if _, err := tool.Execute(context.Background(), in.args(t), in.toolContext(t.TempDir())); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	body, ok := cache.Get(webcache.KindAnswer, deepSearchKey(in, day))
	if !ok {
		t.Fatalf("answer not stored under %q", deepSearchKey(in, day))
	}
	var env struct {
		Answer     string `json:"answer"`
		Researched string `json:"researched"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal envelope %q: %v", body, err)
	}
	if env.Answer != "researched answer #1" {
		t.Errorf("envelope answer = %q, want the pipeline's answer verbatim", env.Answer)
	}
	if env.Researched != day {
		t.Errorf("envelope researched = %q, want %q", env.Researched, day)
	}
}

// TestDeepSearchToolAnswerCacheIgnoresFailures pins that neither an error nor
// an empty answer reaches the cache, so the next call researches again.
func TestDeepSearchToolAnswerCacheIgnoresFailures(t *testing.T) {
	tests := []struct {
		name      string
		counter   *deepSearchCounter
		wantFirst string
	}{
		{
			name:      "pipeline error is not cached",
			counter:   &deepSearchCounter{errs: []error{errors.New("pipeline exploded")}},
			wantFirst: "Search agent error: pipeline exploded",
		},
		{
			name:      "empty answer is not cached",
			counter:   &deepSearchCounter{empty: true},
			wantFirst: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := deepSearchTestCache(t)
			tool := DeepSearchTool{Run: tc.counter.run, Cache: cache}
			in := deepSearchInput{query: "what is ogcode", model: "m", provider: "p"}
			day := time.Now().Format(deepSearchCacheDate)

			first, err := tool.Execute(context.Background(), in.args(t), in.toolContext(t.TempDir()))
			if err != nil {
				t.Fatalf("first Execute: %v", err)
			}
			if first.Output != tc.wantFirst {
				t.Fatalf("first Output = %q, want %q", first.Output, tc.wantFirst)
			}
			if _, ok := cache.Get(webcache.KindAnswer, deepSearchKey(in, day)); ok {
				t.Fatalf("a failed call must not be cached")
			}

			second, err := tool.Execute(context.Background(), in.args(t), in.toolContext(t.TempDir()))
			if err != nil {
				t.Fatalf("second Execute: %v", err)
			}
			if got := tc.counter.count(); got != 2 {
				t.Fatalf("Run calls = %d, want 2 (the retry must research again)", got)
			}
			if tc.counter.empty {
				return
			}
			if second.Output != "researched answer #2" {
				t.Errorf("second Output = %q, want the retry's answer", second.Output)
			}
		})
	}
}
