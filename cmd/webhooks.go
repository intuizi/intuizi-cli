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

Webhooks are the push alternative to polling: audience.completed and
activation.completed carry the finished resource to your receiver, so a long
build does not need a poll loop.

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
