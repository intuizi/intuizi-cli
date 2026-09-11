package cmd

import (
	"fmt"
	"net/url"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// yearMonth is the only shape /usage accepts; checking it here turns a round
// trip into an instant error.
var yearMonth = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func usageCommand() *cobra.Command {
	var month string

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show this month's data-scan usage",
		Long: `Show your company's data-scan usage for a calendar month.

Reports the total bytes scanned, a breakdown by operation type - audience
build, refresh, lookalike, cohort and activation - and your monthly limit.
Defaults to the current month.

    intuizi usage
    intuizi usage --month 2026-06

No cost figures are exposed through the API. Usage requires a permission your
Account Manager enables; without it the read is rejected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A report has no id to print.
			if quietOutput {
				return usageErr("--quiet does not apply: usage returns a report, not an id")
			}

			query := url.Values{}
			if cmd.Flags().Changed("month") {
				if !yearMonth.MatchString(month) {
					return usageErr(fmt.Sprintf("--month %q is not YYYY-MM", month))
				}
				query.Set("yearmonth", month)
			}

			c, err := client()
			if err != nil {
				return err
			}

			if jsonOutput {
				raw, err := c.GetRaw(cmd.Context(), "/usage", query)
				if err != nil {
					return err
				}
				return output.JSON(cmd.OutOrStdout(), raw)
			}

			rec, err := api.Read[output.Record](cmd.Context(), c, "/usage", query)
			if err != nil {
				return err
			}
			return renderUsage(cmd, rec)
		},
	}

	cmd.Flags().StringVar(&month, "month", "",
		"Calendar month to report as YYYY-MM (default: the current month)")
	return cmd
}

// renderUsage prints the headline figures, then the per-operation breakdown as
// its own table. The nested shape would otherwise collapse to "{...}".
func renderUsage(cmd *cobra.Command, rec output.Record) error {
	out := cmd.OutOrStdout()

	summary := output.Record{"month": rec["yearmonth"]}
	if scanned, ok := rec["data_scanned"].(map[string]any); ok {
		summary["scanned"] = scanned["formatted"]
	}
	if limit, ok := rec["limit"].(map[string]any); ok {
		summary["limit"] = limit["data_scan_limit_formatted"]
		summary["percent_used"] = limit["percent_used"]
		summary["over_limit"] = limit["over_limit"]
		summary["enforced"] = limit["enforced"]
	}
	if err := output.Detail(out, summary,
		[]string{"month", "scanned", "limit", "percent_used", "over_limit", "enforced"}); err != nil {
		return err
	}

	ops, _ := rec["operations"].([]any)
	if len(ops) == 0 {
		return nil
	}

	rows := make([]output.Record, 0, len(ops))
	for _, o := range ops {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, output.Record{
			"operation": m["operation_type"],
			"label":     m["label"],
			"scanned":   m["formatted"],
		})
	}

	_, _ = fmt.Fprintln(out)
	return output.TableWith(out, rows, []string{"operation", "label", "scanned"})
}

func init() {
	rootCmd.AddCommand(usageCommand())
}
