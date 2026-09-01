package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tempFile writes content and returns its path.
func tempFile(t *testing.T, content string) string {
	t.Helper()
	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return fp
}

// The upload client must carry no timeout of its own: http.Client.Timeout
// covers writing the body, so any value here caps transfer speed.
func TestUploadClientHasNoTimeout(t *testing.T) {
	if uploadHTTP.Timeout != 0 {
		t.Fatalf("uploadHTTP.Timeout = %s, want 0 - the deadline belongs on the context", uploadHTTP.Timeout)
	}
}

// PutPresigned must not attach the bearer token: the URL signature is the only
// credential and the host is third-party.
func TestPutPresignedSendsNoToken(t *testing.T) {
	const content = "latitude,longitude\n1,2\n"
	fp := tempFile(t, content)

	var (
		gotHeader http.Header
		gotMethod string
		gotLen    int64
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotMethod = r.Method
		gotLen = r.ContentLength
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	if err := PutPresigned(context.Background(), srv.URL+"/obj?X-Amz-Signature=abc", "", fp); err != nil {
		t.Fatalf("PutPresigned: %v", err)
	}

	if got := gotHeader.Get("Authorization"); got != "" {
		t.Errorf("Authorization sent to a third-party host: %q", got)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	// Storage rejects a chunked PUT, so the length must be known up front.
	if gotLen != int64(len(content)) {
		t.Errorf("Content-Length = %d, want %d", gotLen, len(content))
	}
	if got := gotHeader.Get("Content-Type"); got != "text/csv" {
		t.Errorf("Content-Type = %q, want the text/csv default", got)
	}
	if string(gotBody) != content {
		t.Errorf("body = %q", gotBody)
	}
}

// An explicit content type wins over the default.
func TestPutPresignedHonoursContentType(t *testing.T) {
	fp := tempFile(t, "x")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	if err := PutPresigned(context.Background(), srv.URL, "application/gzip", fp); err != nil {
		t.Fatalf("PutPresigned: %v", err)
	}
	if got != "application/gzip" {
		t.Errorf("Content-Type = %q", got)
	}
}

// A rejected PUT reports the status and the storage host's own message.
func TestPutPresignedReportsRejection(t *testing.T) {
	fp := tempFile(t, "x")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer srv.Close()

	err := PutPresigned(context.Background(), srv.URL, "", fp)
	if err == nil {
		t.Fatal("want an error on 403")
	}
	for _, want := range []string{"403", "AccessDenied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// A hit deadline must name the file rather than surfacing the transport's bare
// "context deadline exceeded", which reads like a server fault. The parent
// deadline stands in for the shipped ceiling: WithTimeout takes the earlier of
// the two, so the same branch runs.
func TestUploadTimeoutNamesTheFile(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()

	fp := tempFile(t, "latitude,longitude\n1,2\n")

	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"multipart", func(ctx context.Context) error {
			_, err := CreateMultipart[map[string]any](ctx, New(slow.URL, "t"),
				"/my-data/pois/submissions/create-by-file",
				map[string]string{"name": "Store list", "brand_id": "7"},
				"locations_file", fp)
			return err
		}},
		{"presigned", func(ctx context.Context) error {
			return PutPresigned(ctx, slow.URL, "", fp)
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()

			err := c.call(ctx)
			if err == nil {
				t.Fatal("want a timeout error")
			}
			if !strings.Contains(err.Error(), "locations.csv") {
				t.Errorf("error = %q, want it to name the file", err)
			}
			if strings.Contains(err.Error(), "context deadline exceeded") {
				t.Errorf("error = %q, still leaks the transport wording", err)
			}
		})
	}
}
