package cmd

import (
	"strings"
	"testing"
)

// Every failure now sends its webhook: a build or export that stops at 107
// Additional Info sends audience.failed or activation.failed like a 4xx, a
// cancelled lookalike's 400 sends audience.failed, and a failed cohort import
// ends at 5 and sends cohort.failed. The help used to list 107 and the cohort
// import as end states no webhook reports; the poll fallback is now only for a
// delivery that exhausts its retries.
func TestWebhooksHelpSaysEveryFailureSendsAnEvent(t *testing.T) {
	long := webhooksCmd.Long
	for _, want := range []string{"107 Additional Info", "4xx", "cancelled Lookalike Model",
		"cohort.failed", "5 Not Available", "exhausts its retries"} {
		if !strings.Contains(long, want) {
			t.Errorf("webhooks help omits %q:\n%s", want, long)
		}
	}
	for _, stale := range []string{"no webhook reports", "does not fire", "error code"} {
		if strings.Contains(long, stale) {
			t.Errorf("webhooks help still says %q:\n%s", stale, long)
		}
	}
}
