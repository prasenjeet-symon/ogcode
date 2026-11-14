package logging

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a fake time source that advances one second per call, so every
// rotation gets a distinct stamp and every write re-checks the path, without
// sleeping.
func clock(start time.Time) func() time.Time {
	var mu sync.Mutex
	t := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Second)
		return t
	}
}

// openAt opens path on a fake clock with no compression grace.
func openAt(t *testing.T, path string, opts RotateOptions, start time.Time) *RotatingFile {
	t.Helper()
	r, err := openRotating(path, opts, clock(start), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func openTest(t *testing.T, opts RotateOptions) (*RotatingFile, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "logs")
	r := openAt(t, filepath.Join(dir, "ogcode.log"), opts, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	return r, dir
}

// logFiles lists the directory's files, sorted.
func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func writeLines(t *testing.T, r *RotatingFile, n int, line string) {
	t.Helper()
	for range n {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRotatesBeforeExceedingMaxSize(t *testing.T) {
	r, dir := openTest(t, RotateOptions{MaxSize: 100})
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two fit, a third would not
	writeLines(t, r, 5, line)
	r.Close()

	files := logFiles(t, dir)
	if len(files) != 3 {
		t.Fatalf("want the active file and two backups, got %v", files)
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 100 {
			t.Errorf("%s is %d bytes, past the 100-byte limit", name, info.Size())
		}
	}
	if !strings.HasPrefix(files[0], "ogcode-20260930T10") || !strings.HasSuffix(files[0], "Z.log") {
		t.Errorf("backup name %q does not carry the UTC stamp", files[0])
	}
}

// A record bigger than the limit is written whole rather than split or dropped.
func TestOversizedRecordIsWrittenWhole(t *testing.T) {
	r, dir := openTest(t, RotateOptions{MaxSize: 10})
	big := strings.Repeat("y", 50)
	writeLines(t, r, 1, big)
	r.Close()
	b, err := os.ReadFile(filepath.Join(dir, "ogcode.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != big {
		t.Errorf("active file holds %q, want the whole record", b)
	}
}

func TestKeepsOnlyMaxBackups(t *testing.T) {
	r, dir := openTest(t, RotateOptions{MaxSize: 10, MaxBackups: 2})
	writeLines(t, r, 8, "0123456789")
	r.Close() // waits for the pruning goroutine

	files := logFiles(t, dir)
	if len(files) != 3 {
		t.Fatalf("want the active file plus 2 backups, got %v", files)
	}
}

func TestDropsBackupsOlderThanMaxAge(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "ogcode-20260101T000000.000Z.log")
	recent := filepath.Join(dir, "ogcode-20260929T000000.000Z.log.gz")
	unrelated := filepath.Join(dir, "ogcode-notes.log")
	for _, p := range []string{old, recent, unrelated} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := openAt(t, filepath.Join(dir, "ogcode.log"), RotateOptions{MaxSize: 1 << 20, MaxAge: 7 * 24 * time.Hour},
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	r.Close() // waits for the pass OpenRotating started

	if exists(old) {
		t.Error("a backup past MaxAge survived")
	}
	if !exists(recent) {
		t.Error("a backup inside MaxAge was removed")
	}
	if !exists(unrelated) {
		t.Error("a file that is not one of our backups was removed")
	}
}

func TestCompressesBackups(t *testing.T) {
	r, dir := openTest(t, RotateOptions{MaxSize: 20, Compress: true})
	writeLines(t, r, 1, "first line of text\n")
	writeLines(t, r, 1, "second line\n")
	r.Close()

	var gz string
	for _, name := range logFiles(t, dir) {
		if strings.HasSuffix(name, ".log.gz") {
			gz = name
		} else if name != "ogcode.log" {
			t.Errorf("uncompressed backup %s left behind", name)
		}
	}
	if gz == "" {
		t.Fatalf("no compressed backup in %v", logFiles(t, dir))
	}
	f, err := os.Open(filepath.Join(dir, gz))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "first line of text\n" {
		t.Errorf("backup decompresses to %q", b)
	}
}

// When another process rotates the shared file, this one follows it to the new
// file on its next write instead of writing on into the backup — and it counts
// the other process's writes toward the size it rotates on.
func TestFollowsRotationByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	opts := RotateOptions{MaxSize: 30}
	a := openAt(t, path, opts, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	b := openAt(t, path, opts, time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC))

	writeLines(t, a, 1, "a1 aaaaaaaaaaaaaaaaaaaaaaa\n") // 27 bytes
	writeLines(t, b, 1, "b1\n")                         // b sees a's 27: 30 fits exactly
	writeLines(t, a, 1, "a2\n")                         // a sees 30: rotates
	writeLines(t, b, 1, "b2\n")                         // b must follow, not write into the backup

	files := logFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("want one rotation (2 files), got %v", files)
	}
	backup, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if want := "a1 aaaaaaaaaaaaaaaaaaaaaaa\nb1\n"; string(backup) != want {
		t.Errorf("backup holds %q, want %q", backup, want)
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(cur) != "a2\nb2\n" {
		t.Errorf("active file holds %q, want both processes' lines after the rotation", cur)
	}
}

// Deleting the live log file (rm, a cleanup script) does not silence logging:
// the next write recreates it.
func TestRecreatesDeletedFile(t *testing.T) {
	r, dir := openTest(t, RotateOptions{MaxSize: 1 << 20})
	writeLines(t, r, 1, "before\n")
	if err := os.Remove(filepath.Join(dir, "ogcode.log")); err != nil {
		t.Fatal(err)
	}
	writeLines(t, r, 1, "after\n")
	b, err := os.ReadFile(filepath.Join(dir, "ogcode.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "after\n" {
		t.Errorf("recreated file holds %q", b)
	}
}

// A rotated file still being written is not compressed until it settles.
func TestCompressionWaitsForGrace(t *testing.T) {
	dir := t.TempDir()
	r, err := openRotating(filepath.Join(dir, "ogcode.log"), RotateOptions{MaxSize: 10, Compress: true},
		clock(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	writeLines(t, r, 2, "0123456789")
	r.Close()
	for _, name := range logFiles(t, dir) {
		if strings.HasSuffix(name, ".gz") {
			t.Errorf("%s was compressed inside the grace period", name)
		}
	}
}

func TestFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	r, dir := openTest(t, RotateOptions{MaxSize: 10, Compress: true})
	writeLines(t, r, 3, "0123456789")
	r.Close()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("log directory is %o, want 700", perm)
	}
	for _, name := range logFiles(t, dir) {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s is %o, want 600", name, perm)
		}
	}
}

// A pre-existing log file with loose permissions is narrowed on open.
func TestNarrowsLooseExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ogcode.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRotating(path, RotateOptions{MaxSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("existing file left at %o, want 600", perm)
	}
}

// A directory that already exists is used as it is: its permissions are not
// changed and nothing is written into it besides the log itself, since it may
// be a shared one the user pointed OGCODE_LOG_DIR at.
func TestLeavesAnExistingDirectoryAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	existing := t.TempDir()
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRotating(filepath.Join(existing, "ogcode.log"), RotateOptions{MaxSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("existing directory changed to %o", perm)
	}
	if files := logFiles(t, existing); len(files) != 1 || files[0] != "ogcode.log" {
		t.Errorf("existing directory holds %v, want only the log", files)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	r, _ := openTest(t, RotateOptions{MaxSize: 1 << 20})
	r.Close()
	if _, err := r.Write([]byte("late\n")); err == nil {
		t.Error("write after Close succeeded")
	}
}

// A rotation whose rename fails is reported to the error handler, and logging
// carries on into the oversized file.
func TestRenameFailureIsReportedAndWritingContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ogcode.log")
	origRename := renameFile
	replaced := errors.New("rename blocked")
	renameFile = func(from, to string) error { return replaced }
	t.Cleanup(func() { renameFile = origRename })

	var mu sync.Mutex
	var ops []string
	r := openAt(t, path, RotateOptions{MaxSize: 30}, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	r.SetErrorHandler(func(op string, err error) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, op)
	})
	line := strings.Repeat("a", 29) + "\n"
	writeLines(t, r, 3, line)
	r.Close()

	mu.Lock()
	got := append([]string(nil), ops...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "rotate" {
		t.Errorf("error handler saw %v, want [rotate]", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != strings.Repeat(line, 3) {
		t.Errorf("file holds %d bytes after a failed rotation, want all 3 lines", len(b))
	}
}

// When the rename keeps failing and the live file has grown past twice
// MaxSize, it is truncated in place: disk use is bounded and the tail is
// dropped, and logging continues from a fresh file.
func TestTruncatesLiveFileWhenRotationKeepsFailing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ogcode.log")
	origRename := renameFile
	renameFile = func(from, to string) error { return errors.New("rename blocked") }
	t.Cleanup(func() { renameFile = origRename })

	var mu sync.Mutex
	var bounded string
	r := openAt(t, path, RotateOptions{MaxSize: 40}, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	r.SetErrorHandler(func(op string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if op == "rotate" && strings.Contains(err.Error(), "truncated in place") && strings.Contains(err.Error(), "tail dropped") {
			bounded = err.Error()
		}
	})
	line := strings.Repeat("a", 39) + "\n"
	// rotationRetry is a minute away on the fake clock (one second per call),
	// so keep writing until a retry of the rename grows past twice MaxSize and
	// gets truncated.
	for i := 0; i < 200; i++ {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		boundedYet := bounded != ""
		mu.Unlock()
		if boundedYet {
			break
		}
	}
	r.Close()

	if bounded == "" {
		t.Error("no report that the disk was bounded by truncation")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != line {
		t.Errorf("file holds %d bytes, want only the post-truncation line", len(b))
	}
	files := logFiles(t, dir)
	if len(files) != 1 || files[0] != "ogcode.log" {
		t.Errorf("directory holds %v, want only the live file", files)
	}
}

// A handler that panics costs its one report, not the log.
func TestErrorHandlerPanicDoesNotStopLogging(t *testing.T) {
	origRename := renameFile
	renameFile = func(from, to string) error { return errors.New("rename blocked") }
	t.Cleanup(func() { renameFile = origRename })

	r, dir := openTest(t, RotateOptions{MaxSize: 30})
	r.SetErrorHandler(func(op string, err error) { panic("handler bug") })
	line := strings.Repeat("a", 29) + "\n"
	writeLines(t, r, 2, line) // the second write rotates and reports
	r.Close()
	b, err := os.ReadFile(filepath.Join(dir, "ogcode.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != strings.Repeat(line, 2) {
		t.Errorf("file holds %d bytes, want both lines", len(b))
	}
}

// A reopen that fails (the log directory went read-only out from under the
// live line) is reported, not swallowed.
func TestReopenFailureIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ogcode.log")
	r := openAt(t, path, RotateOptions{MaxSize: 1 << 20}, time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	writeLines(t, r, 1, "first\n")
	// The live line is deleted while the directory still lets us delete it,
	// then the directory is made read-only so reopening cannot recreate it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	var mu sync.Mutex
	var ops []string
	r.SetErrorHandler(func(op string, err error) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, op)
	})
	_, _ = r.Write([]byte("second\n")) // the reopen fails; Write returns the error
	// Restore the directory before anything cleans up, so leftovers can go.
	os.Chmod(dir, 0o700)

	mu.Lock()
	got := append([]string(nil), ops...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "reopen" {
		t.Errorf("error handler saw %v, want [reopen]", got)
	}
}

// Sync flushes only unsynced writes: a clean file is a no-op, a written one
// flushes once and then counts as clean, and Close flushes what is left.
func TestSyncFlushesUnsyncedWritesOnly(t *testing.T) {
	r, _ := openTest(t, RotateOptions{MaxSize: 1 << 20})
	origSync := syncFile
	var mu sync.Mutex
	flushes := 0
	syncFile = func(f *os.File) error {
		mu.Lock()
		flushes++
		mu.Unlock()
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = origSync })

	if err := r.Sync(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if flushes != 0 {
		t.Errorf("Sync with no writes flushed %d times, want 0", flushes)
	}
	mu.Unlock()

	writeLines(t, r, 1, "x\n")
	if err := r.Sync(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if flushes != 1 {
		t.Errorf("Sync after a write flushed %d times, want 1", flushes)
	}
	mu.Unlock()

	if err := r.Sync(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if flushes != 1 {
		t.Errorf("a second Sync flushed again: %d flushes, want 1", flushes)
	}
	mu.Unlock()

	writeLines(t, r, 1, "y\n")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if flushes != 2 {
		t.Errorf("Close flushed %d times total, want the one dirty write", flushes)
	}
	mu.Unlock()
}

// The mill goroutine flushes unsynced writes on its timer, so a crash cannot
// lose more than one tick of tail.
func TestMillLoopFlushesPeriodically(t *testing.T) {
	origEvery := syncEvery
	syncEvery = 20 * time.Millisecond
	t.Cleanup(func() { syncEvery = origEvery })
	origSync := syncFile
	var mu sync.Mutex
	flushes := 0
	syncFile = func(f *os.File) error {
		mu.Lock()
		flushes++
		mu.Unlock()
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = origSync })

	r, _ := openTest(t, RotateOptions{MaxSize: 1 << 20})
	writeLines(t, r, 1, "periodic\n")
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := flushes
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the mill goroutine never flushed the unsynced write")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Close()
}
