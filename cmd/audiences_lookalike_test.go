package cmd

import (
	"encoding/json"
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
