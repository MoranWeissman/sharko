package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	initCmd.Flags().Bool("no-bootstrap", false, "Skip ArgoCD bootstrapping")
	rootCmd.AddCommand(initCmd)
}

// NOTE: The server endpoint POST /api/v1/init is not yet implemented.
// This CLI command is created in advance; it will return whatever the server
// returns (likely 404 until the endpoint is implemented in a separate PR).
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the addons repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		noBootstrap, _ := cmd.Flags().GetBool("no-bootstrap")

		body := map[string]bool{
			"bootstrap_argocd": !noBootstrap,
		}

		fmt.Println("Initializing addons repository...")
		respBody, status, err := apiPost("/api/v1/init", body)
		if err != nil {
			return err
		}

		if status != 200 && status != 201 {
			return printAPIError(respBody, status)
		}

		var result struct {
			Status string `json:"status"`
			Repo   *struct {
				FilesCreated []string `json:"files_created"`
				PRUrl        string   `json:"pr_url"`
				Branch       string   `json:"branch"`
				Merged       bool     `json:"merged"`
			} `json:"repo"`
			ArgoCD *struct {
				Bootstrapped bool   `json:"bootstrapped"`
				RootApp      string `json:"root_app"`
				SyncError    string `json:"sync_error"`
			} `json:"argocd"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		// Repository step.
		if result.Repo != nil {
			fileCount := len(result.Repo.FilesCreated)
			if result.Repo.PRUrl != "" {
				fmt.Printf("  Repository:   \u2713 files created (%d files)\n", fileCount)
				fmt.Printf("  Pull Request: \u2713 %s\n", result.Repo.PRUrl)
			} else if result.Repo.Branch != "" {
				fmt.Printf("  Repository:   \u2713 files pushed to branch %s (%d files)\n", result.Repo.Branch, fileCount)
				fmt.Println("  Pull Request: \u2717 failed (check server logs)")
			} else {
				fmt.Printf("  Repository:   \u2713 %d files created\n", fileCount)
			}
		}

		// ArgoCD bootstrap step.
		if result.ArgoCD != nil {
			if result.ArgoCD.Bootstrapped {
				fmt.Printf("  ArgoCD:       \u2713 bootstrapped (root app: %s)\n", result.ArgoCD.RootApp)
			} else if result.ArgoCD.SyncError != "" {
				fmt.Printf("  ArgoCD:       \u2717 failed (%s)\n", result.ArgoCD.SyncError)
			} else {
				fmt.Println("  ArgoCD:       \u2717 failed (check server logs)")
			}
		}

		return nil
	},
}
