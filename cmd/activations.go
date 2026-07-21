package cmd

import "github.com/spf13/cobra"

var activationsCmd = &cobra.Command{
	Use:   "activations",
	Short: "Manage activations",
}

func init() {
	rootCmd.AddCommand(activationsCmd)
}
