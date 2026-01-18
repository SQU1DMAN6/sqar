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
	level         string
	workers       int
)

var PackCmd = &cobra.Command{
	Use:   "pack [sources...] [output]",
	Short: "Pack files and directories into a SQAR archive",
	Long:  "Pack multiple files and directories recursively into a single SQAR archive.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		out := args[len(args)-1]
		srcs := args[:len(args)-1]

		err := pkg.Pack(srcs, out, pkg.PackOptions{
			Compress:      compress,
			IncludeParent: includeParent,
			Level:         level,
			Workers:       workers,
		})
		if err != nil {
			fmt.Printf("Error packing sources %v into %s: %s\n", srcs, out, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully packed %v into %s.sqar", srcs, out)
	},
}

func init() {
	PackCmd.Flags().BoolVarP(&compress, "compress", "C", false, "Enable file compression")
	PackCmd.Flags().BoolVarP(&includeParent, "include-parent", "P", false, "Include a parent directory in the archival process")
	PackCmd.Flags().StringVarP(&level, "level", "L", "default", "Compression level: fast|default|best")
	PackCmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of compression workers (0 = auto)")
}
