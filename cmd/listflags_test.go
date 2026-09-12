package cmd

import (
	"strings"
	"testing"
)

// The API rejects page=0 and per_page=0 rather than ignoring them. An unset
// flag already stays off the wire; an explicit zero or negative must not go
// out either.
func TestListFlagsRejectNonPositivePages(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		flag string
	}{
		{"page zero", []string{"--page", "0"}, "--page"},
		{"page negative", []string{"--page", "-1"}, "--page"},
		{"per-page zero", []string{"--per-page", "0"}, "--per-page"},
		{"per-page negative", []string{"--per-page", "-25"}, "--per-page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, audienceList)

			_, _, err := run(t, audiencesListCommand(), srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.flag) {
				t.Fatalf("err = %v, want one naming %s", err, tc.flag)
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The smallest valid values are still sent as given.
func TestListFlagsSendPageOne(t *testing.T) {
	srv, got := stub(t, audienceList)

	if _, _, err := run(t, audiencesListCommand(), srv, "--page", "1", "--per-page", "1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if q := got.queries[0]; !strings.Contains(q, "page=1") || !strings.Contains(q, "per_page=1") {
		t.Errorf("query = %s", q)
	}
}
