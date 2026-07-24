package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"mokapay.app/app/services/file/internal"
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Drop every database object owned by the service",
	Run: func(cmd *cobra.Command, args []string) {
		if err := internal.Reset(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(resetCmd)
}
