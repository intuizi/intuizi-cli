package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The flag-built audience, as opposed to the --file one the other tests send.

// providersPOI is the signal-providers catalog every flag-built POI audience
// reads, in the value/text shape it really has.
const providersPOI = `{"status":"success","code":200,"message":"ok","data":[` +
	`{"value":"aaa","text":"Provider A"},{"value":"bbb","text":"Provider B"}]}`

// poiFlags is a complete single-dataset audience, for tests that vary one flag.
var poiFlags = []string{"--type", "poi", "--name", "SF visitors",
	"--start-date", "2026-09-02", "--end-date", "2026-09-09", "--country", "USA"}

// swap returns args with the value following flag replaced.
func swap(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == flag {
			out[i+1] = value
		}
	}
	return out
}

// wantUsageErr asserts err is a usage error - exit 2, not 1 - naming each want.
func wantUsageErr(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a usage error")
	}
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("not a usage error, so it would exit 1: %v", err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error omits %q: %v", w, err)
		}
	}
}

// dryRunBody runs a create with --dry-run and decodes the body it printed.
func dryRunBody(t *testing.T, cmd *cobra.Command, srv *httptest.Server, args ...string) map[string]any {
	t.Helper()
	out, _, err := run(t, cmd, srv, append(append([]string(nil), args...), "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("dry-run output is not a JSON body: %v\n%s", err, out)
	}
	return body
}

// firstDataset digs the single dataset out of a decoded audience body.
func firstDataset(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	sets, _ := body["datasets"].([]any)
	if len(sets) != 1 {
		t.Fatalf("body has %d datasets, want 1: %v", len(sets), body)
	}
	ds, _ := sets[0].(map[string]any)
	return ds
}

// runLoggedOut executes cmd with no token anywhere, to pin which checks run
// before the client is built. run cannot: it sets INTUIZI_API_TOKEN.
func runLoggedOut(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	t.Setenv("INTUIZI_API_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestAudiencesCreateDryRunBuildsBodyFromFlags(t *testing.T) {
	srv, got := stub(t, providersPOI)

	body := dryRunBody(t, audiencesCreateCommand(), srv, poiFlags...)

	// The only round trip is the providers catalog, for the omitted --provider.
	if len(got.paths) != 1 || got.paths[0] != "/api/v2"+providersPath ||
		!strings.Contains(got.queries[0], "dataType=POI") {
		t.Errorf("paths = %v, queries = %v", got.paths, got.queries)
	}
	ds := firstDataset(t, body)
	if body["name"] != "SF visitors" || ds["type"] != "POI" ||
		ds["start_date"] != "2026-09-02" || ds["end_date"] != "2026-09-09" {
		t.Errorf("body = %v", body)
	}
	if raw, _ := json.Marshal(ds["signal_providers"]); string(raw) != `["aaa","bbb"]` {
		t.Errorf("signal_providers = %s, want every provider in catalog order", raw)
	}
}

// A provider id outside the type's catalog is not rejected by the API: the
// audience completes with zero devices. So --provider is checked against the
// same read the default path makes.
func TestAudiencesCreateChecksProvidersAgainstTheCatalog(t *testing.T) {
	t.Run("known ids go through in the order given", func(t *testing.T) {
		srv, _ := stub(t, providersPOI)

		body := dryRunBody(t, audiencesCreateCommand(), srv,
			append(append([]string(nil), poiFlags...), "--provider", "bbb", "--provider", "aaa")...)

		if raw, _ := json.Marshal(firstDataset(t, body)["signal_providers"]); string(raw) != `["bbb","aaa"]` {
			t.Errorf("signal_providers = %s", raw)
		}
	})

	t.Run("an unknown id is a usage error listing the valid ones", func(t *testing.T) {
		srv, got := stub(t, providersPOI)

		_, _, err := run(t, audiencesCreateCommand(), srv,
			append(append([]string(nil), poiFlags...), "--provider", "zzz")...)

		wantUsageErr(t, err, "--provider", "zzz", "aaa", "bbb")
		for _, p := range got.paths {
			if strings.HasSuffix(p, "/create") {
				t.Errorf("the create was sent anyway: %v", got.paths)
			}
		}
	})
}

// Changed is true for --name "" and --country "", so the required-flag check
// passed and the API was sent "" and [""] entries.
func TestAudiencesCreateRejectsEmptyValues(t *testing.T) {
	for _, tc := range []struct {
		flag string
		args []string
	}{
		{"--name", swap(poiFlags, "--name", "")},
		{"--country", swap(poiFlags, "--country", "")},
		{"--provider", append(append([]string(nil), poiFlags...), "--provider", "")},
		{"--brand", append(append([]string(nil), poiFlags...), "--brand", "")},
		{"--brand-all", append(append([]string(nil), poiFlags...), "--brand-all", "")},
		{"--category", append(append([]string(nil), poiFlags...), "--category", "")},
		{"--state", append(append([]string(nil), poiFlags...), "--state", "")},
		{"--city", append(append([]string(nil), poiFlags...), "--city", "  ")},
		{"--zipcode", append(append([]string(nil), poiFlags...), "--zipcode", "")},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			srv, got := stub(t, providersPOI)

			_, _, err := run(t, audiencesCreateCommand(), srv, tc.args...)

			wantUsageErr(t, err, tc.flag)
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// --category on a type with no category catalog is a mistake whoever runs it.
// With the check after client() it read as "not logged in" when there was no
// token, exit 1 for what is a usage error.
func TestAudiencesCreateCategoryTypeCheckPrecedesLogin(t *testing.T) {
	err := runLoggedOut(t, audiencesCreateCommand(), "--type", "ctv", "--name", "x",
		"--start-date", "2026-09-02", "--end-date", "2026-09-09", "--category", "foo")

	wantUsageErr(t, err, "--category applies to", "CTV")
	if strings.Contains(err.Error(), "not logged in") {
		t.Errorf("reported as a login problem: %v", err)
	}
}

// Cobra validates its one-of-required groups after PersistentPreRunE, so its
// own error exited 1. missingFlags covers the case with the better message.
func TestAudiencesCreateWithoutTypeOrFileIsAUsageError(t *testing.T) {
	srv, got := stub(t, created)

	_, _, err := run(t, audiencesCreateCommand(), srv, "--name", "x")

	wantUsageErr(t, err, "--type", "--file")
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}
