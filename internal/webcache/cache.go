// Package webcache keeps public web content — search results, fetched pages and
// deep-research answers — so a repeated question costs no network call and no
// LLM round trip.
//
// It is deliberately two-level: an in-process LRU in front of a SQLite table in
// the global config database. The memory level makes a repeat within one turn
// free; the disk level is what makes the same query fast on the NEXT turn, after
// a restart or from a different session.
//
// Only successful, non-empty results are ever stored — that decision belongs to
// the caller (see internal/search/cache.go for the backend decorator, and the
// deep-search tool for answers), because only the caller knows what a failure
// looks like for its own kind.
package webcache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Kind separates the three payload shapes that share the table. It is part of
// the row key, so the kinds can never collide.
type Kind string

const (
	// KindSearch is a provider's ranked result list for one query.
	KindSearch Kind = "search"
	// KindPage is the extracted text of one URL.
	KindPage Kind = "page"
	// KindAnswer is a synthesised deep-research answer.
	KindAnswer Kind = "answer"
)

// Default TTLs per kind. Search rankings move fastest and are the cheapest to
// redo; a page's body text is stable for a day; an answer is the expensive
// artefact and is worth a working session.
const (
	DefaultSearchTTL = time.Hour
	DefaultPageTTL   = 24 * time.Hour
	DefaultAnswerTTL = 6 * time.Hour
)

// Default capacities. Both levels are bounded — an unbounded cache in a
// long-lived server is a leak, which is exactly what the backend-local caches
// this replaces had.
const (
	DefaultL1Entries = 512
	DefaultL1Bytes   = 64 << 20
	DefaultL2Rows    = 5000
	DefaultL2Bytes   = 256 << 20
)

// trimEvery is how many L2 writes pass between budget checks, so a burst of
// writes cannot grow the table far past its cap without waiting for a sweep.
const trimEvery = 64

// Stats reports what the cache holds and how it is doing. Entries and Bytes
// count the disk level (the durable one); Hits and Misses count every Get.
type Stats struct {
	Entries   int64
	Bytes     int64
	Hits      int64
	Misses    int64
	Evictions int64
}

// Options configures a Cache. Zero values take the defaults above; a nil Now
// takes time.Now. TTL may be partial — a kind missing from the map falls back to
// its default.
type Options struct {
	Enabled   bool
	TTL       map[Kind]time.Duration
	L1Entries int
	L1Bytes   int
	L2Rows    int64
	L2Bytes   int64
	Now       func() time.Time
}

// Cache is a memory LRU in front of the web_cache table. Every method tolerates
// a nil receiver, so a caller can carry "caching is off" as a nil pointer
// instead of branching at each site.
type Cache struct {
	db   *sql.DB
	opts Options
	now  func() time.Time

	mu    sync.Mutex
	ll    *list.List // front = most recently used
	items map[string]*list.Element
	bytes int

	hits, misses, evictions atomic.Int64
	writesSinceTrim         atomic.Int64
}

// entry is one L1 value. The expiry is absolute, never a duration: promoting an
// entry from disk must not extend its life, or a hot key would never expire.
type entry struct {
	key       string
	body      []byte
	expiresAt time.Time
}

// Open returns a cache over db. The web_cache table is created by migration 074,
// which runs when the database is opened — Open does not create it.
func Open(db *sql.DB, opts Options) (*Cache, error) {
	if db == nil {
		return nil, errors.New("webcache: nil database")
	}
	if opts.L1Entries <= 0 {
		opts.L1Entries = DefaultL1Entries
	}
	if opts.L1Bytes <= 0 {
		opts.L1Bytes = DefaultL1Bytes
	}
	if opts.L2Rows <= 0 {
		opts.L2Rows = DefaultL2Rows
	}
	if opts.L2Bytes <= 0 {
		opts.L2Bytes = DefaultL2Bytes
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Cache{
		db:    db,
		opts:  opts,
		now:   now,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}, nil
}

// Enabled reports whether reads and writes are live.
func (c *Cache) Enabled() bool { return c != nil && c.opts.Enabled }

// TTL returns the lifetime configured for kind.
func (c *Cache) TTL(kind Kind) time.Duration {
	if c != nil {
		if d, ok := c.opts.TTL[kind]; ok && d > 0 {
			return d
		}
	}
	return defaultTTL(kind)
}

func defaultTTL(kind Kind) time.Duration {
	switch kind {
	case KindPage:
		return DefaultPageTTL
	case KindAnswer:
		return DefaultAnswerTTL
	default:
		return DefaultSearchTTL
	}
}

// Key hashes a kind and its canonical parts into the opaque key stored in the
// table. Callers never store the raw query or URL — the digest is all that is
// persisted, so the cache cannot leak what someone searched for.
func Key(kind Kind, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type bypassKey struct{}

// Bypass marks ctx so reads are skipped while writes still happen: "give me a
// live answer, and refresh what you keep for next time".
func Bypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

// IsBypass reports whether ctx was marked by Bypass.
func IsBypass(ctx context.Context) bool {
	v, _ := ctx.Value(bypassKey{}).(bool)
	return v
}

func l1Key(kind Kind, key string) string { return string(kind) + "\x00" + key }

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Get returns the stored body for kind/key. The slice is a copy, so a caller may
// keep or mutate it without corrupting the cache.
func (c *Cache) Get(kind Kind, key string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}
	if body, ok := c.getL1(l1Key(kind, key)); ok {
		c.hits.Add(1)
		return body, true
	}

	var body []byte
	var expiresAt int64
	err := c.db.QueryRow(
		`SELECT body, expires_at FROM web_cache WHERE kind = ? AND key = ?`,
		string(kind), key,
	).Scan(&body, &expiresAt)

	switch {
	case err == nil:
		expiry := time.UnixMilli(expiresAt)
		if c.now().Before(expiry) {
			// Promotion copies the stored expiry as-is: it never extends life.
			c.putL1(l1Key(kind, key), body, expiry)
			c.hits.Add(1)
			return clone(body), true
		}
		if _, derr := c.db.Exec(`DELETE FROM web_cache WHERE kind = ? AND key = ?`, string(kind), key); derr != nil {
			slog.Warn("webcache: drop expired row", "err", derr)
		}
	case !errors.Is(err, sql.ErrNoRows):
		slog.Warn("webcache: read", "err", err)
	}

	c.misses.Add(1)
	return nil, false
}

// Put stores body under kind/key with that kind's TTL. An empty body is never
// stored; a write failure is returned but leaves the memory level populated.
func (c *Cache) Put(kind Kind, key string, body []byte) error {
	if !c.Enabled() || len(body) == 0 {
		return nil
	}
	ttl := c.TTL(kind)
	if ttl <= 0 {
		return nil
	}
	now := c.now()
	expiresAt := now.Add(ttl)

	c.putL1(l1Key(kind, key), clone(body), expiresAt)

	_, err := c.db.Exec(`
		INSERT INTO web_cache (kind, key, body, bytes, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, key) DO UPDATE SET
			body       = excluded.body,
			bytes      = excluded.bytes,
			created_at = excluded.created_at,
			expires_at = excluded.expires_at
	`, string(kind), key, body, len(body), now.UnixMilli(), expiresAt.UnixMilli())
	if err != nil {
		slog.Warn("webcache: write", "kind", kind, "err", err)
		return err
	}
	if c.writesSinceTrim.Add(1) >= trimEvery {
		c.writesSinceTrim.Store(0)
		if _, terr := c.trim(); terr != nil {
			slog.Warn("webcache: trim", "err", terr)
		}
	}
	return nil
}

// Delete removes one entry from both levels.
func (c *Cache) Delete(kind Kind, key string) error {
	if c == nil {
		return nil
	}
	c.deleteL1(l1Key(kind, key))
	_, err := c.db.Exec(`DELETE FROM web_cache WHERE kind = ? AND key = ?`, string(kind), key)
	if err != nil {
		slog.Warn("webcache: delete", "err", err)
	}
	return err
}

// Clear empties both levels and reports how many disk rows it removed.
func (c *Cache) Clear() (int64, error) {
	if c == nil {
		return 0, nil
	}
	c.mu.Lock()
	c.ll.Init()
	c.items = make(map[string]*list.Element)
	c.bytes = 0
	c.mu.Unlock()

	res, err := c.db.Exec(`DELETE FROM web_cache`)
	if err != nil {
		slog.Warn("webcache: clear", "err", err)
		return 0, err
	}
	return res.RowsAffected()
}

// Sweep drops expired rows, the memory entries that went with them, and trims
// the table back inside its budget. It is cheap enough to run on a ticker.
func (c *Cache) Sweep() (int64, error) {
	if c == nil {
		return 0, nil
	}
	nowMs := c.now().UnixMilli()
	res, err := c.db.Exec(`DELETE FROM web_cache WHERE expires_at <= ?`, nowMs)
	if err != nil {
		slog.Warn("webcache: sweep", "err", err)
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	c.purgeL1(nowMs)

	trimmed, err := c.trim()
	if err != nil {
		slog.Warn("webcache: trim", "err", err)
	}
	return deleted + trimmed, err
}

// Stats reads the disk level and the in-process counters.
func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	s := Stats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
	}
	var rows, bytes sql.NullInt64
	err := c.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(bytes), 0) FROM web_cache`).Scan(&rows, &bytes)
	if err == nil {
		s.Entries = rows.Int64
		s.Bytes = bytes.Int64
	}
	return s
}

// Close releases nothing — the cache does not own the database handle it was
// opened over — but exists so callers can defer it beside the server's other
// shutdown work.
func (c *Cache) Close() error { return nil }

// trim deletes the oldest rows until the table is back inside its row and byte
// budgets. Expired rows go first because the ordering is by expiry.
func (c *Cache) trim() (int64, error) {
	var rows, bytes int64
	if err := c.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(bytes), 0) FROM web_cache`).Scan(&rows, &bytes); err != nil {
		return 0, err
	}
	var deleted int64
	for rows > 0 && (rows > c.opts.L2Rows || bytes > c.opts.L2Bytes) {
		res, err := c.db.Exec(`
			DELETE FROM web_cache WHERE rowid IN (
				SELECT rowid FROM web_cache ORDER BY expires_at ASC, created_at ASC LIMIT 50
			)`)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break
		}
		deleted += n
		rows -= n
		if bytes > c.opts.L2Bytes {
			if err := c.db.QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM web_cache`).Scan(&bytes); err != nil {
				return deleted, err
			}
		}
	}
	return deleted, nil
}

func (c *Cache) getL1(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if !c.now().Before(e.expiresAt) {
		c.ll.Remove(el)
		delete(c.items, k)
		c.bytes -= len(e.body)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return clone(e.body), true
}

func (c *Cache) putL1(k string, body []byte, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		e := el.Value.(*entry)
		c.bytes -= len(e.body)
		e.body = body
		e.expiresAt = expiresAt
		c.bytes += len(body)
		c.ll.MoveToFront(el)
	} else {
		el := c.ll.PushFront(&entry{key: k, body: body, expiresAt: expiresAt})
		c.items[k] = el
		c.bytes += len(body)
	}
	c.evictLocked()
}

// evictLocked drops least-recently-used entries until the memory level is back
// inside both caps. A single entry larger than the byte cap evicts everything
// and is dropped rather than thrashing.
func (c *Cache) evictLocked() {
	for (len(c.items) > c.opts.L1Entries || c.bytes > c.opts.L1Bytes) && c.ll.Len() > 0 {
		back := c.ll.Back()
		e := back.Value.(*entry)
		c.ll.Remove(back)
		delete(c.items, e.key)
		c.bytes -= len(e.body)
		c.evictions.Add(1)
	}
}

func (c *Cache) deleteL1(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return
	}
	e := el.Value.(*entry)
	c.ll.Remove(el)
	delete(c.items, k)
	c.bytes -= len(e.body)
}

func (c *Cache) purgeL1(nowMs int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		e := el.Value.(*entry)
		if e.expiresAt.UnixMilli() <= nowMs {
			c.ll.Remove(el)
			delete(c.items, e.key)
			c.bytes -= len(e.body)
		}
		el = next
	}
}
