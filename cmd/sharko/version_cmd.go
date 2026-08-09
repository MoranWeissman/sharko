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

		// Honour the global --server override when reporting which server we're talking to.
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

		detail, ok := formatHealthDetail(respBody)
		if !ok {
			fmt.Printf("Server: %s (invalid health response)\n", server)
			return nil
		}

		fmt.Printf("Server: %s (%s)\n", server, detail)
		return nil
	},
}

// healthResponse mirrors the fields of GET /api/v1/health that the CLI
// cares about. It intentionally does not try to capture the whole body —
// internal/api/health.go also writes a bool cluster_test_available field,
// and decoding straight into a map[string]string (the previous approach)
// fails on that mixed-type body every time. A typed struct only looks at
// the string fields it names, so extra non-string fields are ignored
// instead of breaking the decode.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
}

// formatHealthDetail turns a raw /api/v1/health response body into the
// "(...)" detail text printed after "Server: <url>". ok is false when the
// body is not valid JSON at all, so the caller falls back to the
// "invalid health response" message.
func formatHealthDetail(respBody []byte) (detail string, ok bool) {
	var health healthResponse
	if err := json.Unmarshal(respBody, &health); err != nil {
		return "", false
	}

	serverVersion := health.Version
	if serverVersion == "" {
		serverVersion = "unknown"
	}

	detail = fmt.Sprintf("connected, %s", serverVersion)
	if health.Mode != "" {
		detail = fmt.Sprintf("connected, %s, %s", serverVersion, health.Mode)
	}
	return detail, true
}
