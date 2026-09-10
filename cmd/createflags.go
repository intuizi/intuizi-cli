package cmd

import (
	"strings"

	"github.com/spf13/pflag"
)

// rejectBodyFlags enforces "--file carries the whole body". Cobra cannot
// express one flag against a whole set.
func rejectBodyFlags(flags *pflag.FlagSet, fields []string) error {
	for _, f := range fields {
		if flags.Changed(f) {
			return usageErr("--file carries the whole body; drop --" + f)
		}
	}
	return nil
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
