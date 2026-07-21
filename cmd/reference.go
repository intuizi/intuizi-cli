package cmd

import "github.com/spf13/cobra"

var referenceCmd = &cobra.Command{
	Use:   "reference",
	Short: "Access reference data",
}

func init() {
	rootCmd.AddCommand(referenceCmd)
}
