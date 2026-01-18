package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"sqar/pkg"
	"strings"

	"github.com/spf13/cobra"
)

var (
	compress      bool
	includeParent bool
	level         string
	workers       int
)

var PackCmd = &cobra.Command{
	Use:   "pack",
	Short: "Pack files and directories into a SQAR archive",
	Long:  "Pack multiple files and directories recursively into a single SQAR archive. Use -I to specify inputs and -O for output.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		srcs, _ := cmd.Flags().GetStringSlice("input")
		// include any leftover positional args (e.g. shell-expanded globs)
		if len(args) > 0 {
			srcs = append(srcs, args...)
		}
		out, _ := cmd.Flags().GetString("output")

		if len(srcs) == 0 {
			fmt.Println("Error: at least one input must be provided with -I")
			cmd.Usage()
			os.Exit(2)
		}

		// determine default output when not provided
		if out == "" {
			if len(srcs) == 1 {
				base := filepath.Base(strings.TrimRight(srcs[0], string(os.PathSeparator)))
				out = filepath.Join(".", base+".sqar")
			} else {
				out = filepath.Join(".", "Archive.sqar")
			}
		} else {
			// if output is an existing directory or ends with separator, place file inside it
			if info, err := os.Stat(out); err == nil && info.IsDir() {
				var outName string
				if len(srcs) == 1 {
					base := filepath.Base(strings.TrimRight(srcs[0], string(os.PathSeparator)))
					outName = base + ".sqar"
				} else {
					outName = "Archive.sqar"
				}
				out = filepath.Join(out, outName)
			} else if strings.HasSuffix(out, string(os.PathSeparator)) {
				var outName string
				if len(srcs) == 1 {
					base := filepath.Base(strings.TrimRight(srcs[0], string(os.PathSeparator)))
					outName = base + ".sqar"
				} else {
					outName = "Archive.sqar"
				}
				out = filepath.Join(out, outName)
			}
		}

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
		fmt.Printf("Successfully packed %v into %s\n", srcs, out)
	},
}

func init() {
	PackCmd.Flags().BoolVarP(&compress, "compress", "C", false, "Enable file compression")
	PackCmd.Flags().BoolVarP(&includeParent, "include-parent", "P", false, "Include a parent directory in the archival process")
	PackCmd.Flags().StringVarP(&level, "level", "L", "default", "Compression level: fast|default|best")
	PackCmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of compression workers")
	PackCmd.Flags().StringSliceP("input", "I", nil, "Input files/directories")
	PackCmd.Flags().StringP("output", "O", "", "Output archive path")
}
