package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	adoptCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	adoptCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	adoptCmd.Flags().Bool("auto-merge", false, "Auto-merge the adoption PR (overrides server default)")
	rootCmd.AddCommand(adoptCmd)
}

var adoptCmd = &cobra.Command{
	Use:   "adopt <cluster1> [cluster2] ...",
	Short: "Adopt existing ArgoCD clusters under Sharko management",
	Long: `Adopt one or more existing ArgoCD clusters. This creates GitOps
configuration (values file + managed-clusters.yaml entry) for clusters
that are already registered in ArgoCD but not yet managed by Sharko.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		autoMerge, _ := cmd.Flags().GetBool("auto-merge")

		if !dryRun && !yes {
			fmt.Printf("Adopt %d cluster(s): %s\nThis will create GitOps configuration and mark them as Sharko-managed. Continue? [y/N]: ",
				len(args), strings.Join(args, ", "))
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		body := map[string]interface{}{
			"clusters": args,
		}
		if dryRun {
			body["dry_run"] = true
		}
		if autoMerge {
			body["auto_merge"] = true
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing adoption of %d cluster(s)...\n", len(args))
		} else {
			fmt.Printf("Adopting %d cluster(s)... ", len(args))
		}

		respBody, status, err := apiPost("/api/v1/clusters/adopt", body)
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
			Results []struct {
				Name         string `json:"name"`
				Status       string `json:"status"`
				Error        string `json:"error"`
				Message      string `json:"message"`
				Verification *struct {
					Success      bool   `json:"success"`
					ErrorCode    string `json:"error_code,omitempty"`
					ErrorMessage string `json:"error_message,omitempty"`
				} `json:"verification,omitempty"`
				Git *struct {
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
			} `json:"results"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println()
		for _, cr := range result.Results {
			switch cr.Status {
			case "success":
				fmt.Printf("  %s: adopted\n", cr.Name)
			case "partial":
				fmt.Printf("  %s: partial (warning: %s)\n", cr.Name, cr.Error)
			case "failed":
				fmt.Printf("  %s: FAILED — %s\n", cr.Name, cr.Error)
			default:
				fmt.Printf("  %s: %s\n", cr.Name, cr.Status)
			}

			if cr.Verification != nil {
				if cr.Verification.Success {
					fmt.Printf("    Verify: passed\n")
				} else {
					fmt.Printf("    Verify: FAILED [%s] %s\n",
						cr.Verification.ErrorCode, cr.Verification.ErrorMessage)
				}
			}

			if cr.Git != nil {
				if cr.Git.PRUrl != "" {
					if cr.Git.Merged {
						fmt.Printf("    PR:     %s (merged)\n", cr.Git.PRUrl)
					} else {
						fmt.Printf("    PR:     %s (open — merge manually)\n", cr.Git.PRUrl)
					}
				}
			}

			if cr.DryRun != nil {
				fmt.Printf("    PR:     %s\n", cr.DryRun.PRTitle)
				for _, f := range cr.DryRun.FilesToWrite {
					fmt.Printf("    [%s] %s\n", f.Action, f.Path)
				}
			}

			if cr.Message != "" {
				fmt.Printf("    Note:   %s\n", cr.Message)
			}
		}

		return nil
	},
}
