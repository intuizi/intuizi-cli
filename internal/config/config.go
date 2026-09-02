package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBaseURL is the production console. The --base-url flag overrides it;
// see BaseURL for the full precedence chain.
const DefaultBaseURL = "https://console.intuizi.com"

// EnvToken holds a bearer token for CI, where `auth login` is not run
// It takes precedence over the stored token
const EnvToken = "INTUIZI_API_TOKEN"

// Token sources, as reported by TokenSource
const (
	SourceNone   = ""
	SourceEnv    = "env"
	SourceConfig = "config"
)

type Config struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func configDir() (string, error) {
	if runtime.GOOS == "windows" {
		return os.UserConfigDir()
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// Path returns the location of the config file, whether or not it exists.
func Path() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "intuizi", "config.json"), nil
}

// Load reads the config file. A missing file is not an error - it yields a zero
// Config, so first-run callers take the same path as everyone else.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes the config atomically with owner-only permissions.
//
// The write goes to a temp file created at 0600 and is then renamed over the
// target. Writing in place and calling os.Chmod afterwards would leave the
// token readable at the old permissions for the moment between the two calls;
// os.WriteFile's perm argument only applies when it creates the file, so it
// cannot tighten an existing one.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Same directory as the target, so the rename stays on one filesystem
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// BaseURL resolves the console URL: the --base-url flag wins, then the stored
// value, then DefaultBaseURL. Pass "" when no flag was given.
//
// A trailing slash is trimmed so callers can join paths with a leading slash
// without producing a double slash.
func BaseURL(override string) string {
	if u := strings.TrimSpace(override); u != "" {
		return strings.TrimRight(u, "/")
	}
	if cfg, err := Load(); err == nil && cfg.BaseURL != "" {
		return strings.TrimRight(cfg.BaseURL, "/")
	}
	return DefaultBaseURL
}

// TokenSource returns the active token and where it came from, so `auth status`
// can report the source without printing the secret. An unreadable config file
// is reported as no token rather than an error - status should still be able to
// say "not logged in".
func TokenSource() (string, string) {
	if t := os.Getenv(EnvToken); t != "" {
		return t, SourceEnv
	}

	cfg, err := Load()
	if err != nil || cfg.Token == "" {
		return "", SourceNone
	}
	return cfg.Token, SourceConfig
}

// Token returns the active bearer token, or "" if there is none.
func Token() string {
	t, _ := TokenSource()
	return t
}

// ClearToken removes the stored token but keeps the file, which also holds the
// base URL. It does not affect EnvToken - the caller should warn when that is
// set, since logout cannot unset the caller's environment.
//
// This is deliberately local-only. POST /api/v2/auth/api-token/revoke exists but
// is all-or-nothing: it revokes every api token on the account, including one CI
// may be using. Do not call it from logout. The token cleared here stays valid
// server-side until it expires or is revoked individually at My Account > API
// Tokens.
func ClearToken() error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if cfg.Token == "" && cfg.ExpiresAt == "" {
		return nil
	}
	cfg.Token = ""
	cfg.ExpiresAt = ""
	return Save(cfg)
}
