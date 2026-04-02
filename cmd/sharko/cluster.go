package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	addClusterCmd.Flags().String("addons", "", "Comma-separated list of addons to enable")
	addClusterCmd.Flags().String("region", "", "Cluster region")
	rootCmd.AddCommand(addClusterCmd)

	rootCmd.AddCommand(removeClusterCmd)

	updateClusterCmd.Flags().String("add-addon", "", "Comma-separated addons to enable")
	updateClusterCmd.Flags().String("remove-addon", "", "Comma-separated addons to disable")
	rootCmd.AddCommand(updateClusterCmd)

	rootCmd.AddCommand(listClustersCmd)
}

// knownNonAddonLabels are label keys that should not be counted as addons.
var knownNonAddonLabels = map[string]bool{
	"region": true,
}

var addClusterCmd = &cobra.Command{
	Use:   "add-cluster <name>",
	Short: "Register a new cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
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

		body := map[string]interface{}{
			"name":   name,
			"addons": addons,
			"region": region,
		}

		fmt.Printf("Registering cluster %s... ", name)
		respBody, status, err := apiPost("/api/v1/clusters", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 201 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status  string `json:"status"`
			Cluster struct {
				Name          string          `json:"name"`
				Server        string          `json:"server"`
				ServerVersion string          `json:"server_version"`
				Addons        map[string]bool `json:"addons"`
			} `json:"cluster"`
			Git *struct {
				Mode   string `json:"mode"`
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"git"`
			CompletedSteps []string `json:"completed_steps"`
			FailedStep     string   `json:"failed_step"`
			Error          string   `json:"error"`
			Message        string   `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		isPartial := result.Status == "partial" || status == 207

		fmt.Println()
		if isPartial {
			fmt.Println("Cluster registered with warnings (partial success).")
			if result.FailedStep != "" {
				fmt.Printf("  Failed step: %s\n", result.FailedStep)
			}
			if result.Error != "" {
				fmt.Printf("  Error:       %s\n", result.Error)
			}
		} else {
			fmt.Println("Cluster registered:")
		}

		if result.Cluster.Server != "" {
			fmt.Printf("  Server:  %s\n", result.Cluster.Server)
		}
		if result.Cluster.ServerVersion != "" {
			fmt.Printf("  Version: %s\n", result.Cluster.ServerVersion)
		}
		if len(result.Cluster.Addons) > 0 {
			var enabled []string
			for k, v := range result.Cluster.Addons {
				if v {
					enabled = append(enabled, k)
				}
			}
			fmt.Printf("  Addons:  %s\n", strings.Join(enabled, ", "))
		}
		if result.Git != nil {
			if result.Git.PRUrl != "" {
				fmt.Printf("  Git:     PR %s\n", result.Git.PRUrl)
			} else if result.Git.Branch != "" {
				fmt.Printf("  Git:     branch %s\n", result.Git.Branch)
			}
		}
		if result.Message != "" {
			fmt.Printf("  Note:    %s\n", result.Message)
		}

		return nil
	},
}

var removeClusterCmd = &cobra.Command{
	Use:   "remove-cluster <name>",
	Short: "Deregister a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		fmt.Printf("Removing cluster %s... ", name)
		respBody, status, err := apiRequest("DELETE", "/api/v1/clusters/"+url.PathEscape(name), nil)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status     string `json:"status"`
			FailedStep string `json:"failed_step"`
			Error      string `json:"error"`
			Message    string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		isPartial := result.Status == "partial" || status == 207

		if isPartial {
			fmt.Println("Cluster removed with warnings (partial success).")
			if result.FailedStep != "" {
				fmt.Printf("  Failed step: %s\n", result.FailedStep)
			}
			if result.Error != "" {
				fmt.Printf("  Error: %s\n", result.Error)
			}
		} else {
			fmt.Printf("Cluster %s removed.\n", name)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}

var updateClusterCmd = &cobra.Command{
	Use:   "update-cluster <name>",
	Short: "Update addon assignments for a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		addFlag, _ := cmd.Flags().GetString("add-addon")
		removeFlag, _ := cmd.Flags().GetString("remove-addon")

		addons := make(map[string]bool)
		if addFlag != "" {
			for _, a := range strings.Split(addFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					addons[a] = true
				}
			}
		}
		if removeFlag != "" {
			for _, a := range strings.Split(removeFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					if _, conflict := addons[a]; conflict {
						return fmt.Errorf("addon %q appears in both --add-addon and --remove-addon", a)
					}
					addons[a] = false
				}
			}
		}

		if len(addons) == 0 {
			return fmt.Errorf("at least one of --add-addon or --remove-addon is required")
		}

		body := map[string]interface{}{
			"addons": addons,
		}

		fmt.Printf("Updating cluster %s... ", name)
		respBody, status, err := apiRequest("PATCH", "/api/v1/clusters/"+url.PathEscape(name), body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		isPartial := result.Status == "partial" || status == 207

		if isPartial {
			fmt.Println("Update completed with warnings (partial success).")
		} else {
			fmt.Printf("Cluster %s updated.\n", name)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}

var listClustersCmd = &cobra.Command{
	Use:   "list-clusters",
	Short: "List all clusters",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/clusters")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var resp struct {
			Clusters []struct {
				Name             string            `json:"name"`
				Region           string            `json:"region"`
				ConnectionStatus string            `json:"connection_status"`
				Labels           map[string]string `json:"labels"`
			} `json:"clusters"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.Clusters) == 0 {
			fmt.Println("No clusters found.")
			return nil
		}

		nameW, statusW, regionW := 4, 6, 6
		for _, c := range resp.Clusters {
			if len(c.Name) > nameW {
				nameW = len(c.Name)
			}
			s := c.ConnectionStatus
			if s == "" {
				s = "unknown"
			}
			if len(s) > statusW {
				statusW = len(s)
			}
			r := c.Region
			if r == "" {
				r = "-"
			}
			if len(r) > regionW {
				regionW = len(r)
			}
		}

		fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameW, "NAME", statusW, "STATUS", regionW, "REGION", "ADDONS")

		for _, c := range resp.Clusters {
			s := c.ConnectionStatus
			if s == "" {
				s = "unknown"
			}
			r := c.Region
			if r == "" {
				r = "-"
			}
			addonCount := 0
			for k, v := range c.Labels {
				if v == "enabled" && !knownNonAddonLabels[k] && !strings.Contains(k, "/") {
					addonCount++
				}
			}
			fmt.Printf("%-*s  %-*s  %-*s  %d\n", nameW, c.Name, statusW, s, regionW, r, addonCount)
		}

		return nil
	},
}

// printAPIError formats and returns an error from an API error response.
func printAPIError(body []byte, status int) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err != nil {
		return fmt.Errorf("API error (HTTP %d): %s", status, string(body))
	}
	msg, _ := errResp["error"].(string)
	if msg == "" {
		msg = string(body)
	}
	return fmt.Errorf("API error (HTTP %d): %s", status, msg)
}
