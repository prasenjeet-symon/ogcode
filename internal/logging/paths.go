package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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
