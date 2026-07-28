package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"mokapay.app/app/services/compliance/internal"
)

var runMigrate bool

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the compliance gRPC server",
	Run: func(cmd *cobra.Command, args []string) {
		if runMigrate {
			if err := internal.Migrate(); err != nil {
				os.Exit(1)
			}
		}
		if err := internal.Run(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	runCmd.Flags().BoolVar(&runMigrate, "migrate", false, "run database migrations before starting")
	rootCmd.AddCommand(runCmd)
}
