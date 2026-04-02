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

		respBody, status, err := apiGet("/api/v1/health")
		if err != nil {
			fmt.Printf("Server: %s (unreachable: %v)\n", cfg.Server, err)
			return nil
		}

		if status != 200 {
			fmt.Printf("Server: %s (unhealthy, HTTP %d)\n", cfg.Server, status)
			return nil
		}

		var health map[string]string
		if err := json.Unmarshal(respBody, &health); err != nil {
			fmt.Printf("Server: %s (invalid health response)\n", cfg.Server)
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

		fmt.Printf("Server: %s (%s)\n", cfg.Server, detail)
		return nil
	},
}
