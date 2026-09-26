package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/intuizi/intuizi-cli/internal/config"
)

// authEnv isolates one auth test: a throwaway config dir so the developer's
// real ~/.config/intuizi is never read or written, no env token, no terminal,
// and --base-url pointed at base. It returns the config file's path.
func authEnv(t *testing.T, base string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
	t.Setenv(config.EnvNoKeyring, "1")
	t.Setenv(config.EnvToken, "")

	prevBase, prevTTY := baseURLFlag, stdinIsTerminal
	baseURLFlag = base
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { baseURLFlag, stdinIsTerminal = prevBase, prevTTY })

	return filepath.Join(dir, "intuizi", "config.json")
}

// runAuth executes one auth subcommand with stdin, returning stdout and stderr.
func runAuth(t *testing.T, cmd *cobra.Command, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd.SilenceUsage = true
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

// authCapture records what a fake console was asked.
type authCapture struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	auths  []string
}

func (c *authCapture) hits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

// fakeConsole fakes the two routes login touches: the token mint and the verify
// read. verifyStatus is what the verify GET answers; the mint always succeeds.
func fakeConsole(t *testing.T, token string, verifyStatus int) (*httptest.Server, *authCapture) {
	t.Helper()
	got := &authCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.paths = append(got.paths, r.URL.Path)
		got.bodies = append(got.bodies, string(b))
		got.auths = append(got.auths, r.Header.Get("Authorization"))
		got.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/api-token":
			_, _ = w.Write([]byte(`{"status":"success","code":200,"data":{"token":"` + token +
				`","expires_at":"2099-01-01T00:00:00+00:00"}}`))
		case "/api/v2" + verifyPath:
			w.WriteHeader(verifyStatus)
			_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[]}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func loadAuthConfig(t *testing.T, path string) config.Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config file %s: %v", raw, err)
	}
	return cfg
}

func storeAuthConfig(t *testing.T, path string, cfg config.Config) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// --------------------------------------------------------------------------------- login

func TestLoginStoresTokenBaseAndExpiry(t *testing.T) {
	srv, got := fakeConsole(t, "fresh-token", 200)
	path := authEnv(t, srv.URL)

	out, errb, err := runAuth(t, authLoginCommand(), "s3cret\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if got.hits() != 1 || got.paths[0] != "/api/v2/auth/api-token" {
		t.Fatalf("requests = %v, want exactly the mint", got.paths)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["email"] != "a@b.com" || body["password"] != "s3cret" {
		t.Errorf("mint body = %v", body)
	}

	cfg := loadAuthConfig(t, path)
	if cfg.Token != "fresh-token" || cfg.BaseURL != srv.URL || cfg.ExpiresAt != "2099-01-01T00:00:00+00:00" {
		t.Errorf("stored config = %+v", cfg)
	}
	// The account travels with the token, so status can name it.
	if cfg.Email != "a@b.com" {
		t.Errorf("stored email = %q, want a@b.com", cfg.Email)
	}
	if runtime.GOOS != "windows" { // owner-only bits are a Unix guarantee
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("config perms = %v, want 0600", perm)
		}
	}

	// Login prints commentary only, and on stderr: stdout is for data.
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	for _, want := range []string{"Logged in to " + srv.URL + " as a@b.com", "Token saved to " + path, "Expires 2099-01-01"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
}

// When the credential store takes the token, the file holds none, so naming
// the file as where it went would send someone looking in the wrong place.
func TestLoginSaysTheTokenWentToTheCredentialStore(t *testing.T) {
	srv, _ := fakeConsole(t, "fresh-token", 200)
	path := authEnv(t, srv.URL)

	// cmd tests never reach a real store, so stand in for a Save the store
	// accepted: the file keeps everything but the token.
	prev := saveConfig
	saveConfig = func(cfg *config.Config) (string, error) {
		out := *cfg
		out.Token = ""
		if err := config.Save(&out); err != nil {
			return "", err
		}
		return config.SourceKeyring, nil
	}
	t.Cleanup(func() { saveConfig = prev })

	_, errb, err := runAuth(t, authLoginCommand(), "s3cret\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb, "Token saved in the OS credential store") {
		t.Errorf("stderr does not name the credential store:\n%s", errb)
	}
	if strings.Contains(errb, path) {
		t.Errorf("stderr names the config file, which holds no token:\n%s", errb)
	}
}

func TestLoginTakesPasswordFromFlag(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)

	if _, _, err := runAuth(t, authLoginCommand(), "", "--email", "a@b.com", "--password", "flagged"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(got.bodies[0], `"password":"flagged"`) {
		t.Errorf("body = %s", got.bodies[0])
	}
}

// A script has no terminal to prompt on, so the email has to be a flag.
func TestLoginWithoutTerminalNeedsEmailFlag(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)

	_, _, err := runAuth(t, authLoginCommand(), "pw\n")
	if err == nil || !strings.Contains(err.Error(), "--email") {
		t.Fatalf("err = %v, want a hint to pass --email", err)
	}
	if code := exitCode(err, nil, true); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if got.hits() != 0 {
		t.Errorf("no request should be made without credentials, got %v", got.paths)
	}
}

func TestLoginRejectsEmptyPipedPassword(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)

	_, _, err := runAuth(t, authLoginCommand(), "", "--email", "a@b.com")
	if err == nil {
		t.Fatal("an empty stdin should not log in")
	}
	if got.hits() != 0 {
		t.Errorf("no request should be made without a password, got %v", got.paths)
	}
}

// K1: a stored token belongs to the console that minted it. Login for another
// --base-url must mint there, and must never send the stored token to it.
func TestLoginDoesNotReuseTokenBoundToAnotherBase(t *testing.T) {
	other, otherGot := fakeConsole(t, "unused", 200)
	target, targetGot := fakeConsole(t, "new-token", 200)
	path := authEnv(t, target.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: other.URL, Token: "old-token"})

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if strings.Contains(errb, "Already logged in") {
		t.Fatalf("reused a token bound to %s for %s:\n%s", other.URL, target.URL, errb)
	}
	if otherGot.hits() != 0 {
		t.Errorf("the old console was contacted: %v", otherGot.paths)
	}
	for _, a := range targetGot.auths {
		if strings.Contains(a, "old-token") {
			t.Errorf("the old token was sent to the new console: %q", a)
		}
	}
	if targetGot.hits() != 1 || targetGot.paths[0] != "/api/v2/auth/api-token" {
		t.Errorf("target requests = %v, want exactly the mint", targetGot.paths)
	}

	cfg := loadAuthConfig(t, path)
	if cfg.BaseURL != target.URL || cfg.Token != "new-token" {
		t.Errorf("stored config = %+v, want base and token replaced together", cfg)
	}
}

func TestLoginReusesUsableTokenForTheSameBase(t *testing.T) {
	cases := []struct{ name, stored string }{
		{"exact match", "%s"},
		{"trailing slash", "%s/"},
		{"surrounding space", "  %s  "},
		{"legacy config with no base", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := fakeConsole(t, "should-not-mint", 200)
			path := authEnv(t, srv.URL)
			stored := ""
			if tc.stored != "" {
				stored = strings.ReplaceAll(tc.stored, "%s", srv.URL)
			}
			storeAuthConfig(t, path, config.Config{BaseURL: stored, Token: "old-token", Email: "a@b.com"})

			// The same account, spelled differently: email is case-insensitive.
			_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "A@B.com")
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if !strings.Contains(errb, "Already logged in to "+srv.URL+" as a@b.com") {
				t.Errorf("stderr = %q, want the reuse notice naming the account", errb)
			}
			if got.hits() != 1 || got.paths[0] != "/api/v2"+verifyPath {
				t.Errorf("requests = %v, want only the verify read", got.paths)
			}
			if got.auths[0] != "Bearer old-token" {
				t.Errorf("verify used %q, want the stored token", got.auths[0])
			}
			if cfg := loadAuthConfig(t, path); cfg.Token != "old-token" {
				t.Errorf("token was replaced: %+v", cfg)
			}
		})
	}
}

// Without --email there is no other account asked for, so a live token is
// kept, and stderr says whose it is.
func TestLoginWithoutEmailReusesAndNamesTheAccount(t *testing.T) {
	srv, got := fakeConsole(t, "should-not-mint", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "old-token", Email: "a@b.com"})

	_, errb, err := runAuth(t, authLoginCommand(), "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb, "Already logged in to "+srv.URL+" as a@b.com") {
		t.Errorf("stderr = %q, want the account named", errb)
	}
	if got.hits() != 1 || got.paths[0] != "/api/v2"+verifyPath {
		t.Errorf("requests = %v, want only the verify read", got.paths)
	}
}

// The bug: logging in as another account kept the first account's token.
// A different --email mints for that account and replaces the stored one,
// without first sending the old token anywhere.
func TestLoginAsAnotherAccountMintsAndReplaces(t *testing.T) {
	srv, got := fakeConsole(t, "their-token", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "my-token", Email: "a@b.com"})

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "c@d.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if strings.Contains(errb, "Already logged in") {
		t.Fatalf("kept a@b.com's token for c@d.com:\n%s", errb)
	}
	if got.hits() != 1 || got.paths[0] != "/api/v2/auth/api-token" {
		t.Errorf("requests = %v, want exactly the mint", got.paths)
	}
	if !strings.Contains(got.bodies[0], `"email":"c@d.com"`) {
		t.Errorf("mint body = %s", got.bodies[0])
	}
	cfg := loadAuthConfig(t, path)
	if cfg.Token != "their-token" || cfg.Email != "c@d.com" {
		t.Errorf("stored config = %+v, want c@d.com's token", cfg)
	}
	// Not known to be dead - it was never even checked - so it may still work.
	for _, want := range []string{"Logged in to " + srv.URL + " as c@d.com",
		"Replaced the token stored for a@b.com", "stays valid"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
}

// Another account's token that had expired, or that the API refused, does not
// "stay valid until it expires": the replace line says it was already dead.
func TestLoginOverAnotherAccountsDeadToken(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		srv, _ := fakeConsole(t, "their-token", 200)
		path := authEnv(t, srv.URL)
		storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "my-token", Email: "a@b.com",
			ExpiresAt: "2020-01-01T00:00:00Z"})

		_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "c@d.com")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if !strings.Contains(errb, "Replaced the token stored for a@b.com") || strings.Contains(errb, "stays valid") {
			t.Errorf("stderr = %q, want the replace line without \"stays valid\"", errb)
		}
	})

	// A bare login verifies the stored token first; after a 401 the prompt
	// can still name another account.
	t.Run("rejected", func(t *testing.T) {
		srv, got := fakeConsole(t, "their-token", 401)
		path := authEnv(t, srv.URL)
		storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "revoked", Email: "a@b.com"})
		f := installFakeTerminal(t)
		close(f.release)

		cmd := authLoginCommand()
		cmd.SetContext(context.Background())
		_, errb, err := runAuth(t, cmd, "c@d.com\n")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if got.hits() != 2 || got.paths[1] != "/api/v2/auth/api-token" {
			t.Errorf("requests = %v, want verify then mint", got.paths)
		}
		if !strings.Contains(errb, "Replaced the token stored for a@b.com") || strings.Contains(errb, "stays valid") {
			t.Errorf("stderr = %q, want the replace line without \"stays valid\"", errb)
		}
	})
}

// A token stored before accounts were recorded belongs to nobody known, so
// an --email cannot be matched against it: it mints, and records the account.
// The old token still holds one of the account's ten slots, so stderr - all a
// script's log keeps - says it was replaced, as it does for a known account.
func TestLoginWithEmailOverALegacyTokenMints(t *testing.T) {
	srv, got := fakeConsole(t, "new-token", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "old-token", ExpiresAt: "2099-01-01T00:00:00Z"})

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if got.hits() != 1 || got.paths[0] != "/api/v2/auth/api-token" {
		t.Errorf("requests = %v, want exactly the mint", got.paths)
	}
	if cfg := loadAuthConfig(t, path); cfg.Token != "new-token" || cfg.Email != "a@b.com" {
		t.Errorf("stored config = %+v", cfg)
	}
	for _, want := range []string{"Logged in to " + srv.URL + " as a@b.com",
		"Replaced the stored token", "not recorded", "stays valid"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
}

// An expired legacy token is replaced too, but does not "stay valid".
func TestLoginWithEmailOverAnExpiredLegacyToken(t *testing.T) {
	srv, _ := fakeConsole(t, "new-token", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "old-token", ExpiresAt: "2020-01-01T00:00:00Z"})

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb, "Replaced the stored token") || !strings.Contains(errb, "no longer valid") ||
		strings.Contains(errb, "stays valid") {
		t.Errorf("stderr = %q, want the replace line saying it was no longer valid", errb)
	}
}

// A legacy token bound to another console is not "the stored token" here:
// like a known account's, it is replaced without a replace line.
func TestLoginWithEmailOverALegacyTokenForAnotherConsole(t *testing.T) {
	srv, _ := fakeConsole(t, "new-token", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: "https://elsewhere.example", Token: "old-token"})

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if strings.Contains(errb, "Replaced") {
		t.Errorf("stderr = %q, want no replace line for another console's token", errb)
	}
}

// Without --email a legacy token is still reused - minting would spend a
// slot nobody asked for - and stderr says the account is not recorded.
func TestLoginWithoutEmailOverALegacyTokenReuses(t *testing.T) {
	srv, got := fakeConsole(t, "should-not-mint", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "old-token"})

	_, errb, err := runAuth(t, authLoginCommand(), "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if got.hits() != 1 || got.paths[0] != "/api/v2"+verifyPath {
		t.Errorf("requests = %v, want only the verify read", got.paths)
	}
	for _, want := range []string{"Already logged in to " + srv.URL, "not recorded", "--email"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
}

// A 401 on the verify read means the token is dead; mint a replacement.
func TestLoginReplacesARejectedToken(t *testing.T) {
	srv, got := fakeConsole(t, "new-token", 401)
	path := authEnv(t, srv.URL)
	// The same account, so only the verify read can rule the token out.
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "revoked", Email: "a@b.com"})

	if _, _, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got.hits() != 2 || got.paths[1] != "/api/v2/auth/api-token" {
		t.Errorf("requests = %v, want verify then mint", got.paths)
	}
	if cfg := loadAuthConfig(t, path); cfg.Token != "new-token" {
		t.Errorf("stored config = %+v", cfg)
	}
}

// K2: minting costs one of ten token slots for a year. A config file that
// cannot be read or written must fail before the mint, not after it.
func TestLoginFailsBeforeMintingOnCorruptConfig(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfigBytes(t, path, "{not json")

	_, _, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err == nil {
		t.Fatal("a corrupt config should fail login")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name %s", err, path)
	}
	if code := exitCode(err, nil, true); code != exitError {
		t.Errorf("exit = %d, want %d", code, exitError)
	}
	if got.hits() != 0 {
		t.Errorf("a token slot was spent on a config that cannot be saved: %v", got.paths)
	}
}

func TestLoginFailsBeforeMintingOnUnwritableConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory creation on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	srv, got := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	xdg := filepath.Dir(filepath.Dir(path))
	if err := os.Chmod(xdg, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(xdg, 0700) })

	_, _, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err == nil {
		t.Fatal("an unwritable config dir should fail login")
	}
	if got.hits() != 0 {
		t.Errorf("a token slot was spent on a config that cannot be saved: %v", got.paths)
	}
}

func TestLoginWarnsWhenEnvTokenShadowsTheNewOne(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)
	t.Setenv(config.EnvToken, "from-ci")

	_, errb, err := runAuth(t, authLoginCommand(), "pw\n", "--email", "a@b.com")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb, config.EnvToken) || !strings.Contains(errb, "precedence") {
		t.Errorf("stderr = %q, want a warning about %s", errb, config.EnvToken)
	}
}

func storeAuthConfigBytes(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

// --------------------------------------------------------------------------------- prompts

// blockingReader blocks every Read until released, standing in for a terminal
// nobody is typing at.
type blockingReader struct{ release chan struct{} }

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

// fakeTerminal swaps the tty hooks for ones a test controls. Reads block until
// release is closed; restores are recorded.
type fakeTerminal struct {
	state    *term.State
	release  chan struct{}
	onRead   func()
	mu       sync.Mutex
	restored []*term.State
}

func installFakeTerminal(t *testing.T) *fakeTerminal {
	t.Helper()
	f := &fakeTerminal{state: new(term.State), release: make(chan struct{})}

	prevTTY, prevGet, prevRestore, prevRead := stdinIsTerminal, termGetState, termRestore, termReadPassword
	stdinIsTerminal = func() bool { return true }
	termGetState = func(int) (*term.State, error) { return f.state, nil }
	termRestore = func(_ int, st *term.State) error {
		f.mu.Lock()
		f.restored = append(f.restored, st)
		f.mu.Unlock()
		return nil
	}
	termReadPassword = func(int) ([]byte, error) {
		if f.onRead != nil {
			f.onRead()
		}
		<-f.release
		return []byte("typed"), nil
	}
	t.Cleanup(func() {
		stdinIsTerminal, termGetState, termRestore, termReadPassword = prevTTY, prevGet, prevRestore, prevRead
	})
	return f
}

func (f *fakeTerminal) restores() []*term.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*term.State(nil), f.restored...)
}

// K3: Ctrl-C at the hidden prompt. The signal handler only cancels the
// context; the read has to observe it, and the terminal has to come back with
// echo on, or the user's shell is left needing `stty sane`.
func TestReadPasswordCancelRestoresTheTerminal(t *testing.T) {
	f := installFakeTerminal(t)
	defer close(f.release) // let the blocked reader goroutine exit

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onRead = cancel // the interrupt arrives while the read is blocked

	cmd := authLoginCommand()
	cmd.SetContext(ctx)
	var errb bytes.Buffer
	cmd.SetErr(&errb)

	_, err := readPassword(cmd, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled so Execute maps it to 130", err)
	}
	if got := f.restores(); len(got) != 1 || got[0] != f.state {
		t.Errorf("terminal restores = %v, want exactly one with the captured state", got)
	}
	if !strings.HasSuffix(errb.String(), "\n") {
		t.Errorf("stderr = %q, want a newline after the interrupted prompt", errb.String())
	}
}

func TestReadPasswordFromTerminal(t *testing.T) {
	f := installFakeTerminal(t)
	close(f.release) // the read returns at once

	cmd := authLoginCommand()
	cmd.SetContext(context.Background())
	var errb bytes.Buffer
	cmd.SetErr(&errb)

	pw, err := readPassword(cmd, "")
	if err != nil || pw != "typed" {
		t.Fatalf("got (%q, %v), want (typed, nil)", pw, err)
	}
	// ReadPassword restores the terminal itself on return; a second restore
	// from us would be harmless but means the select took the wrong branch.
	if got := f.restores(); len(got) != 0 {
		t.Errorf("restores = %v, want none on a completed read", got)
	}
	if !strings.Contains(errb.String(), "Password: ") {
		t.Errorf("prompt should be on stderr, got %q", errb.String())
	}
}

func TestReadEmailPromptObservesCancellation(t *testing.T) {
	prevTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = prevTTY })

	in := &blockingReader{release: make(chan struct{})}
	defer close(in.release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := authLoginCommand()
	cmd.SetContext(ctx)
	cmd.SetIn(in)
	var errb bytes.Buffer
	cmd.SetErr(&errb)

	if _, err := readEmail(cmd, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(errb.String(), "Email: ") {
		t.Errorf("prompt should be on stderr, got %q", errb.String())
	}
}

// --------------------------------------------------------------------------------- status

func TestStatusWithEnvToken(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)
	t.Setenv(config.EnvToken, "from-ci")

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Base URL: "+srv.URL) {
		t.Errorf("stdout = %q, want the base URL", out)
	}
	if !strings.Contains(out, "present (from "+config.EnvToken+")") {
		t.Errorf("stdout = %q, want the env source", out)
	}
	if strings.Contains(out, "from-ci") {
		t.Errorf("status printed the secret: %q", out)
	}
	// The env token is not the stored one, so the stored account is not its.
	if !strings.Contains(out, "Account:  unknown") || !strings.Contains(out, "not known locally") {
		t.Errorf("stdout = %q, want the account reported unknown", out)
	}
}

// Even when the file names an account: INTUIZI_API_TOKEN is not that token.
func TestStatusWithEnvTokenIgnoresTheStoredAccount(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stored", Email: "a@b.com"})
	t.Setenv(config.EnvToken, "from-ci")

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "a@b.com") {
		t.Errorf("named the stored account for the env token:\n%s", out)
	}
}

// A config written before accounts were recorded: say so, and how to fix it.
func TestStatusWithoutARecordedAccount(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stored"})

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"Account:  unknown", "auth login --email"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestStatusWithConfigToken(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stored", ExpiresAt: "2099-01-01T00:00:00+00:00", Email: "a@b.com"})

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "present (from "+path+")") {
		t.Errorf("stdout = %q, want the file as the source", out)
	}
	if !strings.Contains(out, "Account:  a@b.com") {
		t.Errorf("stdout = %q, want the account", out)
	}
	if !strings.Contains(out, "Expires:  2099-01-01") {
		t.Errorf("stdout = %q, want the expiry", out)
	}
	if strings.Contains(out, "stored") {
		t.Errorf("status printed the secret: %q", out)
	}
}

// A --base-url other than the stored token's console: every other command
// refuses that token, and --verify used to send it to the other host. Status
// says there is none for this console, exits 1, and sends nothing.
func TestStatusAgainstAnotherConsole(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: "https://console.intuizi.com", Token: "stored",
		ExpiresAt: "2099-01-01T00:00:00+00:00", Email: "a@b.com"})

	out, _, err := runAuth(t, authStatusCommand(), "", "--verify")
	if err == nil {
		t.Fatal("status against another console should fail")
	}
	if code := exitCode(err, nil, true); code != exitError {
		t.Errorf("exit = %d, want %d", code, exitError)
	}
	for _, want := range []string{"belongs to https://console.intuizi.com", "not " + srv.URL,
		"auth login --base-url " + srv.URL} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if !strings.Contains(out, "Token:    none for this console") || !strings.Contains(out, "https://console.intuizi.com") {
		t.Errorf("stdout = %q, want no token for this console", out)
	}
	// Another console's account and expiry say nothing about this one.
	if strings.Contains(out, "a@b.com") || strings.Contains(out, "Expires") {
		t.Errorf("stdout describes the other console's token:\n%s", out)
	}
	if got.hits() != 0 {
		t.Errorf("the token must not travel to the other host, got %v", got.paths)
	}
}

// Only a different host is another console: a trailing slash is not, and a
// config from before base URLs were stored names none.
func TestStatusSameConsoleIsNotAMismatch(t *testing.T) {
	for name, stored := range map[string]func(string) string{
		"trailing slash": func(u string) string { return u + "/" },
		"no base url":    func(string) string { return "" },
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := fakeConsole(t, "tok", 200)
			path := authEnv(t, srv.URL)
			storeAuthConfig(t, path, config.Config{BaseURL: stored(srv.URL), Token: "stored", Email: "a@b.com"})

			out, _, err := runAuth(t, authStatusCommand(), "", "--verify")
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if !strings.Contains(out, "Account:  a@b.com") || !strings.Contains(out, "Verified") {
				t.Errorf("stdout = %q", out)
			}
		})
	}
}

func TestStatusWithNoTokenExitsNonZero(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want not logged in", err)
	}
	if code := exitCode(err, nil, true); code != exitError {
		t.Errorf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(out, "Token:    none") {
		t.Errorf("stdout = %q", out)
	}
}

// K2: "Token: none" hides the real problem when the file exists but is broken.
func TestStatusReportsABrokenConfigByPath(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfigBytes(t, path, "{not json")

	out, _, err := runAuth(t, authStatusCommand(), "")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
	if strings.Contains(out, "Token:    none") {
		t.Errorf("a broken file was reported as no token:\n%s", out)
	}
}

func TestStatusVerify(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		srv, got := fakeConsole(t, "tok", 200)
		path := authEnv(t, srv.URL)
		storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stored"})

		out, _, err := runAuth(t, authStatusCommand(), "", "--verify")
		if err != nil {
			t.Fatalf("status --verify: %v", err)
		}
		if !strings.Contains(out, "Verified") {
			t.Errorf("stdout = %q, want the verified line", out)
		}
		if got.hits() != 1 || got.paths[0] != "/api/v2"+verifyPath || got.auths[0] != "Bearer stored" {
			t.Errorf("verify request = %v %v", got.paths, got.auths)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		srv, _ := fakeConsole(t, "tok", 401)
		path := authEnv(t, srv.URL)
		storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stale"})

		_, _, err := runAuth(t, authStatusCommand(), "", "--verify")
		if err == nil || !strings.Contains(err.Error(), "token rejected") {
			t.Fatalf("err = %v, want token rejected", err)
		}
	})
	t.Run("server error is not blamed on the token", func(t *testing.T) {
		srv, _ := fakeConsole(t, "tok", 500)
		path := authEnv(t, srv.URL)
		storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "fine"})

		_, _, err := runAuth(t, authStatusCommand(), "", "--verify")
		if err == nil || !strings.Contains(err.Error(), "could not verify") || strings.Contains(err.Error(), "rejected") {
			t.Fatalf("err = %v, want could not verify", err)
		}
	})
}

// --------------------------------------------------------------------------------- logout

func TestLogoutRemovesTheStoredToken(t *testing.T) {
	srv, got := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfig(t, path, config.Config{BaseURL: srv.URL, Token: "stored", ExpiresAt: "2099-01-01T00:00:00+00:00", Email: "a@b.com"})

	out, errb, err := runAuth(t, authLogoutCommand(), "")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty: logout is commentary", out)
	}
	if !strings.Contains(errb, "Removed the stored token.") {
		t.Errorf("stderr = %q", errb)
	}
	cfg := loadAuthConfig(t, path)
	if cfg.Token != "" || cfg.ExpiresAt != "" || cfg.Email != "" {
		t.Errorf("token not cleared: %+v", cfg)
	}
	if cfg.BaseURL != srv.URL {
		t.Errorf("base URL should survive logout: %+v", cfg)
	}
	// Local only: the revoke endpoint would kill every token on the account.
	if got.hits() != 0 {
		t.Errorf("logout contacted the server: %v", got.paths)
	}
}

func TestLogoutWithoutAStoredToken(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)

	_, errb, err := runAuth(t, authLogoutCommand(), "")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(errb, "No stored token to remove.") {
		t.Errorf("stderr = %q", errb)
	}
}

func TestLogoutReportsABrokenConfigByPath(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	path := authEnv(t, srv.URL)
	storeAuthConfigBytes(t, path, "{not json")

	_, _, err := runAuth(t, authLogoutCommand(), "")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
}

func TestLogoutWarnsWhenEnvTokenRemains(t *testing.T) {
	srv, _ := fakeConsole(t, "tok", 200)
	authEnv(t, srv.URL)
	t.Setenv(config.EnvToken, "from-ci")

	_, errb, err := runAuth(t, authLogoutCommand(), "")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(errb, config.EnvToken) || !strings.Contains(errb, "still set") {
		t.Errorf("stderr = %q, want a warning that %s is still set", errb, config.EnvToken)
	}
}

// A token that still works is kept by a bare or same-account 'auth login', so
// renewing it early takes a logout first. An expired one is replaced by login
// alone, so that warning must not send the user through a needless logout.
func TestNearExpiryWarningNamesLogoutBeforeLogin(t *testing.T) {
	var soon strings.Builder
	warnNearExpiry(&soon, time.Now().Add(10*24*time.Hour+time.Hour).Format(time.RFC3339))
	if !strings.Contains(soon.String(), "'intuizi auth logout' and then 'intuizi auth login'") {
		t.Errorf("near-expiry warning does not say to log out first:\n%s", soon.String())
	}

	var gone strings.Builder
	warnNearExpiry(&gone, time.Now().Add(-24*time.Hour).Format(time.RFC3339))
	if strings.Contains(gone.String(), "logout") {
		t.Errorf("expired warning sends the user through a logout login does not need:\n%s", gone.String())
	}
}

// The warning belongs on stderr: stdout is what a script reads.
func TestAuthStatusWarnsNearExpiry(t *testing.T) {
	for _, tc := range []struct {
		name, expires string
		wantStderr    string
	}{
		// An hour of slack: int(hours/24) truncates to 9 otherwise. formatExpiry
		// truncates the same way, so the two lines agree.
		{"expiring soon", time.Now().Add(10*24*time.Hour + time.Hour).Format(time.RFC3339), "expires in 10 days"},
		{"already expired", time.Now().Add(-24 * time.Hour).Format(time.RFC3339), "expired on"},
		{"plenty of time", time.Now().Add(100 * 24 * time.Hour).Format(time.RFC3339), ""},
		{"no expiry recorded", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeConsole(t, "tok", 200)
			path := authEnv(t, srv.URL)
			cfg := `{"base_url":"` + srv.URL + `","token":"stored"`
			if tc.expires != "" {
				cfg += `,"expires_at":"` + tc.expires + `"`
			}
			storeAuthConfigBytes(t, path, cfg+"}")

			out, errb, err := runAuth(t, authStatusCommand(), "")
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if tc.wantStderr == "" {
				if strings.Contains(errb, "Warning: this token") {
					t.Errorf("warned when it should not:\n%s", errb)
				}
				return
			}
			if !strings.Contains(errb, tc.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tc.wantStderr, errb)
			}
			// Never stdout, or --quiet and --json stop composing.
			if strings.Contains(out, "Warning") {
				t.Errorf("warning reached stdout:\n%s", out)
			}
		})
	}
}
