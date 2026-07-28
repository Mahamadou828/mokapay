package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"mokapay.app/app/services/compliance/internal"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database scripts, migrations, views and procedures",
	Run: func(cmd *cobra.Command, args []string) {
		if err := internal.Migrate(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
