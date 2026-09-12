package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A cohort file_uri is checked offline: the scheme must be s3:// or gs://,
// there must be both a bucket and a path under it, and neither may contain
// whitespace - the server's regex is ^(s3|gs)://[^/\s]+/\S+$, so a space
// anywhere 422s. The suffix is deliberately not checked - .csv, .gz and
// .parquet name a single file, anything else names a folder, so an
// extensionless path is legal.
func TestValidateFileURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		ok   bool
	}{
		{"s3 file", "s3://example-bucket/cohorts/q3.csv", true},
		{"gs file", "gs://example-bucket/cohorts/q3.csv", true},
		{"gzip file", "s3://example-bucket/q3.csv.gz", true},
		{"parquet file", "gs://example-bucket/q3.parquet", true},
		{"folder, no extension", "s3://example-bucket/cohorts/q3", true},
		{"folder, trailing slash", "s3://example-bucket/cohorts/", true},
		{"deep path", "gs://example-bucket/a/b/c/q3.csv", true},

		{"empty", "", false},
		{"no scheme", "example-bucket/cohorts/q3.csv", false},
		{"gcs is not the scheme", "gcs://example-bucket/cohorts/q3.csv", false},
		{"http", "http://example.com/q3.csv", false},
		{"local path", "/tmp/q3.csv", false},
		{"bucket only", "s3://example-bucket", false},
		{"bucket only, trailing slash", "s3://example-bucket/", false},
		{"no bucket", "s3:///cohorts/q3.csv", false},
		{"scheme only", "s3://", false},
		{"uppercase scheme", "S3://example-bucket/q3.csv", false},
		{"whitespace path", "s3://example-bucket/ ", false},
		{"whitespace and slash path", "s3://example-bucket/ /", false},
		{"whitespace bucket", "s3:// /cohorts/q3.csv", false},
		{"tab bucket", "s3://\t/cohorts/q3.csv", false},
		{"space inside the key", "s3://example-bucket/my folder/q3.csv", false},
		{"tab inside the key", "s3://example-bucket/my\tfolder/q3.csv", false},
		{"space inside the bucket", "s3://example bucket/cohorts/q3.csv", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFileURI(tc.uri)
			if tc.ok && err != nil {
				t.Errorf("%q should be accepted, got %v", tc.uri, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("%q should be rejected", tc.uri)
			}
			// A bad URI is a bad invocation, so it exits 2.
			if err != nil && exitCode(err, nil, true) != 2 {
				t.Errorf("%q: exit = %d, want 2", tc.uri, exitCode(err, nil, true))
			}
		})
	}
}

func TestCohortsCreateRejectsABadFileURIOffline(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, cohortsCreateCommand(), srv,
		"--name", "Q3 customers",
		"--file-uri", "s3://example-bucket",
		"--file-format", "csv",
		"--identifier-type", "hem_sha256",
		"--identifier-column", "email_sha256")
	if err == nil {
		t.Fatal("expected an error for a file URI with no path")
	}
	if !strings.Contains(err.Error(), "s3://example-bucket") {
		t.Errorf("the error should quote what was passed, got %v", err)
	}
	if !strings.Contains(err.Error(), "s3://bucket/path") {
		t.Errorf("the error should show the shape it wanted, got %v", err)
	}
	if code := exitCode(err, nil, true); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad --file-uri should cost no round trip, got %v", got.paths)
	}
}

// The folder case is the one a suffix check would quietly break, so it is
// carried all the way to the request body.
func TestCohortsCreateSendsAFolderURI(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":201,"data":[{"id":77,"name":"Q3 customers","status":{"id":2,"name":"Initiating"}}]}`)

	_, _, err := run(t, cohortsCreateCommand(), srv,
		"--name", "Q3 customers",
		"--file-uri", "s3://example-bucket/cohorts/q3",
		"--file-format", "csv",
		"--identifier-type", "hem_sha256",
		"--identifier-column", "email_sha256",
		"--metadata-columns", "city,state",
		"--metadata-columns", "loyalty_tier")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["file_uri"] != "s3://example-bucket/cohorts/q3" {
		t.Errorf("file_uri = %v", body["file_uri"])
	}
	// Repeating the flag adds an entry; a comma inside one is part of the name.
	want := []any{"city,state", "loyalty_tier"}
	cols, _ := body["metadata_columns"].([]any)
	if len(cols) != len(want) || cols[0] != want[0] || cols[1] != want[1] {
		t.Errorf("metadata_columns = %v, want %v", cols, want)
	}
	// Unset optionals must not go out as zero values.
	for _, k := range []string{"ip_enrichment", "device_limit", "project_id"} {
		if _, ok := body[k]; ok {
			t.Errorf("unset --%s leaked: %v", k, body)
		}
	}
}

func TestCohortsCreateRejectsFileMixedWithFlags(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, cohortsCreateCommand(), srv,
		"--file", "cohort.json", "--name", "Q3 customers")
	if err == nil || !strings.Contains(err.Error(), "--file carries the whole body") {
		t.Fatalf("err = %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestCohortsCreateNeedsFileOrTheCoreFields(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, cohortsCreateCommand(), srv, "--name", "Q3 customers")
	if err == nil {
		t.Fatal("expected an error when only --name is given")
	}
	if !strings.Contains(err.Error(), "--file-uri") {
		t.Errorf("the error should name what is missing, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The file-source flags the server checks against a fixed set are checked
// here too, so a typo is named with the accepted values instead of a 422.
func TestCohortsCreateRejectsUnknownEnumerations(t *testing.T) {
	srv, got := stub(t, `{}`)

	base := []string{"--name", "Q3 customers", "--file-uri", "s3://example-bucket/q3.csv",
		"--identifier-column", "email_sha256"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"file format", append(base, "--file-format", "xml", "--identifier-type", "hem_sha256"), "csv, gzip, parquet"},
		{"identifier type", append(base, "--file-format", "csv", "--identifier-type", "email"), "hem_sha256"},
		{"device limit", []string{"--audience-id", "88", "--device-limit", "0"}, "--device-limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, cohortsCreateCommand(), srv, tc.args...)
			var ue usageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usageError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestCohortsCreateRejectsNonPositiveIDFlags(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, args := range [][]string{
		{"--audience-id", "0"},
		{"--name", "Q3", "--file-uri", "s3://example-bucket/q3.csv", "--file-format", "csv",
			"--identifier-type", "hem_sha256", "--identifier-column", "email", "--project-id", "-1"},
	} {
		_, _, err := run(t, cohortsCreateCommand(), srv, args...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usageError", args, err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad id should cost no round trip, got %v", got.paths)
	}
}

// The docs describe --name on an audience source as rejected, and the
// command's own help has to agree with what it does.
func TestCohortsCreateRejectsNameOnAnAudienceSource(t *testing.T) {
	srv, got := stub(t, `{}`)

	cmd := cohortsCreateCommand()
	if !strings.Contains(cmd.Long, "--name is rejected") || strings.Contains(cmd.Long, "--name is ignored") {
		t.Errorf("Long should say --name is rejected on an audience source:\n%s", cmd.Long)
	}
	_, _, err := run(t, cmd, srv, "--audience-id", "88", "--name", "Q3 customers")
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// Cobra validates flag groups after PersistentPreRun, so its own "at least one
// of" error would exit 1. The check in RunE is the one that runs.
func TestCohortsPreviewWithNoFlagsIsAUsageError(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, cohortsPreviewCommand(), srv)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	if !strings.Contains(err.Error(), "--file-uri") || !strings.Contains(err.Error(), "--upload-reference") {
		t.Errorf("the error should name the sources, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestCohortsPreviewRejectsParquet(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, cohortsPreviewCommand(), srv,
		"--file-uri", "s3://example-bucket/q3.parquet", "--file-format", "parquet")
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	if !strings.Contains(err.Error(), "csv, gzip") {
		t.Errorf("the error should list what can be previewed, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The preview exists to pick --identifier-column, so the column names must be
// readable without --json: a header row, the sample rows under it, and the
// count on stderr where commentary goes.
const previewEnvelope = `{"status":"success","code":200,"data":[
 {"columns":["email_sha256","city","zip"],
  "samples":[["ab12","New York","10001"],["cd34","Los Angeles","90001"]],
  "sample_rows":2}]}`

func TestCohortsPreviewRendersColumnsAndSampleRows(t *testing.T) {
	srv, got := stub(t, previewEnvelope)

	out, errb, err := run(t, cohortsPreviewCommand(), srv, "--file-uri", "s3://example-bucket/q3.csv")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/analyses/cohorts/preview" {
		t.Errorf("path = %s", got.paths[0])
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two sample rows, got %d lines:\n%s", len(lines), out)
	}
	if head := strings.Join(strings.Fields(lines[0]), " "); head != "email_sha256 city zip" {
		t.Errorf("header = %q", head)
	}
	if !strings.Contains(lines[1], "ab12") || !strings.Contains(lines[1], "New York") ||
		!strings.Contains(lines[2], "Los Angeles") {
		t.Errorf("sample rows not rendered:\n%s", out)
	}
	if strings.Contains(out, "items]") || strings.Contains(out, "sample_rows") {
		t.Errorf("the generic detail view leaked through:\n%s", out)
	}
	if !strings.Contains(errb, "2 sample rows") {
		t.Errorf("stderr = %q, want the row count", errb)
	}
}

func TestCohortsPreviewJSONPrintsTheEnvelope(t *testing.T) {
	srv, _ := stub(t, previewEnvelope)

	out, _, err := runJSON(t, cohortsPreviewCommand(), srv, "--file-uri", "s3://example-bucket/q3.csv")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{`"status"`, `"samples"`, `"sample_rows"`} {
		if !strings.Contains(out, want) {
			t.Errorf("--json output missing %s:\n%s", want, out)
		}
	}
}

// The live API keys each sample row by column name rather than sending cells
// positionally; the first live run against beta printed a header and two
// empty rows because the renderer only knew the positional shape.
const previewObjectEnvelope = `{"status":"success","code":200,"message":"ok","data":[{
  "columns": ["email_sha256", "tier"],
  "samples": [
    {"email_sha256": "08168cd80dfd534ab0f10af10f1303fe", "tier": "gold"},
    {"email_sha256": "e8f39b3e1382367d6d41ab34dc270d4e", "tier": "silver"}
  ],
  "sample_rows": 2
}]}`

func TestCohortsPreviewRendersObjectSamples(t *testing.T) {
	srv, _ := stub(t, previewObjectEnvelope)

	out, errb, err := run(t, cohortsPreviewCommand(), srv, "--file-uri", "s3://example-bucket/q3.csv")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two sample rows, got %d lines:\n%s", len(lines), out)
	}
	if head := strings.Join(strings.Fields(lines[0]), " "); head != "email_sha256 tier" {
		t.Errorf("header = %q", head)
	}
	if !strings.Contains(lines[1], "08168cd80dfd534ab0f10af10f1303fe") || !strings.Contains(lines[1], "gold") ||
		!strings.Contains(lines[2], "silver") {
		t.Errorf("object-keyed sample rows not rendered:\n%s", out)
	}
	if !strings.Contains(errb, "2 sample rows") {
		t.Errorf("stderr = %q, want the row count", errb)
	}
}
