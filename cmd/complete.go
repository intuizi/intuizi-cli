package cmd

import (
	"sort"

	"github.com/spf13/cobra"
)

// completeValues offers a flag's fixed value set to the shell, the same set it
// is validated against, so the two cannot drift.
func completeValues(cmd *cobra.Command, flag string, values []string) {
	_ = cmd.RegisterFlagCompletionFunc(flag,
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return values, cobra.ShellCompDirectiveNoFileComp
		})
}

// sortedKeys keeps completion order stable: map iteration is randomised.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uploadPurposes matches checkPurpose.
var uploadPurposes = []string{"cohort", "poi_submission"}
