package appendcmd

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

var AppendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append files or directories to a SQAR archive",
	Long:  "Append multiple files and directories into an existing or new SQAR archive. Use -I to specify inputs and -O for output archive.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		srcs, _ := cmd.Flags().GetStringSlice("input")
		// include leftover positional args (shell globs)
		if len(args) > 0 {
			srcs = append(srcs, args...)
		}
		out, _ := cmd.Flags().GetString("output")

		if len(srcs) == 0 {
			fmt.Println("Error: at least one input must be provided with -I")
			cmd.Usage()
			os.Exit(2)
		}

		// resolve '.' or '..' inputs to base names for defaulting
		normSrcs := make([]string, 0, len(srcs))
		for _, s := range srcs {
			if s == "." || s == ".." {
				abs, err := filepath.Abs(s)
				if err == nil {
					normSrcs = append(normSrcs, abs)
					continue
				}
			}
			normSrcs = append(normSrcs, s)
		}

		// determine default output when not provided
		if out == "" {
			if len(normSrcs) == 1 {
				base := filepath.Base(strings.TrimRight(normSrcs[0], string(os.PathSeparator)))
				out = filepath.Join(".", base+".sqar")
			} else {
				out = filepath.Join(".", "Archive.sqar")
			}
		} else {
			// if output is an existing directory or ends with separator, place file inside it
			if info, err := os.Stat(out); err == nil && info.IsDir() {
				var outName string
				if len(normSrcs) == 1 {
					base := filepath.Base(strings.TrimRight(normSrcs[0], string(os.PathSeparator)))
					outName = base + ".sqar"
				} else {
					outName = "Archive.sqar"
				}
				out = filepath.Join(out, outName)
			} else if strings.HasSuffix(out, string(os.PathSeparator)) {
				var outName string
				if len(normSrcs) == 1 {
					base := filepath.Base(strings.TrimRight(normSrcs[0], string(os.PathSeparator)))
					outName = base + ".sqar"
				} else {
					outName = "Archive.sqar"
				}
				out = filepath.Join(out, outName)
			}
		}

		if err := pkg.Append(out, normSrcs, pkg.PackOptions{
			Compress:      compress,
			IncludeParent: includeParent,
			Level:         level,
			Workers:       workers,
		}); err != nil {
			fmt.Printf("Error appending to archive %s: %s\n", out, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully appended %v to %s\n", normSrcs, out)
	},
}

func init() {
	AppendCmd.Flags().BoolVarP(&compress, "compress", "C", false, "Enable file compression")
	AppendCmd.Flags().BoolVarP(&includeParent, "include-parent", "P", false, "Include a parent directory in the archival process")
	AppendCmd.Flags().StringVarP(&level, "level", "L", "default", "Compression level: fast|default|best")
	AppendCmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of compression workers")
	AppendCmd.Flags().StringSliceP("input", "I", nil, "Input files/directories (can be specified multiple times)")
	AppendCmd.Flags().StringP("output", "O", "", "Output archive path (file or directory)")
}
