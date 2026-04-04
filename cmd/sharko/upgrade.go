package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	upgradeAddonCmd.Flags().String("version", "", "Target version (required)")
	upgradeAddonCmd.Flags().String("cluster", "", "Upgrade on a specific cluster only (per-cluster override)")
	upgradeAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(upgradeAddonCmd)

	rootCmd.AddCommand(upgradeAddonsCmd)
}

var upgradeAddonCmd = &cobra.Command{
	Use:   "upgrade-addon <name>",
	Short: "Upgrade an addon version (global or per-cluster)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ver, _ := cmd.Flags().GetString("version")
		cluster, _ := cmd.Flags().GetString("cluster")

		body := map[string]string{
			"version": ver,
		}
		if cluster != "" {
			body["cluster"] = cluster
		}

		target := "globally"
		if cluster != "" {
			target = fmt.Sprintf("on cluster %s", cluster)
		}
		fmt.Printf("Upgrading addon %s to %s %s... ", name, ver, target)

		path := "/api/v1/addons/" + url.PathEscape(name) + "/upgrade"
		respBody, status, err := apiPost(path, body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			PRUrl  string `json:"pr_url"`
			Branch string `json:"branch"`
			Merged bool   `json:"merged"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil
		}
		if result.PRUrl != "" {
			fmt.Printf("  PR: %s\n", result.PRUrl)
		}
		if result.Merged {
			fmt.Println("  Auto-merged: yes")
		}

		return nil
	},
}

var upgradeAddonsCmd = &cobra.Command{
	Use:   "upgrade-addons <addon=version,...>",
	Short: "Upgrade multiple addons in one PR",
	Long:  "Upgrade multiple addons at once. Format: cert-manager=1.15.0,metrics-server=0.7.1",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		upgrades := make(map[string]string)

		pairs := strings.Split(args[0], ",")
		for _, pair := range pairs {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("invalid format %q: expected addon=version", pair)
			}
			upgrades[parts[0]] = parts[1]
		}

		body := map[string]interface{}{
			"upgrades": upgrades,
		}

		fmt.Printf("Upgrading %d addons... ", len(upgrades))
		respBody, status, err := apiPost("/api/v1/addons/upgrade-batch", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			PRUrl  string `json:"pr_url"`
			Branch string `json:"branch"`
			Merged bool   `json:"merged"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil
		}
		if result.PRUrl != "" {
			fmt.Printf("  PR: %s\n", result.PRUrl)
		}
		if result.Merged {
			fmt.Println("  Auto-merged: yes")
		}

		return nil
	},
}
