package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "1.19.0"
var commit = "none"

var rootCmd = &cobra.Command{
	Use:     "sharko",
	Short:   "Addon management for Kubernetes clusters, built on ArgoCD",
	Version: version,
}

// serverFlag holds the value of the global --server flag. It is registered as
// a persistent flag on rootCmd in init() so every subcommand inherits it
// uniformly (V124-3.5 / BUG-010). Per-command --server flag definitions were
// removed in the same change so subcommands now read this single shared value.
//
// Resolution at call sites:
//   - When non-empty, this overrides the server URL stored in the saved CLI
//     config (~/.sharko/config). Used by `sharko list-clusters --server URL`,
//     `sharko status --server URL`, etc.
//   - When empty, callers fall back to the saved config (existing behaviour).
//
// `sharko login` is the one exception: it always requires --server because it
// runs before any saved config exists. The required-flag check lives in
// loginCmd's RunE rather than via cobra.MarkFlagRequired so we don't depend on
// init() ordering between root.go and login.go.
var serverFlag string

func init() {
	rootCmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification (for self-signed certs)")
	rootCmd.PersistentFlags().StringVar(&serverFlag, "server", "", "Sharko server URL (overrides saved config)")
}

// effectiveServer returns the server URL the CLI should use for an API call.
// Precedence:
//  1. --server flag (persistent on rootCmd) when non-empty
//  2. saved config (~/.sharko/config) value passed in by the caller
//
// Callers that have already loaded the saved config pass it as savedServer.
// Callers that have not (e.g. `sharko login`, where there is no saved config
// yet) should pass an empty string and treat the result as authoritative.
func effectiveServer(savedServer string) string {
	if serverFlag != "" {
		return serverFlag
	}
	return savedServer
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
