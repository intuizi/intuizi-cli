package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/config"
)

// version is injected at build time via -ldflags "-X ...version=<tag>".
var version = "dev"

var baseURLFlag, idempotencyKeyFlag string

// jsonOutput is the global --json: print the server's envelope, not a table.
var jsonOutput bool

// flagsParsed goes true in PersistentPreRun, which cobra reaches only once
// flags parse: an error before that is a bad invocation, after it a real one.
var flagsParsed bool

var rootCmd = &cobra.Command{
	Use:   "intuizi",
	Short: "Intuizi CLI",
	// False so a bad flag still prints usage; PersistentPreRun turns it on once
	// flags parse, so a failed API call is not followed by the usage block.
	SilenceErrors: true,
	// Runs after flag parsing. Cobra runs only the closest PersistentPreRun, so
	// a subcommand defining its own must repeat all three: otherwise
	// --idempotency-key is ignored and a failed call exits 2 instead of 1.
	PersistentPreRun: func(cmd *cobra.Command, _ []string) {
		api.IdempotencyKey = idempotencyKeyFlag
		flagsParsed = true
		cmd.Root().SilenceUsage = true
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

// A contract scripts read: 0 success, 1 API or wait failure, 2 bad invocation.
// A signal reports 128+N, as the shell did before we caught signals.
const (
	exitError = 1
	exitUsage = 2
)

func Execute() {
	// Own the handler rather than signal.NotifyContext, which does not hand the
	// signal back - and the exit code has to say which one arrived.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var caught atomic.Pointer[os.Signal]
	go func() {
		s := <-sigCh
		caught.Store(&s)
		signal.Stop(sigCh) // a second press kills outright, as it normally would
		cancel()
	}()

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return
	}

	var sig os.Signal
	if s := caught.Load(); s != nil {
		sig = *s // interrupting is the user's own doing, so it is not reported
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(exitCode(sig, flagsParsed))
}

// exitCode maps how the run ended onto the documented codes. Split out from
// Execute so it can be tested without a subprocess.
func exitCode(caught os.Signal, parsed bool) int {
	if caught != nil {
		if sig, ok := caught.(syscall.Signal); ok {
			return 128 + int(sig)
		}
		return exitError
	}
	if !parsed {
		return exitUsage
	}
	return exitError
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(_ *cobra.Command, _ []string) {
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
