package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const cohortsPrefix = "/analyses/cohorts"

var cohortColumns = []string{"id", "name", "status", "total_eids", "created_at"}

var cohortsCmd = &cobra.Command{
	Use:   "cohorts",
	Short: "Manage cohorts",
	Long: `Manage cohorts.

A cohort is your own list of identifiers - emails, MAIDs, IPs - imported from a
cloud file or an upload, or built from a completed audience. Once imported it
becomes a dataset an audience can be built from.

Cohort status ids are their own scale, not the audience one: 1 Uploading,
2 Initiating, 3 Processing, 4 Completed, 5 Not Available.`,
}

// --------------------------------------------------------------------------------- create

// cohortFields are the flat create flags - the whole body for a cohort imported
// from a cloud file.
var cohortFields = []string{
	"name", "file-uri", "file-format", "identifier-type", "identifier-column",
	"metadata-columns", "ip-enrichment", "device-limit", "project-id",
}

// cohortRequired are the fields a cloud-file import cannot be built without.
var cohortRequired = []string{
	"name", "file-uri", "file-format", "identifier-type", "identifier-column",
}

func cohortsCreateCommand() *cobra.Command {
	var (
		file        string
		name        string
		fileURI     string
		fileFormat  string
		idType      string
		idColumn    string
		metadata    []string
		ipEnrich    bool
		deviceLimit int
		projectID   int
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cohort, from flags or a payload file",
		Long: `Create a cohort.

A cohort imported from your own cloud file has a flat body, so it can be built
from flags:

    intuizi cohorts create --name "Q3 customers" \
      --file-uri s3://example-bucket/cohorts/q3.csv \
      --file-format csv \
      --identifier-type hem_sha256 \
      --identifier-column email_sha256

A file_uri ending .csv, .gz or .parquet is read as a single file; anything else
is read as a folder.

The other two sources are nested, so pass the whole body instead:

  from an upload - upload_reference from 'intuizi uploads reserve', instead of
  file_uri
  from an audience - source "audience" plus audience_id

    intuizi cohorts create --file cohort.json

Check the column mapping before importing with 'intuizi cohorts preview'.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate import.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			if file != "" {
				// Mixing the two would beg the question of which wins.
				for _, f := range cohortFields {
					if flags.Changed(f) {
						return usageErr("--file carries the whole body; drop --" + f)
					}
				}
				return createFromFile(cmd, cohortsPrefix+"/create", file, cohortColumns,
					"importing - run 'intuizi cohorts show <id>' for its status")
			}

			var missing []string
			for _, f := range cohortRequired {
				if !flags.Changed(f) {
					missing = append(missing, "--"+f)
				}
			}
			if len(missing) > 0 {
				return usageErr("a cohort from a cloud file needs " +
					strings.Join(missing, ", ") + " - or pass the whole body with --file")
			}
			if err := validateFileURI(fileURI); err != nil {
				return err
			}

			body := map[string]any{
				"name":              name,
				"file_uri":          fileURI,
				"file_format":       fileFormat,
				"identifier_type":   idType,
				"identifier_column": idColumn,
			}
			if len(metadata) > 0 {
				body["metadata_columns"] = metadata
			}
			// An optional left unset is left out of the body: sending false or
			// 0 is not the same as saying nothing about the field.
			if flags.Changed("ip-enrichment") {
				body["ip_enrichment"] = ipEnrich
			}
			if flags.Changed("device-limit") {
				body["device_limit"] = deviceLimit
			}
			if flags.Changed("project-id") {
				body["project_id"] = projectID
			}

			return createBody(cmd, cohortsPrefix+"/create", body, cohortColumns,
				"importing - run 'intuizi cohorts show <id>' for its status")
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&file, "file", "",
		`Path to the cohort payload, or "-" to read it from stdin`)
	flags.StringVar(&name, "name", "", "The cohort name")
	flags.StringVar(&fileURI, "file-uri", "",
		"The file to import: s3://bucket/path or gs://bucket/path")
	flags.StringVar(&fileFormat, "file-format", "",
		"How the file is encoded: csv, gzip or parquet")
	flags.StringVar(&idType, "identifier-type", "",
		"What the identifier column holds: eid, eid_md5, maid, ip, hem_plaintext, scid, hem_md5, hem_sha1 or hem_sha256")
	flags.StringVar(&idColumn, "identifier-column", "",
		"The column the identifiers are in")
	flags.StringArrayVar(&metadata, "metadata-columns", nil,
		"A column to keep alongside the identifier (repeat the flag for more than one)")
	flags.BoolVar(&ipEnrich, "ip-enrichment", false,
		"Resolve IP addresses to devices")
	flags.IntVar(&deviceLimit, "device-limit", 0,
		"Cap the devices imported")
	flags.IntVar(&projectID, "project-id", 0,
		"The project to create the cohort in")

	return cmd
}

// validateFileURI checks a cohort source URI before it costs a round trip. The
// suffix is deliberately not checked: .csv, .gz and .parquet name a single
// file, anything else names a folder, so an extensionless path is legal.
func validateFileURI(uri string) error {
	const shape = "s3://bucket/path or gs://bucket/path"

	var rest string
	switch {
	case strings.HasPrefix(uri, "s3://"):
		rest = strings.TrimPrefix(uri, "s3://")
	case strings.HasPrefix(uri, "gs://"):
		rest = strings.TrimPrefix(uri, "gs://")
	default:
		return fmt.Errorf("--file-uri must look like %s (got %q)", shape, uri)
	}

	bucket, path, found := strings.Cut(rest, "/")
	if !found || strings.TrimSpace(bucket) == "" ||
		strings.TrimSpace(strings.Trim(path, "/")) == "" {
		return fmt.Errorf("--file-uri must name a bucket and a path under it, "+
			"like %s (got %q)", shape, uri)
	}
	return nil
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
