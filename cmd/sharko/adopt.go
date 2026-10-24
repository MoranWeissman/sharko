package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MoranWeissman/sharko/internal/fanout"
	"github.com/spf13/cobra"
)

func init() {
	adoptCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	adoptCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	adoptCmd.Flags().Bool("auto-merge", false, "Auto-merge the adoption PR (overrides server default)")
	adoptCmd.Flags().StringSlice("acknowledge", nil, "Ids of the warnings you have read, comma-separated (run with --dry-run first to see them). --yes does not skip warnings")
	rootCmd.AddCommand(adoptCmd)
}

var adoptCmd = &cobra.Command{
	Use:     "adopt <cluster1> [cluster2] ...",
	Aliases: []string{"adopt-cluster"},
	Short:   "Adopt existing ArgoCD clusters under Sharko management",
	Long: `Adopt one or more existing ArgoCD clusters. Sharko adds each cluster to
managed-clusters.yaml in your Git repo through a pull request. The clusters
are already in ArgoCD but not yet managed by Sharko. When the pull request is
merged, Sharko takes over the ArgoCD connection and marks it as adopted. If
the pull request is closed without a merge, nothing changes on the cluster.

Sharko checks each cluster first, the same way take over does. If it finds
something you should read (for example an ApplicationSet that could delete
what it installed), it stops and prints each warning with an id. Read them,
then run again with --acknowledge <id,id,...>. --yes only skips the "Continue?"
question; it does not skip warnings. --dry-run prints the warnings too.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		autoMerge, _ := cmd.Flags().GetBool("auto-merge")
		acknowledge, _ := cmd.Flags().GetStringSlice("acknowledge")

		if !dryRun && !yes {
			fmt.Printf("Adopt %s: %s\nThis opens a pull request that adds %s to Sharko. Continue? [y/N]: ",
				clusterCount(len(args)), strings.Join(args, ", "), itOrThem(len(args)))
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
		if len(acknowledge) > 0 {
			body["acknowledged_findings"] = acknowledge
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing adoption of %s...\n", clusterCount(len(args)))
		} else {
			fmt.Printf("Adopting %s... ", clusterCount(len(args)))
		}

		respBody, status, err := apiPost("/api/v1/clusters/adopt", body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}

		if status == 409 {
			if ok, ackErr := adoptNeedsAcknowledgement(respBody); ok {
				fmt.Println("stopped")
				return ackErr
			}
		}

		if status != 200 && status != 207 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		var result struct {
			Outcome fanout.Outcome `json:"outcome"`
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
				DryRun   *cliDryRunResult  `json:"dry_run,omitempty"`
				Warnings []adoptCLIWarning `json:"warnings,omitempty"`
				Blockers []adoptCLIBlocker `json:"blockers,omitempty"`
			} `json:"results"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			if !dryRun {
				fmt.Println("no readable answer")
			}
			return fmt.Errorf("invalid response: %w", err)
		}

		// One count, made from the per-cluster answers, drives the word
		// printed at the end of the progress line, the summary, the review
		// warning and the exit code. Recounted here rather than read off
		// result.Outcome so an older server that does not send it is still
		// reported truthfully.
		statuses := make([]string, 0, len(result.Results))
		for _, cr := range result.Results {
			statuses = append(statuses, cr.Status)
		}
		outcome := fanout.Count(statuses)

		// A cluster whose pull request is still open is not adopted yet:
		// Sharko takes the ArgoCD connection over after the merge.
		waitingCount := 0
		if !dryRun {
			for _, cr := range result.Results {
				if cr.Status == "success" && cr.Git != nil && prWaiting(cr.Git.PRUrl, cr.Git.Merged) {
					waitingCount++
				}
			}
		}

		// "done" used to be printed the moment the HTTP call came back, so an
		// adoption in which every cluster failed still said "done" — and the
		// endpoint answers 200 for exactly that case, so nothing else
		// contradicted it either.
		if !dryRun {
			if waitingCount > 0 && outcome.EverythingCompleted() {
				fmt.Println(waitingProgressWord)
			} else if outcome.EverythingCompleted() {
				fmt.Println("done")
			} else {
				fmt.Println("not all clusters finished")
			}
		}

		fmt.Println()
		if !dryRun {
			if waitingCount > 0 && outcome.EverythingCompleted() {
				fmt.Printf("Nothing is adopted yet: %s for the pull request to be merged.\n",
					waitingClusters(waitingCount))
			} else {
				fmt.Print(outcome.SummaryLine(adoptOperation))
			}
			fmt.Print(outcome.ReviewWarning())
			fmt.Println()
		}
		for _, cr := range result.Results {
			switch {
			case dryRun && cr.Status == "success":
				fmt.Printf("  %s: would adopt\n", cr.Name)
			case cr.Status == "success" && cr.Git != nil && prWaiting(cr.Git.PRUrl, cr.Git.Merged):
				fmt.Printf("  %s: %s\n", cr.Name, waitingForMergeLine(cr.Git.PRUrl))
			case cr.Status == "success":
				fmt.Printf("  %s: adopted\n", cr.Name)
			case cr.Status == "partial":
				fmt.Printf("  %s: partial (warning: %s)\n", cr.Name, cr.Error)
			case dryRun && cr.Status == "failed":
				fmt.Printf("  %s: would NOT adopt — %s\n", cr.Name, cr.Error)
			case cr.Status == "failed":
				fmt.Printf("  %s: FAILED — %s\n", cr.Name, cr.Error)
			default:
				fmt.Printf("  %s: %s\n", cr.Name, cr.Status)
			}

			printAdoptBlockers(cr.Blockers)
			printAdoptWarnings(cr.Warnings)

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
						fmt.Printf("    PR:     %s (waiting for merge)\n", cr.Git.PRUrl)
					}
				}
			}

			if cr.DryRun != nil {
				printDryRun(cr.DryRun)
			}

			if cr.Message != "" {
				fmt.Printf("    Note:   %s\n", cr.Message)
			}
		}

		// A dry run adopts nothing, so there is no completion to confirm and
		// nothing for an exit code to warn about: previewing successfully is
		// a success.
		if dryRun {
			return nil
		}

		// The command used to return nil here whatever came back, so
		// `sharko adopt` exited 0 for an adoption in which every cluster
		// failed. Exit 0 now means every cluster completed.
		return outcome.ExitError(adoptOperation)
	},
}

// adoptOperation names what `sharko adopt` attempted, for the summary line
// and the exit message.
const adoptOperation = "Adoption"

// adoptCLIWarning is one warning from POST /clusters/adopt.
type adoptCLIWarning struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Title    string `json:"title,omitempty"`
	WhatToDo string `json:"what_to_do,omitempty"`
}

// adoptCLIBlocker is one reason a cluster cannot be adopted yet.
type adoptCLIBlocker struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func printAdoptBlockers(blockers []adoptCLIBlocker) {
	if len(blockers) == 0 {
		return
	}
	fmt.Println("    Fix these first:")
	for _, b := range blockers {
		fmt.Printf("      - [%s] %s\n", b.ID, b.Message)
	}
}

func printAdoptWarnings(warnings []adoptCLIWarning) {
	if len(warnings) == 0 {
		return
	}
	fmt.Println("    Read these first:")
	for _, w := range warnings {
		if w.Title != "" {
			fmt.Printf("      - [%s] %s: %s\n", w.ID, w.Title, w.Message)
		} else {
			fmt.Printf("      - [%s] %s\n", w.ID, w.Message)
		}
		if w.WhatToDo != "" {
			fmt.Printf("        What to do: %s\n", w.WhatToDo)
		}
	}
}

// adoptNeedsAcknowledgement reads the 409 the server sends when a warning
// was not acknowledged. It prints each warning and returns the error that
// tells the person how to go ahead. The bool is false when the body is some other
// 409.
func adoptNeedsAcknowledgement(body []byte) (bool, error) {
	var resp struct {
		Code                   string   `json:"code"`
		UnacknowledgedFindings []string `json:"unacknowledged_findings"`
		Results                []struct {
			Name     string            `json:"name"`
			Warnings []adoptCLIWarning `json:"warnings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Code != "unacknowledged_findings" {
		return false, nil
	}
	fmt.Println()
	fmt.Println("Sharko did not adopt anything. There are warnings to read first.")
	for _, r := range resp.Results {
		fmt.Printf("  %s:\n", r.Name)
		printAdoptWarnings(r.Warnings)
	}
	ids := strings.Join(resp.UnacknowledgedFindings, ",")
	fmt.Println()
	fmt.Printf("When you have read them, run the same command again with --acknowledge %s\n", ids)
	return true, fmt.Errorf("adoption stopped: warnings not acknowledged yet: %s", ids)
}

// waitingClusters says how many clusters wait, in plain words.
func waitingClusters(n int) string {
	if n == 1 {
		return "1 cluster waits"
	}
	return fmt.Sprintf("%d clusters wait", n)
}

// clusterCount says how many clusters with the right word: "1 cluster",
// "2 clusters" (EP3 dry run round S, S24).
func clusterCount(n int) string {
	if n == 1 {
		return "1 cluster"
	}
	return fmt.Sprintf("%d clusters", n)
}

// itOrThem is "it" for one cluster and "them" for more.
func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
