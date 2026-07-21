package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is injected at build time via -ldflags "-X ...version=<tag>".
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "intuizi",
	Short: "Intuizi CLI",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
