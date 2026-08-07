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

		// The v3 route this command has always used (POST
		// /addons/{name}/upgrade) writes the catalog/values files a v4 repo
		// does not read. Check the repo format first so a v4 caller gets a
		// plain-English redirect instead of a 409 from a route with no CLI
		// command to follow up with.
		fs, fsErr := repoFormat()
		if fsErr == nil {
			switch fs.Format {
			case "v4":
				if cluster == "" {
					return fmt.Errorf(
						"this repo uses the v4 format, which pins addon versions per cluster rather than globally — pick the clusters and run:\n  %s",
						upgradeClustersCommand(name, ver, []string{"<cluster>"}))
				}
				fmt.Printf("Repo format is v4 — routing through 'upgrade-clusters' for cluster %s.\n", cluster)
				return runUpgradeClusters(name, ver, []string{cluster})
			case "mixed":
				return fmt.Errorf("%s", fs.Message)
			}
		}

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

		// Same v3-route check as upgrade-addon: POST /addons/upgrade-batch
		// writes the v3 catalog shape, which a v4 repo does not read.
		fs, fsErr := repoFormat()
		if fsErr == nil {
			switch fs.Format {
			case "v4":
				var lines []string
				for addon, version := range upgrades {
					lines = append(lines, "  "+upgradeClustersCommand(addon, version, []string{"<cluster>"}))
				}
				return fmt.Errorf(
					"this repo uses the v4 format, which pins addon versions per cluster rather than globally — there is no single batch call for that. Run one 'upgrade-clusters' per addon instead:\n%s",
					strings.Join(lines, "\n"))
			case "mixed":
				return fmt.Errorf("%s", fs.Message)
			}
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

// runUpgradeClusters posts the v4 fleet-upgrade call for a single addon and
// prints the result the same way upgrade-clusters does. Shared by
// upgrade-addon's transparent v4 redirect (--cluster given, one cluster)
// and the upgrade-clusters command itself would use the same route, but
// keeps its own RunE for the multi-cluster, flag-driven case — this helper
// is just for the redirect.
func runUpgradeClusters(addon, version string, clusters []string) error {
	body := map[string]interface{}{
		"version":  version,
		"clusters": clusters,
		"yes":      true,
	}
	path := "/api/v1/addons/" + url.PathEscape(addon) + "/upgrade-clusters"
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

	unwrapped, note := unwrapAttribution(respBody)
	var result cliGitResult
	if err := json.Unmarshal(unwrapped, &result); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	fmt.Println()
	printGitResult(&result)
	if note != "" {
		fmt.Printf("  Note: %s\n", note)
	}
	return nil
}
