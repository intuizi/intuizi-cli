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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTUIZI_API_TOKEN", "tok")
}

// writeConfig stores a config file where the CLI will find it, with no env
// token so the stored one is the one in use.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
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
		api.IdempotencyKey = ""
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

// Plain http to a host other than this machine sends the token in clear, so
// it is said once on stderr. Loopback is where a local console runs, and the
// test stubs live there too, so it stays quiet. The bad id keeps every case
// from dialling.
func TestPlainHTTPBaseURLWarns(t *testing.T) {
	isolate(t)
	cases := []struct {
		url  string
		warn bool
	}{
		{"http://10.0.0.5:8000", true},
		{"http://console.example.com", true},
		{"http://localhost:8000", false},
		{"http://127.0.0.1:1", false},
		{"http://[::1]:8000", false},
		{"https://console.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			res := rootRun(t, "--base-url", c.url, "audiences", "show", "abc")
			if res.code != 2 {
				t.Fatalf("exit = %d, want 2 from the bad id (err: %v)", res.code, res.err)
			}
			if got := strings.Contains(res.stderr, "unencrypted"); got != c.warn {
				t.Errorf("warned = %v, want %v; stderr: %q", got, c.warn, res.stderr)
			}
		})
	}
}
