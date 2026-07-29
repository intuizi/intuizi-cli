package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiPrefix is prepended to every path, so call sites don't repeat the version.
const apiPrefix = "/api/v2"

// UserAgent is overridden from cmd at startup to include the build version.
var UserAgent = "intuizi-cli"

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// envelope is the shape every v2 response shares. Data is RawMessage because
// its type varies: an object on success, an empty array on the error envelope.
type envelope struct {
	Status  string          `json:"status"`
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Errors  map[string]any  `json:"errors"`
}

// Get issues an authenticated GET and unmarshals data into out.
// Pass nil for out to discard the body.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Post issues an authenticated POST with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	url := c.BaseURL + apiPrefix + path

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}

	// Accept is mandatory: the v2 group is json-only and answers 406 without it.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	return decode(resp, url, out)
}

// decode turns a response into either an *Error or the unmarshalled data field.
func decode(resp *http.Response, url string, out any) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Parse leniently: a proxy can return HTML, and the status code still matters.
	var env envelope
	parsed := json.Unmarshal(raw, &env) == nil

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &Error{StatusCode: resp.StatusCode}
		if parsed {
			apiErr.Message = env.Message
			apiErr.Errors = env.Errors

			// Validation failures nest errors under data instead of at the top
			// level. Data may also be an array or absent, so ignore failures.
			if apiErr.Errors == nil && len(env.Data) > 0 {
				var nested struct {
					Errors map[string]any `json:"errors"`
				}
				if json.Unmarshal(env.Data, &nested) == nil {
					apiErr.Errors = nested.Errors
				}
			}
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if !parsed {
		return fmt.Errorf("malformed JSON response from %s", url)
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("response from %s contained no data field", url)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
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

	var out TokenResult
	body := map[string]string{"email": email, "password": password}

	if err := c.Post(ctx, "/auth/api-token", body, &out); err != nil {
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
