package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// Uploading is a three-step primitive shared by POI submissions and cohorts:
// reserve a slot, PUT the bytes to the presigned URL, then hand the
// upload_reference to the create that claims it.
//
// 'uploads put' runs the first two and prints the reference; the create is a
// separate command. 'uploads reserve' exposes step one alone, for a caller that
// wants to do its own PUT.

const uploadsCreatePath = "/uploads/create"

var uploadColumns = []string{"upload_reference", "expires_at", "max_content_length", "upload_url"}

var uploadsCmd = &cobra.Command{
	Use:   "uploads",
	Short: "Upload files for cohorts and POI submissions",
	Long: `Upload a file to Intuizi without owning any cloud storage.

Uploading is three steps: reserve a slot, PUT the bytes to the presigned URL
that comes back, then pass the upload_reference to the create that claims it -
'intuizi cohorts create --upload-reference' for a cohort upload, or
'intuizi poi submissions create --upload-reference' for a poi_submission one.

'uploads put' does the first two and prints just the reference; the create is
a separate command:

    ref=$(intuizi uploads put customers.csv --purpose cohort)
    intuizi cohorts create --upload-reference "$ref" ...

Caps are per purpose: 50 MB for poi_submission, 1 GB for cohort.

A reservation expires at its expires_at, 15 minutes after it is made. The PUT
and the create that claims the reference both have to happen before then: a
create after it is refused even when the PUT succeeded. A reference is claimed
once, and a create refused after claiming it, as by the build budget, has used
it up, so upload the file again for a new one.

The filename names the stored object, and matters twice. A poi_submission
name must end .csv or .txt, or the reservation is refused. A cohort name
decides how 'cohorts create' imports the file: one ending .csv, .gz or
.parquet, case-sensitive and within its first 100 characters, is read as one
file, and anything else as a folder.`,
}

// uploadPurposes is what --purpose accepts. Validation, help text and
// completion all read it, so they cannot drift.
var uploadPurposes = []string{"poi_submission", "cohort"}

// checkPurpose fails a typo here rather than as a 422: the purpose also picks
// the size cap, so the server cannot guess it.
func checkPurpose(purpose string) error {
	if slices.Contains(uploadPurposes, purpose) {
		return nil
	}
	return usageErr(fmt.Sprintf("--purpose must be %s, not %q", strings.Join(uploadPurposes, " or "), purpose))
}

// reserveBody is step one, shared by 'reserve' and 'put'.
func reserveBody(purpose, filename, contentType string, size int64) map[string]any {
	body := map[string]any{
		"purpose":        purpose,
		"content_length": size,
	}
	if filename != "" {
		body["filename"] = filename
	}
	if contentType != "" {
		body["content_type"] = contentType
	}
	return body
}

func uploadsReserveCommand() *cobra.Command {
	var (
		purpose     string
		filename    string
		contentType string
		size        int64
	)

	cmd := &cobra.Command{
		Use:   "reserve",
		Short: "Reserve an upload slot and print its presigned URL",
		Long: `Reserve an upload slot.

Returns an upload_reference and the presigned URL to PUT the bytes to. Use this
when you want to do the PUT yourself; 'uploads put' does the reserve and the
PUT in one command.

content_length must be the exact byte size of the file you will send.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkPurpose(purpose); err != nil {
				return err
			}
			return createBody(cmd, uploadsCreatePath,
				reserveBody(purpose, filename, contentType, size), uploadColumns,
				"PUT the file to upload_url, then pass upload_reference to the create")
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&purpose, "purpose", "", "What the upload is for: "+strings.Join(uploadPurposes, " or "))
	completeValues(cmd, "purpose", uploadPurposes)
	flags.StringVar(&filename, "filename", "",
		"Original filename, which names the stored object: .csv or .txt for\n"+
			"poi_submission; for cohort, a name ending .csv, .gz or .parquet imports\n"+
			"as one file and anything else as a folder. Left out, the API uses\n"+
			"upload.csv")
	flags.StringVar(&contentType, "content-type", "", "MIME type the PUT will send (default text/csv)")
	flags.Int64Var(&size, "content-length", 0, "Exact byte size of the file you will PUT")
	_ = cmd.MarkFlagRequired("purpose")
	_ = cmd.MarkFlagRequired("content-length")

	return cmd
}

func uploadsPutCommand() *cobra.Command {
	var (
		purpose     string
		contentType string
	)

	cmd := &cobra.Command{
		Use:   "put <file>",
		Short: "Reserve a slot, upload a file, and print its reference",
		Long: `Reserve a slot, PUT the file, and print the upload_reference.

The reference is the only thing printed on stdout, so it can be captured
directly:

    ref=$(intuizi uploads put customers.csv --purpose cohort)

With --json the reservation envelope is printed instead, once the PUT has
succeeded, so .data[0].upload_reference is the same value.

The size is taken from the file, so it always matches what is sent, and the
file's own name is sent as the filename. So a poi_submission file must end
.csv or .txt, and a cohort file should end .csv, .gz or .parquet
(case-sensitive), or 'cohorts create' imports it as a folder: rename it before
uploading. The PUT itself carries no Intuizi credentials - the signature in
the URL is what authorises it.

The reference has to be claimed by a create within 15 minutes of the
reservation, before its expires_at.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkPurpose(purpose); err != nil {
				return err
			}
			path := args[0]
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory", path)
			}
			// A zero-length body goes out chunked, which storage rejects, and
			// the reservation would be spent either way.
			if info.Size() == 0 {
				return usageErr(fmt.Sprintf("%s is empty - there is nothing to upload", path))
			}

			c, err := client()
			if err != nil {
				return err
			}

			// The envelope is kept for --json: the PUT spends the reservation,
			// so it cannot be fetched again afterwards.
			slot, raw, err := api.CreateWithEnvelope[output.Record](cmd.Context(), c, uploadsCreatePath,
				reserveBody(purpose, info.Name(), contentType, info.Size()))
			if err != nil {
				if jsonOutput {
					printErrorEnvelope(cmd, err)
				}
				return err
			}

			url, _ := slot["upload_url"].(string)
			ref, _ := slot["upload_reference"].(string)
			if url == "" || ref == "" {
				return fmt.Errorf("the reservation did not come back with an upload URL")
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "uploading %s (%d bytes)...\n", info.Name(), info.Size())
			if err := api.PutPresigned(cmd.Context(), url, putHeaders(slot, contentType), path); err != nil {
				return err
			}

			if jsonOutput {
				return output.JSON(cmd.OutOrStdout(), raw)
			}
			// The reference alone on stdout, so $(...) captures it cleanly.
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), ref)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&purpose, "purpose", "", "What the upload is for: "+strings.Join(uploadPurposes, " or "))
	completeValues(cmd, "purpose", uploadPurposes)
	flags.StringVar(&contentType, "content-type", "", "MIME type to send (default text/csv)")
	_ = cmd.MarkFlagRequired("purpose")

	return cmd
}

// putHeaders is what the PUT must carry: every header the reservation listed,
// with --content-type replacing Content-Type alone. Keys are canonicalised on
// the way in so the override replaces the server's spelling rather than
// racing it on map order.
func putHeaders(slot output.Record, contentType string) map[string]string {
	headers := map[string]string{}
	if listed, ok := slot["headers"].(map[string]any); ok {
		for k, v := range listed {
			switch v := v.(type) {
			case string:
				headers[http.CanonicalHeaderKey(k)] = v
			case json.Number:
				headers[http.CanonicalHeaderKey(k)] = v.String()
			}
		}
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	return headers
}

func init() {
	uploadsCmd.AddCommand(uploadsReserveCommand(), uploadsPutCommand())
	rootCmd.AddCommand(uploadsCmd)
}
