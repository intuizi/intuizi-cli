package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
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
		// Go loads these, but the API's timezone:all list holds none of them.
		{"legacy US zone", []string{"--start", "2999-01-01 06:00:00", "--timezone", "US/Eastern"}, "US/Eastern"},
		{"GMT", []string{"--start", "2999-01-01 06:00:00", "--timezone", "GMT"}, "legacy or Etc/"},
		{"Etc/UTC", []string{"--start", "2999-01-01 06:00:00", "--timezone", "Etc/UTC"}, "use UTC"},
		{"Etc/GMT+5", []string{"--start", "2999-01-01 06:00:00", "--timezone", "Etc/GMT+5"}, "Etc/GMT+5"},
		{"EST5EDT", []string{"--start", "2999-01-01 06:00:00", "--timezone", "EST5EDT"}, "EST5EDT"},
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
		{"only spaces", "   ", false}, // passes the regex; the server's required rule trims it
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

// An --end-date before the --start date is refused before anything is sent.
// The API answers one with a 422 now, but it used to accept it and count the
// gap forward from --start, so the schedule ran for that many days instead of
// not at all. The end date is compared with the start's own date in
// --timezone, so the start date itself is still allowed, as it is by the API.
func TestSchedulesCreateRejectsAnEndDateBeforeTheStart(t *testing.T) {
	srv, got := stub(t, `{}`)
	base := append(append([]string{}, scheduleBase...),
		"--window", "2", "--ending", "3", "--timezone", "America/New_York", "--dry-run")

	for _, tc := range []struct {
		name, start, end string
		ok               bool
	}{
		{"a day before", "2999-03-10 06:00:00", "2999-03-09", false},
		{"a year before", "2999-03-10 06:00:00", "2998-03-10", false},
		{"the start date", "2999-03-10 06:00:00", "2999-03-10", true},
		{"after", "2999-03-10 06:00:00", "2999-04-10", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, base...), "--start", tc.start, "--end-date", tc.end)
			out, _, err := run(t, schedulesCreateCommand(), srv, args...)
			if tc.ok {
				if err != nil {
					t.Fatalf("rejected a valid --end-date: %v", err)
				}
				return
			}
			var ue usageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a usageError", err)
			}
			if !strings.Contains(err.Error(), "--end-date "+tc.end) || !strings.Contains(err.Error(), "2999-03-10") {
				t.Errorf("err = %v, want it to name both dates", err)
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

// The API caps a schedule name at 255 characters; a dry run should not print
// a body it would refuse.
func TestSchedulesCreateChecksTheNameLength(t *testing.T) {
	srv, _ := stub(t, `{}`)
	for _, tc := range []struct {
		n  int
		ok bool
	}{{255, true}, {256, false}} {
		args := []string{"--name", strings.Repeat("a", tc.n), "--audience-id", "88", "--frequency", "weekly",
			"--window", "2", "--start", futureStart(t, "UTC"), "--timezone", "UTC", "--dry-run"}
		_, _, err := run(t, schedulesCreateCommand(), srv, args...)
		if tc.ok && err != nil {
			t.Errorf("%d characters: %v", tc.n, err)
		}
		var ue usageError
		if !tc.ok && (!errors.As(err, &ue) || !strings.Contains(err.Error(), "255")) {
			t.Errorf("%d characters: err = %v, want a usage error naming the cap", tc.n, err)
		}
	}
}

// UTC and every name under the regions PHP lists pass the local check; only
// names outside them are sure to fail at the API.
func TestAPIZoneName(t *testing.T) {
	for tz, want := range map[string]bool{
		"UTC":                            true,
		"America/New_York":               true,
		"America/Argentina/Buenos_Aires": true,
		"Asia/Kolkata":                   true,
		"Europe/Kyiv":                    true,
		"Antarctica/Troll":               true,
		"Arctic/Longyearbyen":            true,
		"Atlantic/Reykjavik":             true,
		"Australia/Sydney":               true,
		"Indian/Maldives":                true,
		"Pacific/Auckland":               true,
		"Africa/Lagos":                   true,
		"US/Eastern":                     false,
		"GMT":                            false,
		"Etc/UTC":                        false,
		"Etc/GMT+5":                      false,
		"EST5EDT":                        false,
		"utc":                            false,
		"america/new_york":               false,
	} {
		if got := apiZoneName(tz); got != want {
			t.Errorf("apiZoneName(%q) = %v, want %v", tz, got, want)
		}
	}
}

// A region-based zone the API accepts still goes out as given.
func TestSchedulesCreateSendsARegionZone(t *testing.T) {
	srv, _ := stub(t, `{}`)
	out, _, err := run(t, schedulesCreateCommand(), srv, append(append([]string{}, scheduleBase...),
		"--window", "2", "--start", futureStart(t, "Asia/Kolkata"), "--timezone", "Asia/Kolkata", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"timezone": "Asia/Kolkata"`) {
		t.Errorf("body should carry the zone as given:\n%s", out)
	}
}

// Activating a schedule whose ending was met sets it Active, but the API arms
// no next run, so it never runs again. The response shows the counts, and the
// command says so on stderr; a schedule with runs left, or one that never
// ends, gets no note.
func TestSchedulesActivateNotesASpentSchedule(t *testing.T) {
	record := func(ending, done, available int) string {
		return fmt.Sprintf(`{"status":"success","code":200,"message":"Resource updated successfully.",`+
			`"data":[{"id":42,"name":"weekly","status":{"id":1,"name":"Active"},"recurrence":{`+
			`"ending":{"type":%d},"cycles":{"done":%d,"available":%d}}}]}`, ending, done, available)
	}
	for _, tc := range []struct {
		name string
		body string
		json bool
		note bool
	}{
		{"recurrences all made", record(2, 4, 4), false, true},
		{"custom date all made", record(3, 12, 12), false, true},
		{"custom date runs left", record(3, 3, 12), false, false},
		{"never ends", record(1, 40, 0), false, false},
		{"spent, with --json", record(2, 4, 4), true, true},
		{"no recurrence in the record", `{"status":"success","code":200,"data":[{"id":42}]}`, false, false},
		{"empty data", `{"status":"success","code":200,"data":[]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, tc.body)
			out, stderr, err := exec(t, tc.json, schedulesActivateCommand(), srv, "42")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(got.paths) != 1 || !strings.HasSuffix(got.paths[0], "/analyses/schedules/activate") {
				t.Errorf("paths = %v", got.paths)
			}
			if got.bodies[0] != `{"id":42}` {
				t.Errorf("body = %s", got.bodies[0])
			}
			if tc.json {
				if !strings.Contains(out, `"status": "success"`) && !strings.Contains(out, `"status":"success"`) {
					t.Errorf("--json should print the envelope, got %q", out)
				}
			} else {
				if out != "" {
					t.Errorf("stdout should stay empty, got %q", out)
				}
				if !strings.Contains(stderr, "activated schedule 42") {
					t.Errorf("stderr = %q", stderr)
				}
			}
			if gotNote := strings.Contains(stderr, "will not run again"); gotNote != tc.note {
				t.Errorf("note = %v, want %v; stderr = %q", gotNote, tc.note, stderr)
			}
		})
	}
}
