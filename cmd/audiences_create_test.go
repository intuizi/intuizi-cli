package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/config"
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
	dir := t.TempDir()
	t.Setenv("INTUIZI_API_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
	t.Setenv(config.EnvNoKeyring, "1")
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

// A WebDomain --category name resolves to the IAB category's id, which is what
// iab_category_codes takes; the code in the row's value is rejected with a 422.
func TestAudiencesCreateWebDomainCategoryResolvesToTheID(t *testing.T) {
	srv, got := stubSeq(t,
		reply{body: `{"status":"success","code":200,"message":"ok","data":[` +
			`{"value":"IAB2","text":"IAB2 - Automotive","id":2}]}`},
		reply{body: providersPOI})

	body := dryRunBody(t, audiencesCreateCommand(), srv,
		"--type", "webdomain", "--category", "Automotive", "--country", "USA",
		"--start-date", "2026-09-02", "--end-date", "2026-09-09", "--name", "Auto")

	if len(got.paths) == 0 || got.paths[0] != "/api/v2"+referencePrefix+"web/iab-categories" {
		t.Errorf("paths = %v, want the IAB category catalog read first", got.paths)
	}
	ds := firstDataset(t, body)
	if raw, _ := json.Marshal(ds["iab_category_codes"]); string(raw) != "[2]" {
		t.Errorf("iab_category_codes = %s, want [2]", raw)
	}
	if _, ok := ds["categories"]; ok {
		t.Errorf("WebDomain wrote categories too: %v", ds)
	}
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

// originFlags is a complete Origin audience. Wednesday to Wednesday, so the
// API widens it to two whole weeks.
var originFlags = []string{"--type", "origin", "--name", "LA residents",
	"--start-date", "2026-09-02", "--end-date", "2026-09-09", "--country", "USA"}

// The keys the API's Origin rules accept: the shared dataset keys and the
// location block, nothing type-specific.
var (
	originDatasetKeys  = map[string]bool{"type": true, "start_date": true, "end_date": true, "signal_providers": true, "location": true}
	originLocationKeys = map[string]bool{"countries": true, "states": true, "cities": true, "zipcodes": true, "dmas": true}
)

// Origin is in production and in Get Dataset Types, but the CLI refused it.
// Its body must pass the API's rules: signal providers and countries
// required, geographic filters only.
func TestAudiencesCreateOriginDryRunBuildsAnAcceptableBody(t *testing.T) {
	srv, got := stub(t, providersPOI)

	out, errb, err := run(t, audiencesCreateCommand(), srv, append(append([]string(nil), originFlags...),
		"--state", "CA", "--city", "Los Angeles", "--zipcode", "90001", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("dry-run output is not a JSON body: %v\n%s", err, out)
	}

	if len(got.paths) != 1 || !strings.Contains(got.queries[0], "dataType=Origin") {
		t.Errorf("providers should be read for Origin: paths = %v, queries = %v", got.paths, got.queries)
	}
	ds := firstDataset(t, body)
	if ds["type"] != "Origin" || ds["start_date"] != "2026-09-02" || ds["end_date"] != "2026-09-09" {
		t.Errorf("dataset = %v", ds)
	}
	for k := range ds {
		if !originDatasetKeys[k] {
			t.Errorf("the API rejects %q on an Origin dataset: %v", k, ds)
		}
	}
	if raw, _ := json.Marshal(ds["signal_providers"]); string(raw) != `["aaa","bbb"]` {
		t.Errorf("signal_providers = %s, want every Origin provider", raw)
	}
	loc, _ := ds["location"].(map[string]any)
	for k := range loc {
		if !originLocationKeys[k] {
			t.Errorf("the API rejects location.%s: %v", k, loc)
		}
	}
	if raw, _ := json.Marshal(loc["countries"]); string(raw) != `["USA"]` {
		t.Errorf("location.countries = %s", raw)
	}

	// The window the API scans is not the one typed, so say which it is.
	if !strings.Contains(errb, "2026-08-31") || !strings.Contains(errb, "2026-09-13") {
		t.Errorf("stderr should give the widened window, got %q", errb)
	}
}

// A window already made of whole weeks is scanned as typed: nothing to say.
func TestAudiencesCreateOriginWholeWeeksNeedNoNote(t *testing.T) {
	srv, _ := stub(t, providersPOI)

	_, errb, err := run(t, audiencesCreateCommand(), srv, append(swap(swap(originFlags,
		"--start-date", "2026-08-31"), "--end-date", "2026-09-13"), "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(errb, "widen") {
		t.Errorf("no widening to report, got %q", errb)
	}
}

// The API requires countries on Origin; without one the body is a 422. It is
// a usage error, so it must not wait for a token to be found.
func TestAudiencesCreateOriginNeedsACountry(t *testing.T) {
	args := []string{"--type", "origin", "--name", "x", "--start-date", "2026-09-02", "--end-date", "2026-09-09"}

	err := runLoggedOut(t, audiencesCreateCommand(), args...)

	wantUsageErr(t, err, "--country", "Origin")
	if strings.Contains(err.Error(), "not logged in") {
		t.Errorf("reported as a login problem: %v", err)
	}
}

// The --brand hint names what each type does take: its own brands field for
// AffinityTransactions, geography for Origin, --category only for a type with
// a category catalog, and nothing for the rest, which refuse --category too.
func TestAudiencesCreateBrandHintPerType(t *testing.T) {
	hints := map[string]string{
		"AffinityTransactions": "; affinity brands need --file",
		"Origin":               "; Origin filters on geography only (--country, --state, --city, --zipcode)",
		"Apps":                 "; use --category",
		"WebDomain":            "; use --category",
		"CTV":                  "",
		"Deidentified":         "",
	}
	for _, typ := range datasetTypes {
		// The file-only types are refused before --brand is looked at.
		if _, fileOnly := fileOnlyTypes[typ]; typ == "POI" || fileOnly {
			continue
		}
		hint, ok := hints[typ]
		if !ok {
			t.Errorf("no expected --brand hint for %s; add it here", typ)
			continue
		}
		t.Run(typ, func(t *testing.T) {
			err := runLoggedOut(t, audiencesCreateCommand(), "--type", typ, "--name", "x",
				"--start-date", "2026-09-01", "--end-date", "2026-09-07", "--country", "USA", "--brand", "coffee")

			wantUsageErr(t, err)
			if want := "--brand applies to --type POI, not " + typ + hint; err.Error() != want {
				t.Errorf("err = %q\nwant  %q", err, want)
			}
		})
	}
}

// Cohorts, Demographics and ProfileAttributes each require a field no flag
// writes, so every body the flags could build is a 422. They are refused
// before anything is read or sent, --dry-run included, and before the
// missing-flag check, which would otherwise ask for dates Demographics
// rejects.
func TestAudiencesCreateRefusesTheTypesFlagsCannotBuild(t *testing.T) {
	for typ, field := range map[string]string{
		"cohorts":           "cohort_id",
		"Demographics":      "demographic filter",
		"PROFILEATTRIBUTES": "profile_attributes",
	} {
		for _, args := range [][]string{
			{"--type", typ},
			{"--type", typ, "--name", "x", "--start-date", "2026-09-01", "--end-date", "2026-09-07",
				"--country", "USA", "--dry-run"},
		} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				srv, got := stub(t, providersPOI)
				_, _, err := run(t, audiencesCreateCommand(), srv, args...)
				wantUsageErr(t, err, "cannot be built from flags", field, "--file")
				if len(got.bodies) != 0 || len(got.paths) != 0 {
					t.Errorf("nothing should be sent, got %v %v", got.paths, got.bodies)
				}
			})
		}
	}
	if !strings.Contains(fileOnlyTypes["Demographics"], "rejects the dates and signal providers") {
		t.Error("the Demographics refusal should say why the flag body is always rejected")
	}
}

// Completion offers only the types the flag path builds.
func TestFlagTypesLeavesOutTheFileOnlyTypes(t *testing.T) {
	got := strings.Join(flagTypes(), ",")
	if want := "affinitytransactions,apps,ctv,deidentified,origin,poi,webdomain"; got != want {
		t.Errorf("flagTypes() = %s, want %s", got, want)
	}
}

// Origin filters on geography only. The --brand hint used to point at
// --category, which Origin refuses too.
func TestAudiencesCreateOriginRejectsBrandAndCategory(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want []string
	}{
		{"--brand", []string{"--brand", "Origin", "geography"}},
		{"--brand-all", []string{"--brand-all", "Origin", "geography"}},
		{"--category", []string{"--category", "Origin"}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			err := runLoggedOut(t, audiencesCreateCommand(),
				append(append([]string(nil), originFlags...), tc.flag, "coffee")...)

			wantUsageErr(t, err, tc.want...)
			if strings.Contains(err.Error(), "use --category") {
				t.Errorf("points at a flag Origin also refuses: %v", err)
			}
		})
	}
}

// --frequency runs the frequency analysis Preview Activation needs, which
// cannot be added once the audience is built. Each dataset type stores it
// under its own key, and without the flag no analyses block is sent at all.
func TestAudiencesCreateFrequencyPicksTheTypesAnalysis(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		want string
	}{
		{"poi", audiencesCreateCommand, append(swap(poiFlags, "--type", "poi"), "--frequency"), `{"frequency":true}`},
		{"apps", audiencesCreateCommand, append(swap(poiFlags, "--type", "apps"), "--frequency"), `{"apps_frequency":true}`},
		{"webdomain", audiencesCreateCommand, append(swap(poiFlags, "--type", "WebDomain"), "--frequency"), `{"web_frequency":true}`},
		{"estimate", estimateCreateCommand, append(swap(poiFlags, "--type", "poi"), "--frequency"), `{"frequency":true}`},
		{"no flag", audiencesCreateCommand, poiFlags, `null`},
		{"--frequency=false", audiencesCreateCommand, append(swap(poiFlags, "--type", "poi"), "--frequency=false"), `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := stub(t, providersPOI)

			body := dryRunBody(t, tc.cmd(), srv, tc.args...)

			if raw, _ := json.Marshal(body["analyses"]); string(raw) != tc.want {
				t.Errorf("analyses = %s, want %s", raw, tc.want)
			}
		})
	}
}

// No other type has a frequency analysis. The API would 422 the flavour sent
// without its dataset, so the flag is refused before anything is read - with
// no token too, since it is a usage mistake whoever makes it.
func TestAudiencesCreateFrequencyRefusedOnOtherTypes(t *testing.T) {
	err := runLoggedOut(t, audiencesCreateCommand(), "--type", "ctv", "--name", "x",
		"--start-date", "2026-09-02", "--end-date", "2026-09-09", "--country", "USA", "--frequency")

	wantUsageErr(t, err, "--frequency", "POI", "Apps", "WebDomain", "CTV")
}

// A --file body writes its own analyses block, so the flag is refused with it
// rather than silently dropped.
func TestAudiencesCreateFrequencyIsABodyFlag(t *testing.T) {
	srv, got := stub(t, created)

	_, _, err := run(t, audiencesCreateCommand(), srv,
		"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"POI"}]}`), "--frequency")

	wantUsageErr(t, err, "--file", "--frequency")
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}
