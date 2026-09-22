package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const scheduleLayout = "2006-01-02 15:04:05"

// scheduleBase is a valid schedule apart from the cadence and start flags,
// which each test sets for itself.
var scheduleBase = []string{"--name", "Weekly refresh", "--audience-id", "88", "--frequency", "weekly"}

// futureStart is tomorrow in tz, so the "must be in the future" rule never
// makes a test rot.
func futureStart(t *testing.T, tz string) string {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	return time.Now().In(loc).Add(24 * time.Hour).Format(scheduleLayout)
}

func TestSchedulesCreateDryRunSendsAValidatedStartAndProject(t *testing.T) {
	srv, got := stub(t, `{}`)
	start := futureStart(t, "America/New_York")

	out, _, err := run(t, schedulesCreateCommand(), srv, append(scheduleBase,
		"--window", "2", "--start", start, "--timezone", "America/New_York",
		"--project-id", "4", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("dry run must send nothing, got %v", got.paths)
	}

	var body struct {
		ProjectID  int `json:"project_id"`
		Recurrence struct {
			Start    string `json:"start"`
			Timezone string `json:"timezone"`
		} `json:"recurrence"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("stdout is not the JSON body: %v\n%s", err, out)
	}
	if body.Recurrence.Start != start || body.Recurrence.Timezone != "America/New_York" {
		t.Errorf("recurrence = %+v", body.Recurrence)
	}
	if body.ProjectID != 4 {
		t.Errorf("project_id = %d, want 4", body.ProjectID)
	}
}

// An unset --project-id must not go out as 0.
func TestSchedulesCreateLeavesProjectOutWhenUnset(t *testing.T) {
	srv, _ := stub(t, `{}`)

	out, _, err := run(t, schedulesCreateCommand(), srv, append(scheduleBase,
		"--window", "2", "--start", futureStart(t, "UTC"), "--timezone", "UTC", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "project_id") {
		t.Errorf("unset --project-id leaked:\n%s", out)
	}
}

// The server enforces the start format, a real zone and future-in-zone; each
// is caught here with the fix in the message, and none costs a round trip.
func TestSchedulesCreateRejectsBadStartAndTimezone(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown zone", []string{"--start", "2999-01-01 06:00:00", "--timezone", "Nowhere/City"}, "Nowhere/City"},
		{"empty zone", []string{"--start", "2999-01-01 06:00:00", "--timezone", ""}, "--timezone"},
		{"Local is not a zone", []string{"--start", "2999-01-01 06:00:00", "--timezone", "Local"}, "Local"},
		{"not a date", []string{"--start", "not a date", "--timezone", "UTC"}, "YYYY-MM-DD HH:MM:SS"},
		{"date only", []string{"--start", "2999-01-01", "--timezone", "UTC"}, "YYYY-MM-DD HH:MM:SS"},
		{"in the past", []string{"--start", "2000-01-01 00:00:00", "--timezone", "UTC"}, "in the future in UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append(append([]string{}, scheduleBase...), "--window", "2", "--dry-run"), tc.args...)
			out, _, err := run(t, schedulesCreateCommand(), srv, args...)
			var ue usageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usageError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if out != "" {
				t.Errorf("a rejected dry run must print no body, got %q", out)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The cadence ids and counts have server-side ranges; and a zero given
// explicitly is a mistake to name, not the same as leaving the flag off.
func TestSchedulesCreateRangeChecksCadenceFlags(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"window 0", []string{"--window", "0"}, "--window"},
		{"window 9", []string{"--window", "9"}, "--window"},
		{"window-days 0", []string{"--window", "3", "--window-days", "0"}, "--window-days"},
		{"window-days 366", []string{"--window", "3", "--window-days", "366"}, "--window-days"},
		{"window-days 0 on window 2", []string{"--window", "2", "--window-days", "0"}, "--window-days"},
		{"ending 1 with after 0", []string{"--window", "2", "--ending", "1", "--after-recurrences", "0"}, "--after-recurrences"},
		{"ending 2 with after 0", []string{"--window", "2", "--ending", "2", "--after-recurrences", "0"}, "--after-recurrences"},
		{"ending 3 with after 0", []string{"--window", "2", "--ending", "3", "--end-date", "2999-01-01", "--after-recurrences", "0"}, "--after-recurrences"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append(append([]string{}, scheduleBase...),
				"--start", "2999-01-01 06:00:00", "--timezone", "UTC", "--dry-run"), tc.args...)
			_, _, err := run(t, schedulesCreateCommand(), srv, args...)
			var ue usageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usageError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The edges of each range are legal.
func TestSchedulesCreateAcceptsRangeEdges(t *testing.T) {
	srv, _ := stub(t, `{}`)

	for _, args := range [][]string{
		{"--window", "1"},
		{"--window", "8"},
		{"--window", "3", "--window-days", "1"},
		{"--window", "3", "--window-days", "365"},
		{"--window", "2", "--ending", "2", "--after-recurrences", "1"},
	} {
		full := append(append(append([]string{}, scheduleBase...),
			"--start", "2999-01-01 06:00:00", "--timezone", "UTC", "--dry-run"), args...)
		if _, _, err := run(t, schedulesCreateCommand(), srv, full...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestSchedulesCreateRejectsNonPositiveIDFlags(t *testing.T) {
	srv, got := stub(t, `{}`)

	valid := []string{"--name", "Weekly refresh", "--frequency", "weekly", "--window", "2",
		"--start", "2999-01-01 06:00:00", "--timezone", "UTC", "--dry-run"}
	for _, args := range [][]string{
		{"--audience-id", "0"},
		{"--audience-id", "88", "--project-id", "-1"},
	} {
		_, _, err := run(t, schedulesCreateCommand(), srv, append(append([]string{}, valid...), args...)...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usageError", args, err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad id should cost no round trip, got %v", got.paths)
	}
}

// Cobra validates flag groups after PersistentPreRun, so its own "at least one
// of" error would exit 1 and pre-empt the list of everything missing.
func TestSchedulesCreateWithNoFlagsIsAUsageError(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, schedulesCreateCommand(), srv)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	for _, want := range []string{"--name", "--audience-id", "--start", "--timezone", "--frequency", "--window"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %s, got %v", want, err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestSchedulesCreateRejectsFileMixedWithProjectID(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, schedulesCreateCommand(), srv, "--file", "schedule.json", "--project-id", "4")
	if err == nil || !strings.Contains(err.Error(), "--file carries the whole body") {
		t.Fatalf("err = %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The created record leads with the project so the filing is visible without
// --json; a lead field prints before the alphabetical rest.
func TestSchedulesCreateDetailLeadsWithProject(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":201,"data":[
	 {"id":7,"name":"Weekly refresh","status":{"id":1,"name":"Active"},
	  "project":{"id":4,"name":"Retail 2026"},"created_at":"2026-01-01 00:00:00"}]}`)

	out, _, err := run(t, schedulesCreateCommand(), srv, append(scheduleBase,
		"--window", "2", "--start", futureStart(t, "UTC"), "--timezone", "UTC", "--project-id", "4")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Retail 2026") {
		t.Errorf("project not flattened to its name:\n%s", out)
	}
	if strings.Index(out, "\nproject") > strings.Index(out, "\ncreated_at") {
		t.Errorf("project should lead, not sort in with the rest:\n%s", out)
	}
}

// The server rejects these names; catching them here costs no round trip.
func TestSchedulesCreateChecksTheNameCharacters(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, tc := range []struct {
		name, value string
		ok          bool
	}{
		{"letters digits space dash underscore", "Weekly refresh_2 - EU", true},
		{"comma", "Weekly, refresh", false},
		{"slash", "weekly/refresh", false},
		{"colon", "weekly:refresh", false},
		{"accented", "refréshe", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--name", tc.value, "--audience-id", "88", "--frequency", "weekly",
				"--window", "2", "--start", futureStart(t, "UTC"), "--timezone", "UTC", "--dry-run"}
			out, _, err := run(t, schedulesCreateCommand(), srv, args...)
			if tc.ok {
				if err != nil {
					t.Fatalf("rejected a valid name: %v", err)
				}
				return
			}
			var ue usageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usageError", err)
			}
			if !strings.Contains(err.Error(), "--name") {
				t.Errorf("error does not name the flag: %v", err)
			}
			if out != "" {
				t.Errorf("a rejected dry run must print no body, got %q", out)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}
