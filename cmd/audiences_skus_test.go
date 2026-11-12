package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The SKUs dataset: merchant and product brand keys, Match Strictness, and the
// two catalogs the keys come from.

// skusFlagsBase is a SKUs audience with the dates and a name, but no merchant
// or brand yet.
var skusFlagsBase = []string{"--type", "skus", "--name", "Dove buyers",
	"--start-date", "2026-10-01", "--end-date", "2026-10-31"}

// skusDatasetKeys are the keys the API's SKUs rules accept on a dataset.
var skusDatasetKeys = map[string]bool{
	"type": true, "start_date": true, "end_date": true, "location": true,
	"merchants": true, "brands": true, "max_devices_per_ip": true,
}

// skusMerchants is the flat merchants catalog, as a search for "example
// outdoor" answers it.
const skusMerchants = `{"status":"success","code":200,"message":"ok","data":[` +
	`{"value":"example outdoor store","text":"Example Outdoor Store"},` +
	`{"value":"example outdoor outlet","text":"Example Outdoor Outlet"}]}`

// skusBrandsPage is one page of the brands catalog.
func skusBrandsPage(total, last int, rows ...string) string {
	b, _ := json.Marshal(map[string]any{
		"current_page": 1, "per_page": 500, "total": total, "last_page": last,
	})
	return `{"status":"success","code":200,"message":"ok","data":{"items":[` + strings.Join(rows, ",") +
		`],"pagination":` + string(b) + `}}`
}

// skusDove is the brands catalog's answer to a search for "dove".
var skusDove = skusBrandsPage(3, 1,
	`{"value":"dove","text":"Dove"}`,
	`{"value":"dove men+care","text":"Dove Men+Care"}`,
	`{"value":"baby dove","text":"Baby Dove"}`)

// skusMenCare is the brands catalog's answer to a search for "Dove Men+Care".
var skusMenCare = skusBrandsPage(1, 1, `{"value":"dove men+care","text":"Dove Men+Care"}`)

// Every SKUs flag lands in its own key: the merchant and brand keys the
// catalogs return (never the names typed), the country and state, and the
// Match Strictness as max_devices_per_ip. No signal providers are read or
// sent. create, estimate and quote share the flags, so all three build it.
func TestAudiencesSkusBuildsTheBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"create", audiencesCreateCommand},
		{"estimate", estimateCreateCommand},
		{"quote", audiencesQuoteCommand},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stubSeq(t, reply{body: skusMerchants}, reply{body: skusDove}, reply{body: skusMenCare})

			body := dryRunBody(t, tc.cmd(), srv, with(skusFlagsBase,
				"--merchant", "Example Outdoor Store", "--brand", "dove", "--brand", "Dove Men+Care",
				"--country", "USA", "--state", "TX", "--state", "CA", "--match-strictness", "4")...)

			wantPaths := []string{"/api/v2" + skusMerchantsPath, "/api/v2" + skusBrandsPath, "/api/v2" + skusBrandsPath}
			wantQueries := []string{"search=Example+Outdoor+Store", "per_page=500&search=dove",
				"per_page=500&search=Dove+Men%2BCare"}
			if strings.Join(got.paths, " ") != strings.Join(wantPaths, " ") ||
				strings.Join(got.queries, " ") != strings.Join(wantQueries, " ") {
				t.Errorf("reads: paths = %v, queries = %v", got.paths, got.queries)
			}
			if body["name"] != "Dove buyers" {
				t.Errorf("name = %v", body["name"])
			}
			ds := firstDataset(t, body)
			for k := range ds {
				if !skusDatasetKeys[k] {
					t.Errorf("the API rejects %q on a SKUs dataset: %v", k, ds)
				}
			}
			for key, want := range map[string]string{
				"type":               `"SKUs"`,
				"start_date":         `"2026-10-01"`,
				"end_date":           `"2026-10-31"`,
				"merchants":          `["example outdoor store"]`,
				"brands":             `["dove","dove men+care"]`,
				"location":           `{"countries":["USA"],"states":["TX","CA"]}`,
				"max_devices_per_ip": `4`,
			} {
				if got := jsonOf(ds[key]); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}

// The least a SKUs dataset takes: one brand and the dates. No country (every
// country is read), no Match Strictness (the API's default applies), so
// neither key is sent, and no merchant catalog is read.
func TestAudiencesSkusBrandAloneSendsOnlyItsKeys(t *testing.T) {
	srv, got := stub(t, skusDove)

	body := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--brand", "dove")...)

	if len(got.paths) != 1 || got.paths[0] != "/api/v2"+skusBrandsPath {
		t.Errorf("only the brands catalog should be read, got %v", got.paths)
	}
	ds := firstDataset(t, body)
	want := []string{"brands", "end_date", "start_date", "type"}
	if got := keysOf(ds); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("dataset keys = %v, want %v", got, want)
	}
}

// A merchant alone is enough too.
func TestAudiencesSkusMerchantAloneSendsOnlyItsKeys(t *testing.T) {
	srv, got := stub(t, skusMerchants)

	body := dryRunBody(t, audiencesCreateCommand(), srv,
		with(skusFlagsBase, "--merchant", "example outdoor outlet", "--match-strictness", "1")...)

	if len(got.paths) != 1 || got.paths[0] != "/api/v2"+skusMerchantsPath {
		t.Errorf("only the merchants catalog should be read, got %v", got.paths)
	}
	ds := firstDataset(t, body)
	if raw := jsonOf(ds["merchants"]); raw != `["example outdoor outlet"]` {
		t.Errorf("merchants = %s", raw)
	}
	if raw := jsonOf(ds["max_devices_per_ip"]); raw != `1` {
		t.Errorf("max_devices_per_ip = %s", raw)
	}
	if _, ok := ds["brands"]; ok {
		t.Errorf("brands sent without --brand: %v", ds)
	}
}

// The API refuses a key named twice. A name and its key, or --brand-all
// repeating a --brand, resolve to the same key, which is sent once.
func TestAudiencesSkusSendsEachKeyOnce(t *testing.T) {
	srv, _ := stubSeq(t, reply{body: skusDove}, reply{body: skusDove}, reply{body: skusDove})

	out, errb, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase,
		"--brand", "dove", "--brand", "Dove", "--brand-all", "dove", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("stdout is not the body alone: %v\n%s", err, out)
	}
	if raw := jsonOf(firstDataset(t, body)["brands"]); raw != `["dove","dove men+care","baby dove"]` {
		t.Errorf("brands = %s", raw)
	}
	if errb != "--brand-all \"dove\" selected 3 brands\n" {
		t.Errorf("stderr = %q", errb)
	}
}

// A key is unique, so a row whose key is the value typed is taken even when
// the search has more matches than one page holds. A name with no such row,
// on a partial page, is refused rather than guessed.
func TestAudiencesSkusExactKeyWinsOnAPartialPage(t *testing.T) {
	partial := skusBrandsPage(900, 2,
		`{"value":"dove men+care","text":"Dove Men+Care"}`,
		`{"value":"dove","text":"Dove"}`)

	srv, _ := stub(t, partial)
	body := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--brand", "dove")...)
	if raw := jsonOf(firstDataset(t, body)["brands"]); raw != `["dove"]` {
		t.Errorf("brands = %s", raw)
	}

	srv, got := stub(t, partial)
	_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--brand", "DOVE")...)
	wantUsageErr(t, err, "--brand DOVE", "900 SKUs brands match it", "showing 2", "dove men+care")
	for _, p := range got.paths {
		if strings.HasSuffix(p, "/create") {
			t.Errorf("the create was sent anyway: %v", got.paths)
		}
	}
}

// A name matches the label ignoring case, and the key is what is sent.
func TestAudiencesSkusNameResolvesToItsKey(t *testing.T) {
	srv, _ := stub(t, skusDove)

	body := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--brand", "BABY DOVE")...)

	if raw := jsonOf(firstDataset(t, body)["brands"]); raw != `["baby dove"]` {
		t.Errorf("brands = %s", raw)
	}
}

// No match, or several with no exact one, is a usage error that names the
// catalog read, and nothing is created.
func TestAudiencesSkusUnresolvedValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		args  []string
		want  []string
	}{
		{"no brand", skusBrandsPage(0, 1), []string{"--brand", "nope"},
			[]string{"--brand nope", "no SKUs brands match it", "'intuizi reference skus brands --search <name>'"}},
		{"no merchant", `{"status":"success","code":200,"message":"ok","data":[]}`, []string{"--merchant", "nope"},
			[]string{"--merchant nope", "no SKUs merchants match it", "'intuizi reference skus merchants'"}},
		{"several merchants", skusMerchants, []string{"--merchant", "example outdoor"},
			[]string{"--merchant example outdoor", "2 SKUs merchants match it", "example outdoor store",
				"Example Outdoor Outlet", "value column"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, tc.reply)
			_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, tc.args...)...)
			wantUsageErr(t, err, tc.want...)
			if len(got.paths) != 1 {
				t.Errorf("only the lookup should be sent, got %v", got.paths)
			}
		})
	}
}

// --brand-all refuses a search that runs past one page rather than taking the
// first page only.
func TestAudiencesSkusBrandAllRefusesManyPages(t *testing.T) {
	srv, _ := stub(t, skusBrandsPage(1200, 3, `{"value":"a","text":"A"}`))

	_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--brand-all", "a")...)

	wantUsageErr(t, err, `"a" matches 1200 SKUs brands across 3 pages`, "narrow the search")
}

// Without --dry-run the body goes to the endpoint of each command: the
// lookups first, then one post, with no signal-provider read in between.
func TestAudiencesSkusSendsToCreateAndEstimate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   func() *cobra.Command
		reply reply
		path  string
	}{
		{"create", audiencesCreateCommand, reply{body: created}, "/api/v2/analyses/audiences/create"},
		{"estimate", estimateCreateCommand, createdEstimate, estimateCreatePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stubSeq(t, reply{body: skusDove}, tc.reply)

			if _, _, err := run(t, tc.cmd(), srv, with(skusFlagsBase, "--brand", "dove", "--country", "USA")...); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(got.paths) != 2 || got.paths[0] != "/api/v2"+skusBrandsPath || got.paths[1] != tc.path {
				t.Fatalf("paths = %v, want the brands catalog then %s", got.paths, tc.path)
			}
			var sent map[string]any
			if err := json.Unmarshal([]byte(got.bodies[1]), &sent); err != nil {
				t.Fatalf("body is not JSON: %v\n%s", err, got.bodies[1])
			}
			want := `{"brands":["dove"],"end_date":"2026-10-31","location":{"countries":["USA"]},` +
				`"start_date":"2026-10-01","type":"SKUs"}`
			if raw := jsonOf(firstDataset(t, sent)); raw != want {
				t.Errorf("dataset = %s\nwant      %s", raw, want)
			}
		})
	}
}

// Each SKUs mistake is a usage error before anything is read, with no token
// too: the flags SKUs does not read, the ones only SKUs reads, and the
// at-least-one rule.
func TestAudiencesSkusUsageErrors(t *testing.T) {
	brand := with(skusFlagsBase, "--brand", "dove")
	poi := []string{"--type", "poi", "--name", "x", "--start-date", "2026-10-01", "--end-date", "2026-10-31",
		"--country", "USA"}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"no merchant and no brand", with(skusFlagsBase, "--country", "USA"),
			[]string{"--type SKUs needs --merchant or --brand", "reference skus merchants", "reference skus brands"}},
		{"provider", with(brand, "--provider", "aaa"), []string{"--provider does not apply to --type SKUs"}},
		{"city", with(brand, "--country", "USA", "--city", "Dallas"),
			[]string{"--city does not apply to --type SKUs", "--country and --state only"}},
		{"zipcode", with(brand, "--zipcode", "75001"), []string{"--zipcode does not apply to --type SKUs"}},
		{"strictness 0", with(brand, "--match-strictness", "0"),
			[]string{"--match-strictness must be between 1 and 5, not 0"}},
		{"strictness 6", with(brand, "--match-strictness", "6"),
			[]string{"--match-strictness must be between 1 and 5, not 6"}},
		{"empty merchant", with(brand, "--merchant", " "), []string{"--merchant cannot be empty"}},
		{"empty brand", with(skusFlagsBase, "--brand", ""), []string{"--brand cannot be empty"}},
		{"--merchant on POI", with(poi, "--merchant", "target"),
			[]string{"--merchant applies to --type SKUs, not POI"}},
		{"--match-strictness on POI", with(poi, "--match-strictness", "3"),
			[]string{"--match-strictness applies to --type SKUs, not POI"}},
		{"--match-strictness on WebDomain", with(swap(poi, "--type", "webdomain"), "--match-strictness", "3"),
			[]string{"--match-strictness applies to --type SKUs, not WebDomain", "max_devices_per_ip needs --file"}},
		{"--merchant on AffinityTransactions", with(swap(poi, "--type", "affinitytransactions"), "--merchant", "x"),
			[]string{"--merchant applies to --type SKUs, not AffinityTransactions"}},
		{"--category on SKUs", with(brand, "--category", "x"), []string{"--category applies to", "not SKUs"}},
		{"--filter on SKUs", with(brand, "--filter", "strict_gps"),
			[]string{"--filter applies to --type POI, not SKUs"}},
		{"--no-filters on SKUs", with(brand, "--no-filters"),
			[]string{"--no-filters applies to --type POI, not SKUs"}},
		{"--frequency on SKUs", with(brand, "--frequency"), []string{"--frequency applies to", "not SKUs"}},
		{"--network-kind on SKUs", with(brand, "--network-kind", "home"),
			[]string{"--network-kind applies to --type Connectivity, not SKUs"}},
		{"switching on SKUs", with(brand, "--switch-from", "2026-09", "--switch-to", "2026-10"),
			[]string{errSwitchingType}},
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

// A --file body carries its own SKUs keys, so the SKUs flags are refused with
// it rather than silently dropped.
func TestAudiencesSkusFlagsAreBodyFlags(t *testing.T) {
	for _, flag := range [][]string{{"--merchant", "target"}, {"--match-strictness", "3"}} {
		t.Run(flag[0], func(t *testing.T) {
			srv, got := stub(t, created)

			_, _, err := run(t, audiencesCreateCommand(), srv,
				append([]string{"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"SKUs"}]}`)}, flag...)...)

			wantUsageErr(t, err, "--file", flag[0])
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The help names what SKUs takes, the combinations it refuses, and that its
// estimate always goes to a worker.
func TestAudiencesSkusHelp(t *testing.T) {
	create := strings.Join(strings.Fields(audiencesCreateCommand().Long), " ")
	for _, want := range []string{
		"--type skus", "--merchant", "--match-strictness", "'intuizi reference skus brands --search <name>'",
		"cannot be combined with a Cohorts, Demographics, Origin, Connectivity or Deidentified dataset",
		"no additional analyses",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("audiences create --help omits %q", want)
		}
	}
	estimate := strings.Join(strings.Fields(estimateCreateCommand().Long), " ")
	if !strings.Contains(estimate, "A SKUs estimate is always answered by a worker") {
		t.Errorf("audiences estimate create --help omits the SKUs rule")
	}
}

// The two catalogs: merchants is one flat list with a search, brands is paged
// and searched by name.
func TestReferenceSkusCatalogs(t *testing.T) {
	srv, got := stub(t, skusMerchants)
	merchants := findEndpoint(t, "skus", "merchants").command("skus")
	out, _, err := run(t, merchants, srv, "--search", "outdoor")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2"+skusMerchantsPath || got.queries[0] != "search=outdoor" {
		t.Errorf("merchants read %s?%s", got.paths[0], got.queries[0])
	}
	if !strings.Contains(out, "example outdoor store") || !strings.Contains(out, "Example Outdoor Store") {
		t.Errorf("merchants table omits the key or the name:\n%s", out)
	}
	if merchants.Flags().Lookup("page") != nil {
		t.Error("merchants is one flat list: it takes no --page")
	}

	srv, got = stub(t, skusDove)
	brands := findEndpoint(t, "skus", "brands").command("skus")
	if _, _, err := run(t, brands, srv, "--search", "dove", "--page", "2", "--per-page", "25"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2"+skusBrandsPath || got.queries[0] != "page=2&per_page=25&search=dove" {
		t.Errorf("brands read %s?%s", got.paths[0], got.queries[0])
	}
	if !strings.Contains(brands.Flags().Lookup("search").Usage, "brand name") {
		t.Errorf("brands --search help = %q", brands.Flags().Lookup("search").Usage)
	}
}
