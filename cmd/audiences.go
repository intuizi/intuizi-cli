package cmd

import "github.com/spf13/cobra"

var audiencesCmd = &cobra.Command{
	Use:   "audiences",
	Short: "Manage audiences",
}

func init() {
	rootCmd.AddCommand(audiencesCmd)
}
