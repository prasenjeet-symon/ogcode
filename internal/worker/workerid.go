package worker

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workerIDFile is where the worker persists its stable identity, next to the
// rest of ogcode's home-directory runtime state (~/.ogcode/). The id is minted
// once and reused on every boot so the master registers the worker under the
// same id across restarts — keeping the panel subdomain <id>.<host> stable.
const workerIDFile = "worker-id"

// loadOrCreateWorkerID returns the worker's stable id, minting and persisting a
// fresh one on first run. It is best-effort: on any failure it returns "" so
// the caller falls back to letting the master mint an id (the worker still
// registers, just without a stable identity).
func loadOrCreateWorkerID() (string, error) {
	dir, err := ogcodeHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, workerIDFile)

	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	id, err := newWorkerID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Write atomically (temp + rename) so a crash mid-write never leaves a
	// truncated id that would silently change the worker's identity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return id, nil
}

// ogcodeHomeDir returns the ogcode home-directory runtime state dir (~/.ogcode/),
// mirroring the convention used by the MCP token store.
func ogcodeHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ogcode"), nil
}

// newWorkerID mints a fresh stable id. It is DNS-safe (lowercase alphanumeric,
// no leading/trailing hyphen) so the master can use it as a subdomain label.
func newWorkerID() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate worker id: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)), nil
}
