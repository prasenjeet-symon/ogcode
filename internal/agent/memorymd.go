package agent

import (
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

const memoryMDFilename = "MEMORY.md"

// memoryFiles is what the prompt discovers about the project's memory
// documents: where they live, never what they say. The prompt lists these
// paths and the agent opens them with file_map and read, so editing a memory
// file no longer rewrites the cached system prompt.
//
// Discovery still runs once per turn: a file appearing or disappearing is
// exactly the event the pointer has to report, and it is rare. Content changes
// do not touch the path list.
type memoryFiles struct {
	agentMD   []string
	memoryMD  []string
	sessionMD string
}

// discoverMemoryFiles resolves the three kinds of memory file for dir.
// sessionKey is the ROOT session's key (see sessionMemoryKey), so a teammate
// shares its parent conversation's file.
func discoverMemoryFiles(dir, sessionKey string) memoryFiles {
	return memoryFiles{
		agentMD:   agentMDPaths(dir),
		memoryMD:  memoryMDPaths(dir),
		sessionMD: memfile.SessionMemoryRelPath(dir, sessionKey),
	}
}

// sessionMemoryKey returns the id a session's memory file is keyed by. When
// the resolver says this session is a teammate, that is the root session —
// the user-facing conversation the tree serves — so the whole conversation,
// the parent session and the teammates it spawned, share one file. Otherwise
// it is the session's own id.
func sessionMemoryKey(sessionID session.SessionID, resolver func(session.SessionID) (session.SessionID, string)) string {
	if resolver != nil {
		if root, _ := resolver(sessionID); root != "" {
			return string(root)
		}
	}
	return string(sessionID)
}

// memoryMDPaths walks from dir up to the filesystem root collecting MEMORY.md
// paths, root-to-leaf (outermost first). As with agentMDPaths the files are not
// read: a change to MEMORY.md's contents must not rewrite the cached prompt.
func memoryMDPaths(dir string) []string {
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
