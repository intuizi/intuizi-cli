package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// http.Client.Timeout caps transfer speed, not the wait. Uploads use a context
// deadline instead - a backstop against a hung connection, sized from the
// documented caps at a ~2 Mbit/s floor.
const (
	multipartTimeout = 10 * time.Minute // 50 MB cap; ~3.5 min at the floor
	presignedTimeout = 90 * time.Minute // 1 GB cap; ~72 min at the floor
)

// No timeout - the deadline is per call. Shares DefaultTransport with the JSON
// client, which keeps its 30s.
//
// Redirects are not followed: a 3xx from the storage host would re-send the
// body, and to a cross-host Location it would hand the file to whoever answers
// there. It surfaces as a rejection carrying its status instead.
var uploadHTTP = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// withoutPresignedURL masks the path and query of the URL that http.Client.Do
// and url.Parse quote in their errors. A presigned URL's query string is its
// credential until it expires, and its path is the object key, which carries
// the customer's file name. The host stays: it is what a DNS or dial failure
// is about.
func withoutPresignedURL(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	masked := "REDACTED"
	if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
		masked = u.Scheme + "://" + u.Host + "/REDACTED"
	}
	return &url.Error{Op: ue.Op, URL: masked, Err: ue.Err}
}

// "context deadline exceeded" alone reads like a server fault.
func timeoutErr(filePath string, d time.Duration) error {
	return fmt.Errorf("uploading %s: gave up after %s - the transfer had not finished, so check connection speed or file size",
		filepath.Base(filePath), d)
}

// postMultipart returns just the data field, which is all most callers want.
func (c *Client) postMultipart(ctx context.Context, path string, fields map[string]string, fileField, filePath string) (json.RawMessage, error) {
	_, data, err := c.postMultipartRaw(ctx, path, fields, fileField, filePath)
	return data, err
}

// postMultipartRaw posts create-by-file as multipart/form-data, streaming the
// file rather than buffering it (POI files run to 50 MB). Like doRaw it returns
// the untouched body alongside the data field. Bounded by multipartTimeout,
// retries included.
//
// Boolean form fields must be "1" or "0" - API rejects the string "true"
func (c *Client) postMultipartRaw(ctx context.Context, path string, fields map[string]string, fileField, filePath string) ([]byte, json.RawMessage, error) {
	target := c.BaseURL + apiPrefix + path

	// Outside the loop: a per-attempt deadline is not a ceiling.
	ctx, cancel := context.WithTimeout(ctx, multipartTimeout)
	defer cancel()

	for attempt := 0; ; attempt++ {
		req, err := c.newMultipartRequest(ctx, target, fields, fileField, filePath)
		if err != nil {
			return nil, nil, err
		}

		resp, err := uploadHTTP.Do(req)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, nil, timeoutErr(filePath, multipartTimeout)
			}
			return nil, nil, fmt.Errorf("calling %s: %w", target, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			if err := sleep(ctx, wait); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, nil, timeoutErr(filePath, multipartTimeout)
				}
				return nil, nil, err
			}
			continue
		}

		raw, data, err := readEnvelope(resp, target)
		_ = resp.Body.Close()
		if err != nil {
			return nil, nil, err
		}
		return raw, data, nil
	}
}

// newMultipartRequest opens the file and build a request whose body is
// produced on the fly: a goroutine writes the form fields and file through a
// pipe as the HTTP transport reads it, so the file is never held in memory.
func (c *Client) newMultipartRequest(ctx context.Context, target string, fields map[string]string, fileField, filePath string) (*http.Request, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer func() { _ = f.Close() }()

		// Sorted so the body is deterministic: map iteration is randomised,
		// and stable bytes make failures reproducible.
		names := make([]string, 0, len(fields))
		for k := range fields {
			names = append(names, k)
		}
		sort.Strings(names)

		for _, k := range names {
			if err := mw.WriteField(k, fields[k]); err != nil {
				pw.CloseWithError(err)
				return
			}
		}

		part, err := mw.CreateFormFile(fileField, filepath.Base(filePath))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, f); err != nil {
			pw.CloseWithError(err)
			return
		}
		// Writes the closing boundary; without it the server sees a truncated form.
		pw.CloseWithError(mw.Close())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, pr)
	if err != nil {
		_ = pr.Close() // unblocks the writer goroutine so it exits
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// CreateWithEnvelope posts a body and returns the created record alongside the
// untouched envelope. For a create whose record the command acts on before
// deciding what to print: reserving an upload is spent by the PUT that follows,
// so --json cannot fetch the envelope a second time.
func CreateWithEnvelope[T any](ctx context.Context, c *Client, path string, body any) (T, []byte, error) {
	var zero T

	raw, data, err := c.doRaw(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return zero, nil, err
	}
	created, err := first[T](data, path)
	if err != nil {
		return zero, nil, err
	}
	return created, raw, nil
}

// PutPresigned sends a file to a presigned upload URL, bounded by presignedTimeout.
//
// Deliberately not a Client method: the signature embedded in the URL is the
// only credential, and attaching the bearer token to a third-party storage host
// would leak it. It is also not retried - a presigned URL is single-use and
// expires after 15 minutes, so a fresh reservation is the correct recovery,
// not a replay.
//
// headers is the set the reservation listed. Every one of them is part of the
// signature, so all are sent; Content-Type alone defaults when absent.
func PutPresigned(ctx context.Context, url string, headers map[string]string, filePath string) error {
	ctx, cancel := context.WithTimeout(ctx, presignedTimeout)
	defer cancel()

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("sizing %s: %w", filePath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return fmt.Errorf("uploading %s: %w", filepath.Base(filePath), withoutPresignedURL(err))
	}
	// Ours first, so a signed header of the same name would still win.
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "text/csv")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Storage rejects a chunked PUT, so the length must be known up front.
	req.ContentLength = info.Size()

	resp, err := uploadHTTP.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return timeoutErr(filePath, presignedTimeout)
		}
		return fmt.Errorf("uploading %s: %w", filepath.Base(filePath), withoutPresignedURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("upload rejected with %d: %s", resp.StatusCode, msg)
	}
	return nil
}
