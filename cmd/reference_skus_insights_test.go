package cmd

import (
	"context"
	"io"
	"strings"
	"testing"
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
	srv, queries := serve(t, fixture(t, "skus-insights-brand.json"))
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
	srv, queries := serve(t, fixture(t, "skus-insights-brand.json"))
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
		{"merchant with merchant dimension", []string{"--dimension", "merchant", "--merchant", "shop-a"}, []string{"--merchant", "--dimension merchant"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, queries := serve(t, fixture(t, "skus-insights-brand.json"))
			_, _, err := runSkus(t, "insights", srv.URL, tc.args...)
			wantUsageErr(t, err, tc.want...)
			if len(*queries) != 0 {
				t.Errorf("a refusal must cost no request, got %v", *queries)
			}
		})
	}
}

func TestSkusInsightsDimensionIsRequired(t *testing.T) {
	srv, queries := serve(t, fixture(t, "skus-insights-brand.json"))
	_, _, err := runSkus(t, "insights", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("err = %v, want a missing --dimension error", err)
	}
	if len(*queries) != 0 {
		t.Errorf("got requests %v", *queries)
	}
}

func TestSkusInsightsTable(t *testing.T) {
	srv, _ := serve(t, fixture(t, "skus-insights-brand.json"))
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
	// A null prints as "-", not as a shifted row.
	if got := strings.Fields(lines[2]); len(got) != 10 || got[8] != "-" || got[9] != "-" {
		t.Errorf("null row = %v, want devices and share_of_orders as -", got)
	}
}

func TestSkusInsightsCategoryTableHasPath(t *testing.T) {
	srv, _ := serve(t, fixture(t, "skus-insights-category.json"))
	out, _, err := runSkus(t, "insights", srv.URL, "--dimension", "category")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "path") || !strings.Contains(out, "Dog Supplies > Dry Dog Food") {
		t.Errorf("a category row must show its path:\n%s", out)
	}
}

func TestSkusInsightsJSONIsUntouched(t *testing.T) {
	body := fixture(t, "skus-insights-brand.json")
	srv, _ := serve(t, body)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })
	out, _, err := runSkus(t, "insights", srv.URL, "--dimension", "total")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"devices": null`) && !strings.Contains(out, `"devices":null`) {
		t.Errorf("--json must keep the null:\n%s", out)
	}
}

func TestSkusInsightsQuietRefusedAndNoSearch(t *testing.T) {
	e := findEndpoint(t, "skus", "insights")
	if !e.noID {
		t.Error("insights rows carry no id column of their own; --quiet must be refused")
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
	_ = io.Discard
}

func TestSkusCoverageTable(t *testing.T) {
	srv, queries := serve(t, fixture(t, "skus-coverage.json"))
	out, _, err := runSkus(t, "coverage", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(*queries) != 1 || (*queries)[0] != "" {
		t.Errorf("queries = %v, want one with no parameters", *queries)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want header and one row, got:\n%s", out)
	}
	wantHeader := "first_day,newest_purchase_day,settled_day,newest_device_day,empty_ranges,thin_days,computed_at"
	if got := strings.Join(strings.Fields(lines[0]), ","); got != wantHeader {
		t.Errorf("header = %s, want %s", got, wantHeader)
	}
	row := strings.Fields(lines[1])
	want := []string{"2026-01-05", "2026-11-12", "2026-11-08", "-", "2", "3", "2026-11-13T04:10:00Z"}
	if strings.Join(row, ",") != strings.Join(want, ",") {
		t.Errorf("row = %v, want %v", row, want)
	}
}

func TestSkusCoverageJSONKeepsTheLists(t *testing.T) {
	srv, _ := serve(t, fixture(t, "skus-coverage.json"))
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
	if !e.noSearch || !e.noID || e.paged || len(e.params) != 0 {
		t.Errorf("coverage takes no parameters and has no id: %+v", e)
	}
}

func TestSkusEmptyReadsSayNoResults(t *testing.T) {
	srv, _ := serve(t, `{"status":"success","code":200,"message":"ok","data":[]}`)
	for _, item := range []string{"coverage"} {
		out, errOut, err := runSkus(t, item, srv.URL)
		if err != nil || out != "" || !strings.Contains(errOut, "no results") {
			t.Errorf("%s: out=%q err=%q %v", item, out, errOut, err)
		}
	}
}
