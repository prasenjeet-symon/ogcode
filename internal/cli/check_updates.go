package cli

import (
	"context"
	"fmt"
	"os"

	verpkg "github.com/prasenjeet-symon/ogcode/internal/version"
	"github.com/spf13/cobra"
)

var checkUpdatesCmd = &cobra.Command{
	Use:   "check-updates",
	Short: "Check for available updates",
	Long: `Check if a new version of ogcode is available.

This command queries the GitHub API to check for the latest release.
It will compare your current version with the latest release and
provide instructions on how to update if available.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return checkForUpdates(cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(checkUpdatesCmd)
}

func checkForUpdates(ctx context.Context) error {
	mgr := verpkg.New()

	fmt.Println("Checking for updates...")
	fmt.Println()

	updateInfo, err := mgr.CheckUpdate(ctx)
	if err != nil {
		// Still show current version info
		response := &verpkg.Response{
			Info: verpkg.GetInfo(),
		}
		printVersionInfo(response)
		fmt.Println()
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	response := &verpkg.Response{
		Info:       verpkg.GetInfo(),
		UpdateInfo: *updateInfo,
	}
	printVersionInfo(response)

	if !updateInfo.UpdateAvailable {
		fmt.Println()
		fmt.Println("✓ You are on the latest version!")
		return nil
	}

	// Update available
	fmt.Println()
	fmt.Println("⚡ A new version is available!")
	fmt.Println()

	fmt.Printf("  Current:  %s\n", response.Version)
	fmt.Printf("  Latest:   %s\n", updateInfo.LatestVersion)
	fmt.Println()

	if updateInfo.PublishedAt != nil {
		fmt.Printf("  Published: %s\n", updateInfo.PublishedAt.Format("2006-01-02"))
		fmt.Println()
	}

	if updateInfo.InstallCommand != "" {
		fmt.Println("Update command:")
		fmt.Printf("  %s\n", updateInfo.InstallCommand)
		fmt.Println()
	}

	if updateInfo.ReleaseURL != "" {
		fmt.Printf("  Release: %s\n", updateInfo.ReleaseURL)
	}

	if updateInfo.ReleaseNotes != "" {
		fmt.Println()
		fmt.Println("Release notes:")
		fmt.Printf("  %s\n", updateInfo.ReleaseNotes)
	}

	// Exit with code 1 to indicate update available
	os.Exit(1)
	return nil
}

func printVersionInfo(response *verpkg.Response) {
	fmt.Printf("Current version: %s\n", response.Version)
	fmt.Printf("  Commit: %s\n", response.Commit)
	fmt.Printf("  Built:  %s\n", response.Date)
	fmt.Printf("  Go:     %s\n", response.GoVersion)
}
