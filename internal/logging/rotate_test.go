package logging

import (
	"compress/gzip"
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
