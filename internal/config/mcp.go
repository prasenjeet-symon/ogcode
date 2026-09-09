package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

// MCPScope reports where the effective definition of MCP server name lives —
// "project" when the project-local ogcode.json defines it (which, under the
// wholesale per-name merge, is what the agent uses), otherwise "global" when
// only the global config does, or "" when neither does.
func MCPScope(dir, name string) string {
	if _, ok := rawMCPServer(findProjectFile(dir), name); ok {
		return "project"
	}
	if _, ok := rawMCPServer(globalPath(), name); ok {
		return "global"
	}
	return ""
}

// SetMCPDisabled turns MCP server name on or off for this project by writing to
// the project-local ogcode.json, and returns the path written. The choice is
// always per-project: it never edits the global config, so disabling a server
// here leaves it enabled in other projects.
//
// Because the config merge replaces a server wholesale per name, disabling a
// server that lives only in the global config means pinning its full definition
// into the project file alongside disabled:true — otherwise the merged view
// would lose the command/url and re-enabling could not reconnect. Enabling
// reverses this: a project entry that is nothing more than a pin of the global
// server (identical but for the disabled flag) is removed so the file falls back
// to the global definition and does not drift; a project-native server just has
// its disabled flag cleared.
func SetMCPDisabled(dir, name string, disabled bool) (string, error) {
	global, _ := rawMCPServer(globalPath(), name)

	path := findProjectFile(dir)
	if path == "" {
		if created := EnsureProjectFile(dir); created != "" {
			path = created
		} else {
			path = findProjectFile(dir)
		}
		if path == "" {
			return "", fmt.Errorf("no ogcode.json for %s and one could not be created", dir)
		}
	}

	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &root)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}

	if disabled {
		obj, _ := mcp[name].(map[string]any)
		if obj == nil {
			// Not defined in the project yet — pin the effective definition so a
			// later re-enable still knows how to connect. Prefer the global one
			// (the only other place it can come from); an empty object as a last
			// resort still records the disable.
			if global != nil {
				obj = cloneJSONObject(global)
			} else {
				obj = map[string]any{}
			}
		}
		obj["disabled"] = true
		mcp[name] = obj
	} else {
		if obj, ok := mcp[name].(map[string]any); ok {
			delete(obj, "disabled")
			// A project entry that is just a pin of the identical global server is
			// removed, so the merged config falls back to global and the project
			// file does not carry a stale copy that could drift from it.
			if global != nil && jsonEqual(obj, global) {
				delete(mcp, name)
			} else {
				mcp[name] = obj
			}
		}
	}

	root["mcp"] = mcp
	return path, writeJSONObjectAtomic(path, root)
}

// rawMCPServer returns the raw JSON object for MCP server name in the config
// file at path, and whether it was present. A missing or unparseable file, or
// an absent server, yields (nil, false).
func rawMCPServer(path, name string) (map[string]any, bool) {
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		return nil, false
	}
	mcp, _ := root["mcp"].(map[string]any)
	obj, ok := mcp[name].(map[string]any)
	if !ok {
		return nil, false
	}
	return obj, true
}

// cloneJSONObject deep-copies a decoded JSON object via a marshal round-trip, so
// edits to the copy cannot reach back into the source map.
func cloneJSONObject(in map[string]any) map[string]any {
	data, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

// jsonEqual reports whether two decoded JSON objects are equal ignoring a
// "disabled" key on either side.
func jsonEqual(a, b map[string]any) bool {
	x := cloneJSONObject(a)
	y := cloneJSONObject(b)
	delete(x, "disabled")
	delete(y, "disabled")
	return reflect.DeepEqual(x, y)
}

// writeJSONObjectAtomic marshals root and writes it to path atomically (temp
// file + rename), so an interrupted write cannot leave a half-written config.
func writeJSONObjectAtomic(path string, root map[string]any) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
