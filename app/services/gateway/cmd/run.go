package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"template.app/app/services/gateway/internal"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the gateway HTTP server",
	Run: func(cmd *cobra.Command, args []string) {
		if err := internal.Run(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
