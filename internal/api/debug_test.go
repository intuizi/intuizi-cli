package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// debugTo turns the transcript on for one test and restores the package globals
// afterwards, so a failure cannot leak --debug into the rest of the suite.
func debugTo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevDebug, prevOut := Debug, DebugOut
	Debug, DebugOut = true, &buf
	t.Cleanup(func() { Debug, DebugOut = prevDebug, prevOut })
	return &buf
}

// The transcript is meant to be pasted into a support ticket, so the bearer
// token must never reach it, and neither must the response body: a body can
// carry a minted token, a presigned URL or the caller's own identifiers.
func TestDebugRedactsTheTokenAndPrintsNoBody(t *testing.T) {
	const secret = "tok-should-never-appear"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","code":200,"data":{"secret_field":"body-should-never-appear"}}`))
	}))
	defer srv.Close()

	buf := debugTo(t)
	if _, err := New(srv.URL, secret).GetRaw(context.Background(), "/things", nil); err != nil {
		t.Fatalf("get: %v", err)
	}

	got := buf.String()
	for _, banned := range []string{secret, "body-should-never-appear", "secret_field"} {
		if strings.Contains(got, banned) {
			t.Errorf("transcript leaked %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"> GET ", "Authorization: <redacted>", "< 200 OK in ", "bytes read"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
}

// A presigned PUT carries no Authorization: the query signature is the only
// credential, so redacting headers alone would publish it. Every x-amz-* value
// goes too, but the names stay, so the line still says what was sent.
func TestDebugRedactsAPresignedSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Amz-Request-Id", "REQ123")
		w.Header().Set("X-Amz-Id-2", "ID456")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "members.csv")
	if err := os.WriteFile(path, []byte("email_sha256\nabc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	buf := debugTo(t)
	url := srv.URL + "/obj?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=cred-secret" +
		"&X-Amz-Signature=sig-secret&X-Amz-SignedHeaders=host&page=2"
	if err := PutPresigned(context.Background(), url, map[string]string{"x-amz-acl": "private"}, path); err != nil {
		t.Fatalf("put: %v", err)
	}

	got := buf.String()
	for _, banned := range []string{"sig-secret", "cred-secret"} {
		if strings.Contains(got, banned) {
			t.Errorf("transcript leaked %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{
		"X-Amz-Signature=REDACTED",         // the marker survives, unescaped
		"X-Amz-Credential=REDACTED",        //
		"X-Amz-Algorithm=AWS4-HMAC-SHA256", // not a secret, and says which scheme signed
		"page=2",                           // the ordinary query stays
		"X-Amz-Request-Id: <redacted>",     // the name stays, the value does not
		"X-Amz-Id-2: <redacted>",
		"> uploading members.csv", // base name only
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
	// A full path names the OS account.
	if strings.Contains(got, t.TempDir()) {
		t.Errorf("transcript carried the directory:\n%s", got)
	}
	// The object key is masked: it carries the customer's file name. (The test
	// server is an address, which has no bucket label - see the unit test below.)
	if !strings.Contains(got, "/REDACTED?") {
		t.Errorf("object key not masked:\n%s", got)
	}
	if strings.Contains(got, "/obj") {
		t.Errorf("object key survived:\n%s", got)
	}
}

// The bucket is the leftmost label of a virtual-hosted storage host, and names
// internal infrastructure. The provider and its region stay: the line still has
// to say who answered and from where.
func TestRedactStorageURLMasksTheBucketNotTheProvider(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{
			"https://example-bucket.s3.us-west-2.amazonaws.com/c0-0/api/cohort/x-t.csv?X-Amz-Signature=sig&X-Amz-Expires=900",
			"https://REDACTED.s3.us-west-2.amazonaws.com/REDACTED?X-Amz-Signature=REDACTED&X-Amz-Expires=900",
		},
		// An address has no label to mask, and masking its first octet would
		// print nonsense.
		{"http://127.0.0.1:9000/bucket/key?a=1", "http://127.0.0.1:9000/REDACTED?a=1"},
	} {
		u, err := url.Parse(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := redactStorageURL(u); got != tc.want {
			t.Errorf("redactStorageURL:\n got  %s\n want %s", got, tc.want)
		}
	}
}

// net/http refuses a response header carrying a control byte, so the escape is
// not defending the header path - it defends the strings we print ourselves,
// chiefly a file name, which is whatever the user typed on the command line.
func TestDebugEscapesControlBytes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"before\x1b[2Jafter", `before\x1b[2Jafter`},
		{"a\rb\nc", `a\x0db\x0ac`},
		{"nul\x00", `nul\x00`},
	} {
		if got := escapeDebug(tc.in); got != tc.want {
			t.Errorf("escapeDebug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A file name reaches the transcript from argv, so it goes through the escape
// and loses its directory: a full path names the OS account.
func TestDebugUploadPrintsBaseNameOnly(t *testing.T) {
	buf := debugTo(t)
	debugUpload(filepath.Join("home", "someone", "members.csv"))
	if got := buf.String(); got != "> uploading members.csv\n" {
		t.Errorf("debugUpload printed %q", got)
	}
}

// The transport error is the case support is usually triaging, and it never
// reaches a status line.
func TestDebugLogsATransportError(t *testing.T) {
	buf := debugTo(t)
	// Port 1 is not listening; the connection is refused before any response.
	_, err := New("http://127.0.0.1:1", "t").GetRaw(context.Background(), "/things", nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if got := buf.String(); !strings.Contains(got, "< transport error after ") {
		t.Errorf("transcript missing the transport error:\n%s", got)
	}
}

// Off by default, and a nil writer silences it the way a nil Client.Warn does.
func TestDebugSilentWhenOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":{}}`))
	}))
	defer srv.Close()

	buf := debugTo(t)
	Debug = false
	if _, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q with --debug off", buf.String())
	}

	Debug, DebugOut = true, nil
	if _, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil); err != nil {
		t.Fatalf("get with nil DebugOut: %v", err)
	}
}

// The design's central claim: --debug observes and never consumes, so a command
// behaves identically with it on. Printing no body is what makes that true -
// nothing here reads the response, it only counts what the caller reads.
func TestDebugLeavesTheBodyIntact(t *testing.T) {
	const body = `{"status":"success","code":200,"data":[{"id":7,"name":"seven"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	off, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil)
	if err != nil {
		t.Fatalf("with --debug off: %v", err)
	}

	buf := debugTo(t)
	on, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil)
	if err != nil {
		t.Fatalf("with --debug on: %v", err)
	}

	if string(on) != string(off) || string(on) != body {
		t.Fatalf("--debug changed the body:\n off = %q\n on  = %q", off, on)
	}
	if !strings.Contains(buf.String(), "64 bytes read") {
		t.Errorf("byte count wrong:\n%s", buf)
	}
}

// The two upload paths run on a second client. A transport installed on only
// one of them would leave every upload invisible, which a test on the JSON
// client alone would not catch.
func TestDebugCoversTheMultipartClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		w.Write([]byte(`{"status":"success","code":200,"data":{}}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "cohort.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	buf := debugTo(t)
	if _, err := CreateMultipartRaw(context.Background(), New(srv.URL, "t"), "/up",
		map[string]string{"purpose": "cohort"}, "file", path); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	// "streaming body" rather than a length: a multipart body is an io.Pipe, so
	// the transport is handed no ContentLength to print.
	for _, want := range []string{"> uploading cohort.csv", "> POST ", "streaming body", "< 200 OK in "} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// A retry is two exchanges. Printing only the last would hide the 429 and the
// Retry-After that explain why a command took a minute.
func TestDebugPrintsEveryRetryAttempt(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n++; n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"status":"error","code":429,"message":"slow down","data":[]}`))
			return
		}
		w.Write([]byte(`{"status":"success","code":200,"data":[]}`))
	}))
	defer srv.Close()

	recordSleeps(t)
	buf := debugTo(t)
	if _, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if c := strings.Count(got, "> GET "); c != 2 {
		t.Errorf("want 2 request lines, got %d:\n%s", c, got)
	}
	for _, want := range []string{"< 429 Too Many Requests in ", "Retry-After: 0", "< 200 OK in "} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// A console answers every request with a Content-Security-Policy of about 900
// characters. Printed whole it is most of the transcript, and the point of the
// flag is that someone reads the result.
func TestDebugClipsALongHeaderValue(t *testing.T) {
	long := strings.Repeat("x", 900)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", long)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","code":200,"data":{}}`))
	}))
	defer srv.Close()

	buf := debugTo(t)
	if _, err := New(srv.URL, "t").GetRaw(context.Background(), "/things", nil); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if strings.Contains(got, long) {
		t.Errorf("a 900-character header was printed whole")
	}
	if !strings.Contains(got, "... (900 bytes)") {
		t.Errorf("no truncation marker:\n%s", got)
	}
	// A short value is untouched, so a reader can tell the two apart.
	if !strings.Contains(got, "Content-Type: application/json") {
		t.Errorf("a short value was altered:\n%s", got)
	}
}

// Every x-amz-* value is redacted - checksums, encryption settings and request
// ids all describe storage internals. The header names stay, so the transcript
// still says what the exchange carried.
func TestDebugRedactsEveryAmzHeaderValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("X-Amz-Request-Id", "T4XYZ9")
		h.Set("X-Amz-Checksum-Crc64nvme", "qq3s8w==")
		h.Set("X-Amz-Server-Side-Encryption", "AES256")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	buf := debugTo(t)
	if err := PutPresigned(context.Background(), srv.URL+"/k?X-Amz-Signature=s",
		map[string]string{"x-amz-acl": "private"}, path); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	for _, want := range []string{
		"X-Amz-Request-Id: <redacted>",
		"X-Amz-Checksum-Crc64nvme: <redacted>",
		"X-Amz-Server-Side-Encryption: <redacted>",
		"X-Amz-Acl: <redacted>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, banned := range []string{"T4XYZ9", "qq3s8w==", "AES256", "private"} {
		if strings.Contains(got, banned) {
			t.Errorf("printed %q:\n%s", banned, got)
		}
	}
}

// A DNS or dial failure names the host, which for storage is the bucket. The
// other storage test uses an address, which has no label to leak.
func TestDebugMasksTheBucketInATransportError(t *testing.T) {
	buf := debugTo(t)

	tr := &debugTransport{base: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: lookup %s: no such host", r.URL.Hostname())
	})}
	req, err := http.NewRequestWithContext(markStorage(context.Background()), http.MethodPut,
		"https://example-bucket.s3.us-west-2.amazonaws.com/customers.csv", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("want a transport error")
	}

	got := buf.String()
	if strings.Contains(got, "example-bucket") {
		t.Errorf("transport error leaked the bucket:\n%s", got)
	}
	if !strings.Contains(got, "REDACTED.s3.us-west-2.amazonaws.com") {
		t.Errorf("masked host missing:\n%s", got)
	}
}

// Forces a transport failure without a real network.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Masking a path-style host would drop the provider the line needs to name.
// The bucket sits in the path either way.
func TestMaskedHostKeepsThePathStyleProvider(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://example-bucket.s3.us-west-2.amazonaws.com/k", "REDACTED.s3.us-west-2.amazonaws.com"},
		{"https://s3.us-west-2.amazonaws.com/example-bucket/k", "s3.us-west-2.amazonaws.com"},
		{"https://storage.googleapis.com/example-bucket/k", "storage.googleapis.com"},
		{"https://example-bucket.storage.googleapis.com/k", "REDACTED.storage.googleapis.com"},
		{"https://127.0.0.1:8080/k", "127.0.0.1:8080"},
		{"https://[::1]:8080/k", "[::1]:8080"},
		{"https://example-bucket.s3.amazonaws.com:8443/k", "REDACTED.s3.amazonaws.com:8443"},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.raw, err)
		}
		if got := maskedHost(u); got != tc.want {
			t.Errorf("maskedHost(%s) = %s, want %s", u.Host, got, tc.want)
		}
	}
}

// Azure spells its SAS signature "sig", which no shape matched.
func TestSecretishCoversTheAzureSpelling(t *testing.T) {
	if !secretish("sig") {
		t.Error("an Azure SAS signature would reach the transcript")
	}
	if secretish("design") {
		t.Error("sig matched as a substring, which would redact ordinary names")
	}
}

// Truncating at a byte offset split runes, printing mojibake into a ticket.
func TestClipCutsOnARuneBoundary(t *testing.T) {
	got := clip(strings.Repeat("\u20ac", 100)) // 3 bytes each, so 200 is mid-rune
	if !utf8.ValidString(got) {
		t.Errorf("clip split a rune: %q", got)
	}
	if !strings.Contains(got, "(300 bytes)") {
		t.Errorf("want the byte count, got %q", got)
	}
}
