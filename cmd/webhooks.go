package cmd

import (
	"github.com/spf13/cobra"
)

// Webhooks are registered in the console, not here: the API exposes the index
// read only. This command exists so a script can confirm from code that a
// subscription exists and which events it covers.

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Inspect webhook endpoints",
	Long: `Inspect the webhook endpoints registered for your company.

Webhooks are the efficient completion signal: audience.completed,
activation.completed and cohort.completed push the finished resource to your
receiver. audience.failed and activation.failed report a build or export that
stopped at 107 Additional Info or ended in a 4xx error state, the 400 a
cancelled Lookalike Model ends at included, and cohort.failed an import that
ended at 5 Not Available. They are a notification channel, not a source of
truth, so keep a low-frequency poll ('show <id>') as the fallback for a
delivery that exhausts its retries.

Registering and editing endpoints is console-only - the API exposes this read
and nothing else.`,
}

var webhooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook endpoints",
	Long: `List the webhook endpoints registered for your company, newest first.

is_active goes false after a manual pause or an automatic disable following
repeated delivery failures, so it is worth checking before assuming a receiver
is still being called.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, "/webhooks/index", nil,
			[]string{"id", "name", "url", "events", "is_active", "last_delivery_at"},
			"no webhook endpoints")
	},
}

func init() {
	webhooksCmd.AddCommand(webhooksListCmd)
	rootCmd.AddCommand(webhooksCmd)
}
