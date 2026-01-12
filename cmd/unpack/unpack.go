package unpack

import (
	"fmt"
	"os"
	"sqar/pkg"

	"github.com/spf13/cobra"
)

var UnpackCmd = &cobra.Command{
	Use:   "unpack [arhive] [output]",
	Short: "Unpack a SQAR archive to an output directory",
	Long:  "Unpack a SQAR archive to an output directory",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		archive := args[0]
		out := args[1]

		err := pkg.Unpack(archive, out)
		if err != nil {
			fmt.Printf("Error unpacking archive %s: %s\n", archive, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully unpacked archive %s to directory %s", archive, out)
	},
}
