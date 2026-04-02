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
	Short: "Show fleet status overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/fleet/status")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var fleet struct {
			TotalClusters        int `json:"total_clusters"`
			HealthyClusters      int `json:"healthy_clusters"`
			DegradedClusters     int `json:"degraded_clusters"`
			DisconnectedClusters int `json:"disconnected_clusters"`
			TotalAddons          int `json:"total_addons"`
			TotalDeployments     int `json:"total_deployments"`
			HealthyDeployments   int `json:"healthy_deployments"`
			DegradedDeployments  int `json:"degraded_deployments"`
			OutOfSyncDeployments int `json:"out_of_sync_deployments"`
		}
		if err := json.Unmarshal(respBody, &fleet); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println("Fleet Status")
		fmt.Println(strings.Repeat("\u2500", 40))
		fmt.Printf("Clusters:    %d total", fleet.TotalClusters)
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
		fmt.Printf("Addons:      %d in catalog\n", fleet.TotalAddons)
		fmt.Printf("Deployments: %d total", fleet.TotalDeployments)
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

		return nil
	},
}
