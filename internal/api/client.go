package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// apiPrefix is prepended to every path, so call sites don't repeat the version.
const apiPrefix = "/api/v2"

// UserAgent is overridden from cmd at startup to include the build version.
var UserAgent = "intuizi-cli"

// Retry policy for 429s. The write bucket allows 30 requests/min and reads 120,
// so a rate-limited CLI is nearly always with waiting out rather than failing.
const (
	maxRetries      = 2
	fallbackRetryIn = 5 * time.Second
	maxRetryIn      = 60 * time.Second
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	// Warn receives commentary about the Idempotency-Key: that --idempotency-key
	// had no effect on a route, or which key a create that died in transit was
	// sent with. Stderr by default; nil silences it.
	Warn io.Writer

	warnedKey bool // the no-effect warning is said once per client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Warn:    os.Stderr,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			// Never follow a redirect: a same-host hop would forward the bearer
			// token, and a 301/302/303 turns a create POST into a GET. A 3xx
			// surfaces as an *Error instead, which almost always means --base-url
			// is wrong (http for https, or a missing path prefix).
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// envelope is the shape every v2 response shares. Data is RawMessage because
// its type varies: an object on success, an empty array on the error envelope.
// Errors is too: it is an object when present, but the framework also emits []
// and null, and a typed map would sink the whole envelope on those.
type envelope struct {
	Status  string          `json:"status"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Errors  json.RawMessage `json:"errors"`
}

// errorMap decodes the errors field leniently: anything but an object is nil.
func (e *envelope) errorMap() map[string]any {
	var m map[string]any
	if json.Unmarshal(e.Errors, &m) != nil {
		return nil
	}
	return m
}

// Get issues an authenticated GET and unmarshals the envelope's data into out.
// Pass a nil query for no parameters, and nil out to discard the body.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	raw, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return unmarshalData(raw, path, out)
}

// GetRaw returns the whole response body rather than the decoded data field.
// --json must print the envelope the server actually sent; re-marshalling the
// decoded value would reorder keys and reformat numbers.
func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	raw, _, err := c.doRaw(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// PostRaw returns the whole response body rather than the decoded data field,
// so a create can print the same envelope --json prints for a read. Without it
// a create would print a bare data array and a read a full envelope, and a
// script could not parse both the same way.
func (c *Client) PostRaw(ctx context.Context, path string, body any) ([]byte, error) {
	raw, _, err := c.doRaw(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// Post issues an authenticated POST with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	raw, err := c.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return err
	}
	return unmarshalData(raw, path, out)
}

// do returns just the envelope's data field, which is all most callers want.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	_, data, err := c.doRaw(ctx, method, path, query, body)
	return data, err
}

// doRaw sends one request and returns both the untouched response body and the
// envelope's data field, so callers can decode data as an object, a bare array
// or a paginated wrapper - or hand the body straight to --json.
func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, body any) ([]byte, json.RawMessage, error) {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding request body: %w", err)
		}
		payload = encoded
	}

	// One key per logical create, chosen before the retry loop so every attempt
	// carries the same logical key. That is what makes the 429 retry safe on creates:
	// only 2xx responses are stored server-side, so the retry either replays the
	// original success or re-executes a create that never ran.
	var key string
	if isIdempotent(path) {
		key = nextIdempotencyKey()
	} else if method == http.MethodPost && IdempotencyKey != "" && !c.warnedKey {
		// The flag's help says "on create commands"; say so when the route is
		// not one of the seven that read the header, rather than ignoring it.
		c.warnedKey = true
		c.warnf("--idempotency-key has no effect on %s", path)
	}

	target := c.BaseURL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		// Rebuilt every attempt: a request body reader is consumed by the first
		// send, so retrying the same *http.Request would post an empty body.
		req, err := c.newRequest(ctx, method, target, key, payload)
		if err != nil {
			return nil, nil, err
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if key != "" {
				// No status came back, so the create may or may not have run.
				// Only a retry with the same key is safe, and the user never
				// saw a generated one.
				c.warnf("Idempotency-Key used: %s; pass --idempotency-key %s to retry safely", key, key)
			}
			return nil, nil, fmt.Errorf("calling %s: %w", target, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			// Drain a little before closing so the connection can be reused.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			if err := sleep(ctx, wait); err != nil {
				return nil, nil, err
			}
			continue
		}

		raw, data, err := readEnvelope(resp, target)
		_ = resp.Body.Close()
		return raw, data, err
	}
}

// retryAfter reads the Retry-After header, which the API sends as seconds and
// a CDN in front of it may send as an HTTP-date. An absent or unparseable value
// falls back to a fixed wait rather than retrying immediately, and an absurd
// one is capped so the CLI cannot hang for minutes.
func retryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if sec, err := strconv.Atoi(header); err == nil {
		if sec <= 0 {
			return fallbackRetryIn
		}
		return min(time.Duration(sec)*time.Second, maxRetryIn)
	}
	if t, err := http.ParseTime(header); err == nil {
		// A date already past means retry now, not the fallback.
		return min(max(time.Until(t), 0), maxRetryIn)
	}
	return fallbackRetryIn
}

// warnf writes one line of commentary to Warn, if there is one.
func (c *Client) warnf(format string, args ...any) {
	if c.Warn == nil {
		return
	}
	_, _ = fmt.Fprintf(c.Warn, format+"\n", args...)
}

// sleep is the retry wait. A variable so tests can record the requested
// durations instead of spending them: a Retry-After of 1s would otherwise
// cost every 429 test a real second.
var sleep = waitFor

// waitFor waits for d unless the context is cancelled first - time.Sleep would
// swallow a Ctrl-C for up to a minute.
func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// newRequest builds one attempt. Kept separate from do because a request body
// reader is consumed by the first send, so a retry needs a freshly built request
// over the same bytes.
func (c *Client) newRequest(ctx context.Context, method, target, key string, payload []byte) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}

	// Accept is mandatory: the v2 group is json-only and answers 406 without it.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req, nil
}

// readEnvelope maps a response to either an *Error or the raw data field, and
// returns the whole body alongside it for GetRaw. The body comes back on the
// error paths too, so the four exits stay uniform; callers check err first.
func readEnvelope(resp *http.Response, target string) ([]byte, json.RawMessage, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("reading response: %w", err)
	}

	// parse leniently: a proxy can return HTML, and the status code still matters.
	var env envelope
	parsed := json.Unmarshal(raw, &env) == nil

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &Error{StatusCode: resp.StatusCode}
		if parsed {
			apiErr.Body = raw
			apiErr.Message = env.Message
			apiErr.Errors = env.errorMap()

			// Validation failures nest under data instead of at the top level.
			// Data may also be array or absent so ignore failures.
			if apiErr.Errors == nil && len(env.Data) > 0 {
				var nested struct {
					Errors map[string]any `json:"errors"`
				}
				if json.Unmarshal(env.Data, &nested) == nil {
					apiErr.Errors = nested.Errors
				}
			}
		}
		// A redirect body is empty or HTML; the Location is the useful part.
		if apiErr.Message == "" && resp.StatusCode >= 300 && resp.StatusCode <= 399 {
			apiErr.Message = resp.Header.Get("Location")
		}
		return raw, nil, apiErr
	}

	// A 2xx with no body is a success with nothing to decode, not a malformed response.
	// Nothing in v2 returns 204: this is defensive.
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, nil, nil
	}
	if !parsed {
		return raw, nil, fmt.Errorf("malformed JSON response from %s", target)
	}
	return raw, env.Data, nil
}

func unmarshalData(raw json.RawMessage, path string, out any) error {
	if out == nil {
		return nil
	}
	if len(raw) == 0 {
		return fmt.Errorf("response from %s contained no data field", path)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding response data: %w", err)
	}
	return nil
}

// TokenResult is what the api-token endpoint returns. ExpiresAt may be empty.
type TokenResult struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// MintAPIToken exchanges credentials for a long-lived api token.
//
// Not named Login: the legacy /auth/login rotates a single shared token. api
// tokens don't rotate - they end on expiry, revoke, or password change.
func MintAPIToken(ctx context.Context, baseURL, email, password string) (TokenResult, error) {
	c := New(baseURL, "")

	body := map[string]string{"email": email, "password": password}

	// Create decodes through first[T], which takes either envelope shape. Auth
	// returns an object today; this survives it being normalised to an array.
	out, err := Create[TokenResult](ctx, c, "/auth/api-token", body)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized:
				// Bad credentials, not a stale token - the usual re-login hint won't fit.
				return TokenResult{}, errors.New("login failed: check your email and password")

			case http.StatusUnprocessableEntity:
				// The server's message points at the revoke endpoint, which kills every
				// token on the account including CI's. Point at the UI instead.
				if !strings.Contains(apiErr.Message, "limit reached") {
					return TokenResult{}, err // a different 422 - don't mislabel it
				}
				return TokenResult{}, errors.New(
					"API token limit reached for this account. Revoke an unused token at " +
						"My Account > API Tokens in the console, then try again. Do not use " +
						"the revoke API endpoint - it revokes every token on the account, " +
						"including any used by CI")
			}
		}
		return TokenResult{}, err
	}

	if out.Token == "" {
		return TokenResult{}, errors.New("server returned no token")
	}
	return out, nil
}
