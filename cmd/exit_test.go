package cmd

import (
	"os"
	"syscall"
	"testing"
)

// The codes are a contract scripts read, so they are pinned by number, not by
// referring to the constants the code under test uses.
func TestExitCode(t *testing.T) {
	cases := []struct {
		name   string
		caught os.Signal
		parsed bool
		want   int
	}{
		{"api error after flags parsed", nil, true, 1},
		{"bad flag, before PersistentPreRun", nil, false, 2},
		{"ctrl-c", syscall.SIGINT, true, 130},
		{"sigterm", syscall.SIGTERM, true, 143},
		// A signal wins over the usage path: the run was cut short, not misused.
		{"signal before flags parsed", syscall.SIGINT, false, 130},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCode(c.caught, c.parsed); got != c.want {
				t.Errorf("exitCode(%v, %v) = %d, want %d", c.caught, c.parsed, got, c.want)
			}
		})
	}
}
