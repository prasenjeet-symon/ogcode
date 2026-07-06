package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentMDFilename = "AGENT.md"
	maxAgentMDSize  = 32 * 1024 // 32KB max total across all files
)

// LoadAgentMD discovers and loads AGENT.md files by walking from dir up to
// the filesystem root. Files are returned in root-to-leaf order (outermost
// first), so that closer/leaf files appear later in the concatenated result
// and naturally take precedence for the LLM.
//
// Missing files are silently skipped. Permission or read errors are logged
// as warnings and the file is skipped.
func LoadAgentMD(dir string) string {
	paths := discoverAgentMDPaths(dir)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder
	var totalSize int

	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("failed to read AGENT.md", "path", p, "err", err)
			}
			continue
		}

		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}

		remaining := maxAgentMDSize - totalSize
		if remaining <= 0 {
			slog.Warn("AGENT.md total size exceeds limit, skipping remaining files", "limit", maxAgentMDSize)
			break
		}

		if len(trimmed) > remaining {
			trimmed = trimmed[:remaining]
			slog.Warn("AGENT.md truncated due to size limit", "path", p, "limit", maxAgentMDSize)
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

		fmt.Fprintf(&b, "\n\n<agent-md path=\"%s\">\n%s\n</agent-md>", relPath, trimmed)
		totalSize += len(trimmed)
	}

	return b.String()
}

// discoverAgentMDPaths walks from dir up to the filesystem root,
// collecting AGENT.md file paths. Returns paths in root-to-leaf order
// (outermost first, innermost last).
func discoverAgentMDPaths(dir string) []string {
	var paths []string

	current := dir
	for {
		candidate := filepath.Join(current, agentMDFilename)
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