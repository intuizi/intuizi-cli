package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// budget429 answers the way the build budget does: a 429 from inside the
// route, carrying Retry-After, plus the X-RateLimit-Limit and -Remaining the
// throttle middleware adds to every response on its way out - but not the
// X-RateLimit-Reset it sends only on its own 429.
func budget429(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("X-RateLimit-Limit", "30")
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"status":"error","code":429,"message":"Your organization has reached its build limit.","data":[]}`))
}

// limiter429 answers the way the per-minute rate limiter does: before the
// route runs, with X-RateLimit-Reset beside Retry-After.
func limiter429(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("X-RateLimit-Limit", "30")
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("X-RateLimit-Reset", "1790000000")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"status":"error","code":429,"message":"Too many requests. Please retry after the time indicated by the Retry-After header.","data":[]}`))
}

// Retry-After as the server sent it, uncapped: whether to wait is decided
// from the real value, and only the wait itself is bounded.
func TestRetryAfterIsReadUncapped(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
		known  bool
	}{
		{"", fallbackRetryIn, false},
		{"abc", fallbackRetryIn, false},
		{"0", fallbackRetryIn, false},
		{" 7 ", 7 * time.Second, true},
		{"3600", time.Hour, true},
	}
	for _, c := range cases {
		got, known := retryAfter(c.header)
		if got != c.want || known != c.known {
			t.Errorf("retryAfter(%q) = %v, %v; want %v, %v", c.header, got, known, c.want, c.known)
		}
	}
}

// Every retry wait is said on stderr: a silent minute reads as a hang.
func TestRetry429IsAnnounced(t *testing.T) {
	recordSleeps(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits <= 2 {
			limiter429(w, "3")
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[{"id":5}]}`))
	}))
	defer srv.Close()

	var warn bytes.Buffer
	c := New(srv.URL, "t")
	c.Warn = &warn
	if err := c.Get(context.Background(), "/anything", nil, nil); err != nil {
		t.Fatalf("get after retries: %v", err)
	}
	want := "rate limited (429): retrying in 3s (retry 1 of 2)\n" +
		"rate limited (429): retrying in 3s (retry 2 of 2)\n"
	if warn.String() != want {
		t.Fatalf("stderr = %q, want %q", warn.String(), want)
	}
}

// A Retry-After longer than the CLI waits is a sure second refusal, so the
// 429 is returned at once, naming the wait the server asked for.
func TestRetry429BeyondTheCapIsReturnedAtOnce(t *testing.T) {
	waits := recordSleeps(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		budget429(w, "1800")
	}))
	defer srv.Close()

	var warn bytes.Buffer
	c := New(srv.URL, "t")
	c.Warn = &warn
	err := c.Post(context.Background(), "/analyses/audiences/create", map[string]string{"name": "a"}, nil)

	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want the 429 back, got %v", err)
	}
	if hits != 1 || len(*waits) != 0 {
		t.Fatalf("hits = %d, waits = %v; a 30-minute Retry-After must not be retried", hits, *waits)
	}
	if apiErr.RetryAfter != 30*time.Minute {
		t.Fatalf("RetryAfter = %v, want 30m", apiErr.RetryAfter)
	}
	if !strings.Contains(err.Error(), "(429, Retry-After: 1800s)") {
		t.Fatalf("error = %q, want it to name the Retry-After", err)
	}
	if warn.Len() != 0 {
		t.Fatalf("stderr = %q; nothing was retried, so nothing should say so", warn.String())
	}
}

// The limiter's own message points at a header the user never sees; the
// error names its value, on the 429 that ends the retries too.
func TestExhausted429NamesRetryAfter(t *testing.T) {
	recordSleeps(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		limiter429(w, "12")
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	c.Warn = nil
	err := c.Get(context.Background(), "/anything", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "(429, Retry-After: 12s)") {
		t.Fatalf("error = %v, want it to name the Retry-After", err)
	}
}

// A create that claims an upload reference is not retried after a 429 that
// came from inside the route, as the build budget's does: the refused attempt
// may already have used the reference up, and a retry would then fail with
// "already been used", hiding the refusal. The limiter's 429 is answered
// before the route runs, so it is retried as usual.
func TestRetry429KeepsAnUploadReference(t *testing.T) {
	bodies := []struct {
		name string
		body any
	}{
		{"flags", map[string]any{"name": "Q3", "upload_reference": "upl_abc"}},
		{"file", json.RawMessage(`{"name":"Q3","upload_reference":"upl_abc"}`)},
	}
	for _, b := range bodies {
		t.Run(b.name+"/budget", func(t *testing.T) {
			waits := recordSleeps(t)
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				budget429(w, "5")
			}))
			defer srv.Close()

			var warn bytes.Buffer
			c := New(srv.URL, "t")
			c.Warn = &warn
			err := c.Post(context.Background(), "/analyses/cohorts/create", b.body, nil)

			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("want the 429 back, got %v", err)
			}
			if hits != 1 || len(*waits) != 0 {
				t.Fatalf("hits = %d, waits = %v; the reference must not be claimed twice", hits, *waits)
			}
			for _, want := range []string{"build limit", "Retry-After: 5s", "upload reference", "upload the file again"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
			if len(apiErr.Body) == 0 {
				t.Error("the error envelope must survive for --json")
			}
		})

		t.Run(b.name+"/limiter", func(t *testing.T) {
			waits := recordSleeps(t)
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				if hits == 1 {
					limiter429(w, "2")
					return
				}
				_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[{"id":7}]}`))
			}))
			defer srv.Close()

			c := New(srv.URL, "t")
			c.Warn = nil
			if err := c.Post(context.Background(), "/analyses/cohorts/create", b.body, nil); err != nil {
				t.Fatalf("a limiter 429 is safe to retry: %v", err)
			}
			if hits != 2 || len(*waits) != 1 || (*waits)[0] != 2*time.Second {
				t.Fatalf("hits = %d, waits = %v; want one 2s retry", hits, *waits)
			}
		})
	}

	// No reference in the body: a route 429 is retried as before.
	t.Run("no reference", func(t *testing.T) {
		waits := recordSleeps(t)
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			if hits == 1 {
				budget429(w, "4")
				return
			}
			_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[{"id":7}]}`))
		}))
		defer srv.Close()

		c := New(srv.URL, "t")
		c.Warn = nil
		body := map[string]any{"name": "Q3", "file_uri": "s3://b/q3.csv"}
		if err := c.Post(context.Background(), "/analyses/cohorts/create", body, nil); err != nil {
			t.Fatalf("post: %v", err)
		}
		if hits != 2 || len(*waits) != 1 {
			t.Fatalf("hits = %d, waits = %v; want one retry", hits, *waits)
		}
	})
}

// create-by-file follows the same rules: announced, and not retried past the
// longest wait the CLI takes.
func TestMultipart429Rules(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte("latitude,longitude\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"name": "Store list", "brand_id": "7"}

	t.Run("announced", func(t *testing.T) {
		recordSleeps(t)
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			if hits == 1 {
				limiter429(w, "1")
				return
			}
			_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[{"id":9}]}`))
		}))
		defer srv.Close()

		var warn bytes.Buffer
		c := New(srv.URL, "t")
		c.Warn = &warn
		if _, err := CreateMultipart[map[string]any](context.Background(), c,
			"/my-data/pois/submissions/create-by-file", fields, "locations_file", fp); err != nil {
			t.Fatalf("multipart: %v", err)
		}
		if !strings.Contains(warn.String(), "rate limited (429): retrying in 1s (retry 1 of 2)") {
			t.Fatalf("stderr = %q, want the retry announced", warn.String())
		}
	})

	t.Run("beyond the cap", func(t *testing.T) {
		waits := recordSleeps(t)
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			limiter429(w, "600")
		}))
		defer srv.Close()

		c := New(srv.URL, "t")
		c.Warn = nil
		_, err := CreateMultipart[map[string]any](context.Background(), c,
			"/my-data/pois/submissions/create-by-file", fields, "locations_file", fp)
		if err == nil || !strings.Contains(err.Error(), "Retry-After: 600s") {
			t.Fatalf("error = %v, want the 429 with its Retry-After", err)
		}
		if hits != 1 || len(*waits) != 0 {
			t.Fatalf("hits = %d, waits = %v; want no retry", hits, *waits)
		}
	})
}
