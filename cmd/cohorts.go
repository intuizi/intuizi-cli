package cmd

import "github.com/spf13/cobra"

var cohortsCmd = &cobra.Command{
	Use:   "cohorts",
	Short: "Manage cohorts",
}

func init() {
	rootCmd.AddCommand(cohortsCmd)
}
