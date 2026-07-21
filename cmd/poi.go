package cmd

import "github.com/spf13/cobra"

var poiCmd = &cobra.Command{
	Use:   "poi",
	Short: "Manage points of interest",
}

func init() {
	rootCmd.AddCommand(poiCmd)
}
