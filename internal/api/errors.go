package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ErrUnauthorized is returned for any 401. Callers can test for it with
// errors.Is when they want to distinguish "log in again" from other failures.
var ErrUnauthorized = errors.New("unauthorized")

// Error is a non-2xx response from the API, carrying the v2 error envelope.
type Error struct {
	StatusCode int
	Message    string         // the envelope's "message"
	Errors     map[string]any // the envelope's "errors", when present
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}

	switch e.StatusCode {
	case http.StatusUnauthorized:
		// api tokens are not rotated by logins and survive console logout.
		// They die on expiry (365d), explicit revoke, or a password change.
		return "not authenticated - your API token may have expired, been " +
			"revoked, or been invalidated by a password change; run 'intuizi auth login'"
	case http.StatusNotAcceptable:
		// The v2 routes carry json-only middleware. If this fires, something
		// stripped our Accept header rather than the user doing anything wrong.
		return fmt.Sprintf("server rejected the request as non-JSON (406): %s", msg)
	}

	if len(e.Errors) == 0 {
		return fmt.Sprintf("%s (%d)", msg, e.StatusCode)
	}
	return fmt.Sprintf("%s (%d): %s", msg, e.StatusCode, e.fieldErrors())
}

// Unwrap lets errors.Is(err, ErrUnauthorized) match a 401.
func (e *Error) Unwrap() error {
	if e.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return nil
}

// fieldErrors renders the "errors" map deterministically - Go map iteration is
// randomised, and unstable error text is miserable to test against.
func (e *Error) fieldErrors() string {
	keys := make([]string, 0, len(e.Errors))
	for k := range e.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", k, flatten(e.Errors[k])))
	}
	return strings.Join(parts, "; ")
}

// flatten renders a value that may be a string or a []string, since Laravel
// validation returns arrays while ad-hoc errors return plain strings.
func flatten(v any) string {
	list, ok := v.([]any)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprintf("%v", item))
	}
	return strings.Join(parts, ", ")
}
