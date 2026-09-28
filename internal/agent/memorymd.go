package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	memoryMDFilename = "MEMORY.md"
	// memoryMDBudgetEnv optionally caps the total bytes of MEMORY.md content
	// interpolated into the system prompt across all discovered files. Unset —
	// the default — means no cap at all: every file is loaded whole.
	memoryMDBudgetEnv = "OGCODE_MEMORY_MD_MAX_BYTES"
)

// LoadMemoryMD discovers and loads MEMORY.md files by walking from dir up to
// the filesystem root. Files are returned in root-to-leaf order (outermost
// first), so that closer/leaf files appear later in the concatenated result
// and naturally take precedence for the LLM.
//
// There is no size limit by default — the whole of every discovered file
// reaches the model. Set OGCODE_MEMORY_MD_MAX_BYTES to a positive number of
// bytes to cap the total, in which case the first file that would overrun it is
// cut (on a rune boundary, with a marker) and the remainder are skipped.
//
// Missing files are silently skipped. Permission or read errors are logged
// as warnings and the file is skipped.
func LoadMemoryMD(dir string) string {
	paths := discoverMemoryMDPaths(dir)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder
	var totalSize int
	// 0 means no budget: include every file whole. A positive value is the total
	// across all discovered files.
	limit := mdSizeLimit(memoryMDBudgetEnv)

	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("failed to read MEMORY.md", "path", p, "err", err)
			}
			continue
		}

		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}

		// remaining is 0 (uncapped) unless an operator set a budget, in which
		// case it is what is left of that budget for this file.
		remaining := 0
		if limit > 0 {
			remaining = limit - totalSize
			if remaining <= 0 {
				slog.Warn("MEMORY.md total size exceeds budget, skipping remaining files", "limit", limit)
				break
			}
		}

		relPath, err := filepath.Rel(dir, p)
		if err != nil {
			relPath = p
		}
		// Normalize to forward slashes so the path is identical on every OS.
		// filepath.Rel returns OS-native separators (backslashes on Windows),
		// which would leak Windows-style paths into the system prompt and break
		// the Anthropic prompt-cache prefix byte-for-byte across platforms.
		relPath = filepath.ToSlash(relPath)

		// Rendered rather than formatted in place: the file's own text is
		// neutralized so it cannot close this block or forge the other one, and
		// the cut to the remaining budget lands on a rune boundary. See
		// renderMDBlock.
		block, used, truncated := renderMDBlock("memory-md", relPath, trimmed, remaining)
		if truncated {
			slog.Warn("MEMORY.md truncated due to size budget", "path", p, "limit", limit)
		}
		b.WriteString(block)
		totalSize += used
	}

	return b.String()
}

// discoverMemoryMDPaths walks from dir up to the filesystem root,
// collecting MEMORY.md file paths. Returns paths in root-to-leaf order
// (outermost first, innermost last).
func discoverMemoryMDPaths(dir string) []string {
	var paths []string

	current := dir
	for {
		candidate := filepath.Join(current, memoryMDFilename)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			paths = append(paths, candidate)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	// Reverse to get root-to-leaf order
	for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
		paths[i], paths[j] = paths[j], paths[i]
	}

	return paths
}
