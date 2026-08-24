package tool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
)

// defaultFileMode is what a newly created file gets, matching the mode
// os.WriteFile was called with before.
const defaultFileMode os.FileMode = 0o644

// maxWriteLinkHops bounds how far resolveWriteTarget will follow a chain of
// symlinks, so a link that points at itself cannot spin.
const maxWriteLinkHops = 16

// writeFileAtomic replaces the contents of path with data. It either fully
// succeeds or leaves the file exactly as it was.
//
// os.WriteFile opens with O_TRUNC, so it empties the file before the new bytes
// land. A failure partway through — a full disk, an I/O error, a quota — left a
// truncated file with the previous contents gone and no way back; the edit tool
// even held the entire original in memory and still could not put it back.
// Writing a temporary file in the same directory and renaming it over the
// target moves the only destructive step to rename(2), which is atomic: a
// concurrent reader sees either the whole old file or the whole new one, and
// any failure before the rename leaves the original untouched.
//
// Two properties of the in-place write are preserved deliberately:
//
//   - The file's mode. Rename replaces the inode, so a fresh temp file's
//     permissions would silently become the file's — stripping the executable
//     bit off every script the agent edits. The existing mode is read first and
//     applied to the temp file.
//
//   - Symlinks are followed, not replaced. Writing through a link updated its
//     target and left the link a link; renaming over the link itself would
//     instead replace it with a regular file, quietly breaking a repo that
//     symlinks a file into place. The link is resolved first so the rename
//     lands on the real file.
//
// Not preserved, both inherent to the temp-and-rename pattern every editor
// uses, and both far cheaper than the truncation this replaces: hard links to
// the target are broken by the rename, and the file ends up owned by this
// process.
//
// The data is deliberately not fsynced before the rename. What this fixes is a
// write that fails partway, which rename handles completely on its own;
// surviving a power cut on top of that would cost an F_FULLFSYNC per file on
// macOS, which is far too much to spend on every edit an agent makes.
func writeFileAtomic(path string, data []byte) error {
	target := resolveWriteTarget(path)

	mode, replacing := defaultFileMode, false
	if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
		mode, replacing = info.Mode().Perm(), true
	}

	// The temp file has to share the target's directory: rename(2) cannot cross
	// filesystems, and $TMPDIR frequently is one.
	dir := filepath.Dir(target)
	f, err := createTempFile(dir, mode)
	if err != nil {
		// A file can be writable inside a directory that is not: the atomic path
		// has to create a sibling, an in-place write does not. Rather than start
		// failing an edit that plainly used to work, fall back to what this
		// replaced. It is the one case that keeps the old truncation risk, and
		// only for as long as the directory stays read-only.
		if errors.Is(err, fs.ErrPermission) {
			return os.WriteFile(target, data, mode)
		}
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()

	// From here on, every failure has to take the temp file with it.
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}
	// A file that already existed keeps its mode exactly. The create above went
	// through the umask — right for a new file, since that is what os.WriteFile
	// did, but it must not narrow permissions a file already had.
	if replacing {
		if err := os.Chmod(tmp, mode); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("set mode on temp file: %w", err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

// tempSeq names successive temp files within this process.
var tempSeq atomic.Uint64

// createTempFile creates a new, exclusively held file in dir, opened with mode
// so the process umask applies to it exactly as it would have to the plain
// os.WriteFile this replaced — os.CreateTemp would instead fix it at 0600 and
// force a chmod that ignores umask entirely. The dot prefix keeps the file out
// of the way of anything watching the tree, in the event a crash strands one.
func createTempFile(dir string, mode os.FileMode) (*os.File, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		name := filepath.Join(dir, fmt.Sprintf(".ogcode-write-%d-%d", os.Getpid(), tempSeq.Add(1)))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no free temporary file name in %s", dir)
}

// resolveWriteTarget follows a symlink at path so the rename lands on the file
// the write is meant to change rather than on the link. A dangling link is
// followed too — a plain write through one creates the file at the far end.
// Anything that is not a link, or that cannot be read, comes back unchanged.
//
// Only the final component needs this. A symlinked parent directory is followed
// by the OS for both the temp file's creation and the rename, so both land in
// the same real directory and rename(2) stays within one filesystem.
func resolveWriteTarget(path string) string {
	for hops := 0; hops < maxWriteLinkHops; hops++ {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return path
		}
		link, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		path = filepath.Clean(link)
	}
	return path
}
