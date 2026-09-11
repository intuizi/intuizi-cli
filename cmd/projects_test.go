package cmd

import (
	"errors"
	"strings"
	"testing"
)

// Cobra checks a required flag after PersistentPreRun, so its own error would
// exit 1 and read like a failed call. The check lives in RunE so a missing
// --name is the usage error it is.
func TestProjectsCreateWithoutNameIsAUsageError(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, projectsCreateCommand(), srv)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usageError", err)
	}
	if code := exitCode(err, nil, true); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("the error should name the flag, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// An empty --name satisfies cobra's required check but not the server's, so
// it is caught here rather than spent on a 422.
func TestProjectsCreateRejectsAnEmptyName(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, name := range []string{"", "   "} {
		_, _, err := run(t, projectsCreateCommand(), srv, "--name", name)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("--name %q: err = %v, want a usageError", name, err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("an empty name should cost no round trip, got %v", got.paths)
	}
}
