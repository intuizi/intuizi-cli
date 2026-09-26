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
  2. the token 'intuizi auth login' stored: in the OS credential store when
     there is one, otherwise in the config file

For CI, mint a token in the console at My Account > API Tokens on a dedicated
service account, rather than running 'auth login'.

Run 'intuizi auth status' to see which token is in use, which account it
belongs to and where it is held.`,
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

If a working token is already stored for the same console and the same
account it is reused rather than minting another - accounts are capped at 10
active tokens - and stderr names the account it belongs to. Without --email
the stored token is kept; an --email other than the stored account's, compared
ignoring case, mints a token for that account and replaces the stored one. A
token stored before accounts were recorded (v0.1.3 and earlier) cannot be
matched, so the first --email login over it mints a new one. Run 'intuizi auth
logout' first if you genuinely need a fresh one. A token is bound to the
console that minted it, so logging in with a different --base-url mints a new
one and replaces the stored base URL, token and account together.

The token goes to the OS credential store when there is one, otherwise to the
config file with owner-only permissions; the account email is kept in the
config file either way, so 'auth status' can name it. The token is not
affected by logging in elsewhere, and 'auth logout' does not revoke it on the
server - it only forgets it locally.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return login(cmd, email, password)
		},
	}

	cmd.Flags().StringVar(&email, "email", "",
		"Account email; prompted for on a terminal, and required to mint a\ntoken when stdin is not a terminal")
	cmd.Flags().StringVar(&password, "password", "",
		"Account password (discouraged - visible in shell history; pipe it on stdin instead)")
	return cmd
}

// saveConfig stores what login minted and reports where the token went. A var
// so a test can stand in for a credential store, which cmd tests never reach.
var saveConfig = config.SaveWhere

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
	// Not "no token": minting on a locked keychain spends one of the ten
	// slots, and logout cannot give it back.
	if err := storeUnavailable(from); err != nil {
		return err
	}
	// Whether the stored token is known to be dead, for the replace line.
	dead := expired(cfg.ExpiresAt)
	// Another account's token is not reused however live it is: checked
	// first, so the old token is not even sent to find out.
	if sameAccount(email, cfg.Email) {
		if storedTokenUsable(ctx, cfg, stored, base) {
			config.MigrateToken(cfg, from)
			reportReuse(stderr, base, cfg.Email)
			warnEnvToken(stderr, "is set and takes precedence over the stored token")
			return nil
		}
		// For a token this console holds, only expiry or a 401 says no.
		dead = true
	}
	// Whether this login replaces a token this console holds, and whose it
	// is: "" for one stored before accounts were recorded. Taken now, as cfg
	// is about to hold the new account.
	heldHere := stored != "" && (cfg.BaseURL == "" || sameBase(cfg.BaseURL, base))
	replaced := cfg.Email

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

	// Base, token and account change together: the token is only good for
	// this base, and only names this account.
	cfg.BaseURL = base
	cfg.Token = res.Token
	cfg.ExpiresAt = res.ExpiresAt
	cfg.Email = strings.TrimSpace(email)
	where, err := saveConfig(cfg)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stderr, "Logged in to %s as %s\n", base, cfg.Email)
	if heldHere {
		reportReplaced(stderr, replaced, cfg.Email, dead)
	}
	// Worded as 'auth status' reports the source.
	if where == config.SourceKeyring {
		_, _ = fmt.Fprintln(stderr, "Token saved in the OS credential store")
	} else {
		_, _ = fmt.Fprintf(stderr, "Token saved to %s\n", path)
	}
	if exp := formatExpiry(res.ExpiresAt); exp != "" {
		_, _ = fmt.Fprintf(stderr, "Expires %s\n", exp)
	}
	warnEnvToken(stderr, "is set and takes precedence over the token just saved")
	return nil
}

// sameAccount reports whether a login for given may keep the token stored
// for stored. No --email asks for no other account. A stored token with no
// recorded account cannot be matched to one, so any --email is another.
func sameAccount(given, stored string) bool {
	given = strings.TrimSpace(given)
	if given == "" {
		return true
	}
	return strings.EqualFold(given, strings.TrimSpace(stored))
}

// reportReplaced says when a login replaced a token this console held that
// may belong to someone other than now: a live one still holds one of that
// account's ten slots. replaced is the old token's account, "" when it was
// stored before accounts were recorded; a replaced token of the account just
// logged in needs no line. dead says it is known to no longer work.
func reportReplaced(w io.Writer, replaced, now string, dead bool) {
	const live = "it stays valid until it expires or you revoke it at My Account > API Tokens."
	switch {
	case replaced == "" && dead:
		_, _ = fmt.Fprintln(w, "Replaced the stored token, whose account was not recorded and which was no longer valid.")
	case replaced == "":
		_, _ = fmt.Fprintln(w, "Replaced the stored token, whose account was not recorded; "+live)
	case sameAccount(now, replaced):
	case dead:
		_, _ = fmt.Fprintf(w, "Replaced the token stored for %s, which was no longer valid.\n", replaced)
	default:
		_, _ = fmt.Fprintf(w, "Replaced the token stored for %s; %s\n", replaced, live)
	}
}

// reportReuse says which account a kept token belongs to, and how to get
// another: stderr, like the rest of login.
func reportReuse(w io.Writer, base, email string) {
	if email != "" {
		_, _ = fmt.Fprintf(w, "Already logged in to %s as %s\n", base, email)
		_, _ = fmt.Fprintln(w, "Pass --email to log in as another account, or run 'intuizi auth logout' "+
			"first if you need a new token.")
		return
	}
	_, _ = fmt.Fprintf(w, "Already logged in to %s (the account was not recorded for this token)\n", base)
	_, _ = fmt.Fprintln(w, "'intuizi auth login --email <address>' mints a token that records it; "+
		"run 'intuizi auth logout' first if you need a new token.")
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
	if expired(cfg.ExpiresAt) {
		return false
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

// expired reports an expiry that has passed. No expiry, or one that does not
// parse, is not known to have passed.
func expired(iso string) bool {
	t, err := time.Parse(time.RFC3339, iso)
	return err == nil && !t.After(time.Now())
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
		// Not the stored token, so the stored account says nothing about it.
		_, _ = fmt.Fprintf(stdout, "Account:  unknown (the token comes from %s, so the account "+
			"is not known locally)\n", config.EnvToken)
	case config.SourceKeyring, config.SourceConfig:
		// Every other command refuses it here, and --verify would send it
		// to this host; its account and expiry are another console's.
		if err := otherConsole(cfg.BaseURL, base); err != nil {
			_, _ = fmt.Fprintf(stdout, "Token:    none for this console (the stored one belongs to %s)\n",
				trimURL(cfg.BaseURL))
			return err
		}
		if source == config.SourceKeyring {
			_, _ = fmt.Fprintln(stdout, "Token:    present (in the OS credential store)")
		} else {
			_, _ = fmt.Fprintf(stdout, "Token:    present (from %s)\n", path)
		}
		printStored(cmd, cfg)
	case config.SourceUnavailable:
		_, _ = fmt.Fprintln(stdout, "Token:    unknown (the credential store did not answer)")
		return storeUnavailable(source)
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

// printStored reports the stored token's account and expiry on stdout, with
// any expiry warning on stderr.
func printStored(cmd *cobra.Command, cfg *config.Config) {
	stdout := cmd.OutOrStdout()
	if cfg.Email != "" {
		_, _ = fmt.Fprintf(stdout, "Account:  %s\n", cfg.Email)
	} else {
		_, _ = fmt.Fprintln(stdout, "Account:  unknown (not recorded for this token; "+
			"'intuizi auth login --email <address>' mints a token that records it)")
	}
	if exp := formatExpiry(cfg.ExpiresAt); exp != "" {
		_, _ = fmt.Fprintf(stdout, "Expires:  %s\n", exp)
	}
	warnNearExpiry(cmd.ErrOrStderr(), cfg.ExpiresAt)
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

	// The console asked for, not whichever the file happens to hold.
	had, err := config.ClearToken(config.BaseURL(baseURLFlag))
	if err != nil {
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
