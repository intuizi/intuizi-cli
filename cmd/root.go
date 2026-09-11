package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
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

// quietOutput is the global --quiet: ids only on stdout, one per line.
var quietOutput bool

// flagsParsed goes true in PersistentPreRun, which cobra reaches only once
// flags parse: an error before that is a bad invocation, after it a real one.
var flagsParsed bool

// usageError is a bad invocation the CLI caught after cobra's flag parsing.
// exitCode returns 2 for it; the message is shown as is, with no usage dump.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usageErr(msg string) error { return usageError{msg} }

// outputFlagsConflict rejects --json with --quiet. Separate so it is testable.
func outputFlagsConflict() error {
	if jsonOutput && quietOutput {
		return usageErr("--json and --quiet contradict: one prints the envelope, the other only ids")
	}
	return nil
}

var rootCmd = &cobra.Command{
	Use:   "intuizi",
	Short: "Intuizi CLI",
	// False so a bad flag still prints usage; PersistentPreRun turns it on once
	// flags parse, so a failed API call is not followed by the usage block.
	SilenceErrors: true,
	// Runs after flag parsing. Cobra runs only the closest PersistentPreRun, so
	// a subcommand defining its own must repeat everything here: otherwise
	// --idempotency-key is ignored, a missing flag exits 1 and a failed call
	// exits 2.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		cmd.Root().SilenceUsage = true
		// Cobra runs these checks only after this hook, when flagsParsed would
		// already make the failure exit 1 like a failed call. Checked here
		// first, so a missing flag is the usage error the README promises.
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return usageErr(err.Error())
		}
		if err := cmd.ValidateFlagGroups(); err != nil {
			return usageErr(err.Error())
		}
		if baseURLFlag != "" {
			if err := config.ValidateBaseURL(baseURLFlag); err != nil {
				return usageErr(err.Error())
			}
			warnPlainHTTP(cmd.ErrOrStderr(), baseURLFlag)
		}
		api.IdempotencyKey = idempotencyKeyFlag
		flagsParsed = true
		return outputFlagsConflict()
	},
}

// warnPlainHTTP says so once when the token is about to travel in clear to a
// host that is not this machine. Loopback is where a local console runs.
func warnPlainHTTP(w io.Writer, raw string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" {
		return
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return
	}
	_, _ = fmt.Fprintf(w, "warning: %s is plain http - the token will travel unencrypted\n", raw)
}

// client builds an authenticated client, or explains why it cannot.
//
// A stored token was minted against the base_url saved beside it and goes
// only to that host: pairing it with another --base-url would hand the
// production credential to whatever host is named. INTUIZI_API_TOKEN is
// exempt, since CI sets the token and the URL together on purpose.
func client() (*api.Client, error) {
	base := config.BaseURL(baseURLFlag)
	if token, source := config.TokenSource(); source == config.SourceEnv {
		return api.New(base, token), nil
	}

	cfg, err := config.Load()
	if err != nil {
		if path, perr := config.Path(); perr == nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		return nil, err
	}
	if cfg.Token == "" {
		return nil, errors.New("not logged in - run 'intuizi auth login', or set " +
			config.EnvToken + " for CI")
	}
	if stored := trimURL(cfg.BaseURL); stored != "" && stored != trimURL(base) {
		return nil, fmt.Errorf("the stored token belongs to %s, not %s - run "+
			"'intuizi auth login --base-url %s' or set %s", stored, base, base, config.EnvToken)
	}
	return api.New(base, cfg.Token), nil
}

// trimURL makes two spellings of one host compare equal.
func trimURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// A contract scripts read: 0 success, 1 API or wait failure, 2 bad invocation.
// A signal reports 128+N, as the shell did before we caught signals.
const (
	exitError = 1
	exitUsage = 2
)

// prepareRoot finishes the command tree once every init has added to it.
// Called from Execute and from the test harness, which runs rootCmd directly.
// The completion command is added first so its group is covered too.
func prepareRoot() {
	rootCmd.InitDefaultCompletionCmd()
	rejectUnknownSubcommands(rootCmd)
}

// Cobra returns flag.ErrHelp for a non-runnable command before ValidateArgs
// runs, so Args alone changes nothing: give each group a Run that shows help
// (as today), which makes it runnable, and NoArgs then rejects the stray word
// with cobra's own "unknown command" text before PersistentPreRun, so
// exitCode returns 2. Root is skipped: legacyArgs already rejects there.
func rejectUnknownSubcommands(c *cobra.Command) {
	for _, sub := range c.Commands() {
		rejectUnknownSubcommands(sub)
	}
	if !c.HasParent() || c.Runnable() || !c.HasSubCommands() {
		return
	}
	c.Args = cobra.NoArgs
	c.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
}

func Execute() {
	prepareRoot()

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
	os.Exit(exitCode(err, sig, flagsParsed))
}

// exitCode maps how the run ended onto the documented codes. Split out from
// Execute so it can be tested without a subprocess.
func exitCode(err error, caught os.Signal, parsed bool) int {
	if caught != nil {
		if sig, ok := caught.(syscall.Signal); ok {
			return 128 + int(sig)
		}
		return exitError
	}
	var ue usageError
	if !parsed || errors.As(err, &ue) {
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

	rootCmd.PersistentFlags().BoolVar(&quietOutput, "quiet", false,
		"Print only ids on stdout, one per line, so commands compose in scripts")

	rootCmd.PersistentFlags().StringVar(&idempotencyKeyFlag, "idempotency-key", "",
		"Reuse this Idempotency-Key on create commands, to retry a create whose "+
			"outcome is unknown (default: a fresh key per create)")

	rootCmd.AddCommand(versionCmd)
}
