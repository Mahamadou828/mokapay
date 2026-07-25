package cmd

import (
	"log"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "app",
	Short: "CLI to manage the payment service",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Printf("error executing root command: %v", err)
		os.Exit(1)
	}
}
