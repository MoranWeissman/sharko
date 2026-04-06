package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster status overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/fleet/status")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var fleet struct {
			ServerVersion        string `json:"server_version"`
			Uptime               string `json:"uptime"`
			GitUnavailable       bool   `json:"git_unavailable"`
			ArgoUnavailable      bool   `json:"argo_unavailable"`
			TotalClusters        int    `json:"total_clusters"`
			HealthyClusters      int    `json:"healthy_clusters"`
			DegradedClusters     int    `json:"degraded_clusters"`
			DisconnectedClusters int    `json:"disconnected_clusters"`
			TotalAddons          int    `json:"total_addons"`
			TotalDeployments     int    `json:"total_deployments"`
			HealthyDeployments   int    `json:"healthy_deployments"`
			DegradedDeployments  int    `json:"degraded_deployments"`
			OutOfSyncDeployments int    `json:"out_of_sync_deployments"`
		}
		if err := json.Unmarshal(respBody, &fleet); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println("Cluster Status Overview")
		fmt.Println(strings.Repeat("\u2500", 40))

		// Server info — always shown.
		serverLine := "Sharko Server: " + fleet.ServerVersion
		if fleet.Uptime != "" {
			serverLine += " (uptime: " + fleet.Uptime + ")"
		}
		fmt.Println(serverLine)

		// Git / ArgoCD availability.
		if fleet.GitUnavailable {
			fmt.Println("Git:          not configured (run 'sharko connect' to set up)")
		}
		if fleet.ArgoUnavailable {
			fmt.Println("ArgoCD:       not connected")
		}

		// Cluster and addon data — only shown when available.
		if !fleet.GitUnavailable && !fleet.ArgoUnavailable {
			fmt.Printf("Clusters:     %d total", fleet.TotalClusters)
			if fleet.TotalClusters > 0 {
				fmt.Printf(" (%d healthy", fleet.HealthyClusters)
				if fleet.DegradedClusters > 0 {
					fmt.Printf(", %d degraded", fleet.DegradedClusters)
				}
				if fleet.DisconnectedClusters > 0 {
					fmt.Printf(", %d disconnected", fleet.DisconnectedClusters)
				}
				fmt.Print(")")
			}
			fmt.Println()
			fmt.Printf("Addons:       %d in catalog\n", fleet.TotalAddons)
			fmt.Printf("Deployments:  %d total", fleet.TotalDeployments)
			if fleet.TotalDeployments > 0 {
				fmt.Printf(" (%d healthy", fleet.HealthyDeployments)
				if fleet.DegradedDeployments > 0 {
					fmt.Printf(", %d degraded", fleet.DegradedDeployments)
				}
				if fleet.OutOfSyncDeployments > 0 {
					fmt.Printf(", %d out-of-sync", fleet.OutOfSyncDeployments)
				}
				fmt.Print(")")
			}
			fmt.Println()
		}

		return nil
	},
}
