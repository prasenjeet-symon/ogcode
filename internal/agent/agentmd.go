package agent

import (
	"os"
	"path/filepath"
)

const agentMDFilename = "AGENT.md"

// agentMDFilenames are the instruction files discovered in each directory.
//
// AGENTS.md is the cross-tool convention several other agents already read, so
// honouring it means a project that has one needs no ogcode-specific file at
// all. AGENT.md is ogcode's own name and comes second, which under the
// root-to-leaf ordering below puts it closer to the model — a project keeping
// both gets the generic instructions first and its ogcode-specific ones last,
// matching how the outer-to-inner precedence already works.
var agentMDFilenames = []string{"AGENTS.md", agentMDFilename}

// agentMDPaths discovers the AGENTS.md/AGENT.md files that govern dir by
// walking from dir up to the filesystem root. Paths come back root-to-leaf
// (outermost first), so a closer file appears later and takes precedence when
// two of them state a rule.
//
// The files are never read here. The prompt carries only their locations and
// the agent opens them with file_map and read, so editing an instruction file
// no longer rewrites the cached system prompt. A file that exists is listed
// even when it is empty: that is the file a writable agent should fill in
// rather than create a second one beside it.
func agentMDPaths(dir string) []string {
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
