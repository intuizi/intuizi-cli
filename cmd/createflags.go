package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// rejectBodyFlags enforces "--file carries the whole body". Cobra cannot
// express one flag against a whole set. Every offender is named, as
// missingFlags does, so one re-run fixes them all.
func rejectBodyFlags(flags *pflag.FlagSet, fields []string) error {
	var set []string
	for _, f := range fields {
		if flags.Changed(f) {
			set = append(set, "--"+f)
		}
	}
	if len(set) == 0 {
		return nil
	}
	return usageErr("--file carries the whole body; drop " + strings.Join(set, ", "))
}

// missingFlags names every unset required flag at once.
func missingFlags(flags *pflag.FlagSet, required []string, what string) error {
	var missing []string
	for _, f := range required {
		if !flags.Changed(f) {
			missing = append(missing, "--"+f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return usageErr(what + " needs " + strings.Join(missing, ", ") +
		" - or pass the whole body with --file")
}

// nonEmpty rejects a blank value. Changed is true for --name "", so
// missingFlags lets it through and the API stores "" - or, for a repeatable
// flag, a [""] entry.
func nonEmpty(flag string, values ...string) error {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return usageErr("--" + flag + " cannot be empty")
		}
	}
	return nil
}

// dedupe drops repeated values, keeping first-seen order.
func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// positiveID rejects an id-valued flag of 0 or less when it was set, the way
// parseID does for a positional id. Shared by every create that takes ids as
// flags; an unset flag is left alone, since it is never sent.
func positiveID(flags *pflag.FlagSet, flag string, v int) error {
	if !flags.Changed(flag) || v > 0 {
		return nil
	}
	what := strings.ReplaceAll(strings.TrimSuffix(flag, "-id"), "-", " ")
	return usageErr(fmt.Sprintf("--%s %d is not a valid %s id", flag, v, what))
}
