package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

// maxRootEntries bounds one directory scan. A skills root is hand-authored; a
// directory with thousands of entries is a wrong path, not a skill library, and
// stat-ing every one of them would stall the turn it was discovered in.
//
// It counts entries examined rather than skills found, because the cost being
// bounded is the scan itself — a root full of directories that are not skills
// is exactly the case worth stopping early.
const maxRootEntries = 200

// projectSkillDirs are the per-project skill locations, relative to a project
// directory. ".agents/skills" is ogcode's own — it sits beside the AGENT.md
// convention ogcode already follows, and outside ".ogcode/", which holds
// runtime state rather than files the user authors. ".claude/skills" is read
// for compatibility, so a skill written for Claude Code works here unchanged.
var projectSkillDirs = [][]string{
	{".agents", "skills"},
	{".claude", "skills"},
}

// ProjectRoots returns the project-scoped skill directories in effect for dir,
// walking up to the repo root so a skill still resolves when ogcode is launched
// from a subdirectory. Roots are returned outermost-first, so a caller
// registering them in order lets the innermost — the one closest to the work —
// take precedence.
func ProjectRoots(dir string) []string {
	// Without a project directory there is no project to scan. Falling through
	// would join relative paths and quietly scan whatever directory the process
	// happens to be running in.
	if dir == "" {
		return nil
	}
	var roots []string
	current := dir
	for {
		for _, parts := range projectSkillDirs {
			roots = append(roots, filepath.Join(append([]string{current}, parts...)...))
		}
		// Stop once the repo root has been covered, so an unrelated .claude or
		// .agents directory outside the repo never contributes skills. .git is
		// checked for existence only: a worktree or submodule has a .git file
		// rather than a directory, and it marks the boundary just as well.
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	// Reverse to outermost-first, matching how AGENT.md files are layered.
	for i, j := 0, len(roots)-1; i < j; i, j = i+1, j-1 {
		roots[i], roots[j] = roots[j], roots[i]
	}
	return roots
}

// GlobalRoots returns the user-scoped skill directories, ordered so that later
// entries win. "~/.config/ogcode/skills" is ogcode's own, beside the config.json
// it already reads there; "~/.ogcode/skills" sits with the rest of ogcode's home
// state; the ".agents" and ".claude" pair mirror the project locations so a
// user's existing library is found without being moved.
func GlobalRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".config", "ogcode", "skills"),
		filepath.Join(home, ".ogcode", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
	}
}

// ScanRoot reads one skill root and returns the skills it holds, sorted by
// name, alongside a diagnostic per file that failed to parse.
//
// A root is normally a directory of skill directories, each holding a SKILL.md.
// A root that holds a SKILL.md directly is taken as a single skill, so a
// configured path may point either at a library or at one skill.
//
// A missing root is not an error — most of the locations ogcode looks in do not
// exist in any given project.
func ScanRoot(root string, source Source) ([]Skill, []error) {
	root = filepath.Clean(root)

	if info, err := os.Stat(filepath.Join(root, Filename)); err == nil && !info.IsDir() {
		s, err := loadFrom(root, source)
		if err != nil {
			return nil, []error{err}
		}
		return []Skill{s}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read skills dir %s: %w", root, err)}
	}

	var skills []Skill
	var problems []error
	for i, e := range entries {
		if i >= maxRootEntries {
			problems = append(problems, fmt.Errorf("skills dir %s holds %d entries; only the first %d were examined", root, len(entries), maxRootEntries))
			break
		}
		// Stat rather than trusting the entry type, so a symlinked skill
		// directory is followed like a real one.
		dir := filepath.Join(root, e.Name())
		info, err := os.Stat(filepath.Join(dir, Filename))
		if err != nil || info.IsDir() {
			continue
		}
		s, err := loadFrom(dir, source)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, problems
}

// loadFrom parses the SKILL.md in dir and stamps it with its source.
func loadFrom(dir string, source Source) (Skill, error) {
	path := filepath.Join(dir, Filename)
	s, err := Load(path)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %s: %w", path, err)
	}
	s.Source = source
	return s, nil
}

// SiblingFiles returns up to limit files shipped alongside a skill's SKILL.md,
// as paths relative to the skill directory. The skill body routinely points at
// scripts and references by relative path; listing what is actually there means
// the agent reads the file that exists instead of guessing at a name.
//
// The second return reports whether the listing was cut short, so the caller can
// say so rather than presenting a sample as the whole set.
func SiblingFiles(dir string, limit int) ([]string, bool) {
	var files []string
	truncated := false

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip VCS and dependency directories: a skill that ships a node
			// package would otherwise fill the entire listing with its tree.
			if path != dir {
				switch d.Name() {
				case ".git", "node_modules", "vendor", "__pycache__", ".venv":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() == Filename && filepath.Dir(path) == dir {
			return nil
		}
		if len(files) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		// WalkDir only surfaces an error the callback returned, and this one
		// never returns one — but a partial listing is still worth returning,
		// and it is sorted like any other.
		slog.Warn("skills: sibling file listing cut short", "dir", dir, "err", err)
	}
	sort.Strings(files)
	return files, truncated
}
