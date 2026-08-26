package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/MoranWeissman/sharko/internal/fanout"
)

// unadoptOperation names what `sharko unadopt-cluster` attempted, for the
// headline, the review warning and the exit message.
const unadoptOperation = "Un-adopting the cluster"

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
secret intact. The cluster must have the sharko.sharko.dev/adopted annotation.`,
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
			DryRun *cliDryRunResult `json:"dry_run,omitempty"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			if !dryRun {
				fmt.Println("no readable answer")
			}
			return fmt.Errorf("invalid response: %w", err)
		}

		// The same one decision the fan-out commands take, on a fan-out of
		// one. It also decides the word that finishes the progress line,
		// which used to be "done" the moment the HTTP call came back.
		outcome := fanout.SingleStatus(result.Status, status == 207)
		if !dryRun {
			fmt.Println(outcome.ProgressWord())
		}

		fmt.Println()

		// A preview un-adopts nothing, so there is no completion to confirm
		// and nothing for an exit code to warn about — the same exception
		// `sharko adopt --dry-run` gets.
		if dryRun {
			printDryRun(result.DryRun)
			if result.Message != "" {
				fmt.Printf("  %s\n", result.Message)
			}
			return nil
		}

		if outcome.Completed() {
			fmt.Printf("Cluster %s un-adopted.\n", name)
		} else {
			fmt.Print(outcome.TroubleHeadline(unadoptOperation, name))
			fmt.Print(outcome.ReviewWarning())
			if result.Error != "" {
				fmt.Printf("  Error: %s\n", result.Error)
			}
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

		// Used to be a bare `return nil`, so an un-adoption that stopped
		// half-way — Sharko's labels already gone from the ArgoCD secret,
		// the pull request still open — exited 0.
		return outcome.ExitError(unadoptOperation, name)
	},
}
