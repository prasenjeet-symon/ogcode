package tool

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// tempFilesIn lists the leftover temp files writeFileAtomic may have stranded in
// a directory. A successful or failed write must leave none.
func tempFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var stray []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ogcode-write-") {
			stray = append(stray, e.Name())
		}
	}
	return stray
}

// The property the whole change exists for: the file is never observable in a
// half-written state. os.WriteFile opens with O_TRUNC, so a reader that lands
// between the truncate and the last byte sees a short file — and a write that
// fails in that window leaves one on disk permanently. Reading concurrently
// with a stream of large writes reliably catches that; against the previous
// implementation this test fails, against rename(2) it cannot.
func TestWriteFileAtomic_ReaderNeverSeesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")

	// Large enough that a non-atomic write spends real time in the torn state.
	before := bytes.Repeat([]byte("a"), 1<<20)
	after := bytes.Repeat([]byte("b"), 1<<20)
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}

	// The reader decides when the test ends, by taking a fixed number of
	// samples; the writer simply keeps swapping the file until then. Sizing it
	// the other way round makes the sample count depend on how fast the two
	// goroutines happen to run, which under -race is slow enough to leave too
	// few reads to conclude anything.
	const reads = 200

	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			content := before
			if i%2 == 1 {
				content = after
			}
			if err := writeFileAtomic(path, content); err != nil {
				t.Errorf("write %d: %v", i, err)
				return
			}
		}
	}()

	var torn int
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < reads; i++ {
			got, err := os.ReadFile(path)
			if err != nil || (!bytes.Equal(got, before) && !bytes.Equal(got, after)) {
				torn++
			}
		}
	}()
	wg.Wait()

	if torn > 0 {
		t.Errorf("%d of %d concurrent reads saw a file that was neither the old nor the new content", torn, reads)
	}
	if stray := tempFilesIn(t, dir); len(stray) > 0 {
		t.Errorf("temp files left behind after successful writes: %v", stray)
	}
}

// Rename replaces the inode, so the temp file's permissions become the file's.
// Without carrying the mode across, every executable script the agent edited
// would come back non-executable.
func TestWriteFileAtomic_PreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	for _, mode := range []os.FileMode{0o755, 0o600, 0o664, 0o444} {
		dir := t.TempDir()
		path := filepath.Join(dir, "f")
		if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := writeFileAtomic(path, []byte("after\n")); err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("mode after write = %v, want %v", got, mode)
		}
		if got, _ := os.ReadFile(path); string(got) != "after\n" {
			t.Errorf("content = %q, want %q", got, "after\n")
		}
	}
}

// A new file is created through the umask, exactly as os.WriteFile(…, 0o644)
// was — the temp file must not fix it at os.CreateTemp's private 0600, nor
// chmod past whatever the umask says.
func TestWriteFileAtomic_NewFileMatchesPlainWriteFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dir := t.TempDir()

	reference := filepath.Join(dir, "reference")
	if err := os.WriteFile(reference, []byte("x"), defaultFileMode); err != nil {
		t.Fatal(err)
	}
	refInfo, err := os.Stat(reference)
	if err != nil {
		t.Fatal(err)
	}

	created := filepath.Join(dir, "created")
	if err := writeFileAtomic(created, []byte("x")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(created)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), refInfo.Mode().Perm(); got != want {
		t.Errorf("new file mode = %v, want %v (what os.WriteFile produces here)", got, want)
	}
}

// Writing through a symlink updated the link's target and left the link a link.
// Renaming over the link itself would replace it with a regular file instead,
// silently breaking any repo that symlinks a file into place.
func TestWriteFileAtomic_FollowsSymlinkInsteadOfReplacingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(link, []byte("after\n")); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(target); string(got) != "after\n" {
		t.Errorf("link's target = %q, want the new content", got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	// The target's own mode survives the write through the link.
	if ti, err := os.Stat(target); err == nil && ti.Mode().Perm() != 0o755 {
		t.Errorf("target mode = %v, want 0755", ti.Mode().Perm())
	}
}

// A dangling link still names the file a write creates, so it is followed too —
// otherwise the rename would land on the link and quietly turn it into a file.
func TestWriteFileAtomic_FollowsDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "not-yet-there.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(link, []byte("created\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "created\n" {
		t.Errorf("dangling link's target = %q, want the written content", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
}

// When the rename cannot happen, the write must fail cleanly: the original
// untouched and no temp file stranded next to it.
func TestWriteFileAtomic_FailedRenameCleansUpAndKeepsOriginal(t *testing.T) {
	dir := t.TempDir()

	// A directory cannot be replaced by rename(2), so this fails at the last
	// step — after the temp file has been fully written.
	blocked := filepath.Join(dir, "target")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "child"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(blocked, []byte("new content\n")); err == nil {
		t.Fatal("expected an error renaming over a directory, got nil")
	}
	if got, err := os.ReadFile(filepath.Join(blocked, "child")); err != nil || string(got) != "keep me\n" {
		t.Errorf("the existing directory was disturbed: %q (%v)", got, err)
	}
	if stray := tempFilesIn(t, dir); len(stray) > 0 {
		t.Errorf("temp files left behind after a failed write: %v", stray)
	}
}

// The atomic path needs to create a sibling, so it needs a writable directory —
// where the in-place write it replaced only needed a writable file. Rather than
// start failing an edit that used to work, it falls back to writing in place.
func TestWriteFileAtomic_FallsBackWhenDirectoryIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "readonly")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "f.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The file stays writable; the directory does not.
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o755)

	if err := writeFileAtomic(path, []byte("after\n")); err != nil {
		t.Fatalf("expected the in-place fallback to succeed, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "after\n" {
		t.Errorf("content = %q, want %q", got, "after\n")
	}
}

// A link that points at itself must terminate rather than spin.
func TestResolveWriteTarget_SelfReferentialLinkTerminates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on windows")
	}
	dir := t.TempDir()
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	go func() { done <- resolveWriteTarget(loop) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("resolveWriteTarget did not terminate on a self-referential link")
	}
}
