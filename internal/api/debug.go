package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Debug prints a transcript of every exchange to DebugOut, for a user to hand
// support. Set from cmd, like UserAgent; nil DebugOut silences it.
//
// Bodies are never printed: --json and --dry-run already show them, and one
// here could carry a password, a token or a presigned URL. Nothing reads a
// body, so this cannot empty one or stall the multipart pipe.
var (
	Debug    bool
	DebugOut io.Writer = os.Stderr
)

// Header values that authenticate.
var redactedHeaders = map[string]bool{
	"authorization":        true,
	"cookie":               true,
	"idempotency-key":      true,
	"set-cookie":           true,
	"x-amz-security-token": true,
}

// By shape, not exact name: S3, Google and Azure spell these differently, and
// an unseen spelling must not be what leaks.
func secretish(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{"signature", "credential", "api-key", "apikey", "accesskey", "security-token"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// A RoundTripper rather than a hook at the three send sites: those live on two
// clients, and PutPresigned is not a Client method. Debug is read per request,
// because New runs before the flag is parsed.
type debugTransport struct{ base http.RoundTripper }

func newDebugTransport() http.RoundTripper { return &debugTransport{} }

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	out := DebugOut
	if !Debug || out == nil {
		return base.RoundTrip(req)
	}

	storage := isStorage(req)
	shown := redactURL(req.URL)
	if storage {
		shown = redactStorageURL(req.URL)
	}
	_, _ = fmt.Fprintf(out, "> %s %s\n", req.Method, shown)
	writeHeaders(out, ">", req.Header, storage)
	switch {
	case req.ContentLength > 0:
		_, _ = fmt.Fprintf(out, ">   %d byte body\n", req.ContentLength)
	case req.Body != nil:
		// A multipart body is an io.Pipe: no length here.
		_, _ = fmt.Fprintln(out, ">   streaming body")
	}

	start := time.Now()
	resp, err := base.RoundTrip(req)
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		// DNS, TLS, a proxy refusing: no status ever arrives.
		_, _ = fmt.Fprintf(out, "< transport error after %s: %s\n", took, escapeDebug(err.Error()))
		return resp, err
	}

	_, _ = fmt.Fprintf(out, "< %s in %s\n", resp.Status, took)
	writeHeaders(out, "<", resp.Header, storage)

	// Counted as the caller reads: nothing buffered, and right when gzipped,
	// where ContentLength is -1.
	resp.Body = &countingBody{rc: resp.Body, out: out}
	return resp, nil
}

// Reports how much was read, once, on Close.
type countingBody struct {
	rc     io.ReadCloser
	out    io.Writer
	n      int64
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.n += int64(n)
	return n, err
}

func (b *countingBody) Close() error {
	if !b.closed {
		b.closed = true
		_, _ = fmt.Fprintf(b.out, "<   %d bytes read\n", b.n)
	}
	return b.rc.Close()
}

// The transport cannot name the file: a multipart body reaches it as a pipe.
// Base name only - a path names the OS account.
func debugUpload(filePath string) {
	if !Debug || DebugOut == nil {
		return
	}
	_, _ = fmt.Fprintf(DebugOut, "> uploading %s\n", escapeDebug(filepath.Base(filePath)))
}

// Sorted: map order is randomised, and transcripts should diff cleanly.
func writeHeaders(out io.Writer, prefix string, h http.Header, storage bool) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			_, _ = fmt.Fprintf(out, "%s   %s: %s\n", prefix, k, clip(redactHeaderValue(k, v, storage)))
		}
	}
}

func redactHeaderValue(key, value string, storage bool) string {
	k := strings.ToLower(key)
	// Every x-amz-* value, not just credential-shaped ones: all of it describes
	// storage internals. The name stays, so the transcript says what was sent.
	if redactedHeaders[k] || secretish(k) || strings.HasPrefix(k, "x-amz-") {
		return "<redacted>"
	}
	// Redirects are refused, but a Location arrives and may be signed.
	if k == "location" {
		if u, err := url.Parse(value); err == nil {
			if storage {
				return redactStorageURL(u)
			}
			return redactURL(u)
		}
	}
	return escapeDebug(value)
}

// Marks a request to storage rather than the console: such a URL names the
// bucket and the object key, neither of which belongs in a ticket.
type storageRequest struct{}

func markStorage(ctx context.Context) context.Context {
	return context.WithValue(ctx, storageRequest{}, true)
}

func isStorage(req *http.Request) bool {
	v, _ := req.Context().Value(storageRequest{}).(bool)
	return v
}

// Masks the leftmost host label (the bucket) and the path. Provider and region
// stay: the line must still say who answered.
func redactStorageURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	if c.User != nil {
		c.User = url.User("REDACTED")
	}
	// An address has no bucket label, and masking an octet prints nonsense.
	if host := c.Hostname(); net.ParseIP(host) == nil {
		if _, rest, ok := strings.Cut(host, "."); ok {
			c.Host = "REDACTED." + rest
			if port := c.Port(); port != "" {
				c.Host += ":" + port
			}
		}
	}
	c.Path, c.RawPath = "/REDACTED", ""
	c.RawQuery = redactRawQuery(c.RawQuery)
	return escapeDebug(c.String())
}

// Strips userinfo and signature-shaped query values. Edited in place because
// url.Values re-sorts and would escape the marker into %3Credacted%3E. The path
// stays, or the line cannot say which call failed.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	if c.User != nil {
		c.User = url.User("REDACTED")
	}
	c.RawQuery = redactRawQuery(c.RawQuery)
	return escapeDebug(c.String())
}

// Replaces signature-shaped values, leaving every other byte.
func redactRawQuery(raw string) string {
	if raw == "" {
		return raw
	}
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		name, _, found := strings.Cut(p, "=")
		if found && secretish(name) {
			parts[i] = name + "=REDACTED"
		}
	}
	return strings.Join(parts, "&")
}

// The console's Content-Security-Policy runs to ~900 characters, which printed
// whole is most of the transcript.
const maxValue = 200

// Says by how much, so short and truncated values differ. After redaction: the
// marker must not be what gets redacted.
func clip(s string) string {
	if len(s) <= maxValue {
		return s
	}
	return s[:maxValue] + fmt.Sprintf("... (%d chars)", len(s))
}

// Bytes that would move a terminal cursor. The far end sends what it likes,
// and this gets pasted into a ticket.
func escapeDebug(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			_, _ = fmt.Fprintf(&b, `\x%02x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
