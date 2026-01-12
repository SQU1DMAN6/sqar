package pack

import (
	"fmt"
	"os"
	"sqar/pkg"

	"github.com/spf13/cobra"
)

var (
	compress      bool
	includeParent bool
)

var PackCmd = &cobra.Command{
	Use:   "pack [directory] [output]",
	Short: "Pack a directory into a SQAR archive",
	Long:  "Pack all files recursively in a directory into a single SQAR archive.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		src := args[0]
		out := args[1]

		err := pkg.Pack(src, out, pkg.PackOptions{
			Compress:      compress,
			IncludeParent: includeParent,
		})
		if err != nil {
			fmt.Printf("Error packing directory %s: %s\n", src, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully packed %s into %s.sqar", src, out)
	},
}

func init() {
	PackCmd.Flags().BoolVarP(&compress, "compress", "C", false, "Enable file compression")
	PackCmd.Flags().BoolVarP(&includeParent, "include-parent", "P", false, "Include a parent directory in the archival process")
}
