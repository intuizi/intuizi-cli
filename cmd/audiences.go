package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// audiencesPrefix is the path every audience call hangs off.
const audiencesPrefix = "/analyses/audiences"

// audienceColumns lead both the list table and the show view. The index returns
// thirteen fields per audience; --json prints all of them.
var audienceColumns = []string{"id", "name", "status", "results_count", "created_at"}

var audiencesCmd = &cobra.Command{
	Use:     "audiences",
	Aliases: []string{"audience"},
	Short:   "Manage audiences",
	Long: `Manage audiences.

An audience defines a group of devices drawn from one or two datasets - places
visited, apps used, CTV viewing, web activity. Delivering one somewhere is a
separate step: see 'intuizi activations'.

Building is asynchronous. 'audiences create' returns as soon as the audience is
queued, in an Initiating state; add --wait to 'create' or 'show' to block until
it reads Completed. An audience that is still building is not a failure.

The ids and codes a payload is built from come from 'intuizi reference'.`,
}

// --------------------------------------------------------------------------------- create

func audiencesCreateCommand() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an audience from a payload file",
		Long: `Create an audience from a payload file.

The body is deeply nested - one or two dataset blocks, an optional per-dataset
refine block, and optional crossvisitation and crosspurchase blocks - so it is
taken from a file rather than modelled as flags:

    intuizi audiences create --file examples/audience-poi.json
    jq '.name = "Q3 rerun"' base.json | intuizi audiences create --file -

The file is passed through untouched, so a field this CLI has never heard of
still reaches the API. See examples/ for a minimal POI audience and one using
refine and crosspurchase.

Creation is asynchronous: the new audience comes back Initiating with a
results_count of 0. That is expected. Add --wait to block until the build
reaches Completed, with --timeout to bound it (default 60m); the final record
is printed and an audience that fails to build exits non-zero:

    intuizi audiences create --file audience.json --wait

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate.`,
		Args: cobra.NoArgs,
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		if !wait {
			return createFromFile(cmd, audiencesPrefix+"/create", file, audienceColumns,
				"still building - run 'intuizi audiences show <id> --wait' to follow it")
		}
		payload, err := readPayload(cmd, file)
		if err != nil {
			return err
		}
		return createAndWait(cmd, audiencesPrefix, "audience", payload, audienceColumns, timeout)
	}

	cmd.Flags().StringVar(&file, "file", "",
		`Path to the audience payload, or "-" to read it from stdin`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// --------------------------------------------------------------------------------- show

// audiencesShowCommand is the generic showCommand plus --wait, so a build can be
// followed to Completed before it is activated. 108 Modeling is waited through.
func audiencesShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one audience",
		Long: `Show one audience.

With --wait, keep polling until the build reaches Completed or fails, printing
each status change to stderr. A lookalike in 108 Modeling is still training and
is waited through:

    intuizi audiences show 1377 --wait`,
		Args: cobra.ExactArgs(1),
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "audience")
		if err != nil {
			return err
		}
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		if !wait {
			return renderOne(cmd, audiencesPrefix+"/"+strconv.Itoa(id), audienceColumns)
		}
		c, err := client()
		if err != nil {
			return err
		}
		return waitAndPrint(cmd, c, audiencesPrefix, "audience", id, audienceColumns, timeout)
	}
	return cmd
}

// --------------------------------------------------------------------------------- lookalikes

func audiencesLookalikeCommand() *cobra.Command {
	lookalike := &cobra.Command{
		Use:     "lookalike",
		Aliases: []string{"lookalikes"},
		Short:   "Build and cancel Lookalike Model audiences",
		Long: `Build and cancel Lookalike Model audiences.

A Lookalike Model trains on a completed seed audience and produces a new
audience of similar devices. It is a gated feature: without the permission the
create is rejected with 403.

Training shows as status 108 Modeling, which is not terminal - keep polling
'audiences show <id>' until it reads Completed.`,
	}

	var file string
	create := &cobra.Command{
		Use:   "create",
		Short: "Train a lookalike audience from a payload file",
		Long: `Train a lookalike audience from a payload file.

The body carries the seed audience id and a config block naming the signals to
learn from, so it is taken from a file:

    intuizi audiences lookalike create --file lookalike.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return createFromFile(cmd, audiencesPrefix+"/create-lookalike", file, audienceColumns,
				"training - status 108 Modeling is not terminal, keep polling")
		},
	}
	create.Flags().StringVar(&file, "file", "",
		`Path to the lookalike payload, or "-" to read it from stdin`)
	_ = create.MarkFlagRequired("file")

	cancel := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a lookalike run in progress",
		Long: `Cancel a lookalike run in progress.

The job stops at its next checkpoint rather than immediately, so the audience
may sit in its current status for a short while after this returns.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "audience")
			if err != nil {
				return err
			}
			return postID(cmd, audiencesPrefix+"/cancel-lookalike", id,
				fmt.Sprintf("cancellation requested for audience %d", id))
		},
	}

	lookalike.AddCommand(create, cancel)
	return lookalike
}

func audiencesListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List audiences",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the audience name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, audiencesPrefix+"/index", query(), audienceColumns, "no audiences")
	}
	return cmd
}

func init() {
	audiencesCmd.AddCommand(
		audiencesListCommand(),
		audiencesShowCommand(),
		deleteCommand("audience", audiencesPrefix),
		audiencesCreateCommand(), audiencesLookalikeCommand(),
	)
	rootCmd.AddCommand(audiencesCmd)
}
