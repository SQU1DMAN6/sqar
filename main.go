package main

import (
	appendcmd "sqar/cmd/append"
	inspect "sqar/cmd/inspect"
	pack "sqar/cmd/pack"
	unpack "sqar/cmd/unpack"
	version "sqar/cmd/version"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "sqar",
	Short: "SQAR archive utility",
	Long:  "SQAR is an archive tool for compressing files in a lossless and fast manner.",
}

func main() {
	rootCmd.AddCommand(pack.PackCmd)
	rootCmd.AddCommand(unpack.UnpackCmd)
	rootCmd.AddCommand(inspect.InspectCmd)
	rootCmd.AddCommand(version.VersionCmd)
	rootCmd.AddCommand(appendcmd.AppendCmd)
	rootCmd.Execute()
}
