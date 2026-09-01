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
	cap422 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	val422 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	top403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"status":"error","code":403,"message":"Missing required request header.","data":[],"errors":{"headers":"X-Requested-With is required."}}`))
	}))
	defer top403.Close()
	err = New(top403.URL, "t").Get(context.Background(), "/x", nil, &discard)
	if err == nil || !strings.Contains(err.Error(), "X-Requested-With is required.") {
		t.Fatalf("top-level errors dropped: %v", err)
	}

	// --- 401 on mint = bad credentials, not the stale-token wording ---
	un := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	flat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","code":200,"data":{"id":7,"name":"Z"}}`))
	}))
	defer obj.Close()
	if _, _, err := ReadList[audience](ctx, New(obj.URL, "t"), "/x", nil); err == nil {
		t.Fatal("ReadList on a single resource should error, not return an empty slice")
	}
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

	start := time.Now()
	var out []map[string]any
	err := New(srv.URL, "t").Post(context.Background(), "/analyses/audiences/create",
		map[string]string{"name": "a"}, &out)
	elapsed := time.Since(start)

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
	if elapsed < 2*time.Second {
		t.Fatalf("elapsed = %v; Retry-After of 1s twice should wait at least 2s", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("elapsed = %v; waited far longer than the two 1s Retry-After values", elapsed)
	}

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

	// --- cancellation: Ctrl-C during the wait must abort promptly ---
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	err = New(burn.URL, "t").Get(ctx, "/anything", nil, &discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait should return context.Canceled, got: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancel took %v; sleep is not honouring the context", time.Since(start))
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
	// The envelope wraps the new record in an array; the caller gets the record.
	if out["id"] != float64(9) {
		t.Fatalf("out: %+v", out)
	}
}
