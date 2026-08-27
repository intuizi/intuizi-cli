// Package output renders command results: an aligned table for people, the
// server's own JSON envelope for scripts. The wider UX layer (--quiet, exit
// codes, error formatting) should extend this rather than replace it.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/intuizi/intuizi-cli/internal/api"
)

// Record is one row of a list read. Keys vary by endpoint - {value, text},
// {id, name}, sometimes extra columns - so rows decode generically rather than
// into a struct that would silently drop what it didn't declare.
type Record map[string]any

// UnmarshalJSON decodes numbers as json.Number: a plain map[string]any makes
// them float64, printing a large id as 1.234e+09 and 2.50 as 2.5.
func (r *Record) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return err
	}
	*r = m
	return nil
}

// JSON writes the body as the server sent it, indented. json.Indent is lexical,
// so key order and number formatting survive; re-marshalling would not.
func JSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// Not JSON. Print what arrived - a proxy's HTML error page is evidence.
		buf.Reset()
		buf.Write(raw)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		buf.WriteByte('\n')
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// preferredColumns lead the table; the rest follow alphabetically, so order is
// stable across runs - Go map iteration is randomised.
var preferredColumns = []string{"value", "text", "id", "name"}

// Table writes an aligned table. An empty list writes nothing: the caller says
// "no results" on stderr, keeping a pipe clean.
func Table(w io.Writer, items []Record) error {
	if len(items) == 0 {
		return nil
	}

	cols := columns(items)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, strings.Join(cols, "\t")); err != nil {
		return err
	}
	for _, item := range items {
		cells := make([]string, len(cols))
		for i, col := range cols {
			cells[i] = cell(item[col])
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// columns is the union of every record's keys: rows in one response can differ.
func columns(items []Record) []string {
	seen := make(map[string]bool)
	for _, item := range items {
		for k := range item {
			seen[k] = true
		}
	}

	cols := make([]string, 0, len(seen))
	for _, p := range preferredColumns {
		if seen[p] {
			cols = append(cols, p)
			delete(seen, p)
		}
	}

	rest := make([]string, 0, len(seen))
	for k := range seen {
		rest = append(rest, k)
	}
	sort.Strings(rest)

	return append(cols, rest...)
}

// cell renders one value. Nested values are summarised, not dumped - --json
// shows them in full. Tabs and newlines are flattened or they break alignment.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.NewReplacer("\t", " ", "\n", " ", "\r", "").Replace(t)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []any:
		return fmt.Sprintf("[%d items]", len(t))
	case map[string]any:
		return "{...}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Footer reports pagination. Goes to stderr: commentary, not payload.
func Footer(w io.Writer, pg *api.Pagination) {
	if pg == nil {
		return
	}
	if pg.LastPage <= 1 {
		fmt.Fprintf(w, "%d total\n", pg.Total)
		return
	}
	// Only point at --page while there is a further page to fetch.
	if pg.CurrentPage >= pg.LastPage {
		fmt.Fprintf(w, "page %d of %d, %d total\n", pg.CurrentPage, pg.LastPage, pg.Total)
		return
	}
	fmt.Fprintf(w, "page %d of %d, %d total - use --page to fetch the rest\n",
		pg.CurrentPage, pg.LastPage, pg.Total)
}
