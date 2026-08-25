package skill

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Config is the "skills" section of ogcode.json.
type Config struct {
	// Paths are extra skill directories. A path may point at a library of skill
	// directories or straight at one skill directory. Relative paths resolve
	// against the project directory.
	Paths []string
	// URLs are index.json manifests to fetch skills from. Empty — the default —
	// means no network work is ever done.
	URLs []string
	// Permissions maps a name pattern to allow, deny, or ask.
	Permissions map[string]string
}

// Enabled reports whether any configured source could contribute skills beyond
// the standard directories. It exists so a caller can tell an entirely default
// configuration from a customized one.
func (c Config) Enabled() bool {
	return len(c.Paths) > 0 || len(c.URLs) > 0 || len(c.Permissions) > 0
}

// Loader resolves the skills available for a project directory.
//
// Directory scans run per call, cheaply and always current: a skill the user
// writes mid-session is picked up on the next turn, the same way an edited
// AGENT.md is. Remote URLs are resolved once per process and cached on disk,
// because a network round trip inside the turn's critical path is not something
// to repeat.
type Loader struct {
	cfg      Config
	client   *http.Client
	cacheDir string

	mu     sync.Mutex
	remote map[string][]string // skills url -> local skill directories

	// reported is the set of diagnostics already logged. Load runs on every
	// turn — twice on a turn that uses the tool — so without this a single
	// malformed SKILL.md would write the same warning to the log for as long as
	// the session lasted, and the one that mattered would scroll away.
	//
	// It has its own mutex rather than sharing mu. mu is held across the remote
	// fetch, so a diagnostic logged from inside that critical section would
	// deadlock against itself; a separate lock makes that impossible to write
	// rather than something to remember.
	reportedMu sync.Mutex
	reported   map[string]bool
}

// NewLoader returns a Loader for cfg. Remote skills are cached under
// ~/.ogcode/cache/skills; when the home directory cannot be determined, remote
// URLs are skipped and everything else still works.
func NewLoader(cfg Config) *Loader {
	cacheDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		cacheDir = filepath.Join(home, ".ogcode", "cache", "skills")
	}
	return &Loader{
		cfg:      cfg,
		client:   &http.Client{Timeout: remoteTimeout},
		cacheDir: cacheDir,
		remote:   map[string][]string{},
		reported: map[string]bool{},
	}
}

// Load returns the skills in effect for a project directory.
//
// Sources are registered lowest-precedence first, so a later one shadows an
// earlier one of the same name: built-in, then remote, then the user's global
// directories, then configured paths, then the project's own — innermost last.
// A skill the user wrote in their project always wins.
//
// Nothing here fails the caller. A malformed SKILL.md, an unreadable directory
// or an unreachable URL costs that one source and is logged; the rest of the
// skills still load, because a broken skill file should not take the working
// ones down with it.
func (l *Loader) Load(dir string) *Registry {
	reg := NewRegistry(l.cfg.Permissions)

	for _, pattern := range Rules(l.cfg.Permissions).Invalid() {
		l.warn(fmt.Sprintf("skills: permission rule %q is not usable as written (action %q); treating the skills it covers as ask",
			pattern, l.cfg.Permissions[pattern]))
	}

	builtin, errs := Embedded()
	l.report(errs)
	for _, s := range builtin {
		reg.Register(s)
	}

	for _, root := range l.remoteRoots() {
		l.add(reg, root, SourceRemote)
	}
	for _, root := range GlobalRoots() {
		l.add(reg, root, SourceGlobal)
	}
	for _, p := range l.cfg.Paths {
		l.add(reg, resolvePath(dir, p), SourceConfig)
	}
	for _, root := range ProjectRoots(dir) {
		l.add(reg, root, SourceProject)
	}

	return reg
}

// remoteRoots resolves every configured skills URL to local directories,
// fetching each at most once per process.
//
// The lock is held across the fetch so concurrent sessions do not each download
// the same manifest. That means the first Load after startup can block the
// others for as long as the fetch takes — bounded by remoteTimeout, paid once
// per process, and only when a skills URL is configured at all.
func (l *Loader) remoteRoots() []string {
	if len(l.cfg.URLs) == 0 || l.cacheDir == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	var roots []string
	for _, u := range l.cfg.URLs {
		if dirs, ok := l.remote[u]; ok {
			roots = append(roots, dirs...)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		dirs, err := fetchRemote(ctx, l.client, l.cacheDir, u)
		cancel()
		if err != nil {
			// dirs may still be a usable cached copy — fetchRemote returns both
			// when it falls back — so the error is a warning, not a discard.
			l.warn("skills: remote source " + u + ": " + err.Error())
		}
		// Recorded even when empty: a URL that failed once must not be retried
		// on every turn for the life of the process.
		l.remote[u] = dirs
		roots = append(roots, dirs...)
	}
	return roots
}

// add scans one root and registers what it holds.
func (l *Loader) add(reg *Registry, root string, source Source) {
	if root == "" {
		return
	}
	skills, errs := ScanRoot(root, source)
	l.report(errs)
	for _, s := range skills {
		reg.Register(s)
	}
}

// resolvePath makes a configured skills path absolute, relative to the project.
//
// A relative path with no project directory to resolve against is dropped
// rather than left relative: it would otherwise be resolved against whatever
// directory the process happens to be running in, which is not what the config
// asked for.
func resolvePath(dir, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, p)
}

func (l *Loader) report(errs []error) {
	for _, err := range errs {
		l.warn("skills: skipped — " + err.Error())
	}
}

// warn logs a diagnostic the first time it is seen and stays quiet afterwards.
func (l *Loader) warn(msg string) {
	l.reportedMu.Lock()
	seen := l.reported[msg]
	l.reported[msg] = true
	l.reportedMu.Unlock()
	if !seen {
		slog.Warn(msg)
	}
}
