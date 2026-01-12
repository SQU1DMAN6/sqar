package version

import (
	"fmt"

	"github.com/spf13/cobra"
)

var VersionCmd = &cobra.Command{
	Use:   "version",
	Short: "View SQAR's version",
	Long:  "Display the version and release of SQAR installed on your system.",
	RunE: func(cmd *cobra.Command, args []string) error {
		var version string = "SQAR version 0.1.0"
		var release string = "Written by Quan Thai, January 12 2026"
		fmt.Println(version)
		fmt.Println(release)

		return nil
	},
}
