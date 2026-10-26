package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/nativehost"
	"github.com/spf13/cobra"
)

// nativeHostIDs collects the repeatable --extension-id flags of install.
var nativeHostIDs []string

// IsBrowserLaunched reports whether argv is Chrome's native-messaging launch
// of this binary. Chrome executes the manifest's path with the calling
// extension's origin as the first argument — "chrome-extension://<id>/" —
// optionally followed by flags such as --parent-window; it never spells the
// `native-host` subcommand, which is only the human's way to run the host.
// A foreign first argument still falls through to cobra's dispatch.
func IsBrowserLaunched(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	first := argv[0]
	if strings.HasPrefix(first, "chrome-extension://") {
		return true
	}
	// Chrome names a parent window for an extension's popup before the
	// origin only on some channels; accepting that flag alone keeps the
	// host runnable however the caller orders its launch arguments.
	return len(argv) > 1 && strings.HasPrefix(first, "--parent-window=")
}

var nativeHostCmd = &cobra.Command{
	Use:   "native-host",
	Short: "Run the Chrome native-messaging host the companion extension pairs through",
	Long: `Run as a Chrome Native Messaging host over stdin/stdout.

Chrome launches this command itself — its registered name is "com.ogcode", set
up by 'ogcode native-host install' (and written at boot by 'ogcode serve' when
the working project ships browser-extension/). Requests arrive as
length-prefixed JSON and only framed replies are ever written to stdout, so
run this from a browser launcher, not a terminal.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nativehost.Run(os.Stdin, os.Stdout)
	},
}

var nativeHostInstallCmd = &cobra.Command{
	Use:   "install [--extension-id <id>]...",
	Short: "Register com.ogcode with every installed Chromium-based browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		ids := nativehost.CandidatesFor(cwd)
		if len(nativeHostIDs) > 0 {
			ids = nativehost.DedupeExtensionIDs(append(append([]string{}, ids...), nativeHostIDs...))
		}
		if len(ids) == 0 {
			nativeHostInstallHint(cwd, os.Stderr)
			return errors.New("no extension id to allowlist — see the hint above")
		}
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate the ogcode executable: %w", err)
		}
		if err := nativehost.Register(exe, ids); err != nil {
			return err
		}
		fmt.Println("Registered the ogcode native-messaging host:")
		for _, path := range nativehost.ManifestPaths() {
			fmt.Println("  " + path)
		}
		fmt.Println("  executable: " + exe)
		noun := "id"
		if len(ids) > 1 {
			noun = "ids"
		}
		fmt.Printf("  extension %s: %s\n", noun, strings.Join(ids, ", "))
		return nil
	},
	SilenceUsage: true,
}

var nativeHostUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the com.ogcode native-messaging manifest from every browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		var existing []string
		for _, path := range nativehost.ManifestPaths() {
			if _, err := os.Stat(path); err == nil {
				existing = append(existing, path)
			}
		}
		if err := nativehost.Uninstall(); err != nil {
			return err
		}
		if len(existing) == 0 {
			fmt.Println("No ogcode native-messaging manifest found — nothing to remove.")
			return nil
		}
		fmt.Println("Removed the ogcode native-messaging manifest from:")
		for _, path := range existing {
			fmt.Println("  " + path)
		}
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(nativeHostCmd)
	nativeHostCmd.AddCommand(nativeHostInstallCmd)
	nativeHostCmd.AddCommand(nativeHostUninstallCmd)
	nativeHostInstallCmd.Flags().StringArrayVar(&nativeHostIDs, "extension-id", nil, "Allowlist this extension id (repeat for more)")
}

// nativeHostInstallHint explains, to a human, where an id would come from and
// which command registers it — run against stderr, never stdout.
func nativeHostInstallHint(cwd string, w io.Writer) {
	if id, ok := nativehost.UnpackedIDFor(cwd); ok {
		dir := filepath.Join(cwd, nativehost.UnpackedExtensionDir)
		fmt.Fprintf(w, `native-host install: no extension id was named, though this project ships the companion.

  Chrome derives an unpacked extension's id from its load path, so this load
  would be allowed by the id:

    %s

  1. Open chrome://extensions and use "Load unpacked", pointing it at:

       %s

  2. Run this command again; it computes that load path's id itself.
`, id, dir)
		return
	}
	fmt.Fprintf(w, `native-host install: nothing to register — no extension id is known.

  A manifest is written once an id is known, either:

  1. By shipping the companion at %s with a manifest.json in it and running
     this command again (its load-path id registers itself).

  2. By naming ids on the command line:

       ogcode native-host install --extension-id <id> [<id>...]

  3. By env: set %s to a comma-separated list of ids and re-run.

  A server for this project also writes the manifest itself at boot when it
  ships %s/manifest.json.
`, filepath.Join(cwd, nativehost.UnpackedExtensionDir), nativehost.ExtensionIDsEnv, nativehost.UnpackedExtensionDir)
}
