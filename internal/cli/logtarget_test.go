package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/logging"
)

// Logs live under the global log root, never inside the project: a log in the
// working tree can be committed, indexed, or read back by the agent as if it
// were project content. Each project command gets its own folder there, the
// worker logs at the root, and commands with nothing worth keeping get no file.
func TestLogTargetIsGlobalNeverInTheProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OGCODE_LOG_DIR", "")
	project := t.TempDir()
	t.Chdir(project)

	root := logging.Root(home)
	projectDir, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	for cmd, wantFile := range map[string]string{"serve": "ogcode.log", "plan": "ogcode.log", "run": "run.log", "index": "index.log"} {
		c, _, err := rootCmd.Find([]string{cmd})
		if err != nil {
			t.Fatal(err)
		}
		dir, name := logTarget(c)
		if name != wantFile {
			t.Errorf("%s logs to %q, want %q", cmd, name, wantFile)
		}
		if filepath.Dir(dir) != root {
			t.Errorf("%s logs to %q, want a project folder directly under %q", cmd, dir, root)
		}
		if strings.HasPrefix(dir, project) || strings.HasPrefix(dir, projectDir) {
			t.Errorf("%s logs inside the project: %q", cmd, dir)
		}
	}
	if dir, name := logTarget(rootCmd); name != "ogcode.log" || filepath.Dir(dir) != root {
		t.Errorf("bare ogcode logs to %q/%q", dir, name)
	}

	w, _, err := rootCmd.Find([]string{"worker"})
	if err != nil {
		t.Fatal(err)
	}
	if dir, name := logTarget(w); dir != root || name != "worker.log" {
		t.Errorf("worker logs to %q/%q, want %q/worker.log", dir, name, root)
	}

	v, _, err := rootCmd.Find([]string{"version"})
	if err != nil {
		t.Fatal(err)
	}
	if _, name := logTarget(v); name != "" {
		t.Errorf("version keeps a log file %q; it has nothing worth keeping", name)
	}
}
