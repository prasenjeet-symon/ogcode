package webcache

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// testClock freezes time so TTL behaviour is asserted rather than waited for.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock(t time.Time) *testClock { return &testClock{t: t} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// openTestDB runs the real migrations, which is also what proves web_cache and
// the search_config columns exist.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func openTestCache(t *testing.T, database *db.DB, clock *testClock, opts Options) *Cache {
	t.Helper()
	if clock != nil {
		opts.Now = clock.Now
	}
	opts.Enabled = true
	c, err := Open(database.DB, opts)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestCacheRoundTrip(t *testing.T) {
	database := openTestDB(t)
	clock := newTestClock(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	c := openTestCache(t, database, clock, Options{})

	key := Key(KindPage, "https://example.com/a")
	if _, ok := c.Get(KindPage, key); ok {
		t.Fatal("empty cache returned a hit")
	}
	if err := c.Put(KindPage, key, []byte("page body")); err != nil {
		t.Fatalf("put: %v", err)
	}
	body, ok := c.Get(KindPage, key)
	if !ok {
		t.Fatal("expected a hit after put")
	}
	if string(body) != "page body" {
		t.Fatalf("body = %q, want %q", body, "page body")
	}

	// A returned slice must be the caller's to keep: mutating it cannot poison
	// what the next Get hands out.
	body[0] = 'X'
	again, _ := c.Get(KindPage, key)
	if string(again) != "page body" {
		t.Fatalf("cache was corrupted through a returned slice: %q", again)
	}

	if s := c.Stats(); s.Entries != 1 || s.Hits != 2 || s.Misses != 1 {
		t.Fatalf("stats = %+v, want 1 entry, 2 hits, 1 miss", s)
	}
}

func TestCacheKindsDoNotCollide(t *testing.T) {
	database := openTestDB(t)
	c := openTestCache(t, database, newTestClock(time.Now()), Options{})

	key := Key(KindSearch, "same-key")
	if err := c.Put(KindSearch, key, []byte("search payload")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := c.Get(KindPage, key); ok {
		t.Fatal("a page read hit a search entry")
	}
	if _, ok := c.Get(KindSearch, key); !ok {
		t.Fatal("search entry went missing")
	}
}

func TestCacheSurvivesRestartAndThenExpires(t *testing.T) {
	database := openTestDB(t)
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	clock := newTestClock(start)

	first := openTestCache(t, database, clock, Options{TTL: map[Kind]time.Duration{KindSearch: time.Hour}})
	key := Key(KindSearch, "golang sqlite wal")
	if err := first.Put(KindSearch, key, []byte(`["cached"]`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	// A second cache over the same file is what a restart looks like: the
	// memory level is empty and the answer has to come off disk.
	clock.Advance(30 * time.Minute)
	second := openTestCache(t, database, clock, Options{TTL: map[Kind]time.Duration{KindSearch: time.Hour}})
	body, ok := second.Get(KindSearch, key)
	if !ok || string(body) != `["cached"]` {
		t.Fatalf("after restart: body = %q, ok = %v; want the stored payload", body, ok)
	}

	clock.Advance(time.Hour)
	third := openTestCache(t, database, clock, Options{TTL: map[Kind]time.Duration{KindSearch: time.Hour}})
	if _, ok := third.Get(KindSearch, key); ok {
		t.Fatal("expired entry was served")
	}
	if s := third.Stats(); s.Entries != 0 {
		t.Fatalf("expired row was left behind: %+v", s)
	}
}

func TestPromotionKeepsOriginalExpiry(t *testing.T) {
	database := openTestDB(t)
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	clock := newTestClock(start)
	opts := Options{TTL: map[Kind]time.Duration{KindPage: 10 * time.Minute}}

	first := openTestCache(t, database, clock, opts)
	key := Key(KindPage, "https://example.com/slow")
	if err := first.Put(KindPage, key, []byte("body")); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Promote it into a fresh memory level nine minutes in.
	clock.Advance(9 * time.Minute)
	second := openTestCache(t, database, clock, opts)
	if _, ok := second.Get(KindPage, key); !ok {
		t.Fatal("expected the entry on disk")
	}

	// Two minutes later the original ten-minute life is over. A promotion that
	// renewed the TTL would still serve it here.
	clock.Advance(2 * time.Minute)
	if _, ok := second.Get(KindPage, key); ok {
		t.Fatal("promotion extended the entry's life")
	}
}

func TestCacheDisabledStoresNothing(t *testing.T) {
	database := openTestDB(t)
	c, err := Open(database.DB, Options{Enabled: false})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if c.Enabled() {
		t.Fatal("Enabled = true on a disabled cache")
	}
	key := Key(KindSearch, "q")
	if err := c.Put(KindSearch, key, []byte("x")); err != nil {
		t.Fatalf("put on disabled cache: %v", err)
	}
	if _, ok := c.Get(KindSearch, key); ok {
		t.Fatal("disabled cache served a hit")
	}
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("disabled cache wrote rows: %+v", s)
	}
}

func TestCacheClearEmptiesBothLevels(t *testing.T) {
	database := openTestDB(t)
	clock := newTestClock(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	c := openTestCache(t, database, clock, Options{})

	for _, k := range []string{"a", "b", "c"} {
		if err := c.Put(KindPage, Key(KindPage, k), []byte("body-"+k)); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	deleted, err := c.Clear()
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("clear removed %d rows, want 3", deleted)
	}
	if _, ok := c.Get(KindPage, Key(KindPage, "a")); ok {
		t.Fatal("memory level survived a clear")
	}
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("disk level survived a clear: %+v", s)
	}
}

func TestCacheDeleteRemovesBothLevels(t *testing.T) {
	database := openTestDB(t)
	c := openTestCache(t, database, newTestClock(time.Now()), Options{})
	key := Key(KindAnswer, "q")
	if err := c.Put(KindAnswer, key, []byte("answer")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := c.Delete(KindAnswer, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := c.Get(KindAnswer, key); ok {
		t.Fatal("entry survived a delete")
	}
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("row survived a delete: %+v", s)
	}
}

func TestCacheL1EvictsAtCapacity(t *testing.T) {
	database := openTestDB(t)
	c := openTestCache(t, database, newTestClock(time.Now()), Options{L1Entries: 2})
	for _, k := range []string{"a", "b", "c"} {
		if err := c.Put(KindSearch, Key(KindSearch, k), []byte("body-"+k)); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	if s := c.Stats(); s.Evictions == 0 {
		t.Fatal("memory level never evicted at a capacity of two with three entries")
	}
	// Eviction is a memory-level decision: the disk copy must still answer.
	if s := c.Stats(); s.Entries != 3 {
		t.Fatalf("disk level lost entries to a memory eviction: %+v", s)
	}
	if _, ok := c.Get(KindSearch, Key(KindSearch, "a")); !ok {
		t.Fatal("evicted entry was not served from disk")
	}
}

func TestCacheSweepDropsExpiredRows(t *testing.T) {
	database := openTestDB(t)
	clock := newTestClock(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	c := openTestCache(t, database, clock, Options{TTL: map[Kind]time.Duration{KindPage: time.Hour}})

	if err := c.Put(KindPage, Key(KindPage, "x"), []byte("body")); err != nil {
		t.Fatalf("put: %v", err)
	}
	clock.Advance(2 * time.Hour)
	deleted, err := c.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("sweep deleted %d rows, want 1", deleted)
	}
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("swept row still counted: %+v", s)
	}
}

func TestCachePutIgnoresEmptyBody(t *testing.T) {
	database := openTestDB(t)
	c := openTestCache(t, database, newTestClock(time.Now()), Options{})
	key := Key(KindPage, "https://example.com/empty")
	if err := c.Put(KindPage, key, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("empty body was stored: %+v", s)
	}
}

func TestBypassContextRoundTrip(t *testing.T) {
	if IsBypass(context.Background()) {
		t.Fatal("a plain context reported itself as bypassed")
	}
	if !IsBypass(Bypass(context.Background())) {
		t.Fatal("Bypass did not mark the context")
	}
}

func TestKeyIsStableAndKindScoped(t *testing.T) {
	a := Key(KindSearch, "provider", "golang", "8")
	if b := Key(KindSearch, "provider", "golang", "8"); a != b {
		t.Fatal("same inputs produced different keys")
	}
	if b := Key(KindSearch, "provider", "golang", "9"); a == b {
		t.Fatal("a different limit collided")
	}
	if b := Key(KindPage, "provider", "golang", "8"); a == b {
		t.Fatal("kinds collided")
	}
	// Parts must not run together: ("ab","c") and ("a","bc") are different keys.
	if Key(KindSearch, "ab", "c") == Key(KindSearch, "a", "bc") {
		t.Fatal("part boundaries were not separated")
	}
}
