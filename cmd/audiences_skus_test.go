package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The SKUs dataset: merchant, product brand and category keys, the
// purchase-device values, Match Strictness, and the catalogs they come from.

// skusFlagsBase is a SKUs audience with the dates and a name, but no merchant
// or brand yet.
var skusFlagsBase = []string{"--type", "skus", "--name", "Dove buyers",
	"--start-date", "2026-10-01", "--end-date", "2026-10-31"}

// skusDatasetKeys are the keys the API's SKUs rules accept on a dataset.
var skusDatasetKeys = map[string]bool{
	"type": true, "start_date": true, "end_date": true, "location": true,
	"merchants": true, "brands": true, "max_devices_per_ip": true, "categories": true,
	"device_types": true, "device_oses": true, "device_makes": true, "device_models": true,
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

// Every SKUs flag at once: each lookup in the order build reads them (merchant,
// brand, the four device fields, then the category), and a body that carries
// every key of skusDatasetKeys and no other.
func TestAudiencesSkusEveryKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"create", audiencesCreateCommand},
		{"estimate", estimateCreateCommand},
		{"quote", audiencesQuoteCommand},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stubSeq(t, reply{body: skusMerchants}, reply{body: skusMenCare},
				reply{body: skusCategories(`{"value":"MOBILE","text":"MOBILE"}`)},
				reply{body: skusCategories(`{"value":"IPADOS","text":"IPADOS"}`, `{"value":"IOS","text":"IOS"}`)},
				reply{body: skusCategories(`{"value":"APPLE","text":"APPLE"}`)},
				reply{body: skusCategories(`{"value":"IPHONE 15","text":"IPHONE 15"}`, `{"value":"IPHONE","text":"IPHONE"}`)},
				reply{body: skusCategories(`{"value":3530,"text":"Dog Food","path":"Animals & Pet Supplies > Dog Food"}`)})

			body := dryRunBody(t, tc.cmd(), srv, with(skusFlagsBase,
				"--merchant", "Example Outdoor Store", "--brand", "Dove Men+Care", "--category", "Dog Food",
				"--device-type", "mobile", "--device-os", "iOS", "--device-make", "Apple", "--device-model", "iphone",
				"--country", "USA", "--state", "TX", "--match-strictness", "2")...)

			wantPaths := []string{skusMerchantsPath, skusBrandsPath, skusDeviceValuesPath, skusDeviceValuesPath,
				skusDeviceValuesPath, skusDeviceValuesPath, skusCategoriesPath}
			for i := range wantPaths {
				wantPaths[i] = "/api/v2" + wantPaths[i]
			}
			wantQueries := []string{"search=Example+Outdoor+Store", "per_page=500&search=Dove+Men%2BCare",
				"field=type&search=mobile", "field=os&search=iOS", "field=make&search=Apple",
				"field=model&search=iphone", "search=Dog+Food"}
			if strings.Join(got.paths, " ") != strings.Join(wantPaths, " ") ||
				strings.Join(got.queries, " ") != strings.Join(wantQueries, " ") {
				t.Errorf("reads:\n paths   = %v\n queries = %v", got.paths, got.queries)
			}
			ds := firstDataset(t, body)
			for k := range ds {
				if !skusDatasetKeys[k] {
					t.Errorf("the API rejects %q on a SKUs dataset: %v", k, ds)
				}
			}
			for k := range skusDatasetKeys {
				if _, ok := ds[k]; !ok {
					t.Errorf("the body omits %q: %v", k, ds)
				}
			}
			for key, want := range map[string]string{
				"type":               `"SKUs"`,
				"merchants":          `["example outdoor store"]`,
				"brands":             `["dove men+care"]`,
				"categories":         `[3530]`,
				"device_types":       `["MOBILE"]`,
				"device_oses":        `["IOS"]`,
				"device_makes":       `["APPLE"]`,
				"device_models":      `["IPHONE"]`,
				"location":           `{"countries":["USA"],"states":["TX"]}`,
				"max_devices_per_ip": `2`,
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
			[]string{"--brand nope", "no SKUs brands match it", "'intuizi reference skus brands --search <name>'",
				"the SKUs dataset is not enabled for this account", "'intuizi reference common dataset-types'"}},
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

	wantUsageErr(t, err, `"a" matches 1200 SKUs brands across 3 pages`, "narrow the search", "pass the keys with --brand")
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
			[]string{"--type SKUs needs --merchant, --brand or --category", "reference skus merchants", "reference skus brands",
				"reference skus categories"}},
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
		{"empty category", with(skusFlagsBase, "--category", ""), []string{"--category cannot be empty"}},
		// An unset shell variable must not post an audience of every purchase device.
		{"empty device-type", with(brand, "--device-type", ""), []string{"--device-type cannot be empty"}},
		{"empty device-os", with(brand, "--device-os", "IOS", "--device-os", ""), []string{"--device-os cannot be empty"}},
		{"blank device-make", with(brand, "--device-make", " "), []string{"--device-make cannot be empty"}},
		{"empty device-model", with(brand, "--device-model", ""), []string{"--device-model cannot be empty"}},
		{"--device-type on POI", with(poi, "--device-type", "mobile"),
			[]string{"--device-type applies to --type SKUs, not POI"}},
		{"--device-os on Apps", with(swap(poi, "--type", "apps"), "--device-os", "ios"),
			[]string{"--device-os applies to --type SKUs, not Apps"}},
		{"--device-make on WebDomain", with(swap(poi, "--type", "webdomain"), "--device-make", "apple"),
			[]string{"--device-make applies to --type SKUs, not WebDomain"}},
		{"--device-model on AffinityTransactions", with(swap(poi, "--type", "affinitytransactions"), "--device-model", "iphone"),
			[]string{"--device-model applies to --type SKUs, not AffinityTransactions"}},
		{"--merchant on POI", with(poi, "--merchant", "target"),
			[]string{"--merchant applies to --type SKUs, not POI"}},
		{"--match-strictness on POI", with(poi, "--match-strictness", "3"),
			[]string{"--match-strictness applies to --type SKUs, not POI"}},
		{"--match-strictness on WebDomain", with(swap(poi, "--type", "webdomain"), "--match-strictness", "3"),
			[]string{"--match-strictness applies to --type SKUs, not WebDomain", "max_devices_per_ip needs --file"}},
		{"--merchant on AffinityTransactions", with(swap(poi, "--type", "affinitytransactions"), "--merchant", "x"),
			[]string{"--merchant applies to --type SKUs, not AffinityTransactions"}},
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

// A --file body carries its own SKUs keys, so every SKUs flag, the device
// flags among them, is refused with it on create, estimate and quote rather
// than silently dropped while the file body is posted.
func TestAudiencesSkusFlagsAreBodyFlags(t *testing.T) {
	for _, d := range skusDeviceFlags {
		if !slices.Contains(skusFlags, d.flag) {
			t.Errorf("--%s is not in skusFlags, so it is neither refused on other types nor next to --file", d.flag)
		}
	}
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"create", audiencesCreateCommand},
		{"estimate", estimateCreateCommand},
		{"quote", audiencesQuoteCommand},
	} {
		for _, flag := range skusFlags {
			value := "ios"
			if flag == "match-strictness" {
				value = "3"
			}
			t.Run(tc.name+" --"+flag, func(t *testing.T) {
				srv, got := stub(t, created)

				_, _, err := run(t, tc.cmd(), srv, "--file",
					payloadFile(t, `{"name":"x","datasets":[{"type":"SKUs"}]}`), "--"+flag, value)

				wantUsageErr(t, err, "--file", "--"+flag)
				if len(got.paths) != 0 {
					t.Errorf("should cost no round trip, got %v", got.paths)
				}
			})
		}
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

// The other SKUs reads and the query each flag writes. brands --categories is
// one comma-separated string, which the API splits (it takes an array too);
// categories walks the tree with --parent-id or searches every level;
// device-values needs --field, one of four, sent lower case.
func TestReferenceSkusTaxonomyReads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		item  string
		args  []string
		path  string
		query string
	}{
		{"brands by category", "brands", []string{"--categories", "3530,10000000"}, skusBrandsPath,
			"categories=3530%2C10000000"},
		{"categories top level", "categories", nil, skusCategoriesPath, ""},
		{"categories children", "categories", []string{"--parent-id", "3530"}, skusCategoriesPath, "parent_id=3530"},
		{"categories search", "categories", []string{"--search", "food"}, skusCategoriesPath, "search=food"},
		{"device-values", "device-values", []string{"--field", "OS"}, skusDeviceValuesPath, "field=os"},
		{"device-values search", "device-values", []string{"--field", "model", "--search", "iphone"},
			skusDeviceValuesPath, "field=model&search=iphone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, skusCategories(`{"value":"IOS","text":"IOS"}`))
			if _, _, err := run(t, findEndpoint(t, "skus", tc.item).command("skus"), srv, tc.args...); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(got.paths) != 1 || got.paths[0] != "/api/v2"+tc.path || got.queries[0] != tc.query {
				t.Errorf("read %v ? %v, want %s ? %s", got.paths, got.queries, tc.path, tc.query)
			}
		})
	}

	// The help names what the group holds, and the row caps of the two
	// searches that are not paged.
	for _, g := range referenceGroups {
		if g.name == "skus" && g.short != "Merchants, brands, categories and purchase devices of online purchases" {
			t.Errorf("reference skus Short = %q", g.short)
		}
	}
	for item, want := range map[string]string{"categories": "at most 100 rows", "device-values": "at most 500 rows"} {
		if help := findEndpoint(t, "skus", item).command("skus").Flags().Lookup("search").Usage; !strings.Contains(help, want) {
			t.Errorf("skus %s --search help = %q, want %q", item, help, want)
		}
	}

	srv, got := stub(t, skusCategories())
	_, _, err := run(t, findEndpoint(t, "skus", "device-values").command("skus"), srv, "--field", "color")
	wantUsageErr(t, err, "unknown --field color", "type, os, make, model")
	if len(got.paths) != 0 {
		t.Errorf("an unknown field should cost no round trip, got %v", got.paths)
	}
}

// skusCategories is the categories catalog's answer to a search.
func skusCategories(rows ...string) string {
	return `{"status":"success","code":200,"message":"ok","data":[` + strings.Join(rows, ",") + `]}`
}

// --category on SKUs takes a category id, or a name the catalog resolves to exactly one category.
func TestSkusCategoryByName(t *testing.T) {
	srv, got := stub(t, skusCategories(
		`{"value":3530,"text":"Dog Food","path":"Animals & Pet Supplies > Pet Supplies > Dog Supplies > Dog Food"}`))

	body := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--category", "dog food")...)

	if raw := jsonOf(firstDataset(t, body)["categories"]); raw != `[3530]` {
		t.Errorf("categories = %s", raw)
	}
	// The name is searched in the SKUs categories catalog, every level at once, and nothing else is read.
	if strings.Join(got.paths, " ") != "/api/v2"+skusCategoriesPath || strings.Join(got.queries, " ") != "search=dog+food" {
		t.Errorf("reads: paths = %v, queries = %v", got.paths, got.queries)
	}
	byID := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--category", "10000000")...)
	if raw := jsonOf(firstDataset(t, byID)["categories"]); raw != `[10000000]` {
		t.Errorf("categories by id = %s", raw)
	}
	if len(got.paths) != 1 {
		t.Errorf("an id is sent as it is, with no lookup: paths = %v", got.paths)
	}
}

// A name that matches several categories, none labelled with it, is a usage error.
// The same leaf name sits under several parents, so the list shows each row's
// path. The search stops at 100 rows and is not paged, so a full answer is
// refused even with one exact label: its twin may be past the 100th row.
func TestSkusCategoryAmbiguous(t *testing.T) {
	full := make([]string, 0, skusCategoriesSearchCap)
	full = append(full, `{"value":3530,"text":"Dog Food","path":"Animals & Pet Supplies > Dog Food"}`)
	for i := 1; i < skusCategoriesSearchCap; i++ {
		full = append(full, fmt.Sprintf(`{"value":%d,"text":"Dog Food Bowls %d"}`, 20000000+i, i))
	}
	for _, tc := range []struct {
		name     string
		reply    string
		category string
		want     []string
	}{
		{"none labelled with it", skusCategories(`{"value":10000000,"text":"Dry Dog Food"}`,
			`{"value":10000002,"text":"Dry Cat Food"}`), "dry",
			[]string{"2 categories match \"dry\"", "Dry Dog Food", "Dry Cat Food"}},
		{"twins under two parents", skusCategories(
			`{"value":10000010,"text":"Accessories","path":"Electronics > Accessories"}`,
			`{"value":10000011,"text":"Accessories","path":"Vehicles & Parts > Accessories"}`), "accessories",
			[]string{"2 categories match \"accessories\"", "Accessories (Electronics > Accessories)",
				"Accessories (Vehicles & Parts > Accessories)"}},
		{"cut short at the cap", skusCategories(full...), "dog food",
			[]string{"100 or more categories match \"dog food\"", "the search stops at 100", "pass an id",
				"Dog Food (Animals & Pet Supplies > Dog Food)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, tc.reply)
			_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--category", tc.category, "--dry-run")...)
			wantUsageErr(t, err, tc.want...)
			if len(got.paths) != 1 {
				t.Errorf("only the category lookup should be sent, got %v", got.paths)
			}
		})
	}
}

// A device value is matched ignoring case and sent as the catalog holds it.
func TestSkusDeviceResolvesCase(t *testing.T) {
	srv, got := stub(t, skusCategories(`{"value":"IOS","text":"IOS"}`, `{"value":"IPADOS","text":"IPADOS"}`))

	body := dryRunBody(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--category", "3530", "--device-os", "ios")...)

	if raw := jsonOf(firstDataset(t, body)["device_oses"]); raw != `["IOS"]` {
		t.Errorf("device_oses = %s", raw)
	}
	// One device-values read for the os field; the category id needs none.
	if strings.Join(got.paths, " ") != "/api/v2"+skusDeviceValuesPath ||
		strings.Join(got.queries, " ") != "field=os&search=ios" {
		t.Errorf("reads: paths = %v, queries = %v", got.paths, got.queries)
	}
	_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, "--category", "3530", "--device-os", "OS X", "--dry-run")...)
	wantUsageErr(t, err, "--device-os OS X", "reference skus device-values --field os")
}

// A device value the catalog does not answer is a usage error, and nothing is
// created. An empty answer can also mean the account lacks the dataset, and a
// search the API cut short at 500 values cannot prove the value is missing.
func TestSkusDeviceUnresolved(t *testing.T) {
	models := make([]string, 0, skusDeviceValuesCap)
	for i := range skusDeviceValuesCap {
		models = append(models, fmt.Sprintf(`{"value":"IPHONE %d","text":"IPHONE %d"}`, i, i))
	}
	for _, tc := range []struct {
		name  string
		reply string
		args  []string
		want  []string
		not   string
	}{
		{"none at all", skusCategories(), []string{"--device-os", "ios"},
			[]string{"--device-os ios", "no SKUs OS value matches it",
				"'intuizi reference skus device-values --field os'",
				"If that lists nothing at all, the SKUs dataset is not enabled for this account",
				"'intuizi reference common dataset-types'"}, "cut short"},
		{"no exact match", skusCategories(`{"value":"IPADOS","text":"IPADOS"}`), []string{"--device-os", "ipad"},
			[]string{"--device-os ipad", "not a value of the SKUs catalog",
				"'intuizi reference skus device-values --field os'"}, "not enabled"},
		{"cut short at the cap", skusCategories(models...), []string{"--device-model", "iphone"},
			[]string{"--device-model iphone", "cut short at 500", "--file"}, "not a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, tc.reply)
			_, _, err := run(t, audiencesCreateCommand(), srv, with(skusFlagsBase, append([]string{"--category", "3530"},
				tc.args...)...)...)
			wantUsageErr(t, err, tc.want...)
			if err != nil && strings.Contains(err.Error(), tc.not) {
				t.Errorf("error says %q: %v", tc.not, err)
			}
			if len(got.paths) != 1 || got.paths[0] != "/api/v2"+skusDeviceValuesPath {
				t.Errorf("only the device lookup should be sent, got %v", got.paths)
			}
		})
	}
}
