package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// quoteLead are the price figures, in the order worth reading them.
var quoteLead = []string{"credits", "minimum", "minimum_applies", "available", "available_after", "run_type"}

func audiencesQuoteCommand() *cobra.Command {
	var req audienceRequest

	cmd := &cobra.Command{
		Use:   "quote",
		Short: "Price an audience in credits before creating it",
		Long: `Price an audience in credits before creating it.

Takes exactly what 'intuizi audiences create' takes - the same flags, names
resolved the same way, or the same --file body - and sends it to Quote
Audience instead of Create Audience. The answer is the price in credits, how
it splits over the datasets, and your organization's credits now and after the
build:

    intuizi audiences quote \
      --type POI --brand starbucks \
      --country USA --state CA --city "San Francisco" \
      --start-date 2026-09-02 --end-date 2026-09-09 \
      --name "Starbucks visitors - SF - 1 week"

    intuizi audiences quote --file audience.json

'intuizi audiences create --help' describes every flag. A quote creates
nothing and is free: it counts toward neither the build budget nor the
data-scan limit. Creating the same audience then charges the quoted price.

Quotes answer only for an organization billed in credits: any other
organization gets a 403, before the body is checked.`,
		Args: cobra.NoArgs,
	}
	req.register(cmd)
	cmd.Flags().Lookup("dry-run").Usage = "Print the body the flags produce; quotes nothing (names still resolve)"
	// The flag is shared with create, but a quote runs nothing.
	cmd.Flags().Lookup("frequency").Usage = "Accepted and checked as on create, but a quote runs nothing"

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if quietOutput {
			return usageErr("--quiet does not apply: a quote returns a price, not an id")
		}
		body, printed, err := req.payload(cmd)
		if err != nil || printed {
			return err
		}
		return postQuote(cmd, audiencesPrefix+"/quote", body)
	}
	return cmd
}

func lookalikeQuoteCommand() *cobra.Command {
	cmd := lookalikeBodyCommand("quote", "Price a lookalike in credits before training it", `Price a lookalike in credits before training it.

Takes exactly what 'intuizi audiences lookalike create' takes - the same flags
or the same --file body - and sends it to Quote Lookalike instead. A lookalike
costs the same for any size. The answer is the price in credits and your
organization's credits now and after the run:

    intuizi audiences lookalike quote \
      --name "Starbucks lookalike" --source-audience-id 1381 \
      --target-size 500000 --signal poi --signal apps --country USA

'intuizi audiences lookalike create --help' describes every flag. A quote
trains nothing and is free.

Quotes answer only for an organization billed in credits: any other
organization gets a 403, before the body is checked. The Lookalike commands
require additional permissions which need to be approved by your Account
Manager.`,
		func(cmd *cobra.Command, body any) error {
			return postQuote(cmd, audiencesPrefix+"/quote-lookalike", body)
		})
	cmd.Flags().Lookup("dry-run").Usage = "Print the body the flags produce and quote nothing"

	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if quietOutput {
			return usageErr("--quiet does not apply: a quote returns a price, not an id")
		}
		return run(cmd, args)
	}
	return cmd
}

// postQuote posts a quote body and prints the price, then its split over the
// datasets as its own table. --json prints the envelope the server sent.
func postQuote(cmd *cobra.Command, path string, body any) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.PostRaw(cmd.Context(), path, body)
		if err != nil {
			printErrorEnvelope(cmd, err)
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	rec, err := api.Create[output.Record](cmd.Context(), c, path, body)
	if err != nil {
		return err
	}
	return renderQuote(cmd, rec)
}

func renderQuote(cmd *cobra.Command, rec output.Record) error {
	out := cmd.OutOrStdout()

	summary := make(output.Record, len(quoteLead)+1)
	for _, k := range quoteLead {
		if v, ok := rec[k]; ok {
			summary[k] = v
		}
	}
	if names, ok := rec["not_charged"].([]any); ok && len(names) > 0 {
		summary["not_charged"] = name(names)
	}
	if err := output.Detail(out, summary, quoteLead); err != nil {
		return err
	}

	lines, _ := rec["lines"].([]any)
	rows := make([]output.Record, 0, len(lines))
	for _, l := range lines {
		m, ok := l.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, output.Record{"dataset": m["dataset"], "days": m["days"], "credits": m["credits"]})
	}
	if len(rows) == 0 {
		return nil
	}

	_, _ = fmt.Fprintln(out)
	return output.TableWith(out, rows, []string{"dataset", "days", "credits"})
}
