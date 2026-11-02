package cmd

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The Connectivity dataset and the Network Switching flags.

// connectivityFlagsHome is a complete Connectivity audience of whole weeks, so
// nothing is widened.
var connectivityFlagsHome = []string{"--type", "connectivity", "--name", "Home networks",
	"--network-kind", "home", "--country", "USA", "--start-date", "2026-10-19", "--end-date", "2026-10-25"}

// connectivityDatasetKeys are the keys the API's Connectivity rules accept on
// a dataset that the flags can write.
var connectivityDatasetKeys = map[string]bool{
	"type": true, "start_date": true, "end_date": true, "signal_providers": true, "location": true,
	"connectivity_network_kind": true, "connectivity_networks": true,
	"connectivity_technologies": true, "connectivity_min_days": true,
}

// networksPage is one page of the networks catalog.
func networksPage(page, last int, rows ...string) string {
	b, _ := json.Marshal(map[string]any{
		"current_page": page, "per_page": 500, "total": len(rows), "last_page": last,
	})
	return `{"status":"success","code":200,"message":"ok","data":{"items":[` + strings.Join(rows, ",") +
		`],"pagination":` + string(b) + `}}`
}

// homeNetworks is the home catalog the tests resolve --network against.
var homeNetworks = networksPage(1, 1,
	`{"value":"Starlink","text":"Starlink","kind":"home","technologies":["Satellite"]}`,
	`{"value":"Starry","text":"Starry","kind":"home","technologies":["Fixed wireless"]}`)

// keysOf lists a decoded object's keys, sorted.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsonOf marshals v for an exact comparison.
func jsonOf(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// with returns base with extra appended, never sharing base's array.
func with(base []string, extra ...string) []string {
	return append(append([]string(nil), base...), extra...)
}

// Every Connectivity flag lands in its own key, in the API's spelling: the
// kind lower-cased, the network in the catalog's spelling, the technologies
// canonical and once each.
func TestAudiencesConnectivityBuildsTheBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"create", audiencesCreateCommand},
		{"estimate", estimateCreateCommand},
		{"quote", audiencesQuoteCommand},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stubSeq(t, reply{body: homeNetworks}, reply{body: providersPOI})

			out, errb, err := run(t, tc.cmd(), srv, with(swap(connectivityFlagsHome, "--network-kind", "HOME"),
				"--network", "starlink", "--technology", "satellite", "--technology", "fixed  WIRELESS",
				"--technology", "Satellite", "--min-days", "4", "--state", "CA", "--dry-run")...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(out), &body); err != nil {
				t.Fatalf("dry-run output is not a JSON body: %v\n%s", err, out)
			}

			if len(got.paths) != 2 ||
				got.paths[0] != "/api/v2"+networksPath || got.queries[0] != "kind=home&per_page=500" ||
				got.paths[1] != "/api/v2"+providersPath || got.queries[1] != "dataType=Connectivity" {
				t.Errorf("reads: paths = %v, queries = %v", got.paths, got.queries)
			}
			ds := firstDataset(t, body)
			for k := range ds {
				if !connectivityDatasetKeys[k] {
					t.Errorf("the API rejects %q on a Connectivity dataset: %v", k, ds)
				}
			}
			for key, want := range map[string]string{
				"type":                      `"Connectivity"`,
				"start_date":                `"2026-10-19"`,
				"end_date":                  `"2026-10-25"`,
				"signal_providers":          `["aaa","bbb"]`,
				"location":                  `{"countries":["USA"],"states":["CA"]}`,
				"connectivity_network_kind": `"home"`,
				"connectivity_networks":     `["Starlink"]`,
				"connectivity_technologies": `["Satellite","Fixed wireless"]`,
				"connectivity_min_days":     `4`,
			} {
				if got := jsonOf(ds[key]); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
			if _, ok := body["networkswitching"]; ok {
				t.Errorf("networkswitching sent without the flags: %v", body)
			}
			// Whole weeks already: nothing widened, nothing to say.
			if strings.Contains(errb, "weekly") {
				t.Errorf("no widening to report, got %q", errb)
			}
		})
	}
}

// Without --network, --technology and --min-days the dataset carries the kind
// alone, so it reads every network of the kind, and the networks catalog is
// not read: there is nothing to check.
func TestAudiencesConnectivityKindAloneReadsNoNetworkCatalog(t *testing.T) {
	srv, got := stub(t, providersPOI)

	body := dryRunBody(t, audiencesCreateCommand(), srv, swap(connectivityFlagsHome, "--network-kind", "mobile")...)

	if len(got.paths) != 1 || got.paths[0] != "/api/v2"+providersPath {
		t.Errorf("only the providers should be read, got %v", got.paths)
	}
	ds := firstDataset(t, body)
	want := []string{"connectivity_network_kind", "end_date", "location", "signal_providers", "start_date", "type"}
	if got := keysOf(ds); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("dataset keys = %v, want %v", got, want)
	}
	if ds["connectivity_network_kind"] != "mobile" {
		t.Errorf("connectivity_network_kind = %v", ds["connectivity_network_kind"])
	}
}

// Connectivity data is weekly, like Origin: the body keeps the dates as given
// and stderr names the whole weeks the API widens them to.
func TestAudiencesConnectivityReportsTheWidenedWeeks(t *testing.T) {
	srv, _ := stub(t, providersPOI)

	out, errb, err := run(t, audiencesCreateCommand(), srv, with(swap(swap(connectivityFlagsHome,
		"--start-date", "2026-09-02"), "--end-date", "2026-09-09"), "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "Connectivity data is weekly: the API widens 2026-09-02..2026-09-09 to the whole weeks " +
		"2026-08-31..2026-09-13, and keeps the weeks that are built\n"
	if errb != want {
		t.Errorf("stderr = %q\nwant     %q", errb, want)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("stdout is not the body alone: %v\n%s", err, out)
	}
	if ds := firstDataset(t, body); ds["start_date"] != "2026-09-02" || ds["end_date"] != "2026-09-09" {
		t.Errorf("the body should keep the dates as given: %v", ds)
	}
}

// Each Connectivity mistake is a usage error before anything is read, with no
// token too.
func TestAudiencesConnectivityUsageErrors(t *testing.T) {
	poi := []string{"--type", "poi", "--name", "x", "--start-date", "2026-10-19", "--end-date", "2026-10-25",
		"--country", "USA"}
	noCountry := []string{"--type", "connectivity", "--name", "x", "--network-kind", "home",
		"--start-date", "2026-10-19", "--end-date", "2026-10-25"}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"no kind", []string{"--type", "connectivity", "--name", "x", "--country", "USA",
			"--start-date", "2026-10-19", "--end-date", "2026-10-25"},
			[]string{"--type Connectivity needs --network-kind", "home", "mobile"}},
		{"unknown kind", swap(connectivityFlagsHome, "--network-kind", "fiber"),
			[]string{"unknown --network-kind fiber", "home, mobile"}},
		{"technology on mobile", with(swap(connectivityFlagsHome, "--network-kind", "mobile"), "--technology", "Fiber"),
			[]string{"--technology applies to home networks only", "mobile"}},
		{"unknown technology", with(connectivityFlagsHome, "--technology", "5G"),
			[]string{"unknown --technology 5G", "Fiber, Cable, DSL, Cable or DSL, Satellite, Fixed wireless"}},
		{"min days 0", with(connectivityFlagsHome, "--min-days", "0"),
			[]string{"--min-days must be between 1 and 7, not 0"}},
		{"min days 8", with(connectivityFlagsHome, "--min-days", "8"),
			[]string{"--min-days must be between 1 and 7, not 8"}},
		{"empty network", with(connectivityFlagsHome, "--network", " "), []string{"--network cannot be empty"}},
		{"empty technology", with(connectivityFlagsHome, "--technology", ""), []string{"--technology cannot be empty"}},
		{"no country", noCountry, []string{"--type Connectivity needs --country", "--dataset-type Connectivity"}},
		{"--network-kind on POI", with(poi, "--network-kind", "home"),
			[]string{"--network-kind applies to --type Connectivity, not POI"}},
		{"--network on POI", with(poi, "--network", "Starlink"),
			[]string{"--network applies to --type Connectivity, not POI"}},
		{"--technology on Origin", with(swap(poi, "--type", "origin"), "--technology", "Fiber"),
			[]string{"--technology applies to --type Connectivity, not Origin"}},
		{"--min-days on CTV", with(swap(poi, "--type", "ctv"), "--min-days", "3"),
			[]string{"--min-days applies to --type Connectivity, not CTV"}},
		{"--brand on Connectivity", with(connectivityFlagsHome, "--brand", "coffee"),
			[]string{"--brand applies to --type POI, not Connectivity", "--network-kind"}},
		{"--category on Connectivity", with(connectivityFlagsHome, "--category", "coffee"),
			[]string{"--category applies to", "not Connectivity"}},
		{"--filter on Connectivity", with(connectivityFlagsHome, "--filter", "strict_gps"),
			[]string{"--filter applies to --type POI, not Connectivity"}},
		{"--frequency on Connectivity", with(connectivityFlagsHome, "--frequency"),
			[]string{"--frequency applies to", "not Connectivity"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runLoggedOut(t, audiencesCreateCommand(), tc.args...)
			wantUsageErr(t, err, tc.want...)
			if strings.Contains(err.Error(), "not logged in") {
				t.Errorf("reported as a login problem: %v", err)
			}
		})
	}
}

// --network is checked against the catalog of the kind, as --provider is
// against its own: a name it does not list is refused before anything is
// created, naming what it does list and the read that lists them.
func TestAudiencesConnectivityRefusesANetworkTheCatalogDoesNotList(t *testing.T) {
	srv, got := stubSeq(t, reply{body: homeNetworks}, reply{body: providersPOI})

	_, _, err := run(t, audiencesCreateCommand(), srv, with(connectivityFlagsHome, "--network", "Example Fiber")...)

	wantUsageErr(t, err, "--network Example Fiber is not a home network", "Starlink, Starry",
		"leave out --network", "'intuizi reference connectivity networks --kind home'")
	for _, p := range got.paths {
		if strings.HasSuffix(p, "/create") {
			t.Errorf("the create was sent anyway: %v", got.paths)
		}
	}
}

// A long catalog is not pasted into the error: it points at the read instead.
func TestAudiencesConnectivityLongCatalogIsNotListed(t *testing.T) {
	rows := make([]string, 0, maxListedNetworks+1)
	for i := 0; i <= maxListedNetworks; i++ {
		rows = append(rows, `{"value":"Network `+string(rune('A'+i%26))+strings.Repeat("x", i/26)+`"}`)
	}
	srv, _ := stubSeq(t, reply{body: networksPage(1, 1, rows...)}, reply{body: providersPOI})

	_, _, err := run(t, audiencesCreateCommand(), srv, with(connectivityFlagsHome, "--network", "Nope")...)

	wantUsageErr(t, err, "--network Nope", "reference connectivity networks --kind home")
	if strings.Contains(err.Error(), "Network A") {
		t.Errorf("a long catalog should not be listed: %v", err)
	}
}

// The catalog is paged. A name on the second page is a name the API accepts,
// so every page is read before one is refused.
func TestAudiencesConnectivityReadsEveryCatalogPage(t *testing.T) {
	srv, got := stubSeq(t,
		reply{body: networksPage(1, 2, `{"value":"Starlink","text":"Starlink","kind":"home"}`)},
		reply{body: networksPage(2, 2, `{"value":"Example DSL","text":"Example DSL","kind":"home"}`)},
		reply{body: providersPOI})

	body := dryRunBody(t, audiencesCreateCommand(), srv,
		with(connectivityFlagsHome, "--network", "example dsl", "--network", "Starlink")...)

	if len(got.queries) != 3 || got.queries[1] != "kind=home&page=2&per_page=500" {
		t.Errorf("queries = %v, want the second page read too", got.queries)
	}
	if raw := jsonOf(firstDataset(t, body)["connectivity_networks"]); raw != `["Example DSL","Starlink"]` {
		t.Errorf("connectivity_networks = %s", raw)
	}
}

// Before the first weekly catalog is published the list is empty and the API
// refuses every create: said plainly, rather than as "not in the catalog".
func TestAudiencesConnectivityEmptyCatalog(t *testing.T) {
	srv, _ := stubSeq(t, reply{body: networksPage(1, 1)}, reply{body: providersPOI})

	_, _, err := run(t, audiencesCreateCommand(), srv, with(connectivityFlagsHome, "--network", "Starlink")...)

	if err == nil || !strings.Contains(err.Error(), "Connectivity data is not available yet") {
		t.Errorf("err = %v, want it to say Connectivity data is not available yet", err)
	}
}

// --switch-from and --switch-to write the top-level networkswitching block,
// all four keys, the two defaults included.
func TestAudiencesSwitchingBuildsTheBlock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		want  string
	}{
		// Decoded, so the keys come back sorted: the set is exact.
		{"defaults", nil,
			`{"from_month":"2026-09","min_home_days":3,"network_type":"home","to_month":"2026-10"}`},
		{"mobile, 5 days", []string{"--switch-network", "MOBILE", "--switch-min-days", "5"},
			`{"from_month":"2026-09","min_home_days":5,"network_type":"mobile","to_month":"2026-10"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := stub(t, providersPOI)

			body := dryRunBody(t, audiencesCreateCommand(), srv, with(with(connectivityFlagsHome,
				"--switch-from", "2026-09", "--switch-to", "2026-10"), tc.extra...)...)

			if got := jsonOf(body["networkswitching"]); got != tc.want {
				t.Errorf("networkswitching = %s\nwant               %s", got, tc.want)
			}
			// The Connectivity dataset keeps its own dates.
			if ds := firstDataset(t, body); ds["start_date"] != "2026-10-19" || ds["end_date"] != "2026-10-25" {
				t.Errorf("dates changed: %v", ds)
			}
		})
	}
}

// The analysis runs on a Connectivity audience only. Any other type gets the
// shared refusal before a request, with no token too, whatever else is wrong.
func TestAudiencesSwitchingRefusesOtherTypes(t *testing.T) {
	sw := []string{"--switch-from", "2026-09", "--switch-to", "2026-10"}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"deidentified", []string{"--type", "deidentified", "--name", "x", "--country", "USA",
			"--start-date", "2026-09-24", "--end-date", "2026-09-30"}},
		{"ctv", []string{"--type", "ctv", "--name", "x", "--country", "USA",
			"--start-date", "2026-10-01", "--end-date", "2026-10-31"}},
		{"poi", []string{"--type", "poi", "--name", "x", "--country", "USA",
			"--start-date", "2026-10-19", "--end-date", "2026-10-25"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, providersPOI)
			_, _, err := run(t, audiencesCreateCommand(), srv, with(with(tc.args, sw...), "--dry-run")...)
			wantUsageErr(t, err, errSwitchingType)
			if n := len(got.paths); n != 0 {
				t.Errorf("%d requests sent, want none", n)
			}
			wantUsageErr(t, runLoggedOut(t, audiencesCreateCommand(), with(tc.args, sw...)...), errSwitchingType)
		})
	}
}

// The shared refusal comes before any missing-flag error: a Deidentified
// create with switching flags and no dates learns at once that switching is
// Connectivity only.
func TestAudiencesSwitchingRefusalComesFirst(t *testing.T) {
	srv, got := stub(t, providersPOI)
	_, _, err := run(t, audiencesCreateCommand(), srv,
		"--type", "deidentified", "--switch-from", "2026-09", "--switch-to", "2026-10")
	wantUsageErr(t, err, errSwitchingType)
	if len(got.paths) != 0 {
		t.Errorf("%d requests sent, want none", len(got.paths))
	}
}

// Each Network Switching mistake is a usage error before anything is read,
// with no token too.
func TestAudiencesSwitchingUsageErrors(t *testing.T) {
	base := with(connectivityFlagsHome)
	poi := []string{"--type", "poi", "--name", "x", "--start-date", "2026-10-19", "--end-date", "2026-10-25",
		"--country", "USA"}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"from alone", with(base, "--switch-from", "2026-09"), []string{"--switch-from and --switch-to go together"}},
		{"to alone", with(base, "--switch-to", "2026-10"), []string{"--switch-from and --switch-to go together"}},
		{"network alone", with(base, "--switch-network", "home"), []string{"--switch-from and --switch-to go together"}},
		{"min days alone", with(base, "--switch-min-days", "4"), []string{"--switch-from and --switch-to go together"}},
		{"POI", with(poi, "--switch-from", "2026-09", "--switch-to", "2026-10"), []string{errSwitchingType}},
		{"Origin", with(swap(poi, "--type", "origin"), "--switch-from", "2026-09", "--switch-to", "2026-10"),
			[]string{errSwitchingType}},
		{"not a month", with(base, "--switch-from", "2026-9", "--switch-to", "2026-10"),
			[]string{"--switch-from must be YYYY-MM, not 2026-9"}},
		{"month 13", with(base, "--switch-from", "2026-09", "--switch-to", "2026-13"),
			[]string{"--switch-to must be YYYY-MM, not 2026-13"}},
		{"to before from", with(base, "--switch-from", "2026-10", "--switch-to", "2026-09"),
			[]string{"--switch-from 2026-10 must be before --switch-to 2026-09"}},
		{"same month", with(base, "--switch-from", "2026-10", "--switch-to", "2026-10"),
			[]string{"must be before"}},
		{"unknown network", with(base, "--switch-from", "2026-09", "--switch-to", "2026-10", "--switch-network", "cable"),
			[]string{"unknown --switch-network cable", "home, mobile"}},
		{"min days 1", with(base, "--switch-from", "2026-09", "--switch-to", "2026-10", "--switch-min-days", "1"),
			[]string{"--switch-min-days must be between 2 and 5, not 1"}},
		{"min days 6", with(base, "--switch-from", "2026-09", "--switch-to", "2026-10", "--switch-min-days", "6"),
			[]string{"--switch-min-days must be between 2 and 5, not 6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runLoggedOut(t, audiencesCreateCommand(), tc.args...)
			wantUsageErr(t, err, tc.want...)
		})
	}
	// The API's own words, verbatim.
	if errSwitchingType != "Network switching runs on an audience with one Connectivity dataset." {
		t.Errorf("errSwitchingType = %q", errSwitchingType)
	}
}

// An estimate and a quote never run the analysis, so they do not take the
// switching flags at all.
func TestAudiencesSwitchingIsCreateOnly(t *testing.T) {
	for name, cmd := range map[string]func() *cobra.Command{
		"estimate": estimateCreateCommand, "quote": audiencesQuoteCommand,
	} {
		t.Run(name, func(t *testing.T) {
			c := cmd()
			for _, f := range switchingFlags {
				if c.Flags().Lookup(f) != nil {
					t.Errorf("%s registers --%s", name, f)
				}
			}
		})
	}
	c := audiencesCreateCommand()
	for _, f := range append(append([]string(nil), switchingFlags...), connectivityFlags...) {
		if c.Flags().Lookup(f) == nil {
			t.Errorf("create does not register --%s", f)
		}
	}
}

// A --file body carries the whole payload: the new flags are refused with it
// rather than silently dropped.
func TestAudiencesConnectivityAndSwitchingAreBodyFlags(t *testing.T) {
	for _, flag := range [][]string{
		{"--network-kind", "home"}, {"--network", "Starlink"}, {"--technology", "Fiber"}, {"--min-days", "3"},
		{"--switch-from", "2026-09"}, {"--switch-to", "2026-10"}, {"--switch-network", "home"},
		{"--switch-min-days", "3"},
	} {
		t.Run(flag[0], func(t *testing.T) {
			srv, got := stub(t, created)

			_, _, err := run(t, audiencesCreateCommand(), srv,
				with([]string{"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"Connectivity"}]}`)}, flag...)...)

			wantUsageErr(t, err, "--file", flag[0])
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// --file is forwarded untouched: a networkswitching block in it is sent as
// written, and no flag rule runs on it.
func TestAudiencesCreateFileWithSwitchingIsForwardedUntouched(t *testing.T) {
	srv, got := stub(t, created)
	payload := `{"name":"x","datasets":[{"type":"Connectivity","connectivity_network_kind":"home"}],` +
		`"networkswitching":{"from_month":"2026-09","to_month":"2026-10"}}`

	if _, _, err := run(t, audiencesCreateCommand(), srv, "--file", payloadFile(t, payload)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.bodies) != 1 || got.bodies[0] != payload {
		t.Errorf("bodies = %q, want the file as written", got.bodies)
	}
}
