package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show Sharko CLI and server version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Sharko CLI: %s\n", version)

		cfg, err := loadConfig()
		if err != nil {
			fmt.Println("Server: not configured (run 'sharko login' first)")
			return nil
		}

		// Honour the global --server override (V124-3.5 / BUG-010) when
		// reporting which server we're talking to.
		server := effectiveServer(cfg.Server)

		respBody, status, err := apiGet("/api/v1/health")
		if err != nil {
			fmt.Printf("Server: %s (unreachable: %v)\n", server, err)
			return nil
		}

		if status != 200 {
			fmt.Printf("Server: %s (unhealthy, HTTP %d)\n", server, status)
			return nil
		}

		var health map[string]string
		if err := json.Unmarshal(respBody, &health); err != nil {
			fmt.Printf("Server: %s (invalid health response)\n", server)
			return nil
		}

		serverVersion := health["version"]
		mode := health["mode"]
		if serverVersion == "" {
			serverVersion = "unknown"
		}

		detail := fmt.Sprintf("connected, %s", serverVersion)
		if mode != "" {
			detail = fmt.Sprintf("connected, %s, %s", serverVersion, mode)
		}

		fmt.Printf("Server: %s (%s)\n", server, detail)
		return nil
	},
}
