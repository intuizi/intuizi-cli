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

var baseURLFlag, idempotencyKeyFlag string

var rootCmd = &cobra.Command{
	Use:           "intuizi",
	Short:         "Intuizi CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
	// Runs after flag parsing, before any subcommand. Note cobra runs only the
	// closest PersistentPreRun in the tree: a subcommand that defines its own
	// must repeat this assignment or --idempotency-key is silently ignored.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		api.IdempotencyKey = idempotencyKeyFlag
	},
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

	rootCmd.PersistentFlags().StringVar(&idempotencyKeyFlag, "idempotency-key", "",
		"Reuse this Idempotency-Key on create commands, to retry a create whose "+
			"outcome is unknown (default: a fresh key per create)")

	rootCmd.AddCommand(versionCmd)
}
