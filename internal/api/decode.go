package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type Pagination struct {
	CurrentPage int `json:"current_page"`
	PerPage     int `json:"per_page"`
	Total       int `json:"total"`
	LastPage    int `json:"last_page"`
}

func Read[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	var zero T

	raw, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return zero, err
	}
	return first[T](raw, path)
}

// Create posts a body and decodes the created resource out of data.
func Create[T any](ctx context.Context, c *Client, path string, body any) (T, error) {
	var zero T

	raw, err := c.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return zero, err
	}
	return first[T](raw, path)
}

// ReadList fetches a collection. The returned Pagination is nil on a flat read.
func ReadList[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, *Pagination, error) {
	raw, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, nil, err
	}

	switch shape(raw) {
	case '[':
		var items []T
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, nil, fmt.Errorf("decoding response data: %w", err)
		}
		return items, nil, nil
	case '{':
		var page struct {
			Items      []T         `json:"items"`
			Pagination *Pagination `json:"pagination"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, nil, fmt.Errorf("decoding response data: %w", err)
		}
		// Neither key present means this is a single resource, not a list. Go
		// ignores unknown fields, so without this check the caller would get an
		// empty slice and no error.
		if page.Items == nil && page.Pagination == nil {
			return nil, nil, fmt.Errorf("data from %s is a single resource, not a list; use Read", path)
		}
		return page.Items, page.Pagination, nil
	}
	return nil, nil, fmt.Errorf("unexpected data shape from %s", path)
}

// first pulls one record out of the data field, accepting either an object or a
// one-element array.
func first[T any](raw json.RawMessage, path string) (T, error) {
	var zero T

	switch shape(raw) {
	case '{':
		// A paginated wrapper is an object too. Decoding one into T would ignore
		// the unknown items/pagination keys and hand back a zero-valued record
		// with no error, so name the mistake instead.
		var probe struct {
			Items      json.RawMessage `json:"items"`
			Pagination json.RawMessage `json:"pagination"`
		}
		if json.Unmarshal(raw, &probe) == nil && (probe.Items != nil || probe.Pagination != nil) {
			return zero, fmt.Errorf("data from %s is a paginated list, not a single resource; use ReadList", path)
		}
		var out T
		if err := json.Unmarshal(raw, &out); err != nil {
			return zero, fmt.Errorf("decoding response data: %w", err)
		}
		return out, nil

	case '[':
		var list []T
		if err := json.Unmarshal(raw, &list); err != nil {
			return zero, fmt.Errorf("decoding response data: %w", err)
		}
		// An empty array means the id does not exist for this account. Returning
		// the zero value would look like a record whose every field is unset.
		if len(list) == 0 {
			return zero, fmt.Errorf("no record returned by %s", path)
		}
		return list[0], nil
	}
	return zero, fmt.Errorf("unexpected data shape from %s", path)
}

// shape returns the first meaningful byte of a JSON value:'{' for an object,
// '[' for an array, or 0 when there is nothing to decode.
func shape(raw json.RawMessage) byte {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return 0
	}
	return trimmed[0]
}
