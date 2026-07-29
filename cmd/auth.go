package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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

var authCmd = &cobra.Command{
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

// --------------------------------------------------------------------------------- login

var (
	loginEmail    string
	loginPassword string
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and store an API token",
	Long: `Log in and store an API token.

Prompts for an email and password unless --email and --password are given.
When stdin is not a terminal the password is read from stdin, so it works
unattended:

    echo "$PASSWORD" | intuizi auth login --email you@example.com

If a working token is already stored it is reused rather than minting another -
accounts are capped at 10 active tokens. Run 'intuizi auth logout' first if you
genuinely need a fresh one.

The token is written to the config file with owner-only permissions. It is not
affected by logging in elsewhere, and 'auth logout' does not revoke it on the
server - it only forgets it locally.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		base := config.BaseURL(baseURLFlag)

		// Accounts cap at 10 active tokens and logout doesn't revoke, so don't
		// mint another when a working one is already stored.
		if storedTokenUsable(cmd.Context(), base) {
			fmt.Printf("Already logged in to %s\n", base)
			fmt.Println("Run 'intuizi auth logout' first if you need a new token.")
			return nil
		}

		email, err := readEmail()
		if err != nil {
			return err
		}
		password, err := readPassword()
		if err != nil {
			return err
		}

		res, err := api.MintAPIToken(cmd.Context(), base, email, password)
		if err != nil {
			return err
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.BaseURL = base
		cfg.Token = res.Token
		cfg.ExpiresAt = res.ExpiresAt
		if err := config.Save(cfg); err != nil {
			return err
		}

		path, _ := config.Path()
		fmt.Printf("Logged in to %s\n", base)
		fmt.Printf("Token saved to %s\n", path)
		if exp := formatExpiry(res.ExpiresAt); exp != "" {
			fmt.Printf("Expires %s\n", exp)
		}
		if os.Getenv(config.EnvToken) != "" {
			fmt.Fprintf(os.Stderr,
				"\nWarning: %s is set and takes precedence over the token just saved.\n",
				config.EnvToken)
		}
		return nil
	},
}

// storedTokenUsable reports whether the config holds a token the API still
// accepts. Expiry is checked locally first to avoid a pointless request.
func storedTokenUsable(ctx context.Context, base string) bool {
	tok, src := config.TokenSource()
	if src != config.SourceConfig || tok == "" {
		return false
	}

	cfg, err := config.Load()
	if err != nil {
		return false
	}
	if cfg.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, cfg.ExpiresAt); err == nil && !t.After(time.Now()) {
			return false
		}
	}

	// Not expired is not enough - it may have been revoked in the console.
	err = api.New(base, tok).Get(ctx, verifyPath, nil)
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

// readEmail takes --email, otherwise prompts. Scripts must use the flag: there
// is no terminal to prompt on.
func readEmail() (string, error) {
	if loginEmail != "" {
		return loginEmail, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("not a terminal - pass --email")
	}

	fmt.Print("Email: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
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
func readPassword() (string, error) {
	if loginPassword != "" {
		return loginPassword, nil
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Print("Password: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // ReadPassword swallows the newline the user typed
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("password is required")
	}
	return string(raw), nil
}

// --------------------------------------------------------------------------------- status

var statusVerify bool

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether you are logged in",
	RunE: func(cmd *cobra.Command, args []string) error {
		base := config.BaseURL(baseURLFlag)
		path, _ := config.Path()

		fmt.Printf("Base URL: %s\n", base)

		token, source := config.TokenSource()
		switch source {
		case config.SourceEnv:
			fmt.Printf("Token:    present (from %s)\n", config.EnvToken)
		case config.SourceConfig:
			fmt.Printf("Token:    present (from %s)\n", path)
			if cfg, err := config.Load(); err == nil {
				if exp := formatExpiry(cfg.ExpiresAt); exp != "" {
					fmt.Printf("Expires:  %s\n", exp)
				}
			}
		default:
			fmt.Println("Token:    none")
			return errors.New("not logged in - run 'intuizi auth login'")
		}

		if statusVerify {
			c := api.New(base, token)
			if err := c.Get(cmd.Context(), verifyPath, nil); err != nil {
				// Only a 401 means the token is the problem. A 500, a timeout or
				// a bad URL would otherwise send people off to re-authenticate.
				if errors.Is(err, api.ErrUnauthorized) {
					return fmt.Errorf("token rejected: %w", err)
				}
				return fmt.Errorf("could not verify the token: %w", err)
			}
			fmt.Println("Verified: the API accepted this token")
		}
		return nil
	},
}

// --------------------------------------------------------------------------------- logout

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Forget the stored token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		had := cfg.Token != ""

		if err := config.ClearToken(); err != nil {
			return err
		}

		if had {
			fmt.Println("Removed the stored token.")
			fmt.Println("It stays valid until it expires or you revoke it at My Account > API Tokens.")
		} else {
			fmt.Println("No stored token to remove.")
		}

		if os.Getenv(config.EnvToken) != "" {
			fmt.Fprintf(os.Stderr,
				"\nWarning: %s is still set in this shell, so you remain authenticated.\n",
				config.EnvToken)
		}
		return nil
	},
}

// --------------------------------------------------------------------------------- helpers

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
	authLoginCmd.Flags().StringVar(&loginEmail, "email", "", "Account email")
	authLoginCmd.Flags().StringVar(&loginPassword, "password", "",
		"Account password (discouraged - visible in shell history; pipe it on stdin instead)")

	authStatusCmd.Flags().BoolVar(&statusVerify, "verify", false,
		"Check the token against the API")

	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}
