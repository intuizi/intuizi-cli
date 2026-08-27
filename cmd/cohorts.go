package cmd

import (
	"github.com/spf13/cobra"
)

const cohortsPrefix = "/analyses/cohorts"

var cohortColumns = []string{"id", "name", "status", "total_eids", "created_at"}

var cohortsCmd = &cobra.Command{
	Use:     "cohorts",
	Aliases: []string{"cohort"},
	Short:   "Manage cohorts",
	Long: `Manage cohorts.

A cohort is your own list of identifiers - emails, MAIDs, IPs - imported from a
cloud file or an upload, or built from a completed audience. Once imported it
becomes a dataset an audience can be built from.

Cohort status ids are their own scale, not the audience one: 1 Uploading,
2 Initiating, 3 Processing, 4 Completed, 5 Not Available.`,
}

// --------------------------------------------------------------------------------- create

func cohortsCreateCommand() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cohort from a payload file",
		Long: `Create a cohort from a payload file.

Two sources, both described by the same payload:

  from your own cloud file - file_uri (s3:// or gs://), file_format,
  identifier_type and identifier_column
  from an upload - upload_reference from 'intuizi uploads reserve', instead of
  file_uri
  from an audience - source "audience" plus audience_id

    intuizi cohorts create --file cohort.json

Check the column mapping before importing with 'intuizi cohorts preview'.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate import.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return createFromFile(cmd, cohortsPrefix+"/create", file, cohortColumns,
				"importing - run 'intuizi cohorts show <id>' for its status")
		},
	}

	cmd.Flags().StringVar(&file, "file", "",
		`Path to the cohort payload, or "-" to read it from stdin`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// --------------------------------------------------------------------------------- preview

func cohortsPreviewCommand() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Preview a cohort file before importing it",
		Long: `Preview a cohort file before importing it.

Reads the first rows of a file you have staged and reports the columns it found,
so you can pick identifier_column with confidence. The body names the file:
file_uri or upload_reference, plus an optional file_format.

    intuizi cohorts preview --file preview.json

Parquet files cannot be previewed - only csv and gzip.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return createFromFile(cmd, cohortsPrefix+"/preview", file, nil, "")
		},
	}

	cmd.Flags().StringVar(&file, "file", "",
		`Path to the preview request, or "-" to read it from stdin`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func cohortsListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cohorts",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the cohort name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, cohortsPrefix+"/index", query(), cohortColumns, "no cohorts")
	}
	return cmd
}

func init() {
	cohortsCmd.AddCommand(
		cohortsListCommand(),
		showCommand("cohort", cohortsPrefix, cohortColumns),
		deleteCommand("cohort", cohortsPrefix),
		cohortsCreateCommand(), cohortsPreviewCommand(),
	)
	rootCmd.AddCommand(cohortsCmd)
}
