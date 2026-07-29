package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/config"
)

// version is injected at build time via -ldflags "-X ...version=<tag>".
var version = "dev"

var baseURLFlag string

var rootCmd = &cobra.Command{
	Use:           "intuizi",
	Short:         "Intuizi CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
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
	api.UserAgent = "intuizi-cli/" + version

	rootCmd.PersistentFlags().StringVar(&baseURLFlag, "base-url", "",
		"Console base URL (default "+config.DefaultBaseURL+")")

	rootCmd.AddCommand(versionCmd)
}
