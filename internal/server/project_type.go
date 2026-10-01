package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/gitignore"
	"github.com/prasenjeet-symon/ogcode/internal/indexer"
)

// projectReportedFilename marks a project as already reported, so its type is
// captured once rather than on every server start. It lives in the project's
// own .ogcode directory — the same place its database does — which scopes the
// marker to one project by construction: a fresh clone reports again, which is
// the intent.
const projectReportedFilename = "project-reported"

// projectTypeEvent is the analytics event carrying a project's detected type.
const projectTypeEvent = "ogcode_project_detected"

// projectTypeByExt maps a source-file extension to the project type it votes
// for. Only source extensions appear: the type is decided among languages a
// project is written in, not among the data, config and generated files every
// project also carries. The set is deliberately small so the reported value
// stays a low-cardinality label rather than an extension census.
//
// A few extensions are shared and fold into one vote: Kotlin and Scala file
// under Java (the JVM it runs on), and a bare .h or .c joins C++ — a header
// alone does not name its language, and C is too rare as a project's only
// language to earn a label of its own.
var projectTypeByExt = map[string]string{
	".go": "go",

	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",

	".py": "python", ".pyi": "python", ".pyw": "python",

	".rs": "rust",

	".java": "java", ".kt": "java", ".kts": "java", ".scala": "java",

	".cs": "csharp", ".csx": "csharp",

	".c": "cpp", ".h": "cpp", ".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp",

	".swift": "swift",
	".dart":  "dart",
	".rb":    "ruby",
	".php":   "php", ".phtml": "php",

	".html": "html", ".htm": "html", ".css": "html", ".scss": "html", ".sass": "html",
	".vue": "vue", ".svelte": "svelte",

	".sh": "shell", ".bash": "shell", ".zsh": "shell",
}

// detectProjectType walks dir and returns the project type that owns the most
// source files, or "" when the tree holds none ogcode recognises.
//
// The walk is the same shape the indexer uses — .gitignore first, then the
// shipped generated-name excludes (node_modules, dist, vendor and the rest),
// with ogcode's own state directory pruned unconditionally — so what it counts
// is the source a project actually keeps, not its dependencies or build
// output. Only the extension is read, never a file's name or contents.
//
// An empty directory, an unreadable one, or one holding only unknown
// extensions all return "": there is nothing to report yet, and the caller
// treats that as "not this time" rather than a type of "unknown".
func detectProjectType(dir string) string {
	if dir == "" {
		return ""
	}
	excludes := docindex.DefaultExcludePatterns()
	ignored := gitignore.New(dir)
	counts := map[string]int{}

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			name := d.Name()
			if name == indexer.StateDirName || name == ".git" {
				return filepath.SkipDir
			}
			if nameExcluded(name, excludes) {
				return filepath.SkipDir
			}
			if ignored.Match(path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if nameExcluded(filepath.Base(path), excludes) {
			return nil
		}
		if ignored.Match(path, false) {
			return nil
		}
		if projectType, ok := projectTypeByExt[strings.ToLower(filepath.Ext(path))]; ok {
			counts[projectType]++
		}
		return nil
	})

	return dominantProjectType(counts)
}

// nameExcluded mirrors the indexer's rule (internal/indexer.IsExcluded): a
// pattern matches a name either exactly or as a glob, and it applies to the
// name alone, in any directory.
func nameExcluded(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == name {
			return true
		}
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// dominantProjectType returns the type with the highest count, breaking a tie
// by alphabetical order so the result never depends on map iteration order.
func dominantProjectType(counts map[string]int) string {
	best, bestN := "", 0
	for projectType, n := range counts {
		if n > bestN || (n == bestN && projectType < best) {
			best, bestN = projectType, n
		}
	}
	return best
}

// reportProjectTypeOnce detects the workspace's project type and captures it,
// once per project. It is a no-op when there is no directory, no capture sink,
// or a marker already recording a report for this project, and when the tree
// holds no recognisable source it captures nothing and leaves no marker, so a
// later start — once the project has source files — still reports.
//
// The event rides the same anonymous server identity as ogcode_server_started
// (posthogDistinctID), so a machine's project mix aggregates under one id.
func reportProjectTypeOnce(dir string, capture func(event, distinctID string, props map[string]any)) {
	if dir == "" || capture == nil {
		return
	}
	marker := filepath.Join(dir, indexer.StateDirName, projectReportedFilename)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	projectType := detectProjectType(dir)
	if projectType == "" {
		return
	}
	capture(projectTypeEvent, posthogDistinctID(), map[string]any{
		"type": projectType,
	})
	// Best-effort: failing to write the marker costs at most a duplicate event.
	_ = os.MkdirAll(filepath.Dir(marker), 0o755)
	_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}
