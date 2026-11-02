package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// switchingAudience is a Get Audience envelope whose analysis_results holds
// results, the API's own example shape for a Connectivity audience.
func switchingAudience(results string) string {
	return `{"status":"success","code":200,"message":"ok","data":[{"id":1377,"name":"Starlink switching",` +
		`"status":{"id":104,"name":"Completed"},"results_count":1189,"created_at":"2026-11-02 10:00:00",` +
		`"analysis_results":` + results + `}]}`
}

// switchingResults is a version 2 result with flows extra flows beyond the
// two of the example, the most devices first.
func switchingResults(flows int) string {
	rows := []string{
		`{"from":"EXAMPLE DSL","to":"EXAMPLE SATELLITE","devices":12000,"switched":3000,"reverse":5000,"reverse_switched":1000}`,
		`{"from":"EXAMPLE SATELLITE","to":"EXAMPLE DSL","devices":5000,"switched":1000,"reverse":12000,"reverse_switched":3000}`,
	}
	for i := 0; i < flows; i++ {
		rows = append(rows, fmt.Sprintf(`{"from":"EXAMPLE %c","to":"OTHER","devices":%d,"switched":1,"reverse":0,"reverse_switched":0}`,
			'A'+i, 100-i))
	}
	return `{"network_switching":{"version":2,"source":"connectivity","network_type":"home","min_home_days":3,` +
		`"min_cell":10,"from":{"month":"2026-09","start":"2026-09-21","end":"2026-09-27"},` +
		`"to":{"month":"2026-10","start":"2026-10-19","end":"2026-10-25"},` +
		`"devices_from":1150000,"devices_to":1200000,"devices_both":1000000,"changed":17000,"switched":4000,` +
		`"networks":[{"network":"EXAMPLE DSL","start":800000,"stayed":788000,"gained":5000,"gained_switched":1000,` +
		`"lost":12000,"lost_switched":3000,"net":-2000,"top_from":[],"top_to":[]}],` +
		`"flows":[` + strings.Join(rows, ",") + `]}}`
}

// The table output follows the audience with a summary of its results: the
// settings, the two windows with their dates, the totals, and the top flows.
func TestAudiencesShowPrintsTheNetworkSwitchingSummary(t *testing.T) {
	srv, _ := stub(t, switchingAudience(switchingResults(0)))

	out, errb, err := run(t, audiencesShowCommand(), srv, "1377")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The audience first, as before.
	if !strings.HasPrefix(out, "id ") || !strings.Contains(out, "Starlink switching") {
		t.Errorf("the audience should lead:\n%s", out)
	}
	summary := out[strings.Index(out, "\nNetwork Switching\n"):]
	for _, line := range []string{
		"version        2",
		"source         connectivity",
		"network_type   home",
		"min_home_days  3",
		"from           2026-09 (2026-09-21 to 2026-09-27)",
		"to             2026-10 (2026-10-19 to 2026-10-25)",
		"devices_from   1150000",
		"devices_to     1200000",
		"devices_both   1000000",
		"changed        17000",
		"switched       4000",
		"\nTop flows\n",
		"from               to                 devices  switched  reverse  reverse_switched",
		"EXAMPLE DSL        EXAMPLE SATELLITE  12000    3000      5000     1000",
		"EXAMPLE SATELLITE  EXAMPLE DSL        5000     1000      12000    3000",
	} {
		if !strings.Contains(summary, line) {
			t.Errorf("summary omits %q:\n%s", line, summary)
		}
	}
	// In the order the API reads them, settings before totals.
	if strings.Index(summary, "version") > strings.Index(summary, "devices_from") {
		t.Errorf("the settings should lead the summary:\n%s", summary)
	}
	if strings.Contains(summary, "min_cell") || strings.Contains(summary, "stayed") {
		t.Errorf("the summary leaves min_cell and the networks to --json:\n%s", summary)
	}
	if !strings.Contains(errb, "--json has every flow and the per-network rows") {
		t.Errorf("stderr should point at --json for the networks, got %q", errb)
	}
}

// Ten flows at most; the heading says how many there are.
func TestAudiencesShowListsTheTopTenFlows(t *testing.T) {
	srv, _ := stub(t, switchingAudience(switchingResults(10)))

	out, errb, err := run(t, audiencesShowCommand(), srv, "1377")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "\nTop flows (10 of 12)\n") {
		t.Errorf("heading should say 10 of 12:\n%s", out)
	}
	if !strings.Contains(out, "EXAMPLE H") || strings.Contains(out, "EXAMPLE I") {
		t.Errorf("want the first ten flows exactly:\n%s", out)
	}
	if !strings.Contains(errb, "--json has every flow") {
		t.Errorf("stderr = %q", errb)
	}
}

// --json is the API's data untouched: no summary, nothing reshaped.
func TestAudiencesShowJSONIsUntouched(t *testing.T) {
	body := switchingAudience(switchingResults(0))
	srv, _ := stub(t, body)

	out, errb, err := runJSON(t, audiencesShowCommand(), srv, "1377")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "Network Switching") || strings.Contains(out, "Top flows") || errb != "" {
		t.Errorf("--json printed the summary:\n%s\nstderr: %s", out, errb)
	}
	compact := strings.Join(strings.Fields(out), "")
	if compact != strings.Join(strings.Fields(body), "") {
		t.Errorf("--json is not the envelope as sent:\n%s", out)
	}
}

// null until the analysis finishes: said in one line, with no table.
func TestAudiencesShowNullResults(t *testing.T) {
	srv, _ := stub(t, switchingAudience(`{"network_switching":null}`))

	out, _, err := run(t, audiencesShowCommand(), srv, "1377")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "\nNetwork Switching: no results - the analysis has not finished, or the build failed\n") ||
		strings.Contains(out, "Top flows") {
		t.Errorf("stdout = %s", out)
	}
}

// An audience built without the analysis, and an empty result, print as
// before: no summary, or no flows table.
func TestAudiencesShowWithoutSwitching(t *testing.T) {
	t.Run("no analysis", func(t *testing.T) {
		srv, _ := stub(t, switchingAudience(`{}`))
		out, errb, err := run(t, audiencesShowCommand(), srv, "1377")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(out, "Network Switching") || errb != "" {
			t.Errorf("stdout = %s\nstderr = %s", out, errb)
		}
	})
	t.Run("no device in both windows", func(t *testing.T) {
		srv, _ := stub(t, switchingAudience(`{"network_switching":{"version":1,"network_type":"home",`+
			`"min_home_days":3,"min_cell":10,"from":{"month":"2026-08","start":"2026-08-25","end":"2026-08-31"},`+
			`"to":{"month":"2026-09","start":"2026-09-24","end":"2026-09-30"},"devices_from":0,"devices_to":120,`+
			`"devices_both":0,"changed":0,"switched":0,"networks":[],"flows":[]}}`))
		out, errb, err := run(t, audiencesShowCommand(), srv, "1377")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out, "devices_to     120") || strings.Contains(out, "Top flows") ||
			strings.Contains(out, "source") || errb != "" {
			t.Errorf("stdout = %s\nstderr = %s", out, errb)
		}
	})
}

// A wait that ends on the audience prints the same summary.
func TestAudiencesShowWaitPrintsTheSummary(t *testing.T) {
	fast(t)
	// The first read is still building, with no results yet.
	srv, _ := stubSeq(t,
		reply{body: strings.Replace(switchingAudience(`{"network_switching":null}`),
			`{"id":104,"name":"Completed"}`, `{"id":101,"name":"Processing"}`, 1)},
		reply{body: switchingAudience(switchingResults(0))})

	out, _, err := run(t, audiencesShowCommand(), srv, "1377", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "\nNetwork Switching\n") || !strings.Contains(out, "switched       4000") {
		t.Errorf("stdout should end with the summary:\n%s", out)
	}
}
