package filechange

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// opTimeout bounds one best-effort write to the store. Both writes happen
// inside a tool call the agent is waiting on, so a slow disk must cost a
// warning, never a stalled turn.
const opTimeout = 5 * time.Second

// fileEntry is what the gate holds for one file: its current revision, the
// most recent changes (newest last), and the fingerprint of its bytes as the
// gate last saw them.
type fileEntry struct {
	rev  int64
	ring []Change
	// fp is the file's bytes at the last read or recorded write; hasFP is
	// false for a path the gate only ever looked up by revision (a stale check
	// on a file nobody read), which has nothing to compare a shell command to.
	fp    Fingerprint
	hasFP bool
}

// Log is the read-bookmark gate. It is one per process (one server per project
// directory; pathlock already assumes process scope), holding revisions and
// bookmarks in memory and mirroring both to the store through Sink and
// Persist.
//
// The zero value is not usable: call NewLog. A nil *Log is a valid no-op, so a
// tool with no gate wired — a bare test, an embedding — behaves exactly as it
// did before this package existed.
type Log struct {
	mu        sync.Mutex
	files     map[string]*fileEntry
	bookmarks map[string]map[session.SessionID]int64
	// saved is the revision the store is known to hold per (file, session).
	// Persist fires only when the observed revision differs from it, so an
	// unchanged re-read writes nothing and a failed write is retried.
	saved map[string]map[session.SessionID]int64

	// Name renders a session as something a refusal can name — the board
	// handle when the server supplies one. nil falls back to the session id.
	Name func(session.SessionID) string
	// Sink is the durable history: one call per Record.
	Sink func(context.Context, Change) error
	// Persist is the durable bookmark: one call per observed revision.
	Persist func(context.Context, Bookmark) error
	// History answers what the ring no longer holds, after a restart or past
	// its bound. nil means a refusal can only list what is in memory.
	History func(ctx context.Context, path string, afterRev int64, limit int) ([]Change, error)
}

// NewLog builds the gate over store and restores what the last process knew:
// every file's highest revision and every live session's bookmark. A nil store
// gives a memory-only gate (the CLI's own session has no bookmark, so it never
// refuses; tests use it the same way).
func NewLog(ctx context.Context, store *Store) *Log {
	l := &Log{
		files:     map[string]*fileEntry{},
		bookmarks: map[string]map[session.SessionID]int64{},
		saved:     map[string]map[session.SessionID]int64{},
	}
	if store == nil {
		return l
	}
	l.Sink = store.Append
	l.Persist = store.SaveBookmark
	l.History = func(ctx context.Context, path string, afterRev int64, limit int) ([]Change, error) {
		return store.After(ctx, path, afterRev, limit)
	}

	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*opTimeout)
	defer cancel()
	revs, err := store.Revisions(restoreCtx)
	if err != nil {
		// A failed restore is not fatal: the gate starts from zero, which
		// refuses nothing until its own first records arrive.
		slog.Warn("file revisions not restored", "dir", store.Dir(), "err", err)
	}
	bookmarks, err := store.Bookmarks(restoreCtx)
	if err != nil {
		slog.Warn("read bookmarks not restored", "dir", store.Dir(), "err", err)
	}
	n, err := store.PruneBookmarks(restoreCtx)
	if err != nil {
		slog.Warn("stale read bookmarks not swept", "dir", store.Dir(), "err", err)
	} else if n > 0 {
		slog.Info("stale read bookmarks swept", "dir", store.Dir(), "rows", n)
	}
	l.Restore(revs, bookmarks)
	return l
}

// Restore seeds revisions and bookmarks from the store. A path present in only
// one of the two tables still gets an entry, so a restored bookmark of 7
// against a restored revision of 9 is stale the way it was before the restart.
func (l *Log) Restore(revs map[string]int64, bookmarks map[string]map[session.SessionID]int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for path, rev := range revs {
		if e := l.entry(path); rev > e.rev {
			e.rev = rev
		}
	}
	for path, bySession := range bookmarks {
		l.entry(path)
		if l.bookmarks[path] == nil {
			l.bookmarks[path] = map[session.SessionID]int64{}
		}
		if l.saved[path] == nil {
			l.saved[path] = map[session.SessionID]int64{}
		}
		for s, rev := range bySession {
			l.bookmarks[path][s] = rev
			l.saved[path][s] = rev
		}
	}
}

// entry returns the file's entry, creating it. Callers hold l.mu.
func (l *Log) entry(path string) *fileEntry {
	e := l.files[path]
	if e == nil {
		e = &fileEntry{}
		l.files[path] = e
	}
	return e
}

// Rev is the file's current revision, 0 when the gate has never seen it.
func (l *Log) Rev(path string) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.files[path]; e != nil {
		return e.rev
	}
	return 0
}

// Seen is the revision session last read path at, or -1 when it has never read
// it. The two are not the same at revision 0: a file no tool has mutated sits
// at 0, and the session that read it there must still be refused when someone
// else changes it — a bookmark of 0 is a bookmark.
func (l *Log) Seen(path string, s session.SessionID) int64 {
	if l == nil || s == "" {
		return -1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rev, ok := l.bookmarks[path][s]
	if !ok {
		return -1
	}
	return rev
}

// Fingerprint is the state of the file's bytes the gate last saw, and ok is
// false when it has never seen them — a path only ever looked up by revision.
// A shell command's change is judged against it.
func (l *Log) Fingerprint(path string) (Fingerprint, bool) {
	if l == nil {
		return Fingerprint{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.files[path]
	if e == nil || !e.hasFP {
		return Fingerprint{}, false
	}
	return e.fp, true
}

// Files lists the paths whose bytes the gate has seen, which is what a shell
// command's observer walks: every file a session read or a tool wrote in this
// project. A path only ever looked up by revision is not listed, because there
// is nothing to compare it to.
func (l *Log) Files() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.files))
	for path, e := range l.files {
		if e.hasFP {
			out = append(out, path)
		}
	}
	return out
}

// Read records that session has now read path at revision rev. The bookmark is
// persisted only when it differs from what the store already holds, so a
// repeat read of an unchanged file writes nothing.
//
// It is also where the gate observes the file's bytes: the fingerprint stamped
// here is what a later shell command is measured against (see Fingerprint).
// Stamped for every caller, including one with no session — a CLI run has no
// bookmark, but its files still deserve a comparison point.
func (l *Log) Read(ctx context.Context, path string, s session.SessionID, rev int64) {
	if l == nil || path == "" {
		return
	}
	// Taken outside the mutex, so a stat cannot hold other sessions up.
	fp := FingerprintOf(path)
	l.mu.Lock()
	e := l.entry(path)
	e.fp, e.hasFP = fp, true
	if s == "" {
		l.mu.Unlock()
		return
	}
	if l.bookmarks[path] == nil {
		l.bookmarks[path] = map[session.SessionID]int64{}
	}
	l.bookmarks[path][s] = rev
	saved, ok := l.saved[path][s]
	l.mu.Unlock()

	if l.Persist == nil || (ok && saved == rev) {
		return
	}
	l.persist(ctx, Bookmark{Path: path, Session: s, Rev: rev, At: time.Now()})
}

// persist writes one bookmark through. On failure memory stays authoritative
// and saved is left alone, so the next read at this revision tries again; a
// bookmark that is never persisted costs protection after a restart, never the
// read itself.
func (l *Log) persist(ctx context.Context, b Bookmark) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
	defer cancel()
	if err := l.Persist(writeCtx, b); err != nil {
		slog.Warn("read bookmark not persisted", "path", b.Path, "session", b.Session, "rev", b.Rev, "err", err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.saved[b.Path] == nil {
		l.saved[b.Path] = map[session.SessionID]int64{}
	}
	l.saved[b.Path][b.Session] = b.Rev
}

// Stale answers what another session has changed since seen. It returns those
// changes in revision order and how many changes in the window it could not
// show at all (the ring's bound, or a restart); the caller refuses when either
// is non-zero.
//
// A session is never blocked by itself: changes it made are filtered out, so a
// fix-mistake-retry loop keeps working. A negative seen is Seen's "no
// bookmark", which is never stale — the caller has not read the file and has
// nothing to catch up on.
func (l *Log) Stale(ctx context.Context, path string, s session.SessionID, seen int64) ([]Change, int) {
	if l == nil || seen < 0 {
		return nil, 0
	}
	l.mu.Lock()
	e := l.files[path]
	if e == nil || e.rev <= seen {
		l.mu.Unlock()
		return nil, 0
	}
	rev := e.rev
	ring := append([]Change(nil), e.ring...)
	l.mu.Unlock()

	window := rev - seen
	// The ring explains the window only when its oldest change reaches back to
	// the first revision the caller has not seen. An empty ring explains
	// nothing, and that is not the same as covering seen: a restart restores
	// revisions and bookmarks but no ring, so treating an empty ring as
	// covering revision 0 made a bookmark of 0 refuse with every change
	// missing while the store, which holds them, was never asked.
	covered := len(ring) > 0 && ring[0].Rev <= seen+1
	if !covered && l.History != nil {
		limit := int(window)
		if limit > 200 {
			limit = 200
		}
		hist, err := l.History(context.WithoutCancel(ctx), path, seen, limit)
		if err != nil {
			slog.Warn("file change history lookup failed", "path", path, "err", err)
		} else {
			ring = hist
		}
	}

	out := make([]Change, 0, len(ring))
	for _, c := range ring {
		if c.Rev <= seen || c.Session == s {
			continue
		}
		out = append(out, c)
	}
	dropped := int(window) - len(ring)
	if dropped < 0 {
		dropped = 0
	}
	return out, dropped
}

// Record appends a change, bumps the file's revision and catches the author's
// own bookmark up to it. It returns the new revision.
//
// The bytes are already on disk when this is called, so a failed Sink is a
// warning, not an error: the gate knows the change either way, and the history
// is a review trail rather than a write-ahead journal.
func (l *Log) Record(ctx context.Context, path string, ch Change) int64 {
	if l == nil || path == "" {
		return 0
	}
	ch = ch.capped()
	ch.Path = path
	if ch.At.IsZero() {
		ch.At = time.Now()
	}

	l.mu.Lock()
	e := l.entry(path)
	e.rev++
	ch.Rev = e.rev
	e.ring = append(e.ring, ch)
	if len(e.ring) > RingPerFile {
		e.ring = append([]Change(nil), e.ring[len(e.ring)-RingPerFile:]...)
	}
	rev := e.rev
	l.mu.Unlock()

	if l.Sink != nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
		err := l.Sink(writeCtx, ch)
		cancel()
		if err != nil {
			slog.Warn("file change not recorded", "path", path, "rev", rev, "err", err)
		}
	}
	l.Read(ctx, path, ch.Session, rev)
	return rev
}
