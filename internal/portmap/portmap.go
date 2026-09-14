// Package portmap remembers which TCP port each project directory serves on, so
// starting the same project always reuses the same port instead of drifting to
// whatever happens to be free at the time (the server walks past a busy port, so
// without memory a project's port depends on start order).
//
// The mapping is a machine-local registry at ~/.ogcode/ports.json — a JSON
// object of absolute-project-dir -> port, sitting beside the global config DB.
// It is deliberately NOT the project-local ogcode.json: an auto-assigned port is
// per-machine state, not shareable project configuration.
package portmap

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// defaultRegistryPath returns ~/.ogcode/ports.json, or "" when the home dir is
// unknown — callers then treat the registry as empty and simply skip memory.
func defaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ogcode", "ports.json")
}

// key canonicalizes a directory into a stable registry key: absolute, symlinks
// resolved when possible, then cleaned — so the same project maps to one entry
// however its path was spelled on the command line.
func key(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func loadFrom(path string) map[string]int {
	m := map[string]int{}
	if path == "" {
		return m
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m // missing or unreadable -> empty registry
	}
	_ = json.Unmarshal(data, &m) // a corrupt file reads as empty; the next Save heals it
	return m
}

func saveTo(path string, m map[string]int) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically so a crash mid-write cannot corrupt the registry.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Lookup returns the port previously recorded for dir, and whether one exists.
func Lookup(dir string) (int, bool) { return lookupIn(defaultRegistryPath(), dir) }

func lookupIn(path, dir string) (int, bool) {
	p, ok := loadFrom(path)[key(dir)]
	if !ok || p <= 0 {
		return 0, false
	}
	return p, true
}

// Save records that dir serves on port, replacing any previous entry.
func Save(dir string, port int) error { return saveIn(defaultRegistryPath(), dir, port) }

func saveIn(path, dir string, port int) error {
	if port <= 0 {
		return nil
	}
	m := loadFrom(path)
	m[key(dir)] = port
	return saveTo(path, m)
}

// SuggestStart returns a starting port for a project that has no recorded port
// yet: base itself when no other project has claimed it, otherwise the next port
// above base that no other project claims. It only avoids collisions with other
// projects' recorded ports — actual bind availability is left to the server,
// which walks past a transiently busy port on its own.
func SuggestStart(dir string, base int) int { return suggestIn(defaultRegistryPath(), dir, base) }

func suggestIn(path, dir string, base int) int {
	if base <= 0 {
		base = 9595
	}
	self := key(dir)
	claimed := map[int]bool{}
	for d, p := range loadFrom(path) {
		if d != self {
			claimed[p] = true
		}
	}
	p := base
	for i := 0; i < 1000 && claimed[p]; i++ {
		p++
	}
	return p
}
