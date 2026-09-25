package cmd

import (
	"strings"
	"testing"
)

// Completion offers the same set the flag is validated against; a value that
// completes but then fails validation is worse than no completion.
func TestFlagCompletionMatchesValidation(t *testing.T) {
	for _, tc := range []struct {
		flag string
		args []string
		want []string
	}{
		{"file-format", []string{"cohorts", "create"}, cohortFileFormats},
		{"identifier-type", []string{"cohorts", "create"}, identifierTypes},
		{"file-format", []string{"cohorts", "preview"}, previewFileFormats},
		{"frequency", []string{"schedules", "create"}, sortedKeys(scheduleFrequencies)},
		{"purpose", []string{"uploads", "reserve"}, uploadPurposes},
		{"purpose", []string{"uploads", "put"}, uploadPurposes},
		{"type", []string{"audiences", "create"}, sortedKeys(datasetTypes)},
		{"signal", []string{"audiences", "lookalike", "create"}, sortedKeys(lookalikeSignals)},
	} {
		t.Run(strings.Join(append(tc.args, tc.flag), " "), func(t *testing.T) {
			args := append(append([]string{"__complete"}, tc.args...), "--"+tc.flag, "")
			res := rootRun(t, args...)
			for _, want := range tc.want {
				if !strings.Contains(res.stdout, want+"\n") {
					t.Errorf("completion omits %q:\n%s", want, res.stdout)
				}
			}
			// NoFileComp, or the shell falls back to listing files.
			if !strings.Contains(res.stdout, ":4") {
				t.Errorf("missing ShellCompDirectiveNoFileComp:\n%s", res.stdout)
			}
		})
	}
}
