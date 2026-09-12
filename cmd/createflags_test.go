package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// Every offending flag at once, as missingFlags already does: naming one per
// run makes the user re-run once per flag.
func TestRejectBodyFlagsNamesEveryOffender(t *testing.T) {
	fs := pflag.NewFlagSet("create", pflag.ContinueOnError)
	var dsType, name, city string
	fs.StringVar(&dsType, "type", "", "")
	fs.StringVar(&name, "name", "", "")
	fs.StringVar(&city, "city", "", "")
	if err := fs.Parse([]string{"--type", "poi", "--city", "SF"}); err != nil {
		t.Fatal(err)
	}

	err := rejectBodyFlags(fs, []string{"type", "name", "city"})
	if err == nil {
		t.Fatal("rejectBodyFlags accepted body flags alongside --file")
	}
	// TestActivationsCreateRejectsFileMixedWithFlags asserts on the prefix.
	for _, want := range []string{"--file carries the whole body", "--type", "--city"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--name") {
		t.Errorf("error names a flag that was not set: %v", err)
	}

	if err := rejectBodyFlags(fs, []string{"name"}); err != nil {
		t.Errorf("an unset flag is not an offender: %v", err)
	}
}
