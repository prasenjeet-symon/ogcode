package tool

import (
	"path/filepath"
	"sync"
)

// fileLocks serializes mutating file operations (write, edit) that target the
// same path. The agent loop executes a turn's tool calls concurrently
// (goroutines + WaitGroup); without this, two write/edit calls aimed at the same
// file would race — a lost update, or edit reading a half-written file. Reads are
// intentionally NOT gated here: a torn read is recoverable (the model re-reads),
// whereas a lost write is silent data loss.
//
// The map grows by one entry per distinct file touched in the process lifetime,
// which is bounded by the number of files a session edits — acceptable, and far
// cheaper than the alternative races.
var fileLocks sync.Map // map[string]*sync.Mutex

// lockPath acquires the per-path mutex for path and returns the unlock func. The
// key is the cleaned absolute path so different spellings of the same file
// ("a/b.go", "./a/b.go", "/abs/a/b.go") share one lock.
func lockPath(path string) func() {
	key := path
	if abs, err := filepath.Abs(path); err == nil {
		key = filepath.Clean(abs)
	}
	muIface, _ := fileLocks.LoadOrStore(key, &sync.Mutex{})
	mu := muIface.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
