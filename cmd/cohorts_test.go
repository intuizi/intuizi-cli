package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A cohort file_uri is checked offline: the scheme must be s3:// or gs://, and
// there must be both a bucket and a path under it. The suffix is deliberately
// not checked - .csv, .gz and .parquet name a single file, anything else names
// a folder, so an extensionless path is legal.
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
		{"space inside the key", "s3://example-bucket/my folder/q3.csv", true},

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
