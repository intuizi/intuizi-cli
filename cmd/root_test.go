package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/config"
)

// The other test files build commands from their constructors, which never
// meets cobra's required-flag check, legacyArgs or PersistentPreRun. The exit
// code is decided in exactly those places, so it is pinned here by running
// through rootCmd the way the binary does.

// rootResult is one run through rootCmd: what the user would see, the code
// the process would exit with, and the error Execute would have printed.
type rootResult struct {
	stdout, stderr string
	code           int
	err            error
}

// rootRun executes args through rootCmd. It does not touch the environment:
// callers isolate the token and config themselves, so a test can also run
// with no token at all.
//
// cobra keeps parsed flag values between runs and the flags bind to package
// globals, so every flag in the tree and every global is reset before the run
// and again on cleanup, or a --quiet here would leak into the constructor-built
// tests in the other files.
func rootRun(t *testing.T, args ...string) rootResult {
	t.Helper()
	resetRoot(t)

	// Before prepareRoot: cobra's completion command binds its writer when it
	// is added, and would otherwise print a shell script into the test log.
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	prepareRoot()
	if args == nil {
		// nil makes cobra read os.Args, which here are the test binary's.
		args = []string{}
	}
	rootCmd.SetArgs(args)

	err := rootCmd.ExecuteContext(context.Background())
	res := rootResult{stdout: out.String(), stderr: errb.String(), err: err}
	if err != nil {
		res.code = exitCode(err, nil, flagsParsed)
	}
	return res
}

// isolate points the config at an empty temp dir and sets the CI token, so a
// harness run reads nothing of the developer's own and needs no login.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
	t.Setenv(config.EnvNoKeyring, "1")
	t.Setenv("INTUIZI_API_TOKEN", "tok")
}

// writeConfig stores a config file where the CLI will find it, with no env
// token so the stored one is the one in use.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
	t.Setenv(config.EnvNoKeyring, "1")
	t.Setenv("INTUIZI_API_TOKEN", "")
	if err := os.MkdirAll(filepath.Join(dir, "intuizi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intuizi", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func resetRoot(t *testing.T) {
	t.Helper()
	reset := func() {
		jsonOutput, quietOutput, flagsParsed = false, false, false
		baseURLFlag, idempotencyKeyFlag = "", ""
		debugFlag = false
		api.IdempotencyKey = ""
		api.Debug, api.DebugOut = false, os.Stderr
		rootCmd.SilenceUsage = false
		resetFlags(rootCmd)
	}
	reset()
	t.Cleanup(reset)
}

// resetFlags puts every flag in the tree back to its default and unset. Slice
// flags go through Replace: their DefValue is "[]", and Set("[]") on one would
// store a literal "[]" as a value. A Value that refuses its own default (the
// page flags refuse 0) keeps its old value but is still marked unset, which is
// all their readers check.
func resetFlags(c *cobra.Command) {
	for _, fs := range []*pflag.FlagSet{c.Flags(), c.PersistentFlags()} {
		fs.VisitAll(func(fl *pflag.Flag) {
			if sv, ok := fl.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = fl.Value.Set(fl.DefValue)
			}
			fl.Changed = false
		})
	}
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// throwaway mounts a leaf on rootCmd for one test. The real commands are
// losing their MarkFlags*Required calls one by one, so cobra's own checks are
// pinned on a command that keeps them.
func throwaway(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	rootCmd.AddCommand(cmd)
	t.Cleanup(func() { rootCmd.RemoveCommand(cmd) })
}

// noop is a leaf whose Run never matters: every case below fails before it.
func noop(use string) *cobra.Command {
	return &cobra.Command{Use: use, Run: func(*cobra.Command, []string) {}}
}

// The codes are a contract scripts read: 2 is "fix the command line", 1 is
// "the call failed", and a script that retries on 1 must never retry a 2.
func TestRootExitCodes(t *testing.T) {
	needsName := noop("needsname")
	needsName.Flags().String("name", "", "")
	_ = needsName.MarkFlagRequired("name")

	needsOne := noop("needsone")
	needsOne.Flags().String("file", "", "")
	needsOne.Flags().String("type", "", "")
	needsOne.MarkFlagsOneRequired("file", "type")

	cases := []struct {
		name string
		args []string
		want int
		// errText, when set, must appear in the error Execute would print.
		errText string
	}{
		{"unknown flag", []string{"version", "--bogus"}, 2, "unknown flag"},
		{"--json with --quiet", []string{"--json", "--quiet", "version"}, 2, "contradict"},
		{"unknown top-level command", []string{"bogus"}, 2, "unknown command"},

		// E2: a stray word under a group used to print help and exit 0.
		{"unknown subcommand under a group", []string{"reference", "common", "state"}, 2, `unknown command "state"`},
		{"unknown subcommand under audiences", []string{"audiences", "bogus"}, 2, "unknown command"},
		{"unknown subcommand two levels down", []string{"poi", "submissions", "bogus"}, 2, "unknown command"},
		{"unknown subcommand under lookalike", []string{"audiences", "lookalike", "nope"}, 2, "unknown command"},
		{"unknown shell under completion", []string{"completion", "bogus"}, 2, "unknown command"},

		// E1: cobra checks these after PersistentPreRun, which used to leave
		// them exiting 1 like a failed API call.
		{"missing required flag", []string{"needsname"}, 2, `required flag(s) "name" not set`},
		{"missing one-of flag group", []string{"needsone"}, 2, "at least one of the flags"},

		// E4: a malformed id is caught before anything is sent.
		{"bad id argument", []string{"audiences", "show", "abc"}, 2, "not a valid audience id"},

		// Not misuse: the command line was fine and the run could not proceed.
		{"no token", []string{"projects", "list"}, 1, "not logged in"},

		// Help in all its forms is a success.
		{"bare intuizi", nil, 0, ""},
		{"a group alone shows help", []string{"reference", "common"}, 0, ""},
		{"the top group alone shows help", []string{"reference"}, 0, ""},
		{"help on a group", []string{"help", "reference", "common"}, 0, ""},
		{"completion script", []string{"completion", "bash"}, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			if c.name == "no token" {
				t.Setenv("INTUIZI_API_TOKEN", "")
			}
			throwaway(t, needsName)
			throwaway(t, needsOne)

			res := rootRun(t, c.args...)
			if res.code != c.want {
				t.Errorf("exit = %d, want %d (err: %v)", res.code, c.want, res.err)
			}
			if c.errText != "" && (res.err == nil || !strings.Contains(res.err.Error(), c.errText)) {
				t.Errorf("err = %v, want it to mention %q", res.err, c.errText)
			}
		})
	}
}

// A group's help still reaches the user when they stop at the group, and a
// stray word is refused with the group named, so the fix is obvious. The
// usage block that follows is the same one a bad flag gets.
func TestGroupHelpAndUnknownSubcommandOutput(t *testing.T) {
	isolate(t)

	res := rootRun(t, "reference", "common")
	if res.code != 0 || !strings.Contains(res.stdout, "states") {
		t.Errorf("group help: exit %d, stdout:\n%s", res.code, res.stdout)
	}

	res = rootRun(t, "reference", "common", "state")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", res.code, res.err)
	}
	if !strings.Contains(res.err.Error(), `"intuizi reference common"`) {
		t.Errorf("the error should name the group, got %v", res.err)
	}
}

// A missing required flag keeps cobra's message and, like every other usage
// error caught after parsing, prints no usage block after it.
func TestMissingRequiredFlagPrintsMessageOnly(t *testing.T) {
	isolate(t)
	needsName := noop("needsname")
	needsName.Flags().String("name", "", "")
	_ = needsName.MarkFlagRequired("name")
	throwaway(t, needsName)

	res := rootRun(t, "needsname")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (err: %v)", res.code, res.err)
	}
	if strings.Contains(res.stdout, "Usage:") || strings.Contains(res.stderr, "Usage:") {
		t.Errorf("usage dumped after a required-flag error:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
	}
}

// The stored token was minted against the base_url saved beside it. Pairing
// it with another --base-url would hand a production credential to whatever
// host is named, so the mismatch is refused before anything is sent.
func TestClientRefusesStoredTokenAgainstAnotherHost(t *testing.T) {
	srv, got := stub(t, listEnvelope)
	writeConfig(t, `{"base_url":"https://console.intuizi.com","token":"stored"}`)

	res := rootRun(t, "--base-url", srv.URL, "projects", "list")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (err: %v)", res.code, res.err)
	}
	for _, want := range []string{
		"belongs to https://console.intuizi.com", "not " + srv.URL,
		"auth login --base-url " + srv.URL, "INTUIZI_API_TOKEN",
	} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error missing %q: %v", want, res.err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("the token must not travel to the other host, got %v", got.paths)
	}
}

// Only a different host is a mismatch: a trailing slash is not, and with no
// flag at all the stored URL is the one in use.
func TestClientAcceptsStoredTokenForItsOwnHost(t *testing.T) {
	srv, got := stub(t, listEnvelope)
	writeConfig(t, `{"base_url":"`+srv.URL+`/","token":"stored"}`)

	if res := rootRun(t, "--base-url", srv.URL, "projects", "list"); res.code != 0 {
		t.Fatalf("with a matching flag: exit %d, err %v", res.code, res.err)
	}
	if res := rootRun(t, "projects", "list"); res.code != 0 {
		t.Fatalf("with no flag: exit %d, err %v", res.code, res.err)
	}
	if len(got.paths) != 2 {
		t.Errorf("expected two requests, got %v", got.paths)
	}
}

// CI sets the token and the URL together on purpose, so the env token is not
// bound to whatever a config file on the runner may say.
func TestClientEnvTokenIsNotBoundToTheStoredURL(t *testing.T) {
	srv, got := stub(t, listEnvelope)
	writeConfig(t, `{"base_url":"https://console.intuizi.com","token":"stored"}`)
	t.Setenv("INTUIZI_API_TOKEN", "ci")

	if res := rootRun(t, "--base-url", srv.URL, "projects", "list"); res.code != 0 {
		t.Fatalf("exit = %d, err %v", res.code, res.err)
	}
	if len(got.paths) != 1 {
		t.Errorf("expected one request, got %v", got.paths)
	}
}

// A corrupt config is not "not logged in": the error names the file to fix.
func TestClientNamesAnUnreadableConfig(t *testing.T) {
	writeConfig(t, `{not json`)

	res := rootRun(t, "projects", "list")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (err: %v)", res.code, res.err)
	}
	if !strings.Contains(res.err.Error(), "config.json") {
		t.Errorf("error should name the config file, got %v", res.err)
	}
}

// A --base-url that could never reach a console is a bad flag value, refused
// before the token goes anywhere. version never dials, so it stands in for
// every command: the check runs in the shared pre-run.
func TestBaseURLFlagIsValidatedUpFront(t *testing.T) {
	isolate(t)
	for _, raw := range []string{"console.intuizi.com", "ftp://console.intuizi.com", "https://"} {
		t.Run(raw, func(t *testing.T) {
			res := rootRun(t, "--base-url", raw, "version")
			if res.code != 2 {
				t.Errorf("exit = %d, want 2 (err: %v)", res.code, res.err)
			}
			if res.err == nil || !strings.Contains(res.err.Error(), "--base-url") {
				t.Errorf("err = %v, want it to name the flag", res.err)
			}
		})
	}
}

// Plain http to a host other than this machine would send the token in clear,
// so the command stops before sending it. Which hosts count as this machine is
// pinned in api.TestCheckBaseURL; every stub-backed test here runs on loopback,
// so a refusal there would fail the suite. .invalid never resolves, so the
// case reaches no real host even when the refusal is missing.
func TestPlainHTTPBaseURLRefused(t *testing.T) {
	isolate(t)
	res := rootRun(t, "--base-url", "http://console.invalid", "audiences", "list")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (err: %v)", res.code, res.err)
	}
	if res.err == nil || !strings.Contains(res.err.Error(), "plain http") {
		t.Errorf("err = %v, want the plain-http refusal", res.err)
	}
}

// A plain-http console stored by an earlier login is refused the same way,
// and the error says how to replace it.
func TestStoredPlainHTTPConsoleRefused(t *testing.T) {
	writeConfig(t, `{"base_url":"http://console.invalid","token":"tok"}`)
	res := rootRun(t, "audiences", "list")
	if res.err == nil || !strings.Contains(res.err.Error(), "plain http") ||
		!strings.Contains(res.err.Error(), "run 'intuizi --base-url https://console.invalid auth login'") {
		t.Fatalf("err = %v, want the plain-http refusal and how to replace the stored console", res.err)
	}
}

// logout sends nothing, so it still forgets a plain-http console's token,
// whether the console is the stored one or named with --base-url.
func TestLogoutForgetsAPlainHTTPConsole(t *testing.T) {
	for name, args := range map[string][]string{
		"stored":     {"auth", "logout"},
		"--base-url": {"--base-url", "http://console.invalid", "auth", "logout"},
	} {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, `{"base_url":"http://console.invalid","token":"tok"}`)
			res := rootRun(t, args...)
			if res.err != nil {
				t.Fatalf("logout: %v", res.err)
			}
			if !strings.Contains(res.stderr, "Removed the stored token") {
				t.Errorf("stderr = %q, want the token removed", res.stderr)
			}
		})
	}
}

// auth status does not report a token as usable for a console every other
// command refuses, whichever token is in play, and --verify sends nothing.
func TestStatusRefusesAPlainHTTPConsole(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		envToken bool
	}{
		{"stored token", []string{"auth", "status"}, false},
		{"stored token, --verify", []string{"auth", "status", "--verify"}, false},
		{"INTUIZI_API_TOKEN, --verify", []string{"auth", "status", "--verify"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeConfig(t, `{"base_url":"http://console.invalid","token":"tok",`+
				`"email":"a@b.com","expires_at":"2099-01-01T00:00:00Z"}`)
			if c.envToken {
				t.Setenv("INTUIZI_API_TOKEN", "tok")
			}
			res := rootRun(t, c.args...)
			if res.code != 1 {
				t.Fatalf("exit = %d, want 1 (err: %v)", res.code, res.err)
			}
			if res.err == nil || !strings.Contains(res.err.Error(), "plain http") ||
				!strings.Contains(res.err.Error(), "run 'intuizi --base-url https://console.invalid auth login'") {
				t.Errorf("err = %v, want the plain-http refusal and how to replace the stored console", res.err)
			}
			if !strings.Contains(res.stdout, "Token:    not checked (the console is plain http)") {
				t.Errorf("stdout should say the token is not checked:\n%s", res.stdout)
			}
			for _, unwanted := range []string{"Account:", "Expires:", "Verified"} {
				if strings.Contains(res.stdout, unwanted) {
					t.Errorf("stdout reports %q for a refused console:\n%s", unwanted, res.stdout)
				}
			}
		})
	}
}

// login, client and status all treat a silent store the same way: a locked
// keychain is not a missing token, and minting on one costs a slot.
func TestStoreUnavailable(t *testing.T) {
	for _, src := range []string{config.SourceNone, config.SourceEnv, config.SourceKeyring, config.SourceConfig} {
		if err := storeUnavailable(src); err != nil {
			t.Errorf("source %q: unexpected error %v", src, err)
		}
	}
	err := storeUnavailable(config.SourceUnavailable)
	if err == nil {
		t.Fatal("a silent store was not reported")
	}
	for _, want := range []string{"did not answer", "unlock it", config.EnvNoKeyring} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message omits %q: %v", want, err)
		}
	}
}

// --debug is wired in PersistentPreRunE, which most tests in this package never
// reach: they execute constructor-built commands with no root. Without a run
// through rootCmd, --debug could be declared, bound to its global, and silently
// never pushed into internal/api.
func TestDebugFlagReachesTheAPIPackage(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"data":[]}`)

	isolate(t)
	res := rootRun(t, "--base-url", srv.URL, "--debug", "audiences", "list")
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	for _, want := range []string{"> GET ", "Authorization: <redacted>", "< 200 OK in "} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	// The transcript is commentary, so it belongs on stderr: stdout is the data
	// that composes in a pipeline.
	if strings.Contains(res.stdout, "> GET ") {
		t.Errorf("transcript reached stdout:\n%s", res.stdout)
	}
}

// Off unless asked for: the same run without the flag prints no transcript.
func TestWithoutDebugNothingIsPrinted(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"data":[]}`)

	isolate(t)
	res := rootRun(t, "--base-url", srv.URL, "audiences", "list")
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if strings.Contains(res.stderr, "> GET ") {
		t.Errorf("transcript printed without --debug:\n%s", res.stderr)
	}
}

// --base-url help states the scheme rule DOCS.md does, so it is not learned
// from a refusal.
func TestBaseURLHelpStatesTheSchemeRule(t *testing.T) {
	isolate(t)
	res := rootRun(t, "--help")
	if res.err != nil {
		t.Fatalf("help: %v", res.err)
	}
	if !strings.Contains(res.stdout, "https://, or http:// only to this machine") {
		t.Errorf("--base-url help does not state the scheme rule:\n%s", res.stdout)
	}
}

// A password written into a stored console URL is never printed: not in the
// refusal, the hint, or the status lines, which reach CI logs.
func TestStoredURLPasswordIsNeverPrinted(t *testing.T) {
	for _, args := range [][]string{{"audiences", "list"}, {"auth", "status"}, {"auth", "status", "--verify"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			writeConfig(t, `{"base_url":"http://bob:hunter2@console.invalid","token":"tok"}`)
			res := rootRun(t, args...)
			if res.err == nil {
				t.Fatal("a plain-http console was not refused")
			}
			for where, text := range map[string]string{"stdout": res.stdout, "stderr": res.stderr, "error": res.err.Error()} {
				if strings.Contains(text, "hunter2") {
					t.Errorf("%s repeats the URL's password: %q", where, text)
				}
			}
			if !strings.Contains(res.err.Error(), "run 'intuizi --base-url https://console.invalid auth login'") {
				t.Errorf("err = %v, want the replacing command without userinfo", res.err)
			}
		})
	}
}

// A stored URL that does not parse is not called plain http, is not printed
// (it may carry a password, and a tidied copy would hide what is wrong with
// it), and the error names the file that holds it, the one place it can be
// corrected.
func TestStatusNamesAnUnparsableStoredURL(t *testing.T) {
	// Named, not keyed by the URL: the subtest name becomes part of the temp
	// dir, which the error prints.
	for name, stored := range map[string]string{
		"leading space":        " https://console.invalid",
		"password, no ] in it": "http://bob:hunter2@[::1",
	} {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, `{"base_url":"`+stored+`","token":"tok"}`)
			res := rootRun(t, "auth", "status")
			if res.code != 1 {
				t.Fatalf("exit = %d, want 1 (err: %v)", res.code, res.err)
			}
			if !strings.Contains(res.stdout, "Base URL: (a URL that does not parse)") {
				t.Errorf("stdout should not print the URL that does not parse:\n%s", res.stdout)
			}
			if !strings.Contains(res.stdout, "Token:    not checked (the console URL does not parse)") {
				t.Errorf("stdout should give the parse failure as the reason:\n%s", res.stdout)
			}
			if res.err == nil || !strings.Contains(res.err.Error(), "does not parse") ||
				!strings.Contains(res.err.Error(), "config.json") {
				t.Errorf("err = %v, want the parse failure and the config file to correct", res.err)
			}
			if strings.Contains(res.stdout+res.stderr+res.err.Error(), "hunter2") {
				t.Errorf("the URL's password was printed:\n%s%s%v", res.stdout, res.stderr, res.err)
			}
		})
	}
}

// The other-console refusal names both consoles as written, with any password
// masked, so two spellings of one console never print alike, and the command
// it offers carries no userinfo.
func TestOtherConsoleMasksURLPasswords(t *testing.T) {
	for _, args := range [][]string{{"audiences", "list"}, {"auth", "status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			writeConfig(t, `{"base_url":"HTTPS://bob:hunter2@Console.Invalid/p","token":"tok"}`)
			res := rootRun(t, append([]string{"--base-url", "https://eve:s3cret@other.invalid"}, args...)...)
			if res.err == nil {
				t.Fatal("another console's token was not refused")
			}
			printed := res.stdout + res.stderr + res.err.Error()
			for _, secret := range []string{"hunter2", "s3cret"} {
				if strings.Contains(printed, secret) {
					t.Errorf("a URL's password %q was printed:\n%s", secret, printed)
				}
			}
			for _, want := range []string{"HTTPS://bob:xxxxx@Console.Invalid/p", "https://eve:xxxxx@other.invalid"} {
				if !strings.Contains(printed, want) {
					t.Errorf("want %q, the console as written with its password masked:\n%s", want, printed)
				}
			}
			if !strings.Contains(res.err.Error(), "--base-url https://other.invalid'") {
				t.Errorf("err = %v, want the command offered without userinfo", res.err)
			}
		})
	}
}
