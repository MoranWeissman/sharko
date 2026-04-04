package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	addClustersCmd.Flags().String("addons", "", "Comma-separated list of addons to enable")
	addClustersCmd.Flags().String("region", "", "Region for all clusters in the batch")
	addClustersCmd.Flags().Bool("from-provider", false, "Discover and register unregistered provider clusters")
	rootCmd.AddCommand(addClustersCmd)
}

// batchRequest is the JSON body for POST /api/v1/clusters/batch.
type batchRequest struct {
	Clusters []batchClusterEntry `json:"clusters"`
}

type batchClusterEntry struct {
	Name   string          `json:"name"`
	Addons map[string]bool `json:"addons"`
	Region string          `json:"region"`
}

// batchResponse is the JSON response from POST /api/v1/clusters/batch.
type batchResponse struct {
	Total     int           `json:"total"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
	Results   []batchResult `json:"results"`
}

type batchResult struct {
	Status  string `json:"status"`
	Cluster struct {
		Name   string `json:"name"`
		Server string `json:"server"`
	} `json:"cluster"`
	Git *struct {
		PRUrl  string `json:"pr_url"`
		Branch string `json:"branch"`
	} `json:"git"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

var addClustersCmd = &cobra.Command{
	Use:   "add-clusters <cluster1,cluster2,...>",
	Short: "Register multiple clusters in a batch",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromProvider, _ := cmd.Flags().GetBool("from-provider")
		addonsFlag, _ := cmd.Flags().GetString("addons")
		region, _ := cmd.Flags().GetString("region")

		addons := make(map[string]bool)
		if addonsFlag != "" {
			for _, a := range strings.Split(addonsFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					addons[a] = true
				}
			}
		}

		var names []string

		if fromProvider {
			// Discover unregistered clusters from the provider.
			respBody, status, err := apiGet("/api/v1/clusters/available")
			if err != nil {
				return err
			}
			if status != 200 {
				return printAPIError(respBody, status)
			}

			var discoverResp struct {
				Clusters []struct {
					Name       string `json:"name"`
					Region     string `json:"region"`
					Registered bool   `json:"registered"`
				} `json:"clusters"`
			}
			if err := json.Unmarshal(respBody, &discoverResp); err != nil {
				return fmt.Errorf("invalid discover response: %w", err)
			}

			for _, c := range discoverResp.Clusters {
				if !c.Registered {
					names = append(names, c.Name)
				}
			}

			if len(names) == 0 {
				fmt.Println("All provider clusters are already registered.")
				return nil
			}
			fmt.Printf("Found %d unregistered cluster(s): %s\n", len(names), strings.Join(names, ", "))
		} else {
			// Parse comma-separated cluster names from argument.
			for _, n := range strings.Split(args[0], ",") {
				n = strings.TrimSpace(n)
				if n != "" {
					names = append(names, n)
				}
			}
			if len(names) == 0 {
				return fmt.Errorf("at least one cluster name is required")
			}
		}

		// Split into batches of 10 if needed.
		const maxBatch = 10
		var allResults []batchResult
		totalSucceeded, totalFailed := 0, 0

		for i := 0; i < len(names); i += maxBatch {
			end := i + maxBatch
			if end > len(names) {
				end = len(names)
			}
			chunk := names[i:end]

			entries := make([]batchClusterEntry, len(chunk))
			for j, name := range chunk {
				entries[j] = batchClusterEntry{
					Name:   name,
					Addons: addons,
					Region: region,
				}
			}

			body := batchRequest{Clusters: entries}
			batchNum := (i / maxBatch) + 1
			if len(names) > maxBatch {
				fmt.Printf("Processing batch %d (%d clusters)... ", batchNum, len(chunk))
			} else {
				fmt.Printf("Registering %d cluster(s)... ", len(chunk))
			}

			respBody, status, err := apiPost("/api/v1/clusters/batch", body)
			if err != nil {
				fmt.Println("failed")
				return err
			}

			if status != 200 && status != 207 {
				fmt.Println("failed")
				return printAPIError(respBody, status)
			}
			fmt.Println("done")

			var resp batchResponse
			if err := json.Unmarshal(respBody, &resp); err != nil {
				return fmt.Errorf("invalid response: %w", err)
			}

			totalSucceeded += resp.Succeeded
			totalFailed += resp.Failed
			allResults = append(allResults, resp.Results...)
		}

		// Print summary.
		fmt.Println()
		fmt.Printf("Batch complete: %d succeeded, %d failed (of %d total)\n", totalSucceeded, totalFailed, len(names))
		fmt.Println()

		for _, r := range allResults {
			switch r.Status {
			case "success":
				line := fmt.Sprintf("  [ok] %s", r.Cluster.Name)
				if r.Cluster.Server != "" {
					line += fmt.Sprintf(" (%s)", r.Cluster.Server)
				}
				if r.Git != nil && r.Git.PRUrl != "" {
					line += fmt.Sprintf(" — PR: %s", r.Git.PRUrl)
				}
				fmt.Println(line)
			case "partial":
				fmt.Printf("  [!!] %s — partial: %s\n", r.Cluster.Name, r.Message)
			case "failed":
				fmt.Printf("  [XX] %s — %s\n", r.Cluster.Name, r.Error)
			}
		}

		return nil
	},
}
