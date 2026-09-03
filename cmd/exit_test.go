package cmd

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

// The codes are a contract scripts read, so they are pinned by number, not by
// referring to the constants the code under test uses.
func TestExitCode(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		caught os.Signal
		parsed bool
		want   int
	}{
		{"api error after flags parsed", errors.New("boom"), nil, true, 1},
		{"bad flag, before PersistentPreRun", errors.New("unknown flag"), nil, false, 2},
		{"own validation, after flags parsed", usageErr("pass --name"), nil, true, 2},
		{"own validation, wrapped", fmt.Errorf("create: %w", usageErr("pass --name")), nil, true, 2},
		{"ctrl-c", nil, syscall.SIGINT, true, 130},
		{"sigterm", nil, syscall.SIGTERM, true, 143},
		// A signal wins over the usage path: the run was cut short, not misused.
		{"signal before flags parsed", nil, syscall.SIGINT, false, 130},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCode(c.err, c.caught, c.parsed); got != c.want {
				t.Errorf("exitCode(%v, %v, %v) = %d, want %d", c.err, c.caught, c.parsed, got, c.want)
			}
		})
	}
}
