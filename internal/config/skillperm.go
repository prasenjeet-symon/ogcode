package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// ProjectSkillPermissions returns the skills.permissions map written in the
// project-local ogcode.json in effect for dir — the file findProjectFile
// locates, not merged with the global config — together with that file's path.
// When no project file exists it returns an empty (non-nil) map and an empty
// path; the caller can still hand the map to SetProjectSkillPermissions, which
// creates the file.
//
// It reads only the project file on purpose. A toggle edits the project file,
// so it must start from the project file's own rules; folding in the global
// config here would copy every global rule into the project on the first save.
func ProjectSkillPermissions(dir string) (map[string]string, string) {
	perms := map[string]string{}
	path := findProjectFile(dir)
	if path == "" {
		return perms, ""
	}
	if c := readFile(path); c != nil {
		for pattern, action := range c.Skills.Permissions {
			perms[pattern] = action
		}
	}
	return perms, path
}

// SetProjectSkillPermissions writes perms as the skills.permissions object of
// the project-local ogcode.json for dir, preserving every other field already
// in the file, and creating the file from the standard template if none exists
// anywhere up the tree. Returns the path written.
//
// The rewrite goes through the file as a free-form object rather than the typed
// Config, so fields ogcode does not model — a future key, or a user's own
// addition — survive untouched. Marshalling a map sorts its keys, so the top
// level settles into a fixed order (mcp, providers, skills); the content is
// unchanged and this package's readers do not depend on key order.
//
// The write is atomic: it lands through a sibling temp file and a rename, so an
// interrupted write cannot leave a half-written config where the next Load would
// read one.
func SetProjectSkillPermissions(dir string, perms map[string]string) (string, error) {
	path := findProjectFile(dir)
	if path == "" {
		// No project file anywhere up the tree — create a blank one at dir. A
		// concurrent process may win the create race, in which case we take the
		// file it made.
		if created := EnsureProjectFile(dir); created != "" {
			path = created
		} else {
			path = findProjectFile(dir)
		}
		if path == "" {
			return "", fmt.Errorf("no ogcode.json for %s and one could not be created", dir)
		}
	}

	// A missing or unparseable file starts from an empty object rather than
	// failing: the template EnsureProjectFile just wrote parses cleanly, and a
	// hand-broken file should not strand the toggle behind a parse error.
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &root)
	}

	skills, _ := root["skills"].(map[string]any)
	if skills == nil {
		skills = map[string]any{}
	}
	object := make(map[string]any, len(perms))
	for pattern, action := range perms {
		object[pattern] = action
	}
	skills["permissions"] = object
	root["skills"] = skills

	return path, writeJSONObjectAtomic(path, root)
}
