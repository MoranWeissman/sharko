package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	addAddonCmd.Flags().String("chart", "", "Helm chart name (required)")
	addAddonCmd.Flags().String("repo", "", "Helm chart repository URL (required)")
	addAddonCmd.Flags().String("version", "", "Chart version (required)")
	addAddonCmd.Flags().String("namespace", "", "Target namespace")
	addAddonCmd.MarkFlagRequired("chart")
	addAddonCmd.MarkFlagRequired("repo")
	addAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(addAddonCmd)

	removeAddonCmd.Flags().Bool("confirm", false, "Execute removal (without this flag, shows dry-run impact)")
	rootCmd.AddCommand(removeAddonCmd)
}

var addAddonCmd = &cobra.Command{
	Use:   "add-addon <name>",
	Short: "Add a new addon to the catalog",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		chart, _ := cmd.Flags().GetString("chart")
		repo, _ := cmd.Flags().GetString("repo")
		ver, _ := cmd.Flags().GetString("version")
		namespace, _ := cmd.Flags().GetString("namespace")

		body := map[string]string{
			"name":      name,
			"chart":     chart,
			"repo_url":  repo,
			"version":   ver,
			"namespace": namespace,
		}

		fmt.Printf("Adding addon %s... ", name)
		respBody, status, err := apiPost("/api/v1/addons", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Git     *struct {
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"git"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Addon %s added to catalog.\n", name)
		if result.Git != nil && result.Git.PRUrl != "" {
			fmt.Printf("  Git: PR %s\n", result.Git.PRUrl)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}

var removeAddonCmd = &cobra.Command{
	Use:   "remove-addon <name>",
	Short: "Remove an addon from the catalog",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		confirm, _ := cmd.Flags().GetBool("confirm")

		path := "/api/v1/addons/" + url.PathEscape(name)
		if confirm {
			path += "?confirm=true"
		}

		if confirm {
			fmt.Printf("Removing addon %s... ", name)
		} else {
			fmt.Printf("Checking impact of removing addon %s...\n", name)
		}

		respBody, status, err := apiRequest("DELETE", path, nil)
		if err != nil {
			if confirm {
				fmt.Println("failed")
			}
			return err
		}

		// Without --confirm, the server returns 400 with a dry-run impact report.
		if !confirm && status == 400 {
			var dryRun struct {
				Error  string `json:"error"`
				Impact struct {
					Addon                   string   `json:"addon"`
					AffectedClusters        []string `json:"affected_clusters"`
					TotalDeploymentsToRemove int      `json:"total_deployments_to_remove"`
					Warning                 string   `json:"warning"`
				} `json:"impact"`
			}
			if err := json.Unmarshal(respBody, &dryRun); err != nil {
				return printAPIError(respBody, status)
			}

			fmt.Println()
			fmt.Printf("Impact of removing %s:\n", name)
			fmt.Printf("  Affected clusters: %d\n", len(dryRun.Impact.AffectedClusters))
			if len(dryRun.Impact.AffectedClusters) > 0 {
				fmt.Printf("  Clusters:          %s\n", strings.Join(dryRun.Impact.AffectedClusters, ", "))
			}
			if dryRun.Impact.Warning != "" {
				fmt.Printf("  Warning:           %s\n", dryRun.Impact.Warning)
			}
			fmt.Println()
			fmt.Println("Run with --confirm to execute removal.")
			return nil
		}

		// 404 — addon not found.
		if status == 404 {
			if confirm {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		if status != 200 {
			if confirm {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		fmt.Println("done")
		fmt.Printf("Addon %s removed from catalog.\n", name)
		return nil
	},
}
