package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
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
	"name", "file-uri", "upload-reference", "audience-id", "file-format",
	"identifier-type", "identifier-column", "metadata-columns",
	"ip-enrichment", "device-limit", "project-id",
}

// cohortRequired covers both file sources. The source itself is checked
// separately: exactly one of three.
var cohortRequired = []string{
	"name", "file-format", "identifier-type", "identifier-column",
}

// cohortAudienceOnly describe a file import. The API ignores them on an
// audience source rather than rejecting them, so name the mistake here.
var cohortAudienceOnly = []string{
	"name", "file-format", "identifier-type", "identifier-column",
	"metadata-columns", "ip-enrichment",
}

// The server's accepted sets, checked here so a typo is named with the values
// it could have been instead of costing a round trip on a 422. Parquet cannot
// be previewed, so the preview set is shorter.
var (
	cohortFileFormats  = []string{"csv", "gzip", "parquet"}
	previewFileFormats = []string{"csv", "gzip"}
	identifierTypes    = []string{
		"eid", "eid_md5", "maid", "ip", "hem_plaintext", "scid",
		"hem_md5", "hem_sha1", "hem_sha256",
	}
)

// oneOf rejects a value outside its set, listing the set.
func oneOf(flag, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return usageErr("unknown --" + flag + " " + value + "; one of " + strings.Join(allowed, ", "))
}

func cohortsCreateCommand() *cobra.Command {
	var (
		file        string
		dryRun      bool
		name        string
		fileURI     string
		uploadRef   string
		audienceID  int
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

The other two sources are flat too. From a file you uploaded, swap --file-uri
for the reference 'intuizi uploads reserve' handed back:

    intuizi cohorts create --name "Q3 customers" \
      --upload-reference upl_abc123 --file-format csv \
      --identifier-type hem_sha256 --identifier-column email_sha256

From a Completed audience, --audience-id is the only flag needed; the cohort
takes the audience's own name, so --name is rejected:

    intuizi cohorts create --audience-id 88 --device-limit 1000

An audience makes at most one live cohort. Capping by visit frequency or by
distance instead of a device count needs freq_limit or distance_limit and their
bounds, so those go through the whole body:

    intuizi cohorts create --file cohort.json

--dry-run prints the body the flags produce and sends nothing.

Check the column mapping before importing with 'intuizi cohorts preview'.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate import.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			next := "importing - run 'intuizi cohorts show <id>' for its status"

			if file != "" {
				// Mixing the two would beg the question of which wins.
				if err := rejectBodyFlags(flags, cohortFields); err != nil {
					return err
				}
				if dryRun {
					return usageErr("--dry-run builds a body from flags; --file already has one")
				}
				return createFromFile(cmd, cohortsPrefix+"/create", file, cohortColumns, next)
			}

			// Two sources would leave the API to decide silently.
			sources := 0
			for _, f := range []string{"file-uri", "upload-reference", "audience-id"} {
				if flags.Changed(f) {
					sources++
				}
			}
			if sources != 1 {
				return usageErr("give exactly one of --file-uri, --upload-reference or " +
					"--audience-id - or pass the whole body with --file")
			}

			body := map[string]any{}

			switch {
			case flags.Changed("audience-id"):
				// The cohort takes the audience's name.
				for _, f := range cohortAudienceOnly {
					if flags.Changed(f) {
						why := " describes a file import"
						if f == "name" {
							why = " is ignored: the cohort takes the audience's name"
						}
						return usageErr("--" + f + why +
							"; drop it when the source is --audience-id")
					}
				}
				if err := positiveID(flags, "audience-id", audienceID); err != nil {
					return err
				}
				body["source"] = "audience"
				body["audience_id"] = audienceID

			default:
				if err := missingFlags(flags, cohortRequired, "a cohort from a file"); err != nil {
					return err
				}
				if err := oneOf("file-format", fileFormat, cohortFileFormats); err != nil {
					return err
				}
				if err := oneOf("identifier-type", idType, identifierTypes); err != nil {
					return err
				}
				body["name"] = name
				body["file_format"] = fileFormat
				body["identifier_type"] = idType
				body["identifier_column"] = idColumn
				if flags.Changed("upload-reference") {
					body["upload_reference"] = uploadRef
				} else {
					if err := validateFileURI(fileURI); err != nil {
						return err
					}
					body["file_uri"] = fileURI
				}
				if len(metadata) > 0 {
					body["metadata_columns"] = metadata
				}
			}
			// An optional left unset is left out of the body: sending false or
			// 0 is not the same as saying nothing about the field.
			if flags.Changed("ip-enrichment") {
				body["ip_enrichment"] = ipEnrich
			}
			if flags.Changed("device-limit") {
				if deviceLimit < 1 {
					return usageErr("--device-limit must be at least 1, not " + strconv.Itoa(deviceLimit))
				}
				body["device_limit"] = deviceLimit
			}
			if err := positiveID(flags, "project-id", projectID); err != nil {
				return err
			}
			if flags.Changed("project-id") {
				body["project_id"] = projectID
			}

			if dryRun {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(body)
			}
			return createBody(cmd, cohortsPrefix+"/create", body, cohortColumns, next)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&file, "file", "",
		`Path to the cohort payload, or "-" to read it from stdin`)
	flags.StringVar(&name, "name", "", "The cohort name")
	flags.BoolVar(&dryRun, "dry-run", false,
		"Print the body the flags produce and send nothing")
	flags.StringVar(&fileURI, "file-uri", "",
		"The file to import: s3://bucket/path or gs://bucket/path")
	flags.StringVar(&uploadRef, "upload-reference", "",
		"An upload_reference from 'intuizi uploads reserve', instead of --file-uri")
	flags.IntVar(&audienceID, "audience-id", 0,
		"Build from this Completed audience instead of a file")
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
// server's rule is ^(s3|gs)://[^/\s]+/\S+$, so whitespace anywhere is
// refused. The suffix is deliberately not checked: .csv, .gz and .parquet name
// a single file, anything else names a folder, so an extensionless path is
// legal.
func validateFileURI(uri string) error {
	const shape = "s3://bucket/path or gs://bucket/path"

	var rest string
	switch {
	case strings.HasPrefix(uri, "s3://"):
		rest = strings.TrimPrefix(uri, "s3://")
	case strings.HasPrefix(uri, "gs://"):
		rest = strings.TrimPrefix(uri, "gs://")
	default:
		return usageErr(fmt.Sprintf("--file-uri must look like %s (got %q)", shape, uri))
	}

	bucket, path, found := strings.Cut(rest, "/")
	if !found || strings.TrimSpace(bucket) == "" ||
		strings.TrimSpace(strings.Trim(path, "/")) == "" {
		return usageErr(fmt.Sprintf("--file-uri must name a bucket and a path under it, "+
			"like %s (got %q)", shape, uri))
	}
	if strings.ContainsFunc(rest, unicode.IsSpace) {
		return usageErr(fmt.Sprintf("--file-uri must not contain whitespace (got %q)", uri))
	}
	return nil
}

// --------------------------------------------------------------------------------- preview

func cohortsPreviewCommand() *cobra.Command {
	var (
		file       string
		fileURI    string
		uploadRef  string
		fileFormat string
	)

	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Preview the columns in a staged cohort file",
		Long: `Preview the columns in a staged cohort file.

Reads the first rows of a file you have staged and reports the columns it
found, so you can pick identifier_column with confidence. The body is two or
three scalars, so it can be built from flags:

    intuizi cohorts preview --file-uri s3://example-bucket/cohorts/q3.csv
    intuizi cohorts preview --upload-reference upl_abc123 --file-format csv

Give exactly one of --file-uri or --upload-reference. --file-format is
optional; the file is sniffed when it is left off.

Parquet files cannot be previewed - only csv and gzip.

The whole body still works if you prefer:

    intuizi cohorts preview --file preview.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			// A preview is sample rows, not a resource, so there is no id to print.
			if quietOutput {
				return usageErr("cohorts preview returns sample rows, not an id - --quiet does not apply")
			}

			previewFields := []string{"file-uri", "upload-reference", "file-format"}
			if file != "" {
				if err := rejectBodyFlags(flags, previewFields); err != nil {
					return err
				}
				payload, err := readPayload(cmd, file)
				if err != nil {
					return err
				}
				return previewCohort(cmd, payload)
			}

			sources := 0
			for _, f := range []string{"file-uri", "upload-reference"} {
				if flags.Changed(f) {
					sources++
				}
			}
			if sources != 1 {
				return usageErr("give exactly one of --file-uri or --upload-reference " +
					"- or pass the whole body with --file")
			}

			body := map[string]any{}
			if flags.Changed("upload-reference") {
				body["upload_reference"] = uploadRef
			} else {
				if err := validateFileURI(fileURI); err != nil {
					return err
				}
				body["file_uri"] = fileURI
			}
			if flags.Changed("file-format") {
				if err := oneOf("file-format", fileFormat, previewFileFormats); err != nil {
					return err
				}
				body["file_format"] = fileFormat
			}

			return previewCohort(cmd, body)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&file, "file", "",
		`Path to the preview request, or "-" to read it from stdin`)
	flags.StringVar(&fileURI, "file-uri", "",
		"The file to preview: s3://bucket/path or gs://bucket/path")
	flags.StringVar(&uploadRef, "upload-reference", "",
		"An upload_reference from 'intuizi uploads reserve', instead of --file-uri")
	flags.StringVar(&fileFormat, "file-format", "",
		"How the file is encoded: csv or gzip (parquet cannot be previewed)")
	return cmd
}

// previewCohort posts the preview request and renders the sample rows. Not
// createBody: a preview is not a resource, and the generic detail view would
// show its columns and samples as "[3 items]".
func previewCohort(cmd *cobra.Command, body any) error {
	c, err := client()
	if err != nil {
		return err
	}
	path := cohortsPrefix + "/preview"

	if jsonOutput {
		raw, err := c.PostRaw(cmd.Context(), path, body)
		if err != nil {
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	rec, err := api.Create[output.Record](cmd.Context(), c, path, body)
	if err != nil {
		return err
	}
	return renderPreview(cmd, rec)
}

// renderPreview prints the column names as a header with each sample row
// under it, so --identifier-column can be read straight off the table. The
// row count is commentary, so it goes to stderr.
func renderPreview(cmd *cobra.Command, rec output.Record) error {
	out := cmd.OutOrStdout()

	cols, _ := rec["columns"].([]any)
	if len(cols) == 0 {
		// Not the preview shape; show what came back rather than nothing.
		return output.Detail(out, flatten(rec), nil)
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = fmt.Sprint(c)
	}

	samples, _ := rec["samples"].([]any)
	rows := make([]output.Record, 0, len(samples))
	for _, s := range samples {
		cells, _ := s.([]any)
		row := make(output.Record, len(names))
		for i, n := range names {
			if i < len(cells) {
				row[n] = cells[i]
			}
		}
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		// The table writer prints nothing for no rows, but the header is the
		// point of a preview.
		if _, err := fmt.Fprintln(out, strings.Join(names, "  ")); err != nil {
			return err
		}
	} else if err := output.TableWith(out, rows, names); err != nil {
		return err
	}

	count := strconv.Itoa(len(rows))
	if n, ok := rec["sample_rows"].(json.Number); ok {
		count = n.String()
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s sample rows\n", count)
	return nil
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
