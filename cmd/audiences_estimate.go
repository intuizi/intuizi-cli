package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/output"
)

// estimatePrefix is Estimate Audience Size: an estimate is POSTed here, with
// no /create, and read at estimatePrefix/{id}.
const estimatePrefix = audiencesPrefix + "/estimate"

// estimateLead puts the figures first, then what explains a missing one.
var estimateLead = []string{
	"id", "name", "status", "uniques", "visits", "signals", "unique_eips",
	"number_of_days", "as_of", "blocked", "status_message",
}

// estimateLifecycle is how --wait follows an estimate. Its status is a bare
// string, not the {id, name} object an audience carries: pending and
// processing keep the wait going, completed is the one success, and blocked
// and failed are final.
var estimateLifecycle = lifecycle{
	singular: "estimate",
	prefix:   estimatePrefix,
	create:   estimatePrefix,
	resume:   "intuizi audiences estimate show",
	lead:     estimateLead,
	read:     readEstimate,
	view:     estimateView,
}

// readEstimate reads one estimate. Any status other than the five documented
// ends the wait as a failure, as an unknown id does on the audience scale, so
// a CI job never waits out its whole timeout on one.
func readEstimate(rec output.Record, id int) waitState {
	status, _ := rec["status"].(string)
	st := waitState{key: status, label: status, name: status}
	if status == "" {
		st.label = "no status"
	}

	switch status {
	case "pending", "processing":
	case "completed":
		st.done = true
	case "blocked":
		// The recipe cannot be answered as sent - most often dates outside the
		// data's coverage - and the reason says what to change.
		st.done = true
		st.err = fmt.Errorf("estimate %d %w: blocked%s", id, errWaitFailed, blockedDetail(rec))
	case "failed":
		st.done = true
		if msg, _ := rec["status_message"].(string); msg != "" {
			st.err = fmt.Errorf("estimate %d %w: %s", id, errWaitFailed, msg)
		} else {
			st.err = fmt.Errorf("estimate %d %w, with no status_message", id, errWaitFailed)
		}
	default:
		st.done = true
		st.err = fmt.Errorf("estimate %d %w: unknown status %q", id, errWaitFailed, status)
	}
	return st
}

// blockedDetail says what blocked an estimate, for the error that ends a wait.
func blockedDetail(rec output.Record) string {
	if text := blockedText(rec, " - "); text != "" {
		return " by " + text
	}
	return ", with no reason given"
}

// blockedText joins estimate.blocked's code and reason with sep, or returns
// whichever half is there.
func blockedText(rec output.Record, sep string) string {
	inner, _ := rec["estimate"].(map[string]any)
	blocked, _ := inner["blocked"].(map[string]any)
	code, _ := blocked["code"].(string)
	reason, _ := blocked["reason"].(string)
	if code != "" && reason != "" {
		return code + sep + reason
	}
	return code + reason
}

// estimateView lifts the figures out of the nested estimate object, so uniques
// and visits read as rows of their own rather than one {...} cell, and a
// blocked reason reads "CODE: reason". The rest of what is nested - the
// normalized payload, query_metrics, the totals row - stays summarised, in
// full under --json. A field of the estimate never shadows one of the record.
//
// normalized_payload is put back after flatten: it has a name of its own, and
// the {id, name} collapse would print the whole payload as that name.
func estimateView(rec output.Record) output.Record {
	view := make(output.Record, len(rec))
	for k, v := range rec {
		if k != "estimate" {
			view[k] = v
		}
	}
	inner, _ := rec["estimate"].(map[string]any)
	for k, v := range inner {
		if _, taken := view[k]; !taken {
			view[k] = v
		}
	}
	if text := blockedText(rec, ": "); text != "" {
		view["blocked"] = text
	}
	view = flatten(view)
	if payload, ok := rec["normalized_payload"].(map[string]any); ok {
		view["normalized_payload"] = payload
	}
	return view
}

// --------------------------------------------------------------------------------- commands

func audiencesEstimateCommand() *cobra.Command {
	estimate := &cobra.Command{
		Use:   "estimate",
		Short: "Size an audience before creating it",
		Long: `Size an audience before creating it.

An estimate runs the build an audience create would run, from the same
payload, and reports how many devices the audience would hold - without
creating it. Nothing appears in Audience Manager, and no export, cohort,
schedule or activation follows.

It runs as long as a real build and scans the same data, so it counts toward
the monthly data-scan limit ('intuizi usage') and the organization's build
budget, as a create does.

    intuizi audiences estimate create --type poi --brand starbucks ... --wait
    intuizi audiences estimate show 12`,
	}
	estimate.AddCommand(estimateCreateCommand(), estimateShowCommand())
	return estimate
}

func estimateCreateCommand() *cobra.Command {
	var req audienceRequest

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Estimate an audience's size, from flags or a payload file",
		Long: `Estimate an audience's size without creating it.

Takes exactly what 'intuizi audiences create' takes - the same flags, names
resolved the same way, or the same --file body - and sends it to Estimate
Audience Size instead of Create Audience:

    intuizi audiences estimate create \
      --type POI --brand starbucks \
      --country USA --state CA --city "San Francisco" \
      --start-date 2026-09-02 --end-date 2026-09-09 \
      --name "Starbucks visitors - SF - 1 week" --wait

    intuizi audiences estimate create --file audience.json --wait

'intuizi audiences create --help' describes every flag. --dry-run prints the
body and sends nothing, and swapping 'estimate create' for 'create' in the
same command line builds that audience.

--frequency is accepted, and checked as on create - it needs the same
permissions - but an estimate never runs an analysis, so it changes neither
the figures nor recipe_hash. It is there so the command line that builds the
audience can be estimated unchanged.

--filter is part of the recipe: the filters change both the figures and
recipe_hash, so estimate with the ones the audience will be built with.

An estimate runs as long as a real build and comes back pending, with no
figures. Add --wait to block until it reads completed, blocked or failed,
with --timeout to bound it (default 60m). completed exits 0 with the figures
on stdout. blocked means the request cannot be answered as sent - most often
dates outside the dataset's data coverage - and failed that the run broke;
both are final and exit non-zero with the reason. A wait that times out or
gives up leaves the estimate running and names the
'intuizi audiences estimate show <id> --wait' that resumes it.

recipe_hash is computed from the operator and datasets only, not the name.
An audience created from the same body carries the same recipe_hash
('intuizi audiences show <id> --json'), which proves it is the audience that
was estimated.

The request carries an Idempotency-Key, and a retry after a 429 reuses it.
Running the command again sends a fresh key and starts a second estimate. When a create gets no response at all,
stderr prints the key it used: rerun with --idempotency-key <key> to retry it
without risking a duplicate.`,
		Args: cobra.NoArgs,
	}
	waitOpts := waitFlags(cmd)
	req.register(cmd)
	// The flag is shared with create, but an estimate never runs the analysis.
	cmd.Flags().Lookup("frequency").Usage = "Accepted and checked as on create, but an estimate never runs\n" +
		"the analysis: the figures and recipe_hash are the same without it"

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		return req.send(cmd, estimateLifecycle, wait, timeout,
			"estimating - run 'intuizi audiences estimate show <id> --wait' to follow it")
	}
	return cmd
}

func estimateShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one size estimate",
		Long: `Show one size estimate.

Takes the id 'estimate create' returned. The figures - uniques, visits, and
signals and unique_eips where the dataset types produce them - lead the
view once the estimate reads completed; until then they are absent. A
blocked estimate shows its code and reason.

With --wait, keep polling until the estimate reads completed, blocked or
failed, printing each status change to stderr and the last record read to
stdout. completed exits 0. blocked and failed are final and exit non-zero,
and so does --timeout running out or three reads in a row failing:

    intuizi audiences estimate show 12 --wait`,
		Args: cobra.ExactArgs(1),
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "estimate")
		if err != nil {
			return err
		}
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		if !wait {
			return renderOneAs(cmd, estimatePrefix+"/"+strconv.Itoa(id), estimateLead, estimateView)
		}
		c, err := client()
		if err != nil {
			return err
		}
		return waitAndPrint(cmd, c, estimateLifecycle, id, timeout)
	}
	return cmd
}
