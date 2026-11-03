package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/output"
)

// The Network Switching results of an audience, read in
// analysis_results.network_switching of Get Audience. Contract:
// docs-src/content/api/v2/audiences.md, "Analysis results", in
// intuizi-console.

// switchingLead orders the Network Switching summary: what was compared, then
// the totals. Every count is a count of devices.
var switchingLead = []string{
	"version", "source", "network_type", "min_home_days", "from", "to",
	"devices_from", "devices_to", "devices_both", "changed", "switched",
}

// flowColumns are the columns of the top flows, as the API names them.
var flowColumns = []string{"from", "to", "devices", "switched", "reverse", "reverse_switched"}

// maxShownFlows is how many flows the summary lists. The API sends up to 50,
// the most devices first; --json has every one.
const maxShownFlows = 10

// printNetworkSwitching prints the Network Switching results of an audience
// below its key/value lines, where the detail view can only show
// analysis_results as {...}. An audience built without the analysis prints
// nothing more. --json is not affected: it prints the API's data untouched.
func printNetworkSwitching(cmd *cobra.Command, rec output.Record) error {
	results, _ := rec["analysis_results"].(map[string]any)
	raw, present := results["network_switching"]
	if !present {
		return nil
	}
	out := cmd.OutOrStdout()
	ns, ok := raw.(map[string]any)
	if !ok {
		// null until the analysis finishes, and for good when the build fails.
		_, err := fmt.Fprintln(out, "\nNetwork Switching: no results - the analysis has not finished, "+
			"or the build failed")
		return err
	}

	summary := make(output.Record, len(switchingLead))
	for _, k := range switchingLead {
		if v, ok := ns[k]; ok {
			summary[k] = v
		}
	}
	for _, k := range []string{"from", "to"} {
		if w, ok := ns[k].(map[string]any); ok {
			summary[k] = switchingWindow(w)
		}
	}
	if _, err := fmt.Fprintln(out, "\nNetwork Switching"); err != nil {
		return err
	}
	if err := output.Detail(out, summary, switchingLead); err != nil {
		return err
	}

	flows, _ := ns["flows"].([]any)
	networks, _ := ns["networks"].([]any)
	rows := make([]output.Record, 0, maxShownFlows)
	for _, f := range flows {
		if len(rows) == maxShownFlows {
			break
		}
		if m, ok := f.(map[string]any); ok {
			rows = append(rows, output.Record(m))
		}
	}
	if len(rows) > 0 {
		heading := "\nTop flows"
		if len(flows) > len(rows) {
			heading = fmt.Sprintf("\nTop flows (%d of %d)", len(rows), len(flows))
		}
		if _, err := fmt.Fprintln(out, heading); err != nil {
			return err
		}
		if err := output.TableWith(out, rows, flowColumns); err != nil {
			return err
		}
	}
	// Commentary, so stderr: what the summary leaves out, and where it is.
	if len(networks) > 0 || len(flows) > len(rows) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--json has every flow and the per-network rows, under "+
			".data[0].analysis_results.network_switching")
	}
	return nil
}

// switchingWindow reads one compared window, {month, start, end}, as
// "2026-09 (2026-09-21 to 2026-09-27)".
func switchingWindow(w map[string]any) string {
	month, _ := w["month"].(string)
	start, _ := w["start"].(string)
	end, _ := w["end"].(string)
	if start == "" || end == "" {
		return month
	}
	return fmt.Sprintf("%s (%s to %s)", month, start, end)
}
