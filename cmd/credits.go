package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// creditsPath is the balance read; the statement hangs off it.
const creditsPath = "/credits"

// statementColumns lead the statement table. The audience is an {id, name}
// object, so the table shows its name; --json prints the id too.
var statementColumns = []string{"id", "occurred_at", "entry", "audience", "user", "credits"}

func creditsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "credits",
		Short: "Show your organization's credits",
		Long: `Show your organization's credits for the current period.

An organization billed in credits pays for its runs in credits: each audience
build, each scheduled refresh cycle and each lookalike. A run that fails, is
cancelled or completes with no results gets its credits back. The period is
your contract term while today falls in it, otherwise the calendar month.

    intuizi credits
    intuizi credits statement --search refund

The balance shows what is available and used, the share used and its state
(ok, low at 70%, critical at 90%, out when nothing is left), the pace and the
day the balance runs out at that pace, and the credits used per run type.
'intuizi audiences quote' prices a build before it runs.

Credits answer only for an organization billed in credits: any other
organization gets a 403. The balance and the statement require additional
permissions which need to be approved by your Account Manager, the same as
'intuizi usage'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A balance has no id to print.
			if quietOutput {
				return usageErr("--quiet does not apply: credits returns a balance, not an id")
			}

			c, err := client()
			if err != nil {
				return err
			}

			if jsonOutput {
				raw, err := c.GetRaw(cmd.Context(), creditsPath, nil)
				if err != nil {
					printErrorEnvelope(cmd, err)
					return err
				}
				return output.JSON(cmd.OutOrStdout(), raw)
			}

			rec, err := api.Read[output.Record](cmd.Context(), c, creditsPath, nil)
			if err != nil {
				return err
			}
			return renderCredits(cmd, rec)
		},
	}
	cmd.AddCommand(creditsStatementCommand())
	return cmd
}

// renderCredits prints the balance, then the credits per run type as its own
// table. The nested shape would otherwise collapse to "{...}".
func renderCredits(cmd *cobra.Command, rec output.Record) error {
	out := cmd.OutOrStdout()

	summary := output.Record{
		"plan":         rec["plan"],
		"available":    rec["available"],
		"used":         rec["used"],
		"percent_used": rec["percent_used"],
		"state":        rec["state"],
	}
	if period, ok := rec["period"].(map[string]any); ok {
		summary["period"] = fmt.Sprintf("%v %v to %v", period["type"], period["from"], period["to"])
	}
	if pace, ok := rec["pace"].(map[string]any); ok {
		summary["credits_per_day"] = pace["credits_per_day"]
		summary["runs_out_on"] = pace["runs_out_on"]
	}
	if next, ok := rec["expiring_next"].(map[string]any); ok {
		summary["expiring_next"] = fmt.Sprintf("%v on %v", next["credits"], next["expires_at"])
	}
	if err := output.Detail(out, summary,
		[]string{"period", "plan", "available", "used", "percent_used", "state", "credits_per_day", "runs_out_on", "expiring_next"}); err != nil {
		return err
	}

	ops, _ := rec["by_operation"].([]any)
	rows := make([]output.Record, 0, len(ops))
	for _, o := range ops {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, output.Record{
			"operation": m["operation_type"],
			"label":     m["label"],
			"credits":   m["credits"],
		})
	}
	if len(rows) == 0 {
		return nil
	}

	_, _ = fmt.Fprintln(out)
	return output.TableWith(out, rows, []string{"operation", "label", "credits"})
}

func creditsStatementCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "statement",
		Short: "List the credits statement of the current period",
		Long: `List the current period's credit entries, newest first.

Each entry is credits added (plan credits, a top-up, credits from Intuizi), a
run charged, a refund, or credits that expired. A run paid from two blocks of
credits is one entry. credits is signed: positive when credits were added or
given back, negative when used.

    intuizi credits statement
    intuizi credits statement --search "coffee"

--search matches the audience name or the entry label, such as Refund or
Top-up.`,
		Args: cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the audience name or the entry label")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, creditsPath+"/statement", query(), statementColumns, "no entries in this period")
	}
	return cmd
}

func init() {
	rootCmd.AddCommand(creditsCommand())
}
