package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "1.6.0"
var commit = "none"

var rootCmd = &cobra.Command{
	Use:     "sharko",
	Short:   "Addon management for Kubernetes clusters, built on ArgoCD",
	Version: version,
}

func init() {
	rootCmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification (for self-signed certs)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
