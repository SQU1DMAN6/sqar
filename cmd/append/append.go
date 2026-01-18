package appendcmd

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

var AppendCmd = &cobra.Command{
	Use:   "append [archive] [sources...]",
	Short: "Append files or directories to an existing SQAR archive",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		archive := args[0]
		srcs := args[1:]

		if err := pkg.Append(archive, srcs, pkg.PackOptions{
			Compress:      compress,
			IncludeParent: includeParent,
			Level:         level,
			Workers:       workers,
		}); err != nil {
			fmt.Printf("Error appending to archive %s: %s\n", archive, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully appended %v to %s\n", srcs, archive)
	},
}

func init() {
	AppendCmd.Flags().BoolVarP(&compress, "compress", "C", false, "Enable file compression")
	AppendCmd.Flags().BoolVarP(&includeParent, "include-parent", "P", false, "Include a parent directory in the archival process")
	AppendCmd.Flags().StringVarP(&level, "level", "L", "default", "Compression level: fast|default|best")
	AppendCmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of compression workers")
}
