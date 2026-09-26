package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"
)

const activationsPrefix = "/analyses/activations"

var activationColumns = []string{"id", "description", "status", "audience", "created_at"}

var activationsCmd = &cobra.Command{
	Use:   "activations",
	Short: "Manage activations",
	Long: `Manage activations.

An activation exports a completed audience to a destination - one of your
endpoint connections. The audience says who; the activation says where.

Delivery is asynchronous and has an extra step: status 105 DataStreaming comes
before 104 Completed, so results are still moving at 105. Once Completed, the
delivered files are listed under datastreams[].results.uri, which --json shows.

Pass --wait to 'create' or 'show' to follow an activation to its end: each
status change is printed to stderr, the record the wait ended on to stdout,
and the exit code is non-zero if it fails, if any datastream fails to deliver,
or --timeout (default 60m) runs out. A wait that fails, times out, or gives up
after three failed reads in a row still prints the last record it read, then
exits non-zero. A Completed activation with no datastreams exits 0, and stderr
says that nothing was delivered. 107 Additional Info is a failure too: the
export stopped and will not continue. The API does not return the reason, but
Audience Manager in the Intuizi console shows it on the activation.

An audience must hold at least 500 devices, 1,000 for a Lookalike Model
result, and pass the other eligibility checks before it can be activated.
'intuizi audiences show <id> --json' reports is_activation_allowed, and
eligibility.reasons says why when it is false. A true can still carry
eligibility.notices, each naming in blocks_identifiers the identifiers it
blocks: a create whose pricing model delivers one of them is refused.`,
}

// --------------------------------------------------------------------------------- create

// activationFields are the body-building flags, for --file to reject.
var activationFields = []string{
	"audience-id", "endpoint-connection-id", "pricing-model-id", "datastream", "description", "project-id",
}

// datastreamSelection enables one of the connection partner's datastreams.
// The API reads a stream without a true status as not selected.
type datastreamSelection struct {
	ID     int  `json:"id"`
	Status bool `json:"status"`
}

// datastreamSelections checks and dedupes the --datastream ids, keeping order.
func datastreamSelections(ids []int) ([]datastreamSelection, error) {
	seen := make(map[int]bool, len(ids))
	out := make([]datastreamSelection, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, usageErr(fmt.Sprintf("--datastream %d is not a valid datastream id", id))
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, datastreamSelection{ID: id, Status: true})
		}
	}
	return out, nil
}

// warnNoDatastream says, in one line, that a flag-built activation without a
// datastream will deliver nothing: the worker uploads only for enabled
// streams, and the activation still reaches Completed.
func warnNoDatastream(w io.Writer, connectionID int) {
	_, _ = fmt.Fprintf(w, "warning: no --datastream, so this activation will deliver nothing - "+
		"list the partner's datastreams with 'intuizi reference common datastreams --partner-id <id>' "+
		"(the partner.id of connection %d in 'intuizi reference common endpoint-connections --json')\n",
		connectionID)
}

func activationsCreateCommand() *cobra.Command {
	var (
		file         string
		dryRun       bool
		audienceID   int
		connectionID int
		pricingModel int
		datastreams  []int
		description  string
		projectID    int
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an activation, from flags or a payload file",
		Long: `Create an activation.

From flags, an activation names the audience, the destination endpoint
connection, the pricing model and the datastreams to deliver:

    intuizi activations create --audience-id 88 \
      --endpoint-connection-id 12 --pricing-model-id 3 --datastream 7

A datastream is one of the connection partner's delivery outputs, and only an
enabled one uploads anything: without --datastream the activation completes
and delivers nothing, so the command warns on stderr. Repeat the flag for more
than one.

Anything richer - per-partner audience_inputs, per-stream inputs or
compression, caller-supplied credentials - is too nested for flags, so pass the
whole body instead:

    intuizi activations create --file activation.json

Add --wait to follow the export to its end, with --timeout to bound it:

    intuizi activations create --file activation.json --wait --timeout 90m

An activation costs money, so --dry-run prints the body the flags produce and
sends nothing; it needs no token.

Collect the ids first: 'intuizi reference common endpoint-connections', then
'intuizi reference common pricing-models --partner-id <id>' and 'intuizi
reference common datastreams --partner-id <id>', where <id> is the
connection's partner.id (shown with --json).

The request carries an Idempotency-Key, and a retry after a 429 reuses it.
Running the command again sends a fresh key and can create a second export.
When a create gets no response at all, stderr prints the key it used: rerun
with --idempotency-key <key> to retry it without risking a duplicate.`,
		Args: cobra.NoArgs,
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()

		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}

		var body any
		if file != "" {
			// Mixing the two would beg the question of which wins.
			if err := rejectBodyFlags(flags, activationFields); err != nil {
				return err
			}
			if dryRun {
				return usageErr("--dry-run builds a body from flags; --file already has one")
			}
			payload, err := readPayload(cmd, file)
			if err != nil {
				return err
			}
			body = payload
		} else {
			if dryRun && wait {
				return usageErr("--dry-run sends nothing, so there is nothing to --wait for")
			}
			if !flags.Changed("audience-id") || !flags.Changed("endpoint-connection-id") ||
				!flags.Changed("pricing-model-id") {
				return usageErr("pass --file, or all of --audience-id, " +
					"--endpoint-connection-id and --pricing-model-id")
			}
			for _, id := range []struct {
				flag  string
				value int
			}{
				{"audience-id", audienceID}, {"endpoint-connection-id", connectionID},
				{"pricing-model-id", pricingModel}, {"project-id", projectID},
			} {
				if err := positiveID(flags, id.flag, id.value); err != nil {
					return err
				}
			}
			streams, err := datastreamSelections(datastreams)
			if err != nil {
				return err
			}
			m := map[string]any{
				"audience_id":            audienceID,
				"endpoint_connection_id": connectionID,
				"pricing_model_id":       pricingModel,
			}
			if len(streams) > 0 {
				m["datastreams"] = streams
			} else {
				// Before anything is sent, so it precedes "created activation".
				warnNoDatastream(cmd.ErrOrStderr(), connectionID)
			}
			if flags.Changed("description") {
				m["description"] = description
			}
			if flags.Changed("project-id") {
				m["project_id"] = projectID
			}
			if dryRun {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(m)
			}
			body = m
		}

		if !wait {
			return createBody(cmd, activationsPrefix+"/create", body, activationColumns,
				"delivering - run 'intuizi activations show <id> --wait' to follow it")
		}

		return createAndWait(cmd, activationsPrefix, "activation", body, activationColumns, timeout)
	}

	flags := cmd.Flags()
	flags.StringVar(&file, "file", "",
		`Path to a full activation payload, or "-" to read it from stdin`)
	flags.BoolVar(&dryRun, "dry-run", false,
		"Print the body the flags produce and send nothing")
	flags.IntVar(&audienceID, "audience-id", 0, "The completed audience to export")
	flags.IntVar(&connectionID, "endpoint-connection-id", 0, "The destination endpoint connection")
	flags.IntVar(&pricingModel, "pricing-model-id", 0, "The pricing model for this export")
	flags.IntSliceVar(&datastreams, "datastream", nil,
		"Datastream id to deliver, from 'reference common datastreams'\n(repeatable or comma-separated; without one nothing is delivered)")
	flags.StringVar(&description, "description", "", "A label for the activation")
	flags.IntVar(&projectID, "project-id", 0, "Project to file the activation under")

	return cmd
}

// --------------------------------------------------------------------------------- show

// activationsShowCommand is the generic showCommand plus --wait. It is its own
// command rather than a flag on the shared helper because the terminal-state
// table is per resource: cohorts finish at 4, audiences pass through 108 and
// 109.
func activationsShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one activation",
		Long: `Show one activation.

With --wait, keep polling until it reaches Completed or fails, printing each
status change to stderr and the last record read to stdout, and exiting
non-zero if it fails, --timeout runs out or three reads in a row fail. 107
Additional Info is a failure: the export stopped, and Audience Manager in the
Intuizi console shows why on the activation. Use --wait to resume following an
export whose create timed out, or as a gate in CI - a Completed activation
returns at once:

    intuizi activations show 501 --wait --timeout 90m`,
		Args: cobra.ExactArgs(1),
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "activation")
		if err != nil {
			return err
		}
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		if !wait {
			return renderOne(cmd, activationsPrefix+"/"+strconv.Itoa(id), activationColumns)
		}
		c, err := client()
		if err != nil {
			return err
		}
		return waitAndPrint(cmd, c, activationsPrefix, "activation", id, activationColumns, timeout)
	}
	return cmd
}

// --------------------------------------------------------------------------------- list

func activationsListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List activations",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the activation description")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, activationsPrefix+"/index", query(), activationColumns, "no activations")
	}
	return cmd
}

func init() {
	activationsCmd.AddCommand(
		activationsListCommand(),
		activationsShowCommand(),
		deleteCommand("activation", activationsPrefix),
		activationsCreateCommand(),
	)
	rootCmd.AddCommand(activationsCmd)
}
