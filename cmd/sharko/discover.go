package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover available clusters from cloud providers",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		roleARNStr, _ := cmd.Flags().GetString("role-arn")
		region, _ := cmd.Flags().GetString("region")
		output, _ := cmd.Flags().GetString("output")

		if provider != "eks" {
			return fmt.Errorf("unsupported provider %q; only \"eks\" is supported", provider)
		}

		var roleARNs []string
		if roleARNStr != "" {
			for _, r := range strings.Split(roleARNStr, ",") {
				r = strings.TrimSpace(r)
				if r != "" {
					roleARNs = append(roleARNs, r)
				}
			}
		}

		body := map[string]interface{}{
			"provider":  provider,
			"role_arns": roleARNs,
		}
		if region != "" {
			body["region"] = region
		}

		respBody, status, err := apiPost("/api/v1/clusters/discover", body)
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
			Clusters []struct {
				Name       string `json:"name"`
				Region     string `json:"region"`
				Account    string `json:"account"`
				K8sVersion string `json:"k8s_version"`
				Endpoint   string `json:"endpoint"`
				Status     string `json:"status"`
				Error      string `json:"error,omitempty"`
			} `json:"clusters"`
			Error string `json:"error,omitempty"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.Clusters) == 0 {
			fmt.Println("No clusters found.")
			return nil
		}

		// Calculate column widths.
		nameW, regionW, accountW, versionW, endpointW, statusW := 4, 6, 7, 7, 8, 6
		for _, c := range resp.Clusters {
			if len(c.Name) > nameW {
				nameW = len(c.Name)
			}
			if len(c.Region) > regionW {
				regionW = len(c.Region)
			}
			if len(c.Account) > accountW {
				accountW = len(c.Account)
			}
			if len(c.K8sVersion) > versionW {
				versionW = len(c.K8sVersion)
			}
			ep := c.Endpoint
			if len(ep) > 50 {
				ep = ep[:47] + "..."
			}
			if len(ep) > endpointW {
				endpointW = len(ep)
			}
			if len(c.Status) > statusW {
				statusW = len(c.Status)
			}
		}

		fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
			nameW, "NAME", regionW, "REGION", accountW, "ACCOUNT", versionW, "VERSION", endpointW, "ENDPOINT", statusW, "STATUS")

		for _, c := range resp.Clusters {
			ep := c.Endpoint
			if len(ep) > 50 {
				ep = ep[:47] + "..."
			}
			fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
				nameW, c.Name, regionW, c.Region, accountW, c.Account, versionW, c.K8sVersion, endpointW, ep, statusW, c.Status)
		}

		if resp.Error != "" {
			fmt.Printf("\nWarning: %s\n", resp.Error)
		}

		return nil
	},
}

func init() {
	discoverCmd.Flags().String("provider", "eks", "Cloud provider (eks)")
	discoverCmd.Flags().String("role-arn", "", "Comma-separated IAM role ARNs for cross-account discovery")
	discoverCmd.Flags().String("region", "", "AWS region to scan (defaults to server config)")
	discoverCmd.Flags().String("output", "table", "Output format (table, json)")

	rootCmd.AddCommand(discoverCmd)
}
