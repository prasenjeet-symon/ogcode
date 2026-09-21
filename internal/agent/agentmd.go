package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentMDFilename = "AGENT.md"
	maxAgentMDSize  = 32 * 1024 // 32KB max total across all files
)

// agentMDFilenames are the instruction files read from each directory, in the
// order they are added to the prompt.
//
// AGENTS.md is the cross-tool convention several other agents already read, so
// honouring it means a project that has one needs no ogcode-specific file at
// all. AGENT.md is ogcode's own name and comes second, which under the
// root-to-leaf ordering below puts it closer to the model — a project keeping
// both gets the generic instructions first and its ogcode-specific ones last,
// matching how the outer-to-inner precedence already works.
var agentMDFilenames = []string{"AGENTS.md", agentMDFilename}

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
	// Content already included, so a project carrying AGENTS.md and AGENT.md with
	// the same text — a copy, or two tools generating the same file — states its
	// instructions once rather than twice. Only an exact match is skipped: files
	// that genuinely differ are both the author's, and dropping either would lose
	// something they wrote.
	seen := make(map[string]bool, len(paths))

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
		if seen[trimmed] {
			slog.Debug("skipping duplicate agent instructions", "path", p)
			continue
		}
		seen[trimmed] = true

		remaining := maxAgentMDSize - totalSize
		if remaining <= 0 {
			slog.Warn("AGENT.md total size exceeds limit, skipping remaining files", "limit", maxAgentMDSize)
			break
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
		block, used, truncated := renderMDBlock("agent-md", relPath, trimmed, remaining)
		if truncated {
			slog.Warn("AGENT.md truncated due to size limit", "path", p, "limit", maxAgentMDSize)
		}
		b.WriteString(block)
		totalSize += used
	}

	return b.String()
}

// discoverAgentMDPaths walks from dir up to the filesystem root,
// collecting AGENT.md file paths. Returns paths in root-to-leaf order
// (outermost first, innermost last).
func discoverAgentMDPaths(dir string) []string {
	// Per directory, innermost-last overall. Collected as groups so a directory
	// holding both names keeps them adjacent and in agentMDFilenames order after
	// the reversal below.
	var groups [][]string

	current := dir
	for {
		var found []string
		for _, name := range agentMDFilenames {
			candidate := filepath.Join(current, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				found = append(found, candidate)
			}
		}
		if len(found) > 0 {
			groups = append(groups, found)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	// Reverse to get root-to-leaf order, flattening each directory's group with
	// its own order intact.
	var paths []string
	for i := len(groups) - 1; i >= 0; i-- {
		paths = append(paths, groups[i]...)
	}
	return paths
}
