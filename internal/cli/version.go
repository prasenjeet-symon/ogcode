package cli

import (
	"fmt"
	"runtime"

	verpkg "github.com/prasenjeet-symon/ogcode/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print ogcode version information",
	Run: func(cmd *cobra.Command, args []string) {
		info := verpkg.GetInfo()
		fmt.Printf("ogcode %s (%s/%s)\n", info.Version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  commit: %s\n", info.Commit)
		fmt.Printf("  built:  %s\n", info.Date)
		fmt.Printf("  go:     %s\n", info.GoVersion)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
