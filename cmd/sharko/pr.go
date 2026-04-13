package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	prListCmd.Flags().String("status", "", "Filter by status (open, merged, closed)")
	prListCmd.Flags().String("cluster", "", "Filter by cluster name")
	prListCmd.Flags().String("user", "", "Filter by user")
	prListCmd.Flags().StringP("output", "o", "table", "Output format (table, json)")

	prWaitCmd.Flags().Duration("timeout", 10*time.Minute, "Maximum time to wait for PR to complete")

	prCmd.AddCommand(prListCmd)
	prCmd.AddCommand(prStatusCmd)
	prCmd.AddCommand(prRefreshCmd)
	prCmd.AddCommand(prWaitCmd)

	rootCmd.AddCommand(prCmd)
}

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage tracked pull requests",
}

var prListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tracked pull requests",
	RunE: func(cmd *cobra.Command, args []string) error {
		statusFilter, _ := cmd.Flags().GetString("status")
		clusterFilter, _ := cmd.Flags().GetString("cluster")
		userFilter, _ := cmd.Flags().GetString("user")
		output, _ := cmd.Flags().GetString("output")

		params := url.Values{}
		if statusFilter != "" {
			params.Set("status", statusFilter)
		}
		if clusterFilter != "" {
			params.Set("cluster", clusterFilter)
		}
		if userFilter != "" {
			params.Set("user", userFilter)
		}

		path := "/api/v1/prs"
		if len(params) > 0 {
			path += "?" + params.Encode()
		}

		respBody, status, err := apiGet(path)
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		if output == "json" {
			fmt.Println(string(respBody))
			return nil
		}

		var resp struct {
			PRs []struct {
				PRID       int    `json:"pr_id"`
				PRUrl      string `json:"pr_url"`
				PRTitle    string `json:"pr_title"`
				Cluster    string `json:"cluster"`
				Operation  string `json:"operation"`
				User       string `json:"user"`
				LastStatus string `json:"last_status"`
				CreatedAt  string `json:"created_at"`
			} `json:"prs"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.PRs) == 0 {
			fmt.Println("No tracked pull requests.")
			return nil
		}

		// Calculate column widths
		idW, statusW, clusterW, opW, userW := 4, 6, 7, 9, 4
		for _, pr := range resp.PRs {
			idStr := fmt.Sprintf("#%d", pr.PRID)
			if len(idStr) > idW {
				idW = len(idStr)
			}
			if len(pr.LastStatus) > statusW {
				statusW = len(pr.LastStatus)
			}
			c := pr.Cluster
			if c == "" {
				c = "-"
			}
			if len(c) > clusterW {
				clusterW = len(c)
			}
			if len(pr.Operation) > opW {
				opW = len(pr.Operation)
			}
			if len(pr.User) > userW {
				userW = len(pr.User)
			}
		}

		fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
			idW, "PR", statusW, "STATUS", clusterW, "CLUSTER", opW, "OPERATION", userW, "USER", "TITLE")

		for _, pr := range resp.PRs {
			c := pr.Cluster
			if c == "" {
				c = "-"
			}
			fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
				idW, fmt.Sprintf("#%d", pr.PRID),
				statusW, pr.LastStatus,
				clusterW, c,
				opW, pr.Operation,
				userW, pr.User,
				pr.PRTitle)
		}

		return nil
	},
}

var prStatusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "Show details for a tracked PR",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		respBody, status, err := apiGet("/api/v1/prs/" + url.PathEscape(id))
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var pr struct {
			PRID       int    `json:"pr_id"`
			PRUrl      string `json:"pr_url"`
			PRBranch   string `json:"pr_branch"`
			PRTitle    string `json:"pr_title"`
			PRBase     string `json:"pr_base"`
			Cluster    string `json:"cluster"`
			Operation  string `json:"operation"`
			User       string `json:"user"`
			Source     string `json:"source"`
			CreatedAt  string `json:"created_at"`
			LastStatus string `json:"last_status"`
			LastPolled string `json:"last_polled_at"`
		}
		if err := json.Unmarshal(respBody, &pr); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("PR #%d — %s\n", pr.PRID, pr.PRTitle)
		fmt.Printf("  Status:    %s\n", pr.LastStatus)
		fmt.Printf("  URL:       %s\n", pr.PRUrl)
		fmt.Printf("  Branch:    %s → %s\n", pr.PRBranch, pr.PRBase)
		if pr.Cluster != "" {
			fmt.Printf("  Cluster:   %s\n", pr.Cluster)
		}
		fmt.Printf("  Operation: %s\n", pr.Operation)
		fmt.Printf("  User:      %s\n", pr.User)
		fmt.Printf("  Source:    %s\n", pr.Source)
		fmt.Printf("  Created:   %s\n", pr.CreatedAt)
		fmt.Printf("  Polled:    %s\n", pr.LastPolled)

		return nil
	},
}

var prRefreshCmd = &cobra.Command{
	Use:   "refresh <id>",
	Short: "Force refresh a tracked PR",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		fmt.Printf("Refreshing PR #%s... ", id)
		respBody, status, err := apiPost("/api/v1/prs/"+url.PathEscape(id)+"/refresh", nil)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var pr struct {
			PRID       int    `json:"pr_id"`
			LastStatus string `json:"last_status"`
		}
		if err := json.Unmarshal(respBody, &pr); err != nil {
			return nil // refresh succeeded even if we can't parse
		}
		fmt.Printf("  Status: %s\n", pr.LastStatus)

		return nil
	},
}

var prWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "Wait for a PR to be merged or closed",
	Long: `Block until the PR is merged (exit 0), closed without merge (exit 1),
or the timeout expires (exit 2). Polls every 5 seconds.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		timeout, _ := cmd.Flags().GetDuration("timeout")

		deadline := time.Now().Add(timeout)
		fmt.Printf("Waiting for PR #%s (timeout %s)...\n", id, timeout)

		for {
			if time.Now().After(deadline) {
				fmt.Println("Timeout waiting for PR.")
				os.Exit(2)
			}

			respBody, status, err := apiPost("/api/v1/prs/"+url.PathEscape(id)+"/refresh", nil)
			if err != nil {
				// Transient error — retry
				time.Sleep(5 * time.Second)
				continue
			}
			if status != 200 {
				return printAPIError(respBody, status)
			}

			var pr struct {
				LastStatus string `json:"last_status"`
			}
			if err := json.Unmarshal(respBody, &pr); err != nil {
				time.Sleep(5 * time.Second)
				continue
			}

			switch strings.ToLower(pr.LastStatus) {
			case "merged":
				fmt.Println("PR merged.")
				return nil // exit 0
			case "closed":
				fmt.Println("PR closed without merge.")
				os.Exit(1)
			}

			time.Sleep(5 * time.Second)
		}
	},
}
