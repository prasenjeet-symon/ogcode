package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/config"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// parseWorkspaceCSV splits a comma-separated allowlist flag into trimmed,
// de-duplicated workspace identifiers, dropping empty pieces (so "a,, b" is
// [a b] and "" is nil = unrestricted). Mirrors the console's field parsing so
// CLI-created and console-created accounts carry the same shape.
func parseWorkspaceCSV(field string) []string {
	if strings.TrimSpace(field) == "" {
		return nil
	}
	parts := strings.Split(field, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// usersCmd manages the per-employee operator accounts that gate the worker-UI
// proxy. The `users add` password is read from $OGCODE_CONTROL_PLANE_USER_PASSWORD
// or --password-file so it never appears in argv (A6 — argv leaks via `ps` and
// shell history); only a bcrypt hash is ever written to the store.
func usersCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage operator accounts (gates the worker-UI proxy)",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return resolveUsersDBPath(&dbPath)
		},
	}

	add := &cobra.Command{
		Use:   "add <username>",
		Short: "Create (or reset the password for) an operator account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if username == "" {
				return fmt.Errorf("username must not be empty")
			}
			if err := auth.ValidateUsername(username); err != nil {
				return err
			}
			password, err := readUserPassword(cmd)
			if err != nil {
				return err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}
			st, err := registry.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open account store %s: %w", dbPath, err)
			}
			defer st.Close()
			added := true
			if _, exists, err := st.GetUser(username); err != nil {
				return err
			} else if exists {
				added = false
			}
			record := registry.UserRecord{Hash: string(hash), CreatedAt: time.Now()}
			if raw, _ := cmd.Flags().GetString("workspaces"); raw != "" {
				record.Workspaces = parseWorkspaceCSV(raw)
				if len(record.Workspaces) == 0 {
					return fmt.Errorf("no usable workspace ids in %q", raw)
				}
			}
			// New records pin the admin flag explicitly (--admin) so the
			// account's reach is what was asked for, never the pre-accounts
			// default. A password reset on an existing account keeps its
			// existing role/assignment (only the hash and timestamp change).
			if added {
				admin, _ := cmd.Flags().GetBool("admin")
				record.Admin = &admin
			}
			if err := st.PutUser(username, record); err != nil {
				return fmt.Errorf("store account %q: %w", username, err)
			}
			verb := "added"
			if !added {
				verb = "updated (password reset)"
			}
			cmd.Printf("account %q %s", username, verb)
			if added {
				if admin, _ := cmd.Flags().GetBool("admin"); admin {
					cmd.Print(" (admin)")
				} else {
					cmd.Print(" (user)")
				}
			}
			if len(record.Workspaces) == 0 {
				cmd.Print(" (all workspaces)\n")
			} else {
				cmd.Printf(" (workspaces: %s)\n", strings.Join(record.Workspaces, ", "))
			}
			return nil
		},
	}
	add.Flags().String("password-file", "", "file holding the account password (else $OGCODE_CONTROL_PLANE_USER_PASSWORD)")
	add.Flags().String("workspaces", "", "comma-separated workspace allowlist: route labels, workspace names, or path suffixes (empty = all workspaces)")
	add.Flags().Bool("admin", false, "grant administrator: sees every worker and session")
	cmd.AddCommand(add)

	rm := &cobra.Command{
		Use:   "rm <username>",
		Short: "Delete an operator account (breaks that user's live sessions immediately)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			username := args[0]
			st, err := registry.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open account store %s: %w", dbPath, err)
			}
			defer st.Close()
			if _, exists, err := st.GetUser(username); err != nil {
				return err
			} else if !exists {
				return fmt.Errorf("no such account %q", username)
			}
			// Refuse to delete the LAST account without an explicit --force, so an
			// operator cannot accidentally lock the whole control plane out of the
			// operator gate (which would fall back to running unauthenticated).
			if !force {
				names, err := st.ListUsers()
				if err != nil {
					return err
				}
				if len(names) <= 1 {
					return fmt.Errorf("refusing to delete the last operator account — pass --force to proceed")
				}
			}
			if _, err := st.DeleteUser(username); err != nil {
				return fmt.Errorf("delete account %q: %w", username, err)
			}
			cmd.Printf("account %q removed\n", username)
			return nil
		},
	}
	rm.Flags().Bool("force", false, "delete even the last remaining account")
	cmd.AddCommand(rm)

	list := &cobra.Command{
		Use:   "list",
		Short: "List operator accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := registry.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open account store %s: %w", dbPath, err)
			}
			defer st.Close()
			names, err := st.ListUsers()
			if err != nil {
				return err
			}
			sort.Strings(names)
			if len(names) == 0 {
				cmd.Println("no operator accounts")
				return nil
			}
			for _, n := range names {
				rec, ok, err := st.GetUser(n)
				if err != nil || !ok {
					cmd.Println(n)
					continue
				}
				if rec.IsAdmin() {
					cmd.Printf("%s (admin)\n", n)
				} else if repos := rec.AllRepos(); len(repos) > 0 {
					cmd.Printf("%s (user, repos: %s)\n", n, strings.Join(repos, ", "))
				} else {
					cmd.Printf("%s (user)\n", n)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(list)

	return cmd
}

// resolveUsersDBPath points the account CLI at the same store the master uses.
// It uses --config's dbPath when resolvable, else the config default. The CLI
// must be able to seed accounts before the master has ever run, so a missing
// config file is not an error.
func resolveUsersDBPath(dbPath *string) error {
	if *dbPath != "" {
		return nil
	}
	cfg, err := config.Load(usersDBPathConfig)
	if err == nil {
		if p := cfg.Master.DBFile(); p != "" {
			*dbPath = p
			return nil
		}
	}
	// Fall back to the default DB file (same as an empty config master).
	if p := (config.MasterConfig{}).DBFile(); p != "" {
		*dbPath = p
		return nil
	}
	return nil
}

// usersDBPathConfig is the default config path used to resolve the store
// location; overridden in tests.
var usersDBPathConfig = "control-plane.json"

// readUserPassword fetches the account password from --password-file or
// $OGCODE_CONTROL_PLANE_USER_PASSWORD (A6: never from argv).
func readUserPassword(cmd *cobra.Command) (string, error) {
	if file := cmd.Flags().Lookup("password-file").Value.String(); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read --password-file %s: %w", file, err)
		}
		pw := strings.TrimRight(string(b), "\r\n")
		if pw == "" {
			return "", fmt.Errorf("--password-file %s is empty", file)
		}
		return pw, nil
	}
	pw := os.Getenv("OGCODE_CONTROL_PLANE_USER_PASSWORD")
	if pw == "" {
		return "", fmt.Errorf("account password required — set OGCODE_CONTROL_PLANE_USER_PASSWORD or pass --password-file (never the argv)")
	}
	return pw, nil
}
