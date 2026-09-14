package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// withUsersStore points usersDBPathConfig at a temp validation-passing config
// whose dbPath is a fresh temp DB file, then restores it. It returns the temp
// DB path (which does not exist yet) and a cleanup.
func withUsersStore(t *testing.T) (dbPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "control-plane.json")
	dbPath = filepath.Join(dir, "cp.db")
	cfg := `{"master":{"listen":":0","pairingSecret":"x","dbPath":"` + dbPath + `"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	prev := usersDBPathConfig
	usersDBPathConfig = cfgPath
	t.Cleanup(func() { usersDBPathConfig = prev })
	return dbPath
}

// run executes the root command with args, capturing stdout/stderr and whether
// Execute returned an error.
func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	root := rootCmd()
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	out, _, err := run(t, args...)
	if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return out
}

func TestUsers_AddFromEnv_BcryptOnly(t *testing.T) {
	db := withUsersStore(t)

	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "hunter22")
	out := runOK(t, "users", "add", "alice")
	if !strings.Contains(out, `"alice" added`) {
		t.Fatalf("add output = %q, want added message", out)
	}

	// The raw password must NEVER be written to the store — only a bcrypt hash.
	data, err := os.ReadFile(db)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if strings.Contains(string(data), "hunter22") {
		t.Fatal("raw password leaked into the DB; only a bcrypt hash should be stored")
	}

	// list shows the account with its role — never the hash.
	list := runOK(t, "users", "list")
	if list != "alice (user)\n" {
		t.Fatalf("list = %q, want %q", list, "alice (user)\n")
	}
}

func TestUsers_AddFromPasswordFile(t *testing.T) {
	withUsersStore(t)
	pwFile := filepath.Join(t.TempDir(), "pw.txt")
	if err := os.WriteFile(pwFile, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("write pw file: %v", err)
	}
	// Ensure env does NOT carry it (argv/file must be the source here).
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "")
	out := runOK(t, "users", "add", "bob", "--password-file", pwFile)
	if !strings.Contains(out, `"bob" added`) {
		t.Fatalf("add output = %q, want added message", out)
	}
}

func TestUsers_AddRequiresPassword(t *testing.T) {
	withUsersStore(t)
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "")
	_, _, err := run(t, "users", "add", "alice")
	if err == nil || !strings.Contains(err.Error(), "password required") {
		t.Fatalf("add without password: err = %v, want a password-required error", err)
	}
}

func TestUsers_AddRejectsEmptyPasswordFile(t *testing.T) {
	withUsersStore(t)
	pwFile := filepath.Join(t.TempDir(), "pw-empty.txt")
	if err := os.WriteFile(pwFile, []byte(""), 0o600); err != nil {
		t.Fatalf("write pw file: %v", err)
	}
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "")
	_, _, err := run(t, "users", "add", "alice", "--password-file", pwFile)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty password-file: err = %v, want an empty error", err)
	}
}

func TestUsers_AddResetsExisting(t *testing.T) {
	withUsersStore(t)
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "first")
	if out := runOK(t, "users", "add", "alice"); !strings.Contains(out, "added") {
		t.Fatalf("first add = %q", out)
	}
	// Reset the password. The new secret must replace the old hash.
	if out := runOK(t, "users", "add", "alice"); !strings.Contains(out, "reset") {
		t.Fatalf("second add = %q, want a reset message", out)
	}
}

func TestUsers_RmRefusesLastAccountWithoutForce(t *testing.T) {
	db := withUsersStore(t)
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "pw")

	// Single account: rm without --force must be refused.
	runOK(t, "users", "add", "solo")
	_, _, err := run(t, "users", "rm", "solo")
	if err == nil || !strings.Contains(err.Error(), "last operator account") {
		t.Fatalf("rm last without --force: err = %v, want refusal", err)
	}
	if _, statErr := os.Stat(db); statErr != nil {
		t.Fatalf("db missing after refused rm: %v", statErr)
	}

	// --force proceeds.
	if out := runOK(t, "users", "rm", "solo", "--force"); !strings.Contains(out, "removed") {
		t.Fatalf("rm --force = %q, want removed", out)
	}
	if out := runOK(t, "users", "list"); !strings.Contains(out, "no operator accounts") {
		t.Fatalf("list after rm --force = %q, want empty", out)
	}
}

func TestUsers_RmDeletesAndRmUnknown(t *testing.T) {
	withUsersStore(t)
	t.Setenv("OGCODE_CONTROL_PLANE_USER_PASSWORD", "pw")
	runOK(t, "users", "add", "alice")
	runOK(t, "users", "add", "bob")

	if out := runOK(t, "users", "rm", "alice"); !strings.Contains(out, "removed") {
		t.Fatalf("rm = %q, want removed", out)
	}
	// alice gone, bob remains (and bob is now the last, so no --force needed
	// for alice as she's not last).
	if out := runOK(t, "users", "list"); !strings.Contains(out, "bob") || strings.Contains(out, "alice") {
		t.Fatalf("list after rm = %q, want only bob", out)
	}

	_, _, err := run(t, "users", "rm", "ghost")
	if err == nil || !strings.Contains(err.Error(), "no such account") {
		t.Fatalf("rm unknown: err = %v, want no-such-account", err)
	}
}

// TestUsers_NoArgvLeak asserts the password never appears in the command line:
// every add path sources it from env or a file, never argv. This is enforced
// structurally (no --password flag exists), but we verify the flag is absent.
func TestUsers_NoArgvPasswordFlag(t *testing.T) {
	withUsersStore(t)
	var add *cobra.Command
	for _, cmd := range usersCmd().Commands() {
		if cmd.Name() == "add" {
			add = cmd
		}
	}
	if add == nil {
		t.Fatal("users add subcommand missing")
	}
	add.Flags().VisitAll(func(f *pflag.Flag) {
		if strings.Contains(f.Name, "password") && f.Name != "password-file" {
			t.Errorf("users add exposes %q — a password flag would leak the secret into argv", f.Name)
		}
	})
}
