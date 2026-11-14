package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Root is the directory every ogcode log lives under: $OGCODE_LOG_DIR when set,
// otherwise ~/.ogcode/logs, beside the global config database. Logs never live
// inside a project, where they could be committed, indexed, or read back by
// the agent as if they were project content.
func Root(home string) string {
	if d := strings.TrimSpace(os.Getenv("OGCODE_LOG_DIR")); d != "" {
		return d
	}
	return filepath.Join(home, ".ogcode", "logs")
}

// ProjectDir is the log directory for the project at dir: one folder per
// project under root, so projects running side by side never share a file.
// The folder is named after the project, for a person looking for it, with a
// short hash of its path appended, so two checkouts that are both called "app"
// stay apart. The path is canonicalized the way portmap keys a project
// (absolute, symlinks resolved, cleaned), so one project has one log folder
// however its path was spelled.
func ProjectDir(root, dir string) string {
	canonical, err := filepath.Abs(dir)
	if err != nil {
		canonical = dir
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	canonical = filepath.Clean(canonical)

	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(root, folderName(filepath.Base(canonical))+"-"+hex.EncodeToString(sum[:4]))
}

// folderName reduces a project's base name to characters that are safe in a
// directory name on every platform, without a leading dot that would hide it.
func folderName(base string) string {
	const maxLen = 48
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= maxLen {
			break
		}
	}
	name := strings.Trim(b.String(), ".-")
	if name == "" {
		return "project"
	}
	return name
}

// projectMaxAgeDefault is how long a project's log folder is kept after its
// last write, when the setting does not say otherwise. Per-project files are
// bounded by rotation, but folders left behind by long-gone projects are not,
// so they get a clock too.
const projectMaxAgeDefault = 90 * 24 * time.Hour

// ProjectMaxAge is how long an untouched project's log folder is kept before
// PruneProjects removes it. OGCODE_LOG_PROJECT_MAX_AGE_DAYS sets it in whole
// days; 0 means never prune. A value that is unparseable or negative is
// ignored in favor of the default and reported at Debug — a typo must not
// silently change how long logs live, but PruneProjects runs before the
// process logger is up, so there is nowhere else to put the note. Parsed here
// rather than with envIntChecked in logging.go, which is a level above this
// package's path helpers and must not grow settings they do not own.
func ProjectMaxAge() time.Duration {
	v := strings.TrimSpace(os.Getenv("OGCODE_LOG_PROJECT_MAX_AGE_DAYS"))
	if v == "" {
		return projectMaxAgeDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Debug("ignoring unparseable log setting",
			"env", "OGCODE_LOG_PROJECT_MAX_AGE_DAYS", "value", v)
		return projectMaxAgeDefault
	}
	return time.Duration(n) * 24 * time.Hour
}

// PruneProjects removes every project log folder directly under root whose
// newest file is older than maxAge. maxAge <= 0 is a no-op. A folder is
// removed only when no file anywhere inside it was modified within the
// window, so a project still being written to is never pruned — that is what
// makes this safe to run while another ogcode works, by construction rather
// than by locking. Symlinks are never followed or removed, and root itself is
// left alone. A folder whose contents cannot be read cannot be shown stale,
// so it is kept and its error is collected; pruning carries on and the first
// error is returned once the pass completes.
func PruneProjects(root string, maxAge time.Duration) (removed int, err error) {
	if maxAge <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue // a ReadDir entry that is a symlink to a dir is not IsDir
		}
		dir := filepath.Join(root, e.Name())
		newest, walkErr := newestModTime(dir)
		if walkErr != nil {
			if firstErr == nil {
				firstErr = walkErr
			}
			continue
		}
		if newest.After(cutoff) {
			continue // something inside was written inside the window
		}
		if rmErr := os.RemoveAll(dir); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			if firstErr == nil {
				firstErr = rmErr
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// newestModTime is the most recent mod time of a regular file under dir,
// recursively. Symlinks are skipped and never descended into, so a folder
// cannot be kept fresh forever by a link out to a file that keeps changing.
// An error means the walk could not finish and the folder cannot be judged.
func newestModTime(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}
