package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/config"
)

// version is injected at build time via -ldflags "-X ...version=<tag>".
var version = "dev"

var baseURLFlag, idempotencyKeyFlag string

// jsonOutput is the global --json: print the server's envelope, not a table.
var jsonOutput bool

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

// client builds an authenticated client, or explains that there is no token.
func client() (*api.Client, error) {
	token := config.Token()
	if token == "" {
		return nil, errors.New("not logged in - run 'intuizi auth login', or set " +
			config.EnvToken + " for CI")
	}
	return api.New(config.BaseURL(baseURLFlag), token), nil
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

	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false,
		"Print the raw JSON response envelope instead of a table")

	rootCmd.PersistentFlags().StringVar(&idempotencyKeyFlag, "idempotency-key", "",
		"Reuse this Idempotency-Key on create commands, to retry a create whose "+
			"outcome is unknown (default: a fresh key per create)")

	rootCmd.AddCommand(versionCmd)
}
