package tool

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// fileLocks serializes the built-in mutating file tools (write, edit) that
// target the same file. The agent loop executes a turn's tool calls
// concurrently (goroutines + WaitGroup); without this, two write/edit calls
// aimed at the same file would race — a lost update, or edit reading a
// half-written file. Reads are intentionally NOT gated here: a torn read is
// recoverable (the model re-reads), whereas a lost write is silent data loss.
// bash is not covered either — a shell command can touch any path, so there is
// nothing to key its writes on; a bash mutation racing an edit in the same
// turn remains the caller's hazard.
//
// The map grows by one entry per distinct file touched in the process lifetime,
// which is bounded by the number of files a session edits — acceptable, and far
// cheaper than the alternative races.
var fileLocks sync.Map // map[string]*sync.Mutex

// caseInsensitivePaths reports whether this platform's default filesystems
// compare paths case-insensitively (APFS/HFS+ on macOS, NTFS on Windows).
// Folding the lock key there errs in the safe direction: on a case-SENSITIVE
// volume two genuinely different files share a lock and merely serialize,
// while not folding on a case-insensitive one hands two spellings of a single
// file different locks — exactly the race this map exists to prevent.
var caseInsensitivePaths = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// lockPath acquires the per-path mutex for path and returns the unlock func.
func lockPath(path string) func() {
	muIface, _ := fileLocks.LoadOrStore(lockKey(path), &sync.Mutex{})
	mu := muIface.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// lockKey canonicalizes path so every spelling of one file shares one lock:
// absolute and cleaned (so "a/b.go", "./a/b.go" and "/abs/a/b.go" agree), with
// symlinks resolved and, on case-insensitive platforms, case folded.
//
// The symlink resolution matters because the write path itself resolves links
// before renaming (see writeFileAtomic): a link and its target are one file to
// the write, so they must be one file here too, or an edit via the link races
// an edit via the target on different locks. The final component is resolved
// the same way the write will resolve it (resolveWriteTarget, dangling links
// included); parent directories go through EvalSymlinks, which needs them to
// exist — as any write into them does. ToLower is an approximation of the
// filesystem's own folding (APFS folds full Unicode), sufficient for the
// spellings that actually occur.
func lockKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	path = resolveWriteTarget(path)
	dir, base := filepath.Split(path)
	if rdir, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		path = filepath.Join(rdir, base)
	}
	if caseInsensitivePaths {
		path = strings.ToLower(path)
	}
	return path
}
