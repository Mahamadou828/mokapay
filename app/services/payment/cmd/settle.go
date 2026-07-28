package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"mokapay.app/app/services/payment/internal"
)

var settleCommissionsCmd = &cobra.Command{
	Use:   "settle-commissions",
	Short: "Pay out every closed accounting week that still has unpaid commissions",
	Long: "The manual entry point of the weekly commission payout, mirroring the " +
		"Tuesday periodic job. Re-running is safe: one payout per agent per " +
		"accounting week is enforced by a UNIQUE constraint.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := internal.SettleCommissions(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(settleCommissionsCmd)
}
