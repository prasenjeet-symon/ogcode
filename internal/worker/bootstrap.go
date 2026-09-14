package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// bootstrapWorkspace prepares the working directory for a session.
//
// If the directory already exists it is used as-is. Otherwise, when a git
// ref (clone source URL) is provided, it is cloned into place. When the dir
// is absent and no ref is given, the error returned doubles as the
// CommandResult git-clone hint and tells the operator to start the agent with
// a ref.
func bootstrapWorkspace(dir, ref string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("bootstrap: stat workspace %q: %w", dir, err)
	}
	if ref == "" {
		return fmt.Errorf("workspace %q not present on worker (provide a git ref to clone it)", dir)
	}
	if err := cloneRepo(ref, dir); err != nil {
		return err
	}
	return nil
}

// cloneRepo clones url into dir. The parent directory is created as needed and
// an already-existing dir is refused rather than clobbered.
func cloneRepo(url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("bootstrap: create parent for %q: %w", dir, err)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("bootstrap: refusing to clone over existing %q", dir)
	}
	cmd := exec.Command("git", "clone", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootstrap: git clone %q failed: %w (%s)", url, err, out)
	}
	return nil
}
