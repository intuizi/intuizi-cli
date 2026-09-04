package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDir holds whole response envelopes captured from the API. Request
// bodies for --file tests belong in a sibling directory: the guard below
// assumes everything here is a response.
const fixtureDir = "testdata/responses"

// fixture returns a captured envelope, for handing to stub as a canned response.
//
// Only realistic multi-field envelopes live in files. Short bodies, and any
// body whose exact values a test asserts on, stay inline where the assertion
// can see them: a golden input tuned to its assertions must not be
// re-captured, and a file invites exactly that. audienceList in runner_test.go
// is one such golden input - it carries a tab inside a name and pagination
// totals that three assertions depend on, and no real response has them.
func fixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(body)
}

// A malformed fixture fails in a confusing place: the command errors on decode
// and the assertion looks like the bug. Check them once, here, where the
// failure names the file.
func TestFixturesAreWellFormedEnvelopes(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		// Nothing committed to guard. A fixture that a test asks for and
		// cannot find is caught by fixture's own Fatalf.
		t.Skip("no captured fixtures")
	}

	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			// Read through the helper, so this covers the path it builds too.
			raw := fixture(t, name)

			// Pointers, not values: a missing key and a zero value are
			// different mistakes, and only the first is worth failing on.
			var env struct {
				Status *string          `json:"status"`
				Code   *int             `json:"code"`
				Data   *json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &env); err != nil {
				t.Fatalf("cannot read as a v2 envelope: %v", err)
			}
			if env.Status == nil || env.Code == nil || env.Data == nil {
				t.Fatal("missing status, code or data; a fixture is a whole v2 envelope, not bare data")
			}
			if s := *env.Status; s != "success" && s != "error" {
				t.Errorf("status = %q, want success or error", s)
			}

			// Captured from a live account, so guard the scrub rather than
			// trusting it. A key called "token" is not a leak; these are.
			for _, leak := range []string{"Bearer ", "@intuizi.com", "INTUIZI_API_TOKEN"} {
				if strings.Contains(raw, leak) {
					t.Errorf("contains %q, scrub before committing", leak)
				}
			}
		})
	}
}
