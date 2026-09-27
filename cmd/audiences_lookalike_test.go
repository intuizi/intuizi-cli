package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// lookalikeFlags is a complete lookalike, for tests that vary one flag.
var lookalikeFlags = []string{"--name", "Starbucks lookalike", "--source-audience-id", "1381",
	"--target-size", "500000", "--signal", "poi", "--country", "USA"}

func lookalikeConfigOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	cfg, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("body has no config block: %v", body)
	}
	return cfg
}

// Changed is true for a zero or empty value, so these all passed the
// required-flag check and went out as written.
func TestLookalikeCreateRejectsBadValues(t *testing.T) {
	more := func(extra ...string) []string {
		return append(append([]string(nil), lookalikeFlags...), extra...)
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"empty name", swap(lookalikeFlags, "--name", ""), []string{"--name"}},
		{"zero seed", swap(lookalikeFlags, "--source-audience-id", "0"), []string{"--source-audience-id"}},
		{"negative seed", swap(lookalikeFlags, "--source-audience-id", "-3"), []string{"--source-audience-id"}},
		{"zero target", swap(lookalikeFlags, "--target-size", "0"), []string{"--target-size"}},
		{"target over the cap", swap(lookalikeFlags, "--target-size", "4000001"), []string{"--target-size", "4,000,000"}},
		{"empty signal", more("--signal", ""), []string{"--signal"}},
		{"empty country", more("--country", ""), []string{"--country"}},
		{"empty state", more("--state", ""), []string{"--state"}},
		{"zero contrast", more("--contrast-audience-id", "0"), []string{"--contrast-audience-id"}},
		// The API refuses a contrast audience that is the seed itself.
		{"contrast is the seed", more("--contrast-audience-id", "1381"), []string{"--contrast-audience-id 1381", "seed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, created)

			_, _, err := run(t, lookalikeCreateCommand(), srv, tc.args...)

			wantUsageErr(t, err, tc.want...)
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The cap is inclusive, and a set --contrast-audience-id is sent.
func TestLookalikeCreateAcceptsTheCapAndAContrast(t *testing.T) {
	srv, _ := stub(t, created)

	body := dryRunBody(t, lookalikeCreateCommand(), srv,
		append(swap(lookalikeFlags, "--target-size", "4000000"), "--contrast-audience-id", "77")...)

	cfg := lookalikeConfigOf(t, body)
	if cfg["target_size"] != float64(4000000) || cfg["contrast_audience_id"] != float64(77) {
		t.Errorf("config = %v", cfg)
	}
}

// A repeated --signal is one signal; order is kept so the body reads as typed.
func TestLookalikeCreateDedupesSignals(t *testing.T) {
	srv, _ := stub(t, created)

	body := dryRunBody(t, lookalikeCreateCommand(), srv,
		append(append([]string(nil), lookalikeFlags...), "--signal", "apps", "--signal", "poi")...)

	if raw, _ := json.Marshal(lookalikeConfigOf(t, body)["signals"]); string(raw) != `["poi","apps"]` {
		t.Errorf("signals = %s", raw)
	}
}

// Cobra validates its one-of-required groups after PersistentPreRunE, so its
// own error exited 1. missingFlags covers the case with the better message.
func TestLookalikeCreateWithoutSeedOrFileIsAUsageError(t *testing.T) {
	srv, got := stub(t, created)

	_, _, err := run(t, lookalikeCreateCommand(), srv, "--name", "x")

	wantUsageErr(t, err, "--source-audience-id", "--file")
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The help used to send a contrast audience to --file, but there is a flag.
func TestLookalikeCreateHelpNamesTheContrastFlag(t *testing.T) {
	long := lookalikeCreateCommand().Long
	if !strings.Contains(long, "--contrast-audience-id") {
		t.Errorf("help does not mention --contrast-audience-id:\n%s", long)
	}
	if strings.Contains(long, "A contrast audience, or anything") {
		t.Errorf("help still routes a contrast audience through --file:\n%s", long)
	}
}

// The API emails the run's creator on completion unless told otherwise, so
// the flag defaults on and is always sent: with omitempty, --notify=false went
// out as nothing and the email was sent anyway.
func TestLookalikeCreateAlwaysSendsNotification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		want  bool
	}{
		{"default on", nil, true},
		{"--notify", []string{"--notify"}, true},
		{"--notify=false", []string{"--notify=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := stub(t, created)

			body := dryRunBody(t, lookalikeCreateCommand(), srv,
				append(append([]string(nil), lookalikeFlags...), tc.extra...)...)

			got, ok := body["notification"]
			if !ok {
				t.Fatalf("notification not sent: %v", body)
			}
			if got != tc.want {
				t.Errorf("notification = %v, want %v", got, tc.want)
			}
		})
	}
}

// --file carries the whole body, notification included.
func TestLookalikeCreateRejectsNotifyWithFile(t *testing.T) {
	srv, got := stub(t, created)
	path := payloadFile(t, `{"name":"x"}`)

	_, _, err := run(t, lookalikeCreateCommand(), srv, "--file", path, "--notify=false")

	wantUsageErr(t, err, "--file carries the whole body", "--notify")
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// "keep polling" named no command. The next step is the one that follows a
// run through 108 Modeling, on the id just created.
func TestLookalikeCreateNamesTheCommandThatFollowsTheRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(t *testing.T) []string
	}{
		{"flags", func(*testing.T) []string { return lookalikeFlags }},
		{"file", func(t *testing.T) []string { return []string{"--file", payloadFile(t, `{"name":"x"}`)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := stub(t, created)

			_, stderr, err := run(t, lookalikeCreateCommand(), srv, tc.args(t)...)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if !strings.Contains(stderr, "'intuizi audiences show <id> --wait'") {
				t.Errorf("stderr does not name the follow-up command:\n%s", stderr)
			}
		})
	}
}

// A cancelled run reads 108 Modeling until it reaches its next checkpoint,
// then ends at 400 Error, which is final: the console records it when it
// hands the cancel to the worker. show --wait follows it there and exits 1,
// so neither the confirmation nor the help may warn off --wait any more, as
// they did while a cancelled run read 108 for good.
func TestLookalikeCancelSaysTheRunEndsAt400(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"message":"Cancellation requested.","data":[]}`)

	_, stderr, err := run(t, audiencesLookalikeCommand(), srv, "cancel", "42")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 1 || !strings.HasSuffix(got.paths[0], "/cancel-lookalike") {
		t.Errorf("paths = %v", got.paths)
	}
	for _, want := range []string{"cancellation requested for audience 42", "400 Error",
		"intuizi audiences delete 42"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr omits %q: %q", want, stderr)
		}
	}
	for _, stale := range []string{"keeps reading", "do not --wait"} {
		if strings.Contains(stderr, stale) {
			t.Errorf("stderr still says %q: %q", stale, stderr)
		}
	}

	group := audiencesLookalikeCommand()
	var long string
	for _, c := range group.Commands() {
		if c.Name() == "cancel" {
			long = c.Long
		}
	}
	for _, want := range []string{"108 Modeling", "400 Error", "show <id> --wait", "exits non-zero",
		"Cancelled on request.", "already finished", "audiences delete <id>"} {
		if !strings.Contains(long, want) {
			t.Errorf("cancel help omits %q:\n%s", want, long)
		}
	}
	for name, text := range map[string]string{"cancel": long, "lookalike": group.Long,
		"show": audiencesShowCommand().Long} {
		for _, stale := range []string{"never reaches Completed and", "keeps reading", "for good",
			"never does", "Do not follow", "only ends at --timeout", "polls until --timeout"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s help still says %q:\n%s", name, stale, text)
			}
		}
	}
	if !strings.Contains(group.Long, "400 Error") {
		t.Errorf("lookalike help does not say how a cancelled run ends:\n%s", group.Long)
	}
	if !strings.Contains(audiencesShowCommand().Long, "400 Error") {
		t.Errorf("show help does not say a wait on a cancelled lookalike ends at 400:\n%s",
			audiencesShowCommand().Long)
	}
}

// A cancelled lookalike moves from 108 Modeling to 400 Error once the run
// stops. 108 is waited through and 400 is a failure, so show --wait follows the
// run to 400, stops polling there, prints the record and exits 1.
func TestWaitOnACancelledLookalikeEndsAt400(t *testing.T) {
	fast(t)
	replies := []reply{audience(108, "Modeling"), audience(108, "Modeling"), audience(400, "Error")}
	srv, got := stubSeq(t, replies...)

	out, errb, err := run(t, audiencesShowCommand(), srv, "1377", "--wait", "--timeout", "5s")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed\nstderr:\n%s", err, errb)
	}
	if code := exitCode(err, nil, true); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if len(got.paths) != len(replies) {
		t.Errorf("expected %d calls, got %d - the wait did not stop at 400", len(replies), len(got.paths))
	}
	if !strings.Contains(err.Error(), "Error (400)") {
		t.Errorf("error should name the status it stopped on: %v", err)
	}
	if !strings.Contains(errb, "Modeling (108)") {
		t.Errorf("stderr should show the wait going through 108:\n%s", errb)
	}
	if !strings.HasPrefix(out, "id") || !strings.Contains(out, "Error") {
		t.Errorf("stdout should be the record the wait stopped on:\n%s", out)
	}
}
