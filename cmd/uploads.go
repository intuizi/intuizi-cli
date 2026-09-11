package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// Uploading is a three-step primitive shared by POI submissions and cohorts:
// reserve a slot, PUT the bytes to the presigned URL, then hand the
// upload_reference to the create that claims it.
//
// 'uploads put' runs all three. 'uploads reserve' exposes step one alone, for a
// caller that wants to do its own PUT.

const uploadsCreatePath = "/uploads/create"

var uploadColumns = []string{"upload_reference", "expires_at", "max_content_length", "upload_url"}

var uploadsCmd = &cobra.Command{
	Use:   "uploads",
	Short: "Upload files for cohorts and POI submissions",
	Long: `Upload a file to Intuizi without owning any cloud storage.

Uploading is three steps: reserve a slot, PUT the bytes to the presigned URL
that comes back, then pass the upload_reference to the create that claims it -
'intuizi cohorts create' or 'intuizi poi submissions create --upload-reference'.

'uploads put' does all three and prints just the reference:

    ref=$(intuizi uploads put customers.csv --purpose cohort)

Caps are per purpose: 50 MB for poi_submission, 1 GB for cohort. A reservation
that is never used simply expires.`,
}

// checkPurpose fails a typo here rather than as a 422: the purpose also picks
// the size cap, so the server cannot guess it.
func checkPurpose(purpose string) error {
	switch purpose {
	case "poi_submission", "cohort":
		return nil
	}
	return usageErr(fmt.Sprintf("--purpose must be poi_submission or cohort, not %q", purpose))
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
when you want to do the PUT yourself; 'uploads put' is the one-step version.

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
	flags.StringVar(&purpose, "purpose", "", "What the upload is for: poi_submission or cohort")
	flags.StringVar(&filename, "filename", "", "Original filename, used to name the stored object")
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

The size is taken from the file, so it always matches what is sent. The PUT
itself carries no Intuizi credentials - the signature in the URL is what
authorises it.`,
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
	flags.StringVar(&purpose, "purpose", "", "What the upload is for: poi_submission or cohort")
	flags.StringVar(&contentType, "content-type", "", "MIME type to send (default text/csv)")
	_ = cmd.MarkFlagRequired("purpose")

	return cmd
}

// putHeaders is what the PUT must carry: every header the reservation signed,
// with --content-type replacing Content-Type alone. Keys are canonicalised on
// the way in so the override replaces the server's spelling rather than
// racing it on map order.
func putHeaders(slot output.Record, contentType string) map[string]string {
	headers := map[string]string{}
	if signed, ok := slot["headers"].(map[string]any); ok {
		for k, v := range signed {
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
