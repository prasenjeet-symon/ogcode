package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// These variables are set via ldflags during build by GoReleaser.
var (
	version = "v0.16.0"
	commit  = "none"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print ogcode version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("ogcode %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		fmt.Printf("  go:     %s\n", runtime.Version())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
