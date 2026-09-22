package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/config"
)

// verifyPath is a cheap authenticated GET used to check a token is live.
const verifyPath = "/analyses/reference/common/operators"

// The terminal is reached through these so tests can stand in for it:
// term.ReadPassword blocks on a real tty and nothing else. The check stays on
// os.Stdin rather than cmd.InOrStdin() because only a real file has a tty.
var (
	stdinIsTerminal  = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	termGetState     = term.GetState
	termRestore      = term.Restore
	termReadPassword = term.ReadPassword
)

func authCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
		Long: `Manage authentication.

The CLI looks for a token in two places, highest priority first:

  1. the INTUIZI_API_TOKEN environment variable, intended for CI
  2. the config file written by 'intuizi auth login'

For CI, mint a token in the console at My Account > API Tokens on a dedicated
service account, rather than running 'auth login'.

Run 'intuizi auth status' to see which token is in use and where the file lives.`,
	}
	cmd.AddCommand(authLoginCommand(), authStatusCommand(), authLogoutCommand())
	return cmd
}

// --------------------------------------------------------------------------------- login

func authLoginCommand() *cobra.Command {
	var email, password string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in and store an API token",
		Long: `Log in and store an API token.

Prompts for an email and password unless --email and --password are given.
When stdin is not a terminal the password is read from stdin, so it works
unattended:

    echo "$PASSWORD" | intuizi auth login --email you@example.com

If a working token is already stored for the same console it is reused rather
than minting another - accounts are capped at 10 active tokens. Run 'intuizi
auth logout' first if you genuinely need a fresh one. A token is bound to the
console that minted it, so logging in with a different --base-url mints a new
one and replaces the stored base URL and token together.

The token is written to the config file with owner-only permissions. It is not
affected by logging in elsewhere, and 'auth logout' does not revoke it on the
server - it only forgets it locally.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return login(cmd, email, password)
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "Account email")
	cmd.Flags().StringVar(&password, "password", "",
		"Account password (discouraged - visible in shell history; pipe it on stdin instead)")
	return cmd
}

// login is commentary end to end - nothing here is data for a pipe - so every
// line goes to stderr.
func login(cmd *cobra.Command, email, password string) error {
	ctx := cmd.Context()
	stderr := cmd.ErrOrStderr()
	base := config.BaseURL(baseURLFlag)

	// Before minting: a corrupt file or an unwritable directory would otherwise
	// burn one of the account's ten token slots on every attempt and store
	// nothing, and a slot cannot be freed without revoking CI's token too.
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := config.EnsureDir(); err != nil {
		return fmt.Errorf("config directory is not writable: %w", err)
	}
	path, _ := config.Path()

	// Accounts cap at 10 active tokens and logout doesn't revoke, so don't
	// mint another when a working one is already stored for this console.
	stored, from := config.StoredToken(cfg)
	if storedTokenUsable(ctx, cfg, stored, base) {
		config.MigrateToken(cfg, from)
		_, _ = fmt.Fprintf(stderr, "Already logged in to %s\n", base)
		_, _ = fmt.Fprintln(stderr, "Run 'intuizi auth logout' first if you need a new token.")
		warnEnvToken(stderr, "is set and takes precedence over the stored token")
		return nil
	}

	email, err = readEmail(cmd, email)
	if err != nil {
		return err
	}
	password, err = readPassword(cmd, password)
	if err != nil {
		return err
	}

	res, err := api.MintAPIToken(ctx, base, email, password)
	if err != nil {
		return err
	}

	// Base and token change together: the token is only good for this base.
	cfg.BaseURL = base
	cfg.Token = res.Token
	cfg.ExpiresAt = res.ExpiresAt
	if err := config.Save(cfg); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stderr, "Logged in to %s\n", base)
	_, _ = fmt.Fprintf(stderr, "Token saved to %s\n", path)
	if exp := formatExpiry(res.ExpiresAt); exp != "" {
		_, _ = fmt.Fprintf(stderr, "Expires %s\n", exp)
	}
	warnEnvToken(stderr, "is set and takes precedence over the token just saved")
	return nil
}

// storedTokenUsable reports whether cfg holds a token the API at base still
// accepts. Expiry is checked locally first to avoid a pointless request.
func storedTokenUsable(ctx context.Context, cfg *config.Config, token, base string) bool {
	// Passed in: the store may hold it, leaving cfg.Token empty, and minting
	// a replacement costs one of ten slots.
	if token == "" {
		return false
	}
	// A token is bound to the console that minted it. Checking it against a
	// different base would send it there, and an unreachable host below would
	// then pass for "usable" and nothing would be stored for the new base.
	if cfg.BaseURL != "" && !sameBase(cfg.BaseURL, base) {
		return false
	}
	if cfg.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, cfg.ExpiresAt); err == nil && !t.After(time.Now()) {
			return false
		}
	}

	// Not expired is not enough - it may have been revoked in the console.
	err := api.New(base, token).Get(ctx, verifyPath, nil, nil)
	if err == nil {
		return true
	}
	if errors.Is(err, api.ErrUnauthorized) {
		return false // the server says it's dead - mint a replacement
	}

	// Server error or unreachable: we can't prove the token is bad. Minting
	// costs one of 10 slots for a year and can't be undone without revoking
	// CI's token too, so keep what we have. 'auth logout' forces a new one.
	return true
}

// sameBase compares two base URLs the way BaseURL normalises them.
func sameBase(a, b string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	return norm(a) == norm(b)
}

// warnEnvToken says when INTUIZI_API_TOKEN will shadow whatever login or
// logout just did to the file; the CLI cannot change the caller's shell.
func warnEnvToken(w io.Writer, effect string) {
	if os.Getenv(config.EnvToken) == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "\nWarning: %s %s.\n", config.EnvToken, effect)
}

// readEmail takes --email, otherwise prompts. Scripts must use the flag: there
// is no terminal to prompt on.
func readEmail(cmd *cobra.Command, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !stdinIsTerminal() {
		return "", usageErr("not a terminal - pass --email")
	}

	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Email: ")
	line, err := awaitLine(cmd.Context(), cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	email := strings.TrimSpace(line)
	if email == "" {
		return "", errors.New("email is required")
	}
	return email, nil
}

// readPassword takes --password, then stdin when piped, then a hidden prompt.
func readPassword(cmd *cobra.Command, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}

	if !stdinIsTerminal() {
		line, err := awaitLine(cmd.Context(), cmd.InOrStdin())
		if err != nil && line == "" {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return "", errors.New("password is required")
		}
		return password, nil
	}

	stderr := cmd.ErrOrStderr()
	_, _ = fmt.Fprint(stderr, "Password: ")
	raw, err := readHidden(cmd.Context(), int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(stderr) // ReadPassword swallows the newline the user typed
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("password is required")
	}
	return string(raw), nil
}

// awaitLine reads one line off the main goroutine so a Ctrl-C at the prompt
// ends the command: the signal handler only cancels the context, which a
// blocking read never observes, and the second press would kill the process.
func awaitLine(ctx context.Context, in io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		ch <- result{line, err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.line, r.err
	}
}

// readHidden is awaitLine for the hidden prompt, with one more duty: ReadPassword
// turns echo off and restores it only when it returns, so an interrupted read
// would leave the user's shell needing `stty sane`. The state is captured up
// front and put back here when the context ends the prompt.
func readHidden(ctx context.Context, fd int) ([]byte, error) {
	state, err := termGetState(fd)
	if err != nil {
		return nil, err
	}

	type result struct {
		raw []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		raw, err := termReadPassword(fd)
		ch <- result{raw, err}
	}()

	select {
	case <-ctx.Done():
		_ = termRestore(fd, state)
		return nil, ctx.Err()
	case r := <-ch:
		return r.raw, r.err
	}
}

// --------------------------------------------------------------------------------- status

func authStatusCommand() *cobra.Command {
	var verify bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether you are logged in",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(cmd, verify)
		},
	}

	cmd.Flags().BoolVar(&verify, "verify", false, "Check the token against the API")
	return cmd
}

// status is the one auth command whose output is data, so it goes to stdout.
func status(cmd *cobra.Command, verify bool) error {
	stdout := cmd.OutOrStdout()
	base := config.BaseURL(baseURLFlag)
	path, _ := config.Path()

	token, source := config.TokenSource()

	// TokenSource hides a broken file behind "none"; someone who cannot log in
	// needs to know which file to fix, so read it here when it is in play.
	var cfg *config.Config
	if source != config.SourceEnv {
		var err error
		if cfg, err = config.Load(); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(stdout, "Base URL: %s\n", base)

	switch source {
	case config.SourceEnv:
		_, _ = fmt.Fprintf(stdout, "Token:    present (from %s)\n", config.EnvToken)
	case config.SourceKeyring:
		_, _ = fmt.Fprintf(stdout, "Token:    present (in the OS credential store)\n")
		if exp := formatExpiry(cfg.ExpiresAt); exp != "" {
			_, _ = fmt.Fprintf(stdout, "Expires:  %s\n", exp)
		}
		warnNearExpiry(cmd.ErrOrStderr(), cfg.ExpiresAt)
	case config.SourceConfig:
		_, _ = fmt.Fprintf(stdout, "Token:    present (from %s)\n", path)
		if exp := formatExpiry(cfg.ExpiresAt); exp != "" {
			_, _ = fmt.Fprintf(stdout, "Expires:  %s\n", exp)
		}
		warnNearExpiry(cmd.ErrOrStderr(), cfg.ExpiresAt)
	default:
		_, _ = fmt.Fprintln(stdout, "Token:    none")
		return errors.New("not logged in - run 'intuizi auth login'")
	}

	if verify {
		c := api.New(base, token)
		if err := c.Get(cmd.Context(), verifyPath, nil, nil); err != nil {
			// Only a 401 means the token is the problem. A 500, a timeout or
			// a bad URL would otherwise send people off to re-authenticate.
			if errors.Is(err, api.ErrUnauthorized) {
				return fmt.Errorf("token rejected: %w", err)
			}
			return fmt.Errorf("could not verify the token: %w", err)
		}
		_, _ = fmt.Fprintln(stdout, "Verified: the API accepted this token")
	}
	return nil
}

// --------------------------------------------------------------------------------- logout

func authLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the stored token",
		RunE:  logout,
	}
}

// logout, like login, is all commentary: stderr throughout.
func logout(cmd *cobra.Command, _ []string) error {
	stderr := cmd.ErrOrStderr()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token, _ := config.StoredToken(cfg)
	had := token != ""

	if err := config.ClearToken(); err != nil {
		return err
	}

	if had {
		_, _ = fmt.Fprintln(stderr, "Removed the stored token.")
		_, _ = fmt.Fprintln(stderr, "It stays valid until it expires or you revoke it at My Account > API Tokens.")
	} else {
		_, _ = fmt.Fprintln(stderr, "No stored token to remove.")
	}

	warnEnvToken(stderr, "is still set in this shell, so you remain authenticated")
	return nil
}

// --------------------------------------------------------------------------------- helpers

// A month is enough notice before a scheduled job starts failing at 401.
const expiryWarning = 30 * 24 * time.Hour

// Stderr, while the Expires: line stays on stdout: stdout is what scripts read.
func warnNearExpiry(w io.Writer, iso string) {
	if iso == "" {
		return
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return
	}
	switch left := time.Until(t); {
	case left <= 0:
		_, _ = fmt.Fprintf(w, "\nWarning: this token expired on %s. Run 'intuizi auth login'.\n",
			t.Format("2006-01-02"))
	case left < expiryWarning:
		_, _ = fmt.Fprintf(w, "\nWarning: this token expires in %d days. Run 'intuizi auth login' to mint a new one.\n",
			int(left.Hours()/24))
	}
}

// formatExpiry renders an ISO 8601 timestamp as a date plus days remaining.
// Returns "" when there is no expiry or it cannot be parsed.
func formatExpiry(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}

	days := int(time.Until(t).Hours() / 24)
	switch {
	case days < 0:
		return t.Format("2006-01-02") + " (expired)"
	case days == 0:
		return t.Format("2006-01-02") + " (today)"
	default:
		return fmt.Sprintf("%s (in %d days)", t.Format("2006-01-02"), days)
	}
}

func init() {
	rootCmd.AddCommand(authCommand())
}
