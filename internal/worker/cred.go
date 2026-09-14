package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// workerCredFile is where the worker persists its current auth token, next to
// the rest of ogcode's home-directory runtime state (~/.ogcode/worker-cred). On
// a worker or master restart the token is reused — the master re-recognizes it
// from its own persisted store — so the worker reconnects without re-pairing.
const workerCredFile = "worker-cred"

// persistCred atomically writes the auth token and its expiry so a crash
// mid-write can't leave a truncated credentials file. Best-effort: a failure
// only costs a re-pair on the next boot.
func persistCred(token string, exp time.Time) error {
	dir, err := ogcodeHomeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, workerCredFile)
	tmp := path + ".tmp"
	content := fmt.Sprintf("%d\n%s\n", exp.Unix(), token)
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// loadCred reads back a persisted token and expiry. It is best-effort: any
// invalid content (truncated file, garbage, non-empty token) returns ok=false
// so the caller falls back to a full register.
func loadCred() (token string, exp time.Time, ok bool) {
	dir, err := ogcodeHomeDir()
	if err != nil {
		return "", time.Time{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, workerCredFile))
	if err != nil {
		return "", time.Time{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		return "", time.Time{}, false
	}
	unix, err := strconv.ParseInt(lines[0], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	if lines[1] == "" {
		return "", time.Time{}, false
	}
	return lines[1], time.Unix(unix, 0), true
}
