package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// 'uploads put' talks to two hosts: the API, which reserves the slot, and the
// storage host the presigned URL points at. stub plays the API; storage plays
// the other, recording the one PUT it should receive.

type putCapture struct {
	method string
	header http.Header
	length int64
	body   string
	hits   int
}

func storage(t *testing.T, status int) (*httptest.Server, *putCapture) {
	t.Helper()
	got := &putCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hits++
		got.method = r.Method
		got.header = r.Header.Clone()
		got.length = r.ContentLength
		b, _ := io.ReadAll(r.Body)
		got.body = string(b)
		if status != 0 {
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// reservation is the envelope /uploads/create answers with, pointed at the
// given storage URL. headers is the signed header object, as JSON.
func reservation(uploadURL, headers string) string {
	return `{"status":"success","code":201,"message":"Resource created successfully.",` +
		`"data":[{"upload_reference":"upl_abc","upload_url":"` + uploadURL + `/obj?X-Amz-Signature=sig",` +
		`"expires_at":"2026-09-11T12:15:00Z","max_content_length":1073741824,"headers":` + headers + `}]}`
}

func uploadFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "customers.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The reference alone on stdout is what makes ref=$(intuizi uploads put ...)
// work, and the PUT must carry the bytes with a known length.
func TestUploadsPutPrintsTheBareReference(t *testing.T) {
	const content = "email\na@example.com\n"
	store, put := storage(t, 0)
	srv, got := stub(t, reservation(store.URL, `{"Content-Type":"text/csv"}`))

	out, errb, err := run(t, uploadsPutCommand(), srv, uploadFile(t, content), "--purpose", "cohort")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "upl_abc\n" {
		t.Errorf("stdout = %q, want the bare reference", out)
	}
	if !strings.Contains(errb, "uploading customers.csv") {
		t.Errorf("stderr = %q, want the progress line", errb)
	}

	if got.paths[0] != "/api/v2/uploads/create" {
		t.Errorf("path = %s", got.paths[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	// The size comes from the file, never from a flag, so it always matches.
	if body["content_length"] != float64(len(content)) || body["filename"] != "customers.csv" || body["purpose"] != "cohort" {
		t.Errorf("reserve body = %v", body)
	}

	if put.hits != 1 || put.method != http.MethodPut || put.body != content || put.length != int64(len(content)) {
		t.Errorf("PUT = %d hits, %s, %d bytes, body %q", put.hits, put.method, put.length, put.body)
	}
}

// README promises --json everywhere. Here it is the reservation envelope, so
// ... --json | jq -r .data[0].upload_reference gives what the bare form gives.
func TestUploadsPutHonoursJSON(t *testing.T) {
	store, put := storage(t, 0)
	srv, _ := stub(t, reservation(store.URL, `{"Content-Type":"text/csv"}`))

	out, _, err := runJSON(t, uploadsPutCommand(), srv, uploadFile(t, "x"), "--purpose", "cohort")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if put.hits != 1 {
		t.Fatalf("--json must still upload; PUT hits = %d", put.hits)
	}

	var env struct {
		Status string `json:"status"`
		Data   []struct {
			Ref string `json:"upload_reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json did not print a parseable envelope: %v\n%s", err, out)
	}
	if env.Status != "success" || len(env.Data) != 1 || env.Data[0].Ref != "upl_abc" {
		t.Errorf("envelope = %s", out)
	}
}

// The envelope is printed after the PUT, so a rejected upload leaves stdout
// empty rather than handing a script a reference nothing was uploaded to.
func TestUploadsPutJSONPrintsNothingWhenThePUTFails(t *testing.T) {
	store, _ := storage(t, http.StatusForbidden)
	srv, _ := stub(t, reservation(store.URL, `{}`))

	out, _, err := runJSON(t, uploadsPutCommand(), srv, uploadFile(t, "x"), "--purpose", "cohort")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want the storage rejection", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing after a failed PUT", out)
	}
}

// Every header in the reservation is signed. Only Content-Type used to reach
// the PUT, so a server that started signing an encryption header would have
// failed every upload.
func TestUploadsPutForwardsTheReservationHeaders(t *testing.T) {
	const signed = `{"Content-Type":"text/csv","x-amz-server-side-encryption":"AES256"}`

	t.Run("as reserved", func(t *testing.T) {
		store, put := storage(t, 0)
		srv, _ := stub(t, reservation(store.URL, signed))

		if _, _, err := run(t, uploadsPutCommand(), srv, uploadFile(t, "x"), "--purpose", "cohort"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if put.header.Get("Content-Type") != "text/csv" || put.header.Get("x-amz-server-side-encryption") != "AES256" {
			t.Errorf("PUT headers = %v", put.header)
		}
	})

	// --content-type replaces Content-Type alone; the rest still travel.
	t.Run("content-type overridden", func(t *testing.T) {
		store, put := storage(t, 0)
		srv, _ := stub(t, reservation(store.URL, signed))

		if _, _, err := run(t, uploadsPutCommand(), srv, uploadFile(t, "x"),
			"--purpose", "cohort", "--content-type", "application/gzip"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if put.header.Get("Content-Type") != "application/gzip" {
			t.Errorf("Content-Type = %q, want the flag to win", put.header.Get("Content-Type"))
		}
		if put.header.Get("x-amz-server-side-encryption") != "AES256" {
			t.Errorf("the signed encryption header was dropped: %v", put.header)
		}
		if vals := put.header.Values("Content-Type"); len(vals) != 1 {
			t.Errorf("Content-Type sent %d times: %v", len(vals), vals)
		}
	})
}

// Nothing to upload: a zero-length body goes out chunked, which storage
// rejects, and the reservation is spent either way. Caught before reserving.
func TestUploadsPutRejectsAnEmptyFile(t *testing.T) {
	store, put := storage(t, 0)
	srv, got := stub(t, reservation(store.URL, `{}`))

	path := uploadFile(t, "")
	_, _, err := run(t, uploadsPutCommand(), srv, path, "--purpose", "cohort")
	if err == nil || !strings.Contains(err.Error(), "customers.csv") || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want one naming the empty file", err)
	}
	if exitCode(err, nil, true) != 2 {
		t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
	}
	if len(got.paths) != 0 || put.hits != 0 {
		t.Errorf("an empty file must spend no reservation: api %v, storage %d", got.paths, put.hits)
	}
}

// --purpose is a closed set on both commands, and picks the size cap, so a
// typo is caught here rather than as a 422.
func TestUploadsRejectAnUnknownPurpose(t *testing.T) {
	tests := []struct {
		name  string
		build func() *cobra.Command
		args  []string
	}{
		{"reserve", uploadsReserveCommand, []string{"--purpose", "photos", "--content-length", "10"}},
		{"put", uploadsPutCommand, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{}`)
			args := tc.args
			if args == nil {
				args = []string{uploadFile(t, "x"), "--purpose", "photos"}
			}

			_, _, err := run(t, tc.build(), srv, args...)
			if err == nil || !strings.Contains(err.Error(), "--purpose must be") ||
				!strings.Contains(err.Error(), "poi_submission or cohort") {
				t.Fatalf("err = %v, want one listing the accepted purposes", err)
			}
			if exitCode(err, nil, true) != 2 {
				t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}
