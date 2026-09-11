package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVerifyAll(t *testing.T) {
	// --- headers on a POST, plus the happy path with expires_at ---
	var got http.Header
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"status":"success","code":200,"data":{"token":"abc123","expires_at":"2027-07-29T00:00:00+00:00"}}`))
	}))
	defer ok.Close()

	res, err := MintAPIToken(context.Background(), ok.URL, "a@b.com", "pw")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if res.Token != "abc123" || res.ExpiresAt != "2027-07-29T00:00:00+00:00" {
		t.Fatalf("result: %+v", res)
	}
	if v := got.Get("Content-Type"); v != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", v)
	}
	if v := got.Get("Accept"); v != "application/json" {
		t.Fatalf("Accept = %q", v)
	}
	if got.Get("Context-Type") != "" {
		t.Fatal("bogus Context-Type header still being sent")
	}

	// --- Accept must be present on GETs too (no body) ---
	var getHdr http.Header
	g := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getHdr = r.Header.Clone()
		w.Write([]byte(`{"data":{}}`))
	}))
	defer g.Close()
	c := New(g.URL, "tok")
	var discard map[string]any
	if err := c.Get(context.Background(), "/whatever", nil, &discard); err != nil {
		t.Fatalf("get: %v", err)
	}
	if getHdr.Get("Accept") != "application/json" {
		t.Fatal("Accept missing on GET")
	}
	if getHdr.Get("Authorization") != "Bearer tok" {
		t.Fatalf("Authorization = %q", getHdr.Get("Authorization"))
	}

	// --- 422 cap: must NOT echo the server's revoke-endpoint advice ---
	cap422 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"status":"error","code":422,"message":"API token limit reached (10 active tokens). Revoke existing tokens via POST /api/v2/auth/api-token/revoke and retry.","data":[]}`))
	}))
	defer cap422.Close()
	_, err = MintAPIToken(context.Background(), cap422.URL, "a@b.com", "pw")
	if err == nil {
		t.Fatal("expected 422 error")
	}
	if strings.Contains(err.Error(), "POST /api/v2/auth/api-token/revoke") {
		t.Fatalf("CLI echoed the CI-killing revoke advice: %v", err)
	}
	if !strings.Contains(err.Error(), "My Account > API Tokens") {
		t.Fatalf("422 text: %v", err)
	}

	// --- 422 validation: errors nest under data, and must NOT be mislabelled
	// as the token cap. Body is the real staging response. ---
	val422 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"status":"error","code":422,"message":"Validation error.","data":{"errors":{"email":["The email field is required."],"password":["The password field is required."]}}}`))
	}))
	defer val422.Close()
	_, err = MintAPIToken(context.Background(), val422.URL, "", "")
	if err == nil {
		t.Fatal("expected 422 error")
	}
	if strings.Contains(err.Error(), "limit reached") {
		t.Fatalf("validation 422 mislabelled as the token cap: %v", err)
	}
	if !strings.Contains(err.Error(), "The email field is required.") {
		t.Fatalf("nested data.errors were dropped: %v", err)
	}
	if !strings.Contains(err.Error(), "The password field is required.") {
		t.Fatalf("nested data.errors incomplete: %v", err)
	}

	// --- top-level errors (the middleware shape) still work ---
	top403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"status":"error","code":403,"message":"Missing required request header.","data":[],"errors":{"headers":"X-Requested-With is required."}}`))
	}))
	defer top403.Close()
	err = New(top403.URL, "t").Get(context.Background(), "/x", nil, &discard)
	if err == nil || !strings.Contains(err.Error(), "X-Requested-With is required.") {
		t.Fatalf("top-level errors dropped: %v", err)
	}

	// --- 401 on mint = bad credentials, not the stale-token wording ---
	un := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"status":"error","code":401,"message":"Unauthenticated.","data":[]}`))
	}))
	defer un.Close()
	_, err = MintAPIToken(context.Background(), un.URL, "a@b.com", "wrong")
	if err == nil || !strings.Contains(err.Error(), "check your email and password") {
		t.Fatalf("401 on mint: %v", err)
	}

	// --- 401 elsewhere = the re-login wording, and matches the sentinel ---
	c2 := New(un.URL, "stale")
	err = c2.Get(context.Background(), "/anything", nil, &discard)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal("401 should match ErrUnauthorized")
	}
	if strings.Contains(err.Error(), "replaced by") {
		t.Fatalf("401 text still describes legacy rotation: %v", err)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("401 text: %v", err)
	}

	// --- HTML 502 from a proxy: status wins, no JSON parse error ---
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("<html><head><title>502 Bad Gateway</title></head></html>"))
	}))
	defer bad.Close()
	err = New(bad.URL, "t").Get(context.Background(), "/x", nil, &discard)
	if err == nil || err.Error() != "Bad Gateway (502)" {
		t.Fatalf("502 text: %v", err)
	}
}

func TestReadShapes(t *testing.T) {
	type audience struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	ctx := context.Background()

	// --- single read: the resource arrives wrapped in a one-element array ---
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":42,"name":"Shoppers"}]}`))
	}))
	defer arr.Close()

	got, err := Read[audience](ctx, New(arr.URL, "t"), "/analyses/audiences/42", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != 42 || got.Name != "Shoppers" {
		t.Fatalf("data[0] not unwrapped: %+v", got)
	}

	// --- an empty array is "no such record", not a blank resource ---
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":[]}`))
	}))
	defer empty.Close()

	if _, err := Read[audience](ctx, New(empty.URL, "t"), "/analyses/audiences/9", nil); err == nil {
		t.Fatal("empty data array should error, not yield a zero-valued record")
	}

	// --- paginated list: items plus the pagination block ---
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("per_page"); q != "25" {
			t.Errorf("per_page = %q, want 25", q)
		}
		w.Write([]byte(`{"status":"success","code":200,"data":{"items":[{"id":88,"name":"A"}],` +
			`"pagination":{"current_page":1,"per_page":25,"total":137,"last_page":6}}}`))
	}))
	defer list.Close()

	paged := url.Values{"per_page": {"25"}}
	items, page, err := ReadList[audience](ctx, New(list.URL, "t"), "/analyses/audiences/index", paged)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].ID != 88 {
		t.Fatalf("items: %+v", items)
	}
	if page == nil || page.Total != 137 || page.LastPage != 6 {
		t.Fatalf("pagination: %+v", page)
	}

	// --- flat list: a bare array, no pagination block ---
	flat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":1,"name":"X"},{"id":2,"name":"Y"}]}`))
	}))
	defer flat.Close()

	items, page, err = ReadList[audience](ctx, New(flat.URL, "t"), "/reference/countries", nil)
	if err != nil {
		t.Fatalf("flat list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("flat items: %+v", items)
	}
	if page != nil {
		t.Fatalf("flat read should carry no pagination, got %+v", page)
	}

	// --- crossed wires: neither helper may silently return an empty value ---
	if _, err := Read[audience](ctx, New(list.URL, "t"), "/analyses/audiences/index", paged); err == nil {
		t.Fatal("Read on a paginated route should error, not return a zero record")
	}

	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":{"id":7,"name":"Z"}}`))
	}))
	defer obj.Close()
	if _, _, err := ReadList[audience](ctx, New(obj.URL, "t"), "/x", nil); err == nil {
		t.Fatal("ReadList on a single resource should error, not return an empty slice")
	}
}

// recordSleeps replaces the retry wait with a recorder, so a 429 test asserts
// the requested durations instead of spending them.
func recordSleeps(t *testing.T) *[]time.Duration {
	t.Helper()
	var got []time.Duration
	prev := sleep
	sleep = func(_ context.Context, d time.Duration) error {
		got = append(got, d)
		return nil
	}
	t.Cleanup(func() { sleep = prev })
	return &got
}

func TestRetry429(t *testing.T) {
	// --- header parsing rules, no server needed ---
	if d := retryAfter(""); d != fallbackRetryIn {
		t.Errorf("empty header: %v, want %v", d, fallbackRetryIn)
	}
	if d := retryAfter("abc"); d != fallbackRetryIn {
		t.Errorf("garbage header: %v, want %v", d, fallbackRetryIn)
	}
	if d := retryAfter(" 7 "); d != 7*time.Second {
		t.Errorf("7s header: %v", d)
	}
	if d := retryAfter("3600"); d != maxRetryIn {
		t.Errorf("huge header not capped: %v", d)
	}

	waits := recordSleeps(t)

	// --- two 429s then success: the third attempt must carry the body intact ---
	var (
		hits   int
		bodies []string
		keys   []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if hits <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			w.Write([]byte(`{"status":"error","code":429,"message":"Too many requests.","data":[]}`))
			return
		}
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":5}]}`))
	}))
	defer srv.Close()

	var out []map[string]any
	err := New(srv.URL, "t").Post(context.Background(), "/analyses/audiences/create",
		map[string]string{"name": "a"}, &out)

	if err != nil {
		t.Fatalf("post after retries: %v", err)
	}
	if hits != 3 {
		t.Fatalf("hits = %d, want 3 (initial + 2 retries)", hits)
	}
	want := `{"name":"a"}`
	for i, b := range bodies {
		if b != want {
			t.Fatalf("attempt %d body = %q, want %q (drained reader reused?)", i+1, b, want)
		}
	}
	if len(keys[0]) != 36 {
		t.Fatalf("Idempotency-Key = %q, want a UUID", keys[0])
	}
	if keys[1] != keys[0] || keys[2] != keys[0] {
		t.Fatalf("key changed across retries: %v", keys)
	}
	if len(*waits) != 2 || (*waits)[0] != time.Second || (*waits)[1] != time.Second {
		t.Fatalf("waits = %v; Retry-After of 1s twice should request two 1s sleeps", *waits)
	}
	*waits = nil

	// --- exhaustion: a third 429 is returned to the caller, not retried again ---
	always := 0
	burn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		always++
		if k := r.Header.Get("Idempotency-Key"); k != "" {
			t.Errorf("Idempotency-Key on a non-create route: %q", k)
		}
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		w.Write([]byte(`{"status":"error","code":429,"message":"Too many requests.","data":[]}`))
	}))
	defer burn.Close()

	var discard map[string]any
	err = New(burn.URL, "t").Get(context.Background(), "/anything", nil, &discard)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Fatalf("exhausted retries should surface the 429: %v", err)
	}
	if always != 3 {
		t.Fatalf("always = %d, want exactly 3 attempts", always)
	}
	if len(*waits) != 2 {
		t.Fatalf("waits = %v; two retries should request two sleeps", *waits)
	}
}

// The real wait, not the recorder: Ctrl-C during a Retry-After must abort
// promptly rather than run the timer out.
func TestSleepHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := waitFor(ctx, maxRetryIn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait should return context.Canceled, got: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancel took %v; sleep is not honouring the context", time.Since(start))
	}
	if err := waitFor(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("an uncancelled wait should return nil, got %v", err)
	}
}

func TestErrorWording(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   string
	}{
		{403, "Forbidden.", "permission denied"},
		{404, "Not found.", "not found"},
		{409, "Conflict.", "Idempotency-Key"},
	}
	for _, c := range cases {
		e := &Error{StatusCode: c.status, Message: c.msg}
		if !strings.Contains(e.Error(), c.want) {
			t.Errorf("%d text = %q, want it to mention %q", c.status, e.Error(), c.want)
		}
	}
}

func TestCreateMultipart(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte("latitude,longitude\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	waits := recordSleeps(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if k := r.Header.Get("Idempotency-Key"); k != "" {
			t.Errorf("Idempotency-Key on create-by-file: %q", k)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("name"); got != "Store list" {
			t.Errorf("name = %q", got)
		}
		if got := r.FormValue("update"); got != "1" {
			t.Errorf("update = %q, want the string 1", got)
		}
		file, hdr, err := r.FormFile("locations_file")
		if err != nil {
			t.Errorf("file part: %v", err)
		} else {
			b, _ := io.ReadAll(file)
			file.Close()
			if hdr.Filename != "locations.csv" {
				t.Errorf("filename = %q", hdr.Filename)
			}
			if string(b) != "latitude,longitude\n1,2\n" {
				t.Errorf("file content = %q (empty means the body was not re-streamed)", b)
			}
		}
		if hits == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			w.Write([]byte(`{"status":"error","code":429,"message":"Too many requests.","data":[]}`))
			return
		}
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":9}]}`))
	}))
	defer srv.Close()

	out, err := CreateMultipart[map[string]any](context.Background(), New(srv.URL, "t"),
		"/my-data/pois/submissions/create-by-file",
		map[string]string{"name": "Store list", "brand_id": "7", "update": "1"},
		"locations_file", fp)
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (429 then success)", hits)
	}
	if len(*waits) != 1 || (*waits)[0] != time.Second {
		t.Fatalf("waits = %v; one Retry-After of 1s should request one 1s sleep", *waits)
	}
	// The envelope wraps the new record in an array; the caller gets the record.
	if out["id"] != float64(9) {
		t.Fatalf("out: %+v", out)
	}
}

// A 30s client timeout would be a ceiling on transfer time: a 50 MB POI file at
// 15 Mbit/s is already ~27s.
func TestMultipartIgnoresTheJSONClientTimeout(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte("latitude,longitude\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Stands in for a large file on a slow link.
		time.Sleep(80 * time.Millisecond)
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":9}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	c.HTTP.Timeout = 10 * time.Millisecond

	if _, err := CreateMultipart[map[string]any](context.Background(), c,
		"/my-data/pois/submissions/create-by-file",
		map[string]string{"name": "Store list", "brand_id": "7"},
		"locations_file", fp); err != nil {
		t.Fatalf("multipart failed under a short JSON timeout: %v", err)
	}
}

// Auth returns an object today; the array case is what first[T] buys.
func TestMintAPITokenTakesEitherEnvelopeShape(t *testing.T) {
	cases := []struct{ name, data string }{
		{"object, as auth returns today", `{"token":"abc123","expires_at":"2027-07-29T00:00:00+00:00"}`},
		{"one-element array, as every other route returns", `[{"token":"abc123","expires_at":"2027-07-29T00:00:00+00:00"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"status":"success","code":200,"data":` + tc.data + `}`))
			}))
			defer srv.Close()

			got, err := MintAPIToken(context.Background(), srv.URL, "a@example.com", "pw")
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			if got.Token != "abc123" {
				t.Errorf("token = %q, want abc123", got.Token)
			}
			if got.ExpiresAt == "" {
				t.Error("expires_at was dropped")
			}
		})
	}
}

// A CDN sends Retry-After as an HTTP-date (RFC 7231), not delta-seconds. The
// header has one-second resolution, so a +3s date lands anywhere in (2s, 3s].
func TestRetryAfterHTTPDate(t *testing.T) {
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if d := retryAfter(future); d <= 1500*time.Millisecond || d > 3*time.Second {
		t.Errorf("date 3s ahead: %v, want about 3s", d)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d := retryAfter(past); d != 0 {
		t.Errorf("date in the past: %v, want 0 (retry now, not the fallback)", d)
	}

	far := time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)
	if d := retryAfter(far); d != maxRetryIn {
		t.Errorf("date a day ahead not capped: %v", d)
	}
}

// A top-level "errors": [] must not make the whole envelope unparseable: on a
// 422 that would lose the message, on a 200 it would fail as malformed.
func TestEnvelopeToleratesErrorsArray(t *testing.T) {
	ctx := context.Background()
	var discard map[string]any

	val := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"status":"error","code":422,"message":"Validation error.","data":[],"errors":[]}`))
	}))
	defer val.Close()
	err := New(val.URL, "t").Get(ctx, "/x", nil, &discard)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 {
		t.Fatalf("want a 422 *Error, got %v", err)
	}
	if apiErr.Message != "Validation error." {
		t.Errorf("message lost to the errors array: %q", apiErr.Message)
	}
	if !strings.Contains(err.Error(), "Validation error. (422)") {
		t.Errorf("text = %q", err.Error())
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":{"id":7},"errors":[]}`))
	}))
	defer ok.Close()
	if err := New(ok.URL, "t").Get(ctx, "/x", nil, &discard); err != nil {
		t.Fatalf("200 with an empty errors array should decode, got %v", err)
	}
	if discard["id"] != float64(7) {
		t.Errorf("data = %v", discard)
	}

	// null and an object keep working as before.
	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"status":"error","code":403,"message":"Missing header.","data":[],"errors":{"headers":"X-Requested-With is required."}}`))
	}))
	defer obj.Close()
	err = New(obj.URL, "t").Get(ctx, "/x", nil, &discard)
	if err == nil || !strings.Contains(err.Error(), "headers: X-Requested-With is required.") {
		t.Errorf("object errors regressed: %v", err)
	}
	nul := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":{"id":8},"errors":null}`))
	}))
	defer nul.Close()
	if err := New(nul.URL, "t").Get(ctx, "/x", nil, &discard); err != nil {
		t.Errorf("null errors regressed: %v", err)
	}
}

// A 3xx from a proxy or a wrong --base-url must surface, not be followed: a
// same-host hop would forward the bearer token, and a 301 turns a create POST
// into a GET.
func TestRedirectsAreNotFollowed(t *testing.T) {
	var leaked int
	var leakedAuth string
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked++
		leakedAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"status":"success","code":200,"data":[{"id":1}]}`))
	}))
	defer second.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+r.URL.Path, http.StatusFound)
	}))
	defer first.Close()

	var out []map[string]any
	err := New(first.URL, "secret").Post(context.Background(), "/analyses/audiences/create",
		map[string]string{"name": "a"}, &out)

	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusFound {
		t.Fatalf("want a 302 *Error, got %v", err)
	}
	if leaked != 0 {
		t.Fatalf("redirect target was hit %d times (Authorization %q); redirects must not be followed",
			leaked, leakedAuth)
	}
	// "Found (302)" alone is a puzzle; the text should point at the cause.
	if !strings.Contains(err.Error(), "302") || !strings.Contains(err.Error(), "--base-url") {
		t.Errorf("text = %q, want the status and a --base-url hint", err.Error())
	}
	if !strings.Contains(err.Error(), second.URL) {
		t.Errorf("text = %q, want the Location the server named", err.Error())
	}
}

// --json prints the envelope the server sent, and on a failure that has to
// include the error envelope: a script reads 422 field errors from it.
func TestErrorCarriesTheEnvelopeBody(t *testing.T) {
	ctx := context.Background()
	body := `{"status":"error","code":422,"message":"Validation error.",` +
		`"data":{"errors":{"name":["The name field is required."]}}}`
	val := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(body))
	}))
	defer val.Close()

	_, err := New(val.URL, "t").PostRaw(ctx, "/analyses/projects/create", map[string]string{"name": ""})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 {
		t.Fatalf("want a 422 *Error, got %v", err)
	}
	if string(apiErr.Body) != body {
		t.Errorf("Body = %s, want the envelope verbatim", apiErr.Body)
	}

	// Not JSON: nothing a script could parse, so Body stays empty.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("<html>502</html>"))
	}))
	defer bad.Close()
	_, err = New(bad.URL, "t").GetRaw(ctx, "/x", nil)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 {
		t.Fatalf("want a 502 *Error, got %v", err)
	}
	if len(apiErr.Body) != 0 {
		t.Errorf("Body = %s, want empty for a non-JSON response", apiErr.Body)
	}
}

// setKey installs --idempotency-key for one test.
func setKey(t *testing.T, key string) {
	t.Helper()
	prev := IdempotencyKey
	IdempotencyKey = key
	t.Cleanup(func() { IdempotencyKey = prev })
}

// --idempotency-key's help says "on create commands", but only seven routes
// read the header. Silently ignoring it elsewhere breaks the promise.
func TestIdempotencyKeyWarnsOnUnkeyedRoutes(t *testing.T) {
	ctx := context.Background()
	setKey(t, "abc-123")

	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Write([]byte(`{"status":"success","code":200,"data":[]}`))
	}))
	defer srv.Close()

	// Unkeyed POST: one warning, however many times the client posts.
	c := New(srv.URL, "t")
	var warn strings.Builder
	c.Warn = &warn
	for range 2 {
		if err := c.Post(ctx, "/analyses/cohorts/preview", map[string]string{"a": "b"}, nil); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	want := "--idempotency-key has no effect on /analyses/cohorts/preview"
	if got := warn.String(); strings.Count(got, want) != 1 {
		t.Errorf("warn = %q, want exactly one %q", got, want)
	}

	// Reads stay quiet: a --wait create polls with GETs after its keyed POST.
	c = New(srv.URL, "t")
	warn.Reset()
	c.Warn = &warn
	if err := c.Get(ctx, "/analyses/audiences/1", nil, nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("a GET warned: %q", warn.String())
	}

	// Keyed route: quiet, and the header carries the chosen key.
	c = New(srv.URL, "t")
	warn.Reset()
	c.Warn = &warn
	if err := c.Post(ctx, "/analyses/projects/create", map[string]string{"name": "x"}, nil); err != nil {
		t.Fatalf("post: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("a keyed create warned: %q", warn.String())
	}
	if last := keys[len(keys)-1]; last != "abc-123" {
		t.Errorf("Idempotency-Key = %q, want abc-123", last)
	}

	// No writer: no output and no panic.
	c = New(srv.URL, "t")
	c.Warn = nil
	if err := c.Post(ctx, "/analyses/cohorts/preview", map[string]string{"a": "b"}, nil); err != nil {
		t.Fatalf("post with nil Warn: %v", err)
	}
}

// When a keyed create dies in transit its outcome is unknown, and the only
// safe retry is one that reuses the key - which the user never saw when it
// was generated.
func TestKeyedPostTransportErrorNamesTheKey(t *testing.T) {
	ctx := context.Background()

	// Guarded: with no response there is no happens-before between the handler
	// and the test goroutine, and the race detector notices.
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		// Drop the connection without answering: the request may have landed.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	var warn strings.Builder
	c.Warn = &warn
	err := c.Post(ctx, "/analyses/audiences/create", map[string]string{"name": "a"}, nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		t.Fatalf("a dropped connection is not an HTTP status, got %v", err)
	}
	mu.Lock()
	sent := append([]string(nil), seen...)
	mu.Unlock()
	if len(sent) == 0 || len(sent[0]) != 36 {
		t.Fatalf("server saw keys %v, want one generated UUID", sent)
	}
	want := "Idempotency-Key used: " + sent[0] + "; pass --idempotency-key " + sent[0] + " to retry safely"
	if !strings.Contains(warn.String(), want) {
		t.Errorf("warn = %q, want %q", warn.String(), want)
	}

	// An unkeyed POST has nothing to retry with, so no hint.
	c = New(srv.URL, "t")
	warn.Reset()
	c.Warn = &warn
	if err := c.Post(ctx, "/analyses/cohorts/preview", map[string]string{"a": "b"}, nil); err == nil {
		t.Fatal("expected a transport error")
	}
	if warn.Len() != 0 {
		t.Errorf("an unkeyed POST printed a key hint: %q", warn.String())
	}
}
