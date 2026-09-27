package cmd

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/config"
)

// jsonRunner is one path that honours --json: a shared runner wrapped in a
// throwaway command so the test depends on no resource's constructor, or a
// command whose request is its own, run with the args it needs.
type jsonRunner struct {
	name string
	cmd  *cobra.Command
	args []string
}

func jsonRunners(t *testing.T) []jsonRunner {
	wrap := func(run func(cmd *cobra.Command) error) *cobra.Command {
		return &cobra.Command{
			Use:  "x",
			RunE: func(cmd *cobra.Command, _ []string) error { return run(cmd) },
		}
	}
	return []jsonRunner{
		{name: "runReference", cmd: wrap(func(cmd *cobra.Command) error {
			return runReference(cmd, "/analyses/reference/common/dataset-types", url.Values{})
		})},
		{name: "previewCohort", cmd: wrap(func(cmd *cobra.Command) error {
			return previewCohort(cmd, map[string]string{"file_uri": "s3://example-bucket/k.csv"})
		})},
		{name: "previewActivation", cmd: wrap(func(cmd *cobra.Command) error {
			return previewActivation(cmd, url.Values{"audience_id": {"88"}, "freq_min": {"2"}, "freq_max": {"5"}})
		})},
		{name: "createAndWait", cmd: wrap(func(cmd *cobra.Command) error {
			return createAndWait(cmd, resourceLifecycle("project", "/analyses/projects", nil), map[string]string{"name": ""}, time.Minute)
		})},
		{name: "usage", cmd: usageCommand()},
		{name: "uploads put", cmd: uploadsPutCommand(), args: []string{uploadFile(t, "x"), "--purpose", "cohort"}},
		{name: "renderList", cmd: wrap(func(cmd *cobra.Command) error {
			return renderList(cmd, "/analyses/projects/index", url.Values{}, nil, "none")
		})},
		{name: "renderOne", cmd: wrap(func(cmd *cobra.Command) error {
			return renderOne(cmd, "/analyses/projects/4", nil)
		})},
		{name: "postID", cmd: wrap(func(cmd *cobra.Command) error {
			return postID(cmd, "/analyses/projects/delete-by-id", 4, "deleted project 4")
		})},
		{name: "createBody", cmd: wrap(func(cmd *cobra.Command) error {
			return createBody(cmd, "/analyses/projects/create", map[string]string{"name": ""}, nil, "")
		})},
	}
}

// K5: --json promises the raw envelope, and on a failure that is the one a
// script needs most - the 422 field errors. It lands on stdout; the exit code
// stays 1 and the error still goes where errors go.
func TestJSONPrintsTheErrorEnvelopeOnFailure(t *testing.T) {
	for _, r := range jsonRunners(t) {
		t.Run(r.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
			t.Setenv(config.EnvNoKeyring, "1")
			srv, _ := stubSeq(t, reply{status: 422, body: valEnvelope})

			out, _, err := runJSON(t, r.cmd, srv, r.args...)
			if err == nil {
				t.Fatal("expected an error from a 422 envelope")
			}
			if code := exitCode(err, nil, true); code != exitError {
				t.Errorf("exit code = %d, want %d", code, exitError)
			}
			if !strings.Contains(err.Error(), "(422)") {
				t.Errorf("error = %v, want the status code", err)
			}

			var env map[string]any
			if uerr := json.Unmarshal([]byte(out), &env); uerr != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", uerr, out)
			}
			if env["code"] != float64(422) || env["message"] != "Validation error." {
				t.Errorf("stdout = %s, want the 422 envelope", out)
			}
			if !strings.Contains(out, "The name field is required.") {
				t.Errorf("stdout = %s, want the field errors", out)
			}
		})
	}
}

// HTML from a proxy is not an envelope a script could parse, so stdout stays
// empty and only the error explains what happened.
func TestJSONPrintsNothingForANonJSONFailure(t *testing.T) {
	for _, r := range jsonRunners(t) {
		t.Run(r.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
			t.Setenv(config.EnvNoKeyring, "1")
			srv, _ := stubSeq(t, reply{status: 502, body: "<html>502 Bad Gateway</html>"})

			out, _, err := runJSON(t, r.cmd, srv, r.args...)
			if err == nil {
				t.Fatal("expected an error from a 502")
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty for a non-JSON failure", out)
			}
		})
	}
}
