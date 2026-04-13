package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func init() {
	unadoptClusterCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	unadoptClusterCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	rootCmd.AddCommand(unadoptClusterCmd)
}

var unadoptClusterCmd = &cobra.Command{
	Use:   "unadopt-cluster <name>",
	Short: "Reverse adoption of a cluster",
	Long: `Un-adopt a cluster that was previously adopted. This removes Sharko
management (GitOps config, managed-by labels) but keeps the ArgoCD cluster
secret intact. The cluster must have the sharko.sharko.io/adopted annotation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if !dryRun && !yes {
			fmt.Printf("Un-adopt cluster %q? This removes Sharko management but keeps the ArgoCD secret. [y/N]: ", name)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		body := map[string]interface{}{
			"yes": true, // API requires confirmation flag
		}
		if dryRun {
			body["dry_run"] = true
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing un-adoption of cluster %s...\n", name)
		} else {
			fmt.Printf("Un-adopting cluster %s... ", name)
		}

		respBody, status, err := apiPost("/api/v1/clusters/"+url.PathEscape(name)+"/unadopt", body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}

		if status != 200 && status != 207 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		if !dryRun {
			fmt.Println("done")
		}

		var result struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Error   string `json:"error"`
			Message string `json:"message"`
			Git     *struct {
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
				Merged bool   `json:"merged"`
			} `json:"git,omitempty"`
			DryRun *struct {
				FilesToWrite []struct {
					Path   string `json:"path"`
					Action string `json:"action"`
				} `json:"files_to_write"`
				PRTitle string `json:"pr_title"`
			} `json:"dry_run,omitempty"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println()

		if dryRun && result.DryRun != nil {
			fmt.Println("Dry-run preview (no changes made):")
			fmt.Printf("  PR:  %s\n", result.DryRun.PRTitle)
			for _, f := range result.DryRun.FilesToWrite {
				fmt.Printf("  [%s] %s\n", f.Action, f.Path)
			}
			return nil
		}

		isPartial := result.Status == "partial" || status == 207

		if isPartial {
			fmt.Println("Cluster un-adopted with warnings (partial success).")
			if result.Error != "" {
				fmt.Printf("  Error: %s\n", result.Error)
			}
		} else {
			fmt.Printf("Cluster %s un-adopted.\n", name)
		}

		if result.Git != nil && result.Git.PRUrl != "" {
			if result.Git.Merged {
				fmt.Printf("  PR: %s (merged)\n", result.Git.PRUrl)
			} else {
				fmt.Printf("  PR: %s (open — merge manually)\n", result.Git.PRUrl)
			}
		}

		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}
