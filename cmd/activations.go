package cmd

import (
	"encoding/json"
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
status change is printed to stderr, the final state to stdout, and the exit
code is non-zero if it fails, if any datastream fails to deliver, or --timeout
(default 60m) runs out.

An audience must hold at least 500 devices before it can be activated.`,
}

// --------------------------------------------------------------------------------- create

// activationFields are the body-building flags, for --file to reject.
var activationFields = []string{
	"audience-id", "endpoint-connection-id", "pricing-model-id", "description", "project-id",
}

func activationsCreateCommand() *cobra.Command {
	var (
		file         string
		dryRun       bool
		audienceID   int
		connectionID int
		pricingModel int
		description  string
		projectID    int
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an activation, from flags or a payload file",
		Long: `Create an activation.

A minimal activation is three ids, so it can be built from flags:

    intuizi activations create --audience-id 88 \
      --endpoint-connection-id 12 --pricing-model-id 3

Anything richer - per-partner audience_inputs, datastreams, caller-supplied
credentials - is too nested for flags, so pass the whole body instead:

    intuizi activations create --file activation.json

Add --wait to follow the export to its end, with --timeout to bound it:

    intuizi activations create --file activation.json --wait --timeout 90m

An activation costs money, so --dry-run prints the body the flags produce and
sends nothing; it needs no token.

Collect the ids first: 'intuizi reference common endpoint-connections' and
'intuizi reference common pricing-models --partner-id <id>'.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate export.`,
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
			m := map[string]any{
				"audience_id":            audienceID,
				"endpoint_connection_id": connectionID,
				"pricing_model_id":       pricingModel,
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
	flags.StringVar(&description, "description", "", "A label for the activation")
	flags.IntVar(&projectID, "project-id", 0, "Project to file the activation under")

	return cmd
}

// --------------------------------------------------------------------------------- show

// activationsShowCommand is the generic showCommand plus --wait. It is its own
// command rather than a flag on the shared helper because the terminal-state
// table is per resource: cohorts finish at 4, audiences pass through 108.
func activationsShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one activation",
		Long: `Show one activation.

With --wait, keep polling until it reaches Completed or fails, printing each
status change to stderr. Use it to resume following an export whose create
timed out, or as a gate in CI - a Completed activation returns at once:

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
