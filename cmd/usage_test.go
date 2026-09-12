package cmd

import (
	"errors"
	"strings"
	"testing"
)

// A malformed --month is a bad invocation, so it exits 2 like every other
// value the CLI checks itself - not 1, which is a failed call.
func TestUsageBadMonthIsAUsageError(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, usageCommand(), srv, "--month", "June")
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	if code := exitCode(err, nil, true); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "June") || !strings.Contains(err.Error(), "YYYY-MM") {
		t.Errorf("the error should quote the value and the shape, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// Usage is a report with no id, so --quiet is refused rather than silently
// printing the whole thing a script would then try to parse.
func TestQuietRefusedOnUsage(t *testing.T) {
	quiet(t)
	srv, got := stub(t, usageEnvelope)

	out, _, err := run(t, usageCommand(), srv)
	if err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Fatalf("err = %v", err)
	}
	if code := exitCode(err, nil, true); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stdout should stay clean, got %q", out)
	}
	if len(got.paths) != 0 {
		t.Errorf("refusal should cost no round trip, got %v", got.paths)
	}
}
