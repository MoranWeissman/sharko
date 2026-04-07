package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(refreshSecretsCmd)
	rootCmd.AddCommand(secretStatusCmd)
}

var refreshSecretsCmd = &cobra.Command{
	Use:   "refresh-secrets",
	Short: "Trigger secret reconciliation",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, status, err := apiPost("/api/v1/secrets/reconcile", nil)
		if err != nil {
			return err
		}
		if status != 202 {
			return fmt.Errorf("unexpected status %d", status)
		}
		fmt.Println("Secret reconciliation triggered.")
		return nil
	},
}

var secretStatusCmd = &cobra.Command{
	Use:   "secret-status",
	Short: "Show last secret reconciliation status",
	RunE: func(cmd *cobra.Command, args []string) error {
		body, status, err := apiGet("/api/v1/secrets/status")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(body, status)
		}

		var stats struct {
			Checked  int    `json:"checked"`
			Created  int    `json:"created"`
			Updated  int    `json:"updated"`
			Deleted  int    `json:"deleted"`
			Skipped  int    `json:"skipped"`
			Errors   int    `json:"errors"`
			Duration string `json:"duration"`
			LastRun  string `json:"last_run"`
		}
		if err := json.Unmarshal(body, &stats); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println("Secret Reconciliation Status")
		fmt.Println("────────────────────────────")
		fmt.Printf("Last run:  %s\n", stats.LastRun)
		fmt.Printf("Duration:  %s\n", stats.Duration)
		fmt.Printf("Checked:   %d\n", stats.Checked)
		fmt.Printf("Created:   %d\n", stats.Created)
		fmt.Printf("Updated:   %d\n", stats.Updated)
		fmt.Printf("Deleted:   %d\n", stats.Deleted)
		fmt.Printf("Skipped:   %d\n", stats.Skipped)
		fmt.Printf("Errors:    %d\n", stats.Errors)
		return nil
	},
}
