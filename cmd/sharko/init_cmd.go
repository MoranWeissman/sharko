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

		fmt.Print("Initializing addons repository... ")
		respBody, status, err := apiPost("/api/v1/init", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 && status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status       string   `json:"status"`
			FilesCreated []string `json:"files_created"`
			ArgoCD       *struct {
				Bootstrapped bool   `json:"bootstrapped"`
				RootApp      string `json:"root_app"`
			} `json:"argocd"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(result.FilesCreated) > 0 {
			fmt.Printf("Files created: %d\n", len(result.FilesCreated))
			for _, f := range result.FilesCreated {
				fmt.Printf("  %s\n", f)
			}
		}
		if result.ArgoCD != nil && result.ArgoCD.Bootstrapped {
			fmt.Printf("ArgoCD bootstrapped (root app: %s)\n", result.ArgoCD.RootApp)
		}

		return nil
	},
}
