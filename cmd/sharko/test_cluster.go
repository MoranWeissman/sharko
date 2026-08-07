package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(testClusterCmd)
}

var testClusterCmd = &cobra.Command{
	Use:   "test-cluster <name>",
	Short: "Test connectivity to a cluster",
	Long: `Test connectivity to a cluster. Verifies that Sharko can reach the
cluster's Kubernetes API using the credentials stored in the secrets
provider (a secret create/read/delete round-trip).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiPost("/api/v1/clusters/"+url.PathEscape(name)+"/test", nil)
		if err != nil {
			return err
		}

		if status != 200 {
			// The server returns a non-200 only when the test feature itself
			// is unavailable (e.g. no secrets backend configured) — the
			// cluster being unreachable is still a 200 with reachable: false.
			return printAPIError(respBody, status)
		}

		var result struct {
			Reachable     bool     `json:"reachable"`
			ErrorMessage  string   `json:"error_message"`
			ServerVersion string   `json:"server_version"`
			Suggestions   []string `json:"suggestions"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Cluster: %s\n", name)
		if result.Reachable {
			fmt.Println("Reachable: yes")
			if result.ServerVersion != "" {
				fmt.Printf("Kubernetes version: %s\n", result.ServerVersion)
			}
		} else {
			fmt.Println("Reachable: no")
			if result.ErrorMessage != "" {
				fmt.Printf("Error: %s\n", result.ErrorMessage)
			}
			if len(result.Suggestions) > 0 {
				fmt.Println("Did you mean one of these secret paths:")
				for _, s := range result.Suggestions {
					fmt.Printf("  %s\n", s)
				}
			}
		}

		return nil
	},
}
