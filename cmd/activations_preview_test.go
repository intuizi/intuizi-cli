package cmd

import (
	"net/url"
	"strings"
	"testing"
)

// activationPreviewEnvelope is Preview Activation's documented response for audience 88
// over the range 2-5.
const activationPreviewEnvelope = `{"status":"success","code":200,"message":"Resource fetched successfully.","data":[{
 "audience":{"id":88,"name":"Coffee Buyers NYC","status":{"id":104,"name":"Completed"},
  "is_activation_allowed":true,
  "eligibility":{"allowed":true,"reasons":[],"metrics":{"unique_eids":5100,"unique_scids":null,"eid_scid_ratio":null,"is_affinity":false}}},
 "dataset_type":"POI","analysis_type":"frequency",
 "recency":[{"dataset_type":"POI","start_date":"2026-06-01","end_date":"2026-07-31"}],
 "source_count":5100,"histogram_total":5000,"filtered_count":2000,
 "frequency_bounds":{"min":1,"max":5},
 "histogram":[{"index":1,"counts":3000},{"index":2,"counts":1200},{"index":3,"counts":500},{"index":5,"counts":300}],
 "applied_filters":{"freq_limit":true,"freq_min":2,"freq_max":5},
 "filter_hash":"sha256:4f9d0c7e1b2a8d3f6e5c4b3a2918f7e6d5c4b3a291807f6e5d4c3b2a19180706",
 "method":"histogram_sum","as_of":"2026-08-01 06:12:44",
 "limitations":["filtered_count is the exact number of devices whose distinct visit-day count inside the audience date window falls within [freq_min, freq_max].",
  "source_count is the audience total shown on the audience itself."]}]}`

var activationPreviewArgs = []string{"--audience-id", "88", "--freq-min", "2", "--freq-max", "5"}

// The preview's contract is closed: exactly these three parameters, and any
// other is a 422.
func TestActivationsPreviewSendsTheThreeParameters(t *testing.T) {
	srv, got := stub(t, activationPreviewEnvelope)

	if _, _, err := run(t, activationsPreviewCommand(), srv, activationPreviewArgs...); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/analyses/activations/preview" {
		t.Errorf("path = %s", got.paths[0])
	}
	q, err := url.ParseQuery(got.queries[0])
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"audience_id": {"88"}, "freq_min": {"2"}, "freq_max": {"5"}}
	if q.Encode() != want.Encode() {
		t.Errorf("query = %s, want %s", q.Encode(), want.Encode())
	}
}

// The figures lead, the bounds and range read as ranges, and the histogram is
// a table of its own - the thing a range is picked from.
func TestActivationsPreviewShowsTheCountAndHistogram(t *testing.T) {
	srv, _ := stub(t, activationPreviewEnvelope)

	out, _, err := run(t, activationsPreviewCommand(), srv, activationPreviewArgs...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"Coffee Buyers NYC", "filtered_count", "2000", "freq_range", "2-5",
		"frequency_bounds", "1-5", "sha256:4f9d0c7e1b2a8d3f6e5c4b3a2918f7e6d5c4b3a291807f6e5d4c3b2a19180706",
		"POI 2026-06-01..2026-07-31",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "filtered_count") > strings.Index(out, "analysis_type") {
		t.Errorf("filtered_count should lead the alphabetical fields:\n%s", out)
	}

	// The histogram, one row per bucket, under its own header.
	table := out[strings.Index(out, "days"):]
	for _, row := range [][2]string{{"1", "3000"}, {"2", "1200"}, {"3", "500"}, {"5", "300"}} {
		if !containsRow(table, row[0], row[1]) {
			t.Errorf("histogram missing %s -> %s:\n%s", row[0], row[1], table)
		}
	}
	// The limitations are prose for --json, and a {...} or [n items] cell
	// says nothing.
	for _, noise := range []string{"exact number of devices", "{...}", "items]"} {
		if strings.Contains(out, noise) {
			t.Errorf("stdout carries %q:\n%s", noise, out)
		}
	}
}

// containsRow reports a line whose fields are exactly cells.
func containsRow(table string, cells ...string) bool {
	for _, line := range strings.Split(table, "\n") {
		if strings.Join(strings.Fields(line), " ") == strings.Join(cells, " ") {
			return true
		}
	}
	return false
}

func TestActivationsPreviewJSONPrintsTheEnvelope(t *testing.T) {
	srv, _ := stub(t, activationPreviewEnvelope)

	out, _, err := runJSON(t, activationsPreviewCommand(), srv, activationPreviewArgs...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"message": "Resource fetched successfully."`) ||
		!strings.Contains(out, `"limitations": [`) {
		t.Errorf("--json should print the server's envelope untouched:\n%s", out)
	}
}

// Every mistake below is caught before a request is made, and exits 2.
func TestActivationsPreviewRejectsBadInputLocally(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"no flags", nil, []string{"--audience-id", "--freq-min", "--freq-max"}},
		{"no --freq-max", []string{"--audience-id", "88", "--freq-min", "2"}, []string{"--freq-max"}},
		{"inverted range", []string{"--audience-id", "88", "--freq-min", "5", "--freq-max", "2"},
			[]string{"--freq-min 5", "--freq-max 2"}},
		{"negative bound", []string{"--audience-id", "88", "--freq-min", "-1", "--freq-max", "2"},
			[]string{"--freq-min"}},
		{"bad audience id", []string{"--audience-id", "0", "--freq-min", "1", "--freq-max", "2"},
			[]string{"--audience-id 0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, activationPreviewEnvelope)

			_, _, err := run(t, activationsPreviewCommand(), srv, tc.args...)

			wantUsageErr(t, err, tc.want...)
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// A preview is a count, not a resource, so --quiet has no id to print.
func TestActivationsPreviewRefusesQuiet(t *testing.T) {
	quiet(t)
	srv, got := stub(t, activationPreviewEnvelope)

	_, _, err := run(t, activationsPreviewCommand(), srv, activationPreviewArgs...)

	wantUsageErr(t, err, "--quiet")
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}
