package output

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intuizi/intuizi-cli/internal/api"
)

func decode(t *testing.T, body string) []Record {
	t.Helper()

	var items []Record
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	return items
}

func TestTableValueTextShape(t *testing.T) {
	items := decode(t, `[{"value":"US","text":"United States (US)"}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want a header and one row:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "value") || !strings.Contains(lines[0], "text") {
		t.Errorf("header = %q, want value before text", lines[0])
	}
	if !strings.Contains(lines[1], "United States (US)") {
		t.Errorf("row = %q", lines[1])
	}
}

// Some reads return {id, name}; a typed struct would render blank rows.
func TestTableIDNameShape(t *testing.T) {
	items := decode(t, `[{"id":9,"name":"CPM","price":"2.50"}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}

	header := strings.Fields(strings.Split(out.String(), "\n")[0])
	want := []string{"id", "name", "price"}
	for i := range want {
		if header[i] != want[i] {
			t.Errorf("header = %v, want %v", header, want)
			break
		}
	}
	if !strings.Contains(out.String(), "2.50") {
		t.Errorf("price lost its trailing zero:\n%s", out.String())
	}
}

// float64 would print this id as 1.234567891e+09.
func TestTableKeepsLargeIntegersExact(t *testing.T) {
	items := decode(t, `[{"value":1234567891,"text":"big"}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1234567891") {
		t.Errorf("id was reformatted:\n%s", out.String())
	}
}

func TestTableColumnsAreStableAcrossRuns(t *testing.T) {
	body := `[{"value":"latitude","text":"Latitude","group":"geoLocation","group_text":"Geo-Location"}]`

	var first string
	for i := 0; i < 20; i++ {
		var out strings.Builder
		if err := Table(&out, decode(t, body)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.String()
			continue
		}
		if out.String() != first {
			t.Fatalf("column order changed between runs:\n%s\n---\n%s", first, out.String())
		}
	}
	header := strings.Fields(strings.Split(first, "\n")[0])
	want := []string{"value", "text", "group", "group_text"}
	for i := range want {
		if header[i] != want[i] {
			t.Errorf("header = %v, want %v", header, want)
			break
		}
	}
}

// A column present on only some rows still appears, empty where absent.
func TestTableUnionOfKeys(t *testing.T) {
	items := decode(t, `[{"value":1,"text":"a"},{"value":2,"text":"b","category_id":7}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "category_id") {
		t.Errorf("column from the second row is missing:\n%s", out.String())
	}
}

// Nested values are summarised, not dumped.
func TestTableSummarisesNestedValues(t *testing.T) {
	items := decode(t, `[{"id":12,"name":"Acme","fields":[{"name":"Account ID"},{"name":"Creds"}],"meta":{"a":1}}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[2 items]") {
		t.Errorf("nested array not summarised:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "{...}") {
		t.Errorf("nested object not summarised:\n%s", out.String())
	}
}

func TestTableFlattensEmbeddedTabsAndNewlines(t *testing.T) {
	items := decode(t, `[{"value":"a","text":"one\ttwo\nthree"}]`)

	var out strings.Builder
	if err := Table(&out, items); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(out.String()), "\n"); lines != 1 {
		t.Errorf("a newline in a value broke the table into %d rows:\n%s", lines+1, out.String())
	}
}

func TestTableEmptyWritesNothing(t *testing.T) {
	var out strings.Builder
	if err := Table(&out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" {
		t.Errorf("wrote %q for an empty list, want nothing so a pipe stays clean", out.String())
	}
}

func TestJSONPreservesKeyOrderAndNumbers(t *testing.T) {
	raw := []byte(`{"status":"success","code":200,"data":[{"value":2.50,"id":1234567891}]}`)

	var out strings.Builder
	if err := JSON(&out, raw); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	// Indented, otherwise byte-for-byte what the server sent.
	if !strings.Contains(got, "2.50") {
		t.Errorf("number was reformatted:\n%s", got)
	}
	if strings.Index(got, `"status"`) > strings.Index(got, `"code"`) {
		t.Errorf("key order changed:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("output should end in a newline")
	}
}

func TestJSONPassesThroughNonJSON(t *testing.T) {
	var out strings.Builder
	if err := JSON(&out, []byte("<html>502 Bad Gateway</html>")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "502 Bad Gateway") {
		t.Errorf("a non-JSON body should be shown, not swallowed: %q", out.String())
	}
}

func TestFooter(t *testing.T) {
	tests := []struct {
		name string
		pg   *api.Pagination
		want string
	}{
		{name: "flat read says nothing", pg: nil, want: ""},
		{
			name: "a further page points at --page",
			pg:   &api.Pagination{CurrentPage: 1, LastPage: 15, Total: 7374},
			want: "page 1 of 15, 7374 total - use --page to fetch the rest\n",
		},
		{
			name: "last page does not advertise pages that do not exist",
			pg:   &api.Pagination{CurrentPage: 15, LastPage: 15, Total: 7374},
			want: "page 15 of 15, 7374 total\n",
		},
		{
			name: "single page needs no hint",
			pg:   &api.Pagination{CurrentPage: 1, LastPage: 1, Total: 3},
			want: "3 total\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			Footer(&out, tc.pg)
			if out.String() != tc.want {
				t.Errorf("Footer = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

// Rows with no keys used to render as nothing at all: no header, no error,
// exit 0, and a script none the wiser that the response was empty objects.
func TestTableRejectsRowsWithNoFields(t *testing.T) {
	items := decode(t, `[{},{}]`)

	var out strings.Builder
	err := Table(&out, items)
	if err == nil || !strings.Contains(err.Error(), "no fields") {
		t.Fatalf("err = %v, want the rows reported as carrying no fields", err)
	}
	if out.String() != "" {
		t.Errorf("wrote %q alongside the error", out.String())
	}
	// Explicit columns are the same contract.
	if err := TableWith(&out, decode(t, `[{"a":1}]`), nil); err == nil {
		t.Error("TableWith with rows but no columns should fail, not print nothing")
	}
}
