package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

const activationsPrefix = "/analyses/activations"

var activationColumns = []string{"id", "description", "status", "audience", "created_at"}

var activationsCmd = &cobra.Command{
	Use:     "activations",
	Aliases: []string{"activation"},
	Short:   "Manage activations",
	Long: `Manage activations.

An activation exports a completed audience to a destination - one of your
endpoint connections. The audience says who; the activation says where.

Delivery is asynchronous and has an extra step: status 105 DataStreaming comes
before 104 Completed, so results are still moving at 105. Once Completed, the
delivered files are listed under datastreams[].results.uri, which --json shows.

An audience must hold at least 500 devices before it can be activated.`,
}

// --------------------------------------------------------------------------------- create

func activationsCreateCommand() *cobra.Command {
	var (
		file         string
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

Collect the ids first: 'intuizi reference common endpoint-connections' and
'intuizi reference common pricing-models --partner-id <id>'.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate export.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			if file != "" {
				// Mixing the two would beg the question of which wins.
				for _, f := range []string{"audience-id", "endpoint-connection-id",
					"pricing-model-id", "description", "project-id"} {
					if flags.Changed(f) {
						return errors.New("--file carries the whole body; drop --" + f)
					}
				}
				return createFromFile(cmd, activationsPrefix+"/create", file, activationColumns,
					"delivering - run 'intuizi activations show <id>' for its status")
			}

			if !flags.Changed("audience-id") || !flags.Changed("endpoint-connection-id") ||
				!flags.Changed("pricing-model-id") {
				return errors.New("pass --file, or all of --audience-id, " +
					"--endpoint-connection-id and --pricing-model-id")
			}

			body := map[string]any{
				"audience_id":            audienceID,
				"endpoint_connection_id": connectionID,
				"pricing_model_id":       pricingModel,
			}
			if flags.Changed("description") {
				body["description"] = description
			}
			if flags.Changed("project-id") {
				body["project_id"] = projectID
			}

			return createBody(cmd, activationsPrefix+"/create", body, activationColumns,
				"delivering - run 'intuizi activations show <id>' for its status")
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&file, "file", "",
		`Path to a full activation payload, or "-" to read it from stdin`)
	flags.IntVar(&audienceID, "audience-id", 0, "The completed audience to export")
	flags.IntVar(&connectionID, "endpoint-connection-id", 0, "The destination endpoint connection")
	flags.IntVar(&pricingModel, "pricing-model-id", 0, "The pricing model for this export")
	flags.StringVar(&description, "description", "", "A label for the activation")
	flags.IntVar(&projectID, "project-id", 0, "Project to file the activation under")

	return cmd
}

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
		showCommand("activation", activationsPrefix, activationColumns),
		deleteCommand("activation", activationsPrefix),
		activationsCreateCommand(),
	)
	rootCmd.AddCommand(activationsCmd)
}
