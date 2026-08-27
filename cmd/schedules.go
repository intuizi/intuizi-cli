package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const schedulesPrefix = "/analyses/schedules"

var scheduleColumns = []string{"id", "name", "status"}

var schedulesCmd = &cobra.Command{
	Use:     "schedules",
	Aliases: []string{"schedule"},
	Short:   "Manage schedules",
	Long: `Manage schedules.

A schedule rebuilds a completed audience on a recurring data window, and can
re-export it every cycle. The audience definition is snapshotted when the
schedule is created, so later edits to the original do not change it.

Each cycle produces its own audience, named "<name> - #<cycle>".

The cadence values a schedule accepts come from 'intuizi reference':
common schedule-frequencies, schedule-windows and schedule-endings.`,
}

// --------------------------------------------------------------------------------- create

func schedulesCreateCommand() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a schedule from a payload file",
		Long: `Create a schedule from a payload file.

The body nests a recurrence block (start, timezone, frequency, window and
ending) and an optional activation block for auto-export, so it is taken from a
file:

    intuizi schedules create --file schedule.json

recurrence.start must be in the future, and is read in recurrence.timezone.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate schedule.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return createFromFile(cmd, schedulesPrefix+"/create", file, scheduleColumns, "")
		},
	}

	cmd.Flags().StringVar(&file, "file", "",
		`Path to the schedule payload, or "-" to read it from stdin`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// --------------------------------------------------------------------------------- activate / deactivate

// toggleCommand builds activate and deactivate, which differ only in their verb.
func toggleCommand(verb, path, done, long string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <id>",
		Short: fmt.Sprintf("%s a schedule", cases(verb)),
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "schedule")
			if err != nil {
				return err
			}
			return postID(cmd, schedulesPrefix+"/"+path, id,
				fmt.Sprintf("%s schedule %d", done, id))
		},
	}
}

// cases upper-cases the first letter, for a Short that must start capitalised.
func cases(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func schedulesListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List schedules",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the schedule name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, schedulesPrefix+"/index", query(), scheduleColumns, "no schedules")
	}
	return cmd
}

func init() {
	schedulesCmd.AddCommand(
		schedulesListCommand(),
		showCommand("schedule", schedulesPrefix, scheduleColumns),
		deleteCommand("schedule", schedulesPrefix),

		schedulesCreateCommand(),
		toggleCommand("activate", "activate", "activated",
			`Activate a schedule.

Resumes the recurrence. The next cycle runs at the next scheduled time; a
missed window while it was paused is not backfilled.`),
		toggleCommand("deactivate", "deactivate", "deactivated",
			`Deactivate a schedule.

Pauses the recurrence without deleting it. Audiences already built by earlier
cycles are untouched.`),
	)
	rootCmd.AddCommand(schedulesCmd)
}
