package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// backupStamp is the timestamp a rotated file carries in its name, in UTC. It
// sorts lexically in time order and holds no ':' (Windows rejects it in names).
const backupStamp = "20060102T150405.000Z"

// rotationRetry is how long a failed rotation waits before it is tried again,
// so a file that cannot be renamed (another process holds it open on Windows)
// costs one attempt a minute rather than one per log line.
const rotationRetry = time.Minute

// followInterval is how often a writer checks that the path still names the
// file it holds, and re-reads that file's size. Two stats a second at most.
const followInterval = time.Second

// compressGrace is how long a rotated file must go unwritten before it is
// compressed: a process sharing the file may still append to it for up to
// followInterval after another process rotated it away, and compressing it
// under that writer would drop those lines.
const compressGrace = 3 * time.Second

// syncEvery is how often the mill goroutine flushes unsynced writes to disk,
// so a crash or power loss costs at most one tick of tail. A var so tests can
// tick faster.
var syncEvery = 5 * time.Second

// renameFile and syncFile stand in for os.Rename and (*os.File).Sync, which
// tests cannot otherwise make fail on demand.
var (
	renameFile = os.Rename
	syncFile   = (*os.File).Sync
)

// RotateOptions bound how much disk a log file may take.
type RotateOptions struct {
	// MaxSize is the size in bytes at which the active file is rotated.
	MaxSize int64
	// MaxBackups is how many rotated files are kept; 0 keeps them all.
	MaxBackups int
	// MaxAge removes rotated files older than this; 0 keeps them regardless.
	MaxAge time.Duration
	// Compress gzips rotated files.
	Compress bool
}

// RotatingFile is an io.Writer that appends to a log file and, when the file
// would grow past MaxSize, renames it to <stem>-<UTC stamp><ext> and starts a
// fresh one. Pruning and compressing the rotated files happens off the write
// path, in one background goroutine.
//
// Files are created 0600 and a directory it creates 0700: log lines carry
// paths, prompts and provider errors, which are the user's alone to read.
//
// Several processes may append to the same file (two servers or two `ogcode
// run` in one project). Appends are O_APPEND, so records never interleave
// mid-line; each writer re-checks the path every followInterval, so after one
// process rotates, the others follow it to the new file within a second rather
// than writing on into the backup — and the size each one rotates on includes
// the others' writes.
type RotatingFile struct {
	path  string
	opts  RotateOptions
	now   func() time.Time
	grace time.Duration // compressGrace; zero in tests

	mu        sync.Mutex
	f         *os.File
	size      int64
	dirty     bool // r.f holds writes not yet flushed by Sync
	checkAt   time.Time
	retryAt   time.Time
	closed    bool
	millTimer *time.Timer

	// report() runs with or without r.mu held — from Write under it, from the
	// mill goroutine without — so the handler is guarded by its own lock.
	errMu   sync.Mutex
	onError func(op string, err error)

	millCh   chan struct{}
	millDone chan struct{}
}

// OpenRotating opens (creating if needed) the log file at path.
func OpenRotating(path string, opts RotateOptions) (*RotatingFile, error) {
	return openRotating(path, opts, time.Now, compressGrace)
}

func openRotating(path string, opts RotateOptions, now func() time.Time, grace time.Duration) (*RotatingFile, error) {
	if opts.MaxSize <= 0 {
		return nil, fmt.Errorf("log max size must be positive, got %d", opts.MaxSize)
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	r := &RotatingFile{
		path:     path,
		opts:     opts,
		now:      now,
		grace:    grace,
		millCh:   make(chan struct{}, 1),
		millDone: make(chan struct{}),
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	go r.millLoop()
	// Apply retention once at start, so lowering MaxBackups takes effect on
	// the next launch rather than on the next rotation — and compress what an
	// earlier process rotated but exited before compressing.
	r.mu.Lock()
	r.signalMill()
	r.mu.Unlock()
	return r, nil
}

// Path is the active log file.
func (r *RotatingFile) Path() string { return r.path }

// SetErrorHandler installs fn as the sink for every failure of this file's
// background work — rotation, reopening, pruning, compressing, listing the
// backups, syncing — as a short operation name plus the error. It never gates
// logging: each report is guarded against a panicking handler, and the failing
// operation carries on. The default handler writes one Scrubbed line to
// stderr.
func (r *RotatingFile) SetErrorHandler(fn func(op string, err error)) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	r.onError = fn
}

// report hands one failure to the error handler, defaulting to a line on
// stderr keyed like ogcode's other stderr notes. It never panics and never
// stops logging: a handler that panics costs that report, nothing more.
func (r *RotatingFile) report(op string, err error) {
	r.errMu.Lock()
	fn := r.onError
	r.errMu.Unlock()
	defer func() { _ = recover() }() // a handler bug must not take logging down
	if fn != nil {
		fn(op, err)
		return
	}
	fmt.Fprintf(os.Stderr, "ogcode: could not %s: %s\n", op, Scrub(err.Error()))
}

// Write appends p, rotating first when p would take the file past MaxSize. A
// single record larger than MaxSize is still written whole, to a fresh file.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	now := r.now()
	if r.f != nil && !now.Before(r.checkAt) {
		r.checkAt = now.Add(followInterval)
		r.follow()
	}
	if r.f != nil && r.size > 0 && r.size+int64(len(p)) > r.opts.MaxSize && !now.Before(r.retryAt) {
		r.rotate(int64(len(p)))
	}
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	if n > 0 {
		r.dirty = true
	}
	return n, err
}

// Close closes the file and waits for any pruning or compression in flight.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	if r.millTimer != nil {
		r.millTimer.Stop()
	}
	close(r.millCh)
	var err error
	r.syncLocked() // a sync failure is reported to the error handler, not fatal
	if r.f != nil {
		err = r.f.Close()
		r.f = nil
	}
	r.mu.Unlock()
	<-r.millDone
	return err
}

// Sync flushes unsynced writes to the active file. It is a no-op once the
// file is clean — the mill goroutine's timer and Close flush too — and a sync
// failure is reported to the error handler and returned, but never fatal.
func (r *RotatingFile) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.syncLocked()
}

// syncLocked flushes the active file when it has unsynced writes. Called with
// r.mu held. It reports its own failure and only then returns the error.
func (r *RotatingFile) syncLocked() error {
	if r.f == nil || !r.dirty {
		return nil
	}
	err := syncFile(r.f)
	if err != nil {
		r.report("sync", err)
		return err
	}
	r.dirty = false
	return nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log file: %w", err)
	}
	// A file left by an older build, or created under a looser umask, is
	// narrowed to the owner. Best effort: on Windows this is a no-op.
	if info.Mode().Perm()&0o077 != 0 {
		_ = f.Chmod(0o600)
	}
	r.f, r.size, r.dirty = f, info.Size(), false
	return nil
}

// follow re-syncs with the path. If another process rotated it (or the user
// deleted it), the path names a different file than ours: reopen it. Otherwise
// take the file's real size, which counts other writers' appends. Called with
// r.mu held; r.f may be nil on return if reopening failed.
func (r *RotatingFile) follow() {
	mine, err := r.f.Stat()
	if err != nil {
		return
	}
	if cur, err := os.Stat(r.path); err == nil && os.SameFile(cur, mine) {
		r.size = mine.Size()
		return
	}
	r.f.Close()
	r.f = nil
	if err := r.open(); err != nil {
		r.report("reopen", fmt.Errorf("log file %s: %w", r.path, err))
	}
}

// rotate moves the active file aside and opens a fresh one. incoming is the
// size of the write that triggered it. Called with r.mu held; on return r.f is
// open, or nil if reopening failed (Write retries the open).
//
// A rename that keeps failing is bounded rather than endured: once the live
// file would pass twice MaxSize, it is truncated in place (truncateLive), so
// an unrenamable file cannot grow forever.
func (r *RotatingFile) rotate(incoming int64) {
	// Another process may already have rotated the path: follow it, and rotate
	// only if the file it now names is itself full.
	r.follow()
	if r.f == nil || r.size == 0 || r.size+incoming <= r.opts.MaxSize {
		return
	}

	r.syncLocked() // flush the full file, so the backup holds every line
	r.f.Close()
	r.f = nil
	if err := renameFile(r.path, r.backupPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Keep appending to the oversized file and try again later — but a
		// rename that never succeeds would grow the live file past MaxSize
		// forever, so once it is past twice the limit, bound the disk by
		// truncating it in place and dropping the tail: losing the tail beats
		// filling the disk.
		r.retryAt = r.now().Add(rotationRetry)
		r.report("rotate", fmt.Errorf("log file %s: %w", r.path, err))
		if r.size+incoming > 2*r.opts.MaxSize {
			r.truncateLive()
		}
	}
	if err := r.open(); err != nil {
		r.report("reopen", fmt.Errorf("log file %s: %w", r.path, err))
		return
	}
	r.signalMill()
}

// truncateLive resets an oversized live file in place after a rotation whose
// rename failed, so the disk stays bounded even though the file could not be
// moved aside and the tail is dropped. Called with r.mu held, with r.f closed.
func (r *RotatingFile) truncateLive() {
	if err := os.Truncate(r.path, 0); err != nil {
		r.report("rotate", fmt.Errorf("log file %s: %w", r.path, err))
		return
	}
	r.size = 0
	r.report("rotate", fmt.Errorf("log file %s: rename keeps failing past twice MaxSize: truncated in place, disk bounded, log tail dropped", r.path))
}

// backupPath names a rotated file, suffixing a counter in the unlikely case
// that two rotations land in the same millisecond.
func (r *RotatingFile) backupPath() string {
	dir, stem, ext := r.parts()
	base := stem + "-" + r.now().UTC().Format(backupStamp)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		p := filepath.Join(dir, name+ext)
		if !exists(p) && !exists(p+".gz") {
			return p
		}
	}
}

func (r *RotatingFile) parts() (dir, stem, ext string) {
	dir = filepath.Dir(r.path)
	base := filepath.Base(r.path)
	ext = filepath.Ext(base)
	return dir, strings.TrimSuffix(base, ext), ext
}

// signalMill asks the mill goroutine for a pass. Called with r.mu held.
func (r *RotatingFile) signalMill() {
	if r.closed {
		return
	}
	select {
	case r.millCh <- struct{}{}:
	default: // a run is already pending; it will see this rotation too
	}
}

// millLoop runs a retention pass whenever millCh is signalled, and flushes
// unsynced writes on a timer in between, so a crash costs at most one tick of
// tail.
func (r *RotatingFile) millLoop() {
	defer close(r.millDone)
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()
	for {
		select {
		case _, ok := <-r.millCh:
			if !ok {
				return // Close signalled the last pass
			}
			r.mill()
		case <-ticker.C:
			r.mu.Lock()
			r.syncLocked()
			r.mu.Unlock()
		}
	}
}

// backup is one rotated file on disk.
type backup struct {
	path string
	when time.Time
	gz   bool
}

// mill enforces retention, then compresses what is kept. Errors are reported
// to the error handler and otherwise ignored: a pruning failure must never
// stop logging.
func (r *RotatingFile) mill() {
	backups, err := r.backups()
	if err != nil {
		r.report("list", err)
		return
	}
	var keep []backup
	cutoff := time.Time{}
	if r.opts.MaxAge > 0 {
		cutoff = r.now().Add(-r.opts.MaxAge)
	}
	for i, b := range backups { // newest first
		tooMany := r.opts.MaxBackups > 0 && i >= r.opts.MaxBackups
		tooOld := !cutoff.IsZero() && b.when.Before(cutoff)
		if tooMany || tooOld {
			if err := os.Remove(b.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				r.report("prune", fmt.Errorf("log file %s: %w", b.path, err))
			}
			continue
		}
		keep = append(keep, b)
	}
	if !r.opts.Compress {
		return
	}
	pending := false
	for _, b := range keep {
		if b.gz {
			continue
		}
		info, err := os.Stat(b.path)
		if err != nil {
			r.report("compress", fmt.Errorf("log file %s: %w", b.path, err))
		} else if time.Since(info.ModTime()) < r.grace {
			pending = true // still settling; see compressGrace
			continue
		}
		if err := compressFile(b.path); err != nil {
			r.report("compress", fmt.Errorf("log file %s: %w", b.path, err))
		}
	}
	if pending {
		r.mu.Lock()
		if !r.closed {
			r.millTimer = time.AfterFunc(r.grace, func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.signalMill()
			})
		}
		r.mu.Unlock()
	}
}

// backups lists this file's rotated copies, newest first. Only names that parse
// as <stem>-<stamp>[-n]<ext>[.gz] count, so other files sharing the directory
// are never touched.
func (r *RotatingFile) backups() ([]backup, error) {
	dir, stem, ext := r.parts()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []backup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") && strings.HasPrefix(name, stem+"-") {
			// A compression cut short by the process dying. Once it is old
			// enough that no live compression can own it, it is litter.
			if info, err := e.Info(); err == nil && r.now().Sub(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		rest, ok := strings.CutPrefix(name, stem+"-")
		if !ok {
			continue
		}
		gz := strings.HasSuffix(rest, ext+".gz")
		rest = strings.TrimSuffix(rest, ".gz")
		rest, ok = strings.CutSuffix(rest, ext)
		if !ok || len(rest) < len(backupStamp) {
			continue
		}
		when, err := time.Parse(backupStamp, rest[:len(backupStamp)])
		if err != nil {
			continue
		}
		if tail := rest[len(backupStamp):]; tail != "" && !isCounter(tail) {
			continue
		}
		out = append(out, backup{path: filepath.Join(dir, name), when: when, gz: gz})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].when.Equal(out[j].when) {
			return out[i].when.After(out[j].when)
		}
		return out[i].path > out[j].path
	})
	return out, nil
}

func isCounter(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// compressFile gzips src to src.gz and removes src. It writes through a temp
// file so a crash mid-way never leaves a truncated .gz that looks complete.
func compressFile(src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(src), filepath.Base(src)+".*.gz.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed
	_ = tmp.Chmod(0o600)
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, in); err != nil {
		tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, src+".gz"); err != nil {
		return err
	}
	return os.Remove(src)
}

// ensureDir creates a log directory, and any parent it has to create,
// owner-only. A directory that already exists is left as it is — it may be one
// the user pointed OGCODE_LOG_DIR at, such as /var/log.
func ensureDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
