package inspect

import (
	"fmt"
	"os"
	"sqar/pkg"

	"github.com/spf13/cobra"
)

var InspectCmd = &cobra.Command{
	Use:   "inspect [archive]",
	Short: "View a SQAR archive",
	Long:  "Inspect the files in an SQAR archive",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		archive := args[0]
		quiet, _ := cmd.Flags().GetBool("quiet")

		entries, err := pkg.List(archive)
		if err != nil {
			fmt.Printf("Error inspecting archive %s: %s\n", archive, err)
			os.Exit(1)
		}

		fmt.Printf("Files in archive %s:\n\r", archive)
		for i, e := range entries {
			if quiet {
				fmt.Printf("%d: %s\n", i, e.Path)
			} else {
				fmt.Printf("=====================\n\r%d:\n\r\tPath: %s\n\r\tCompressed: %d\n\r\tRaw: %d\n\r\tOffset: %d\n",
					i, e.Path, e.CompressionSize, e.RawSize, e.DataOffset)
			}
		}
	},
}

func init() {
	InspectCmd.Flags().BoolP("quiet", "Q", false, "Suppress output of file inspection")
}
