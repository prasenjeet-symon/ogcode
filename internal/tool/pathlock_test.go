package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink and its target are one file to writeFileAtomic, which resolves the
// link before renaming — so they must be one file to the lock too, or an edit
// via the link and an edit via the target serialize on different mutexes and
// race on the same bytes.
func TestLockKey_SymlinkAndTargetShareOneLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.go")
	mustWriteFile(t, target, "package x\n")
	link := filepath.Join(dir, "alias.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if lockKey(link) != lockKey(target) {
		t.Errorf("link and target got different lock keys:\n link:   %q\n target: %q",
			lockKey(link), lockKey(target))
	}
}

// A symlinked directory is the same aliasing one level up: repo layouts that
// link a directory into place give one file two spellings through the parent.
// This also covers the file-does-not-exist-yet path, where the key comes from
// resolving the directory and keeping the base name.
func TestLockKey_SymlinkedParentSharesOneLock(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "pkg")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "pkglink")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	// The file need not exist: write creates files, and its lock must already
	// agree across spellings when it does.
	viaReal := filepath.Join(realDir, "new.go")
	viaLink := filepath.Join(linkDir, "new.go")
	if lockKey(viaReal) != lockKey(viaLink) {
		t.Errorf("spellings through a linked dir got different lock keys:\n real: %q\n link: %q",
			lockKey(viaReal), lockKey(viaLink))
	}
}

// On platforms whose default filesystems are case-insensitive, two case
// spellings name one file and must share one lock. Folding errs safely: on a
// case-sensitive volume it over-locks two distinct files, which merely
// serializes them, while not folding under-locks one file, which loses writes.
func TestLockKey_CaseSpellingsShareOneLock(t *testing.T) {
	if !caseInsensitivePaths {
		t.Skip("this platform treats path case as significant")
	}
	dir := t.TempDir()

	if lockKey(filepath.Join(dir, "Foo.go")) != lockKey(filepath.Join(dir, "foo.go")) {
		t.Error("case spellings of one file got different lock keys")
	}
}

// Different spellings of one path — relative pieces, dot segments — were
// already collapsed by Abs+Clean before this key existed; that must survive
// the canonicalization growing stricter.
func TestLockKey_CleanedSpellingsAgree(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "a", "b.go")
	dotted := filepath.Join(dir, "a", ".", "b.go")

	if lockKey(plain) != lockKey(dotted) {
		t.Errorf("dot-segment spelling got a different lock key:\n plain:  %q\n dotted: %q",
			lockKey(plain), lockKey(dotted))
	}
}
