package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"mokapay.app/app/services/user/internal"
)

var seedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed the fixed admin/user/agent test accounts (see internal/seed.go)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := internal.Seed(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(seedCmd)
}
