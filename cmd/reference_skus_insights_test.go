package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The bodies below are made up, with neutral names: the tests read their exact
// values, so they are golden inputs and live here, not in testdata/responses.
// The settled day is two days before the newest purchase day, as documented.
const (
	skusInsightsBrandBody = `{"status":"success","code":200,"message":"ok","data":[
 {"value":"brand-a","text":"Example Brand A","orders":1520,"spend":48210.5,"avg_order_value":31.72,
  "networks":1204,"devices":2890,"share_of_orders":0.0412,"as_of":"2026-11-10","period_days":30},
 {"value":"brand-b","text":"Example Brand B","orders":980,"spend":20150,"avg_order_value":20.56,
  "networks":801,"devices":null,"share_of_orders":null,"as_of":"2026-11-10","period_days":30}
]}`

	skusInsightsCategoryBody = `{"status":"success","code":200,"message":"ok","data":[
 {"value":"3530","text":"Dry Dog Food","path":"Animals & Pet Supplies > Pet Supplies > Dog Supplies > Dry Dog Food",
  "orders":410,"spend":9800.25,"avg_order_value":23.9,"networks":350,"devices":690,"share_of_orders":0.011,
  "as_of":"2026-11-10","period_days":7}
]}`

	skusCoverageBody = `{"status":"success","code":200,"message":"ok","data":[
 {"first_day":"2026-01-05","newest_purchase_day":"2026-11-12","settled_day":"2026-11-10",
  "newest_device_day":null,
  "empty_days":[["2026-03-02","2026-03-04"],["2026-07-19","2026-07-19"]],
  "thin_days":["2026-05-11","2026-05-12","2026-09-30"],
  "computed_at":"2026-11-13T04:10:00Z"}
]}`
)

// runSkus runs one skus read against a fake server and returns stdout, stderr
// and the error, for the refusals that must cost no request.
func runSkus(t *testing.T, item, baseURL string, args ...string) (string, string, error) {
	t.Helper()

	t.Setenv("INTUIZI_API_TOKEN", "test-token")
	previous := baseURLFlag
	baseURLFlag = baseURL
	t.Cleanup(func() { baseURLFlag = previous })

	cmd := findEndpoint(t, "skus", item).command("skus")
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestSkusInsightsQuery(t *testing.T) {
	srv, queries := serve(t, skusInsightsBrandBody)
	_, _, err := runSkus(t, "insights", srv.URL,
		"--dimension", "Brand", "--period", "90", "--category", "3530", "--limit", "50")
	if err != nil {
		t.Fatal(err)
	}
	want := "category=3530&dimension=brand&limit=50&period=90"
	if len(*queries) != 1 || (*queries)[0] != want {
		t.Errorf("queries = %v, want [%s]", *queries, want)
	}
}

func TestSkusInsightsMerchantFilterWithOtherDimension(t *testing.T) {
	srv, queries := serve(t, skusInsightsBrandBody)
	if _, _, err := runSkus(t, "insights", srv.URL, "--dimension", "category", "--merchant", "shop-a"); err != nil {
		t.Fatal(err)
	}
	if want := "dimension=category&merchant=shop-a"; (*queries)[0] != want {
		t.Errorf("query = %q, want %q", (*queries)[0], want)
	}
}

func TestSkusInsightsRefusals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"unknown dimension", []string{"--dimension", "color"}, []string{"--dimension", "color", "brand", "merchant", "category", "total"}},
		{"unknown period", []string{"--dimension", "brand", "--period", "14"}, []string{"--period", "14", "7", "30", "90"}},
		{"limit zero", []string{"--dimension", "brand", "--limit", "0"}, []string{"--limit", "1", "50"}},
		{"limit above 50", []string{"--dimension", "brand", "--limit", "51"}, []string{"--limit", "51", "50"}},
		{"category zero", []string{"--dimension", "brand", "--category", "0"}, []string{"--category", "1"}},
		{"category negative", []string{"--dimension", "brand", "--category", "-5"}, []string{"--category", "1"}},
		{"merchant with merchant dimension", []string{"--dimension", "merchant", "--merchant", "shop-a"}, []string{"--merchant", "--dimension merchant"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, queries := serve(t, skusInsightsBrandBody)
			_, _, err := runSkus(t, "insights", srv.URL, tc.args...)
			wantUsageErr(t, err, tc.want...)
			if len(*queries) != 0 {
				t.Errorf("a refusal must cost no request, got %v", *queries)
			}
		})
	}
}

func TestSkusInsightsDimensionIsRequired(t *testing.T) {
	srv, queries := serve(t, skusInsightsBrandBody)
	_, _, err := runSkus(t, "insights", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("err = %v, want a missing --dimension error", err)
	}
	if len(*queries) != 0 {
		t.Errorf("got requests %v", *queries)
	}
}

func TestSkusInsightsTable(t *testing.T) {
	srv, _ := serve(t, skusInsightsBrandBody)
	out, _, err := runSkus(t, "insights", srv.URL, "--dimension", "brand")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header and two rows, got:\n%s", out)
	}
	wantHeader := []string{"value", "text", "orders", "spend", "avg_order_value", "networks", "devices", "share_of_orders"}
	if got := strings.Fields(lines[0]); strings.Join(got, ",") != strings.Join(wantHeader, ",") {
		t.Errorf("header = %v, want %v", got, wantHeader)
	}
	// Money has 2 decimals, whatever the JSON spelling.
	if got := strings.Fields(lines[1]); got[5] != "48210.50" || got[6] != "31.72" {
		t.Errorf("spend and avg_order_value = %v %v, want 48210.50 and 31.72", got[5], got[6])
	}
	if got := strings.Fields(lines[2]); got[5] != "20150.00" || got[6] != "20.56" {
		t.Errorf("spend and avg_order_value = %v %v, want 20150.00 and 20.56", got[5], got[6])
	}
	// A null prints as "-", not as a shifted row.
	if got := strings.Fields(lines[2]); len(got) != 10 || got[8] != "-" || got[9] != "-" {
		t.Errorf("null row = %v, want devices and share_of_orders as -", got)
	}
}

func TestSkusInsightsCategoryTableHasPath(t *testing.T) {
	srv, _ := serve(t, skusInsightsCategoryBody)
	out, _, err := runSkus(t, "insights", srv.URL, "--dimension", "category")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasSuffix(lines[0], "share_of_orders  path") {
		t.Errorf("path must be the last column:\n%s", out)
	}
	if !strings.HasSuffix(lines[1], "Dog Supplies > Dry Dog Food") {
		t.Errorf("a category row must end with its path:\n%s", out)
	}
}

func TestSkusJSONIsByteForByte(t *testing.T) {
	for _, tc := range []struct{ item, body string }{
		{"insights", skusInsightsBrandBody},
		{"coverage", skusCoverageBody},
	} {
		t.Run(tc.item, func(t *testing.T) {
			body := tc.body
			srv, _ := serve(t, body)
			jsonOutput = true
			t.Cleanup(func() { jsonOutput = false })
			args := []string(nil)
			if tc.item == "insights" {
				args = []string{"--dimension", "total"}
			}
			out, _, err := runSkus(t, tc.item, srv.URL, args...)
			if err != nil {
				t.Fatal(err)
			}
			// The CLI indents every --json body; compacted, the bytes are the
			// server's, so no field, null or number spelling was changed.
			var got, want bytes.Buffer
			if err := json.Compact(&got, []byte(out)); err != nil {
				t.Fatal(err)
			}
			if err := json.Compact(&want, []byte(body)); err != nil {
				t.Fatal(err)
			}
			if got.String() != want.String() {
				t.Errorf("--json changed the body:\n%s\nwant\n%s", got.String(), want.String())
			}
		})
	}
}

func TestSkusCoverageQuietIsRefusedBeforeAnyRequest(t *testing.T) {
	srv, queries := serve(t, skusCoverageBody)
	quietOutput = true
	t.Cleanup(func() { quietOutput = false })
	_, _, err := runSkus(t, "coverage", srv.URL)
	wantUsageErr(t, err, "--quiet")
	if len(*queries) != 0 {
		t.Errorf("got requests %v", *queries)
	}
}

func TestSkusInsightsQuietPrintsTheValues(t *testing.T) {
	srv, _ := serve(t, skusInsightsBrandBody)
	quietOutput = true
	t.Cleanup(func() { quietOutput = false })
	out, _, err := runSkus(t, "insights", srv.URL, "--dimension", "brand")
	if err != nil {
		t.Fatal(err)
	}
	if out != "brand-a\nbrand-b\n" {
		t.Errorf("--quiet printed %q, want the two brand keys, one per line", out)
	}
}

func TestSkusInsightsQuietWithTotalIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, d := range []string{"total", " Total "} {
		srv, queries := serve(t, skusInsightsBrandBody)
		quietOutput = true
		_, _, err := runSkus(t, "insights", srv.URL, "--dimension", d)
		quietOutput = false
		wantUsageErr(t, err, "--quiet", "--dimension total", "--json")
		if len(*queries) != 0 {
			t.Errorf("--dimension %q: got requests %v", d, *queries)
		}
	}
}

func TestSkusInsightsNoSearchAndTakesQuiet(t *testing.T) {
	e := findEndpoint(t, "skus", "insights")
	if e.noID {
		t.Error("insights rows carry a value; --quiet must be allowed")
	}
	cmd := e.command("skus")
	if cmd.Flags().Lookup("search") != nil || cmd.Flags().Lookup("page") != nil {
		t.Error("insights takes no search or paging")
	}
	for _, flag := range []string{"dimension", "period", "category", "merchant", "limit"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing --%s", flag)
		}
	}
}

func TestSkusCoverageIsADetailView(t *testing.T) {
	srv, queries := serve(t, skusCoverageBody)
	out, _, err := runSkus(t, "coverage", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(*queries) != 1 || (*queries)[0] != "" {
		t.Errorf("queries = %v, want one with no parameters", *queries)
	}
	want := []string{"first_day", "newest_purchase_day", "settled_day", "newest_device_day",
		"empty_ranges", "thin_days", "computed_at"}
	wantVal := []string{"2026-01-05", "2026-11-12", "2026-11-10", "-", "2", "3", "2026-11-13T04:10:00Z"}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("want one line per field, got:\n%s", out)
	}
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != want[i] || f[1] != wantVal[i] {
			t.Errorf("line %d = %v, want %s %s", i+1, f, want[i], wantVal[i])
		}
		if len(line) > 60 {
			t.Errorf("line %d is %d wide", i+1, len(line))
		}
	}
}

func TestSkusCoverageJSONKeepsTheLists(t *testing.T) {
	srv, _ := serve(t, skusCoverageBody)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })
	out, _, err := runSkus(t, "coverage", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"2026-03-02", "2026-05-12", "empty_days", "thin_days"} {
		if !strings.Contains(out, s) {
			t.Errorf("--json omits %q:\n%s", s, out)
		}
	}
}

func TestSkusCoverageTakesNoFlagsAndNoQuiet(t *testing.T) {
	e := findEndpoint(t, "skus", "coverage")
	if !e.noSearch || !e.noID || e.paged || len(e.params) != 0 || e.paramsWithoutSearch {
		t.Errorf("coverage takes no parameters and has no id: %+v", e)
	}
}

func TestSkusEmptyReadsSayWhy(t *testing.T) {
	srv, _ := serve(t, `{"status":"success","code":200,"message":"ok","data":[]}`)
	for _, tc := range []struct {
		item string
		args []string
		want []string
	}{
		{"coverage", nil, []string{"no results", "SKUs dataset", "first daily update"}},
		{"insights", []string{"--dimension", "brand"},
			[]string{"no results", "SKUs dataset", "first daily update", "10 home networks", "10 orders", "filter"}},
	} {
		out, errOut, err := runSkus(t, tc.item, srv.URL, tc.args...)
		if err != nil || out != "" {
			t.Errorf("%s: out=%q err=%v", tc.item, out, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(errOut, w) {
				t.Errorf("%s: stderr omits %q: %q", tc.item, w, errOut)
			}
		}
	}
}

func TestSkusCoverageEmptyAndNullFields(t *testing.T) {
	srv, _ := serve(t, `{"status":"success","code":200,"message":"ok","data":[{"first_day":"2026-02-01",
"newest_purchase_day":"2026-11-12","settled_day":"2026-11-10","newest_device_day":null,
"empty_days":[],"thin_days":[],"computed_at":"2026-11-13T04:10:00Z"}]}`)
	out, _, err := runSkus(t, "coverage", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		got = append(got, strings.Join(strings.Fields(line), "="))
	}
	want := "first_day=2026-02-01,newest_purchase_day=2026-11-12,settled_day=2026-11-10," +
		"newest_device_day=-,empty_ranges=0,thin_days=0,computed_at=2026-11-13T04:10:00Z"
	if strings.Join(got, ",") != want {
		t.Errorf("lines = %v, want %s", got, want)
	}
}

func TestSkusHelpSaysWhatTheFiguresMean(t *testing.T) {
	ins := findEndpoint(t, "skus", "insights").command("skus").Long
	for _, w := range []string{"fewer than 10 home networks", "more than 100%", "0.1707 = 17.07%",
		"USD", "ALL", "one level under", "approximate", "panel", "--quiet prints the values",
		"no row passes the filters"} {
		if !strings.Contains(ins, w) {
			t.Errorf("insights help omits %q", w)
		}
	}
	if strings.Contains(ins, "An empty list means an account without the SKUs dataset, or no daily update yet") {
		t.Error("insights help blames an empty list on the dataset alone")
	}
	cov := findEndpoint(t, "skus", "coverage").command("skus").Long
	for _, w := range []string{"two days before", "about three days", "no purchases", "small part",
		"purchase-click day", "no search or paging", "one field per line"} {
		if !strings.Contains(cov, w) {
			t.Errorf("coverage help omits %q", w)
		}
	}
	if strings.Contains(cov, "Takes no flags") {
		t.Error("coverage help must not say it takes no flags")
	}
	for name, text := range map[string]string{"insights": ins, "coverage": cov} {
		for i, line := range strings.Split(text, "\n") {
			if len(line) > 78 {
				t.Errorf("%s help line %d is %d wide", name, i+1, len(line))
			}
		}
	}
}
