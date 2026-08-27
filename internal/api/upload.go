package api

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PostMultipart issues an multipart/form-data Post to create-by-file, streaming
// the file rather than buffering it (POI files run to 50 MB)
//
// Boolean form fields must be "1" or "0" - API rejects the string "true"
func (c *Client) PostMultipart(ctx context.Context, path string, fields map[string]string, fileField, filePath string, out any) error {
	target := c.BaseURL + apiPrefix + path

	for attempt := 0; ; attempt++ {
		req, err := c.newMultipartRequest(ctx, target, fields, fileField, filePath)
		if err != nil {
			return err
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("calling %s: %w", target, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}

		_, data, err := readEnvelope(resp, target)
		resp.Body.Close()
		if err != nil {
			return err
		}
		return unmarshalData(data, path, out)
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
		defer f.Close()

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
		pr.Close() // unblocks the writer goroutine so it exits
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

// PutPresigned sends a file to a presigned upload URL.
//
// Deliberately not a Client method: the signature embedded in the URL is the
// only credential, and attaching the bearer token to a third-party storage host
// would leak it. It is also not retried - a presigned URL is single-use and
// expires, so a fresh reservation is the correct recovery, not a replay.
func PutPresigned(ctx context.Context, url, contentType, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("sizing %s: %w", filePath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "text/csv"
	}
	req.Header.Set("Content-Type", contentType)
	// Storage rejects a chunked PUT, so the length must be known up front.
	req.ContentLength = info.Size()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading %s: %w", filepath.Base(filePath), err)
	}
	defer resp.Body.Close()

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
