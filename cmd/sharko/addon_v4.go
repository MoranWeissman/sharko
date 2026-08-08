package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	enableAddonCmd.Flags().String("version", "", "Pin the chart version for this cluster only (omit to leave any existing pin unchanged)")
	enableAddonCmd.Flags().Bool("clear-version", false, "Clear the per-cluster version pin and follow the catalog default again")
	enableAddonCmd.Flags().String("values-json", "", "Inline JSON object of values to deep-merge onto the cluster's values file")
	enableAddonCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	enableAddonCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	enableAddonCmd.Flags().Bool("auto-merge", false, "Auto-merge the PR (overrides the server default; only sent when you set this flag)")
	rootCmd.AddCommand(enableAddonCmd)

	disableAddonCmd.Flags().Bool("remove", false, "Delete the addon's entry entirely instead of keeping it disabled")
	disableAddonCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	disableAddonCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	disableAddonCmd.Flags().Bool("auto-merge", false, "Auto-merge the PR (overrides the server default; only sent when you set this flag)")
	rootCmd.AddCommand(disableAddonCmd)

	upgradeClustersCmd.Flags().String("version", "", "Target version (required)")
	upgradeClustersCmd.Flags().StringArray("cluster", nil, "Cluster to upgrade (repeatable; at least one required)")
	upgradeClustersCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	upgradeClustersCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	upgradeClustersCmd.Flags().Bool("auto-merge", false, "Auto-merge the PR (overrides the server default; only sent when you set this flag)")
	upgradeClustersCmd.MarkFlagRequired("version")
	upgradeClustersCmd.MarkFlagRequired("cluster")
	rootCmd.AddCommand(upgradeClustersCmd)
}

// ─────────────────────────────────────────────────────────────────────────
// enable-addon <cluster> <addon>
// ─────────────────────────────────────────────────────────────────────────

var enableAddonCmd = &cobra.Command{
	Use:   "enable-addon <cluster> <addon>",
	Short: "Enable an addon on a cluster (v4 format)",
	Long: `Enables an addon on a cluster by writing its cluster-addons entry and,
when values are supplied, the per-cluster values file. Requires the repo to
be in the v4 format — run 'sharko upgrade-clusters' style commands only
after migrating.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, addon := args[0], args[1]
		ver, _ := cmd.Flags().GetString("version")
		clearVersion, _ := cmd.Flags().GetBool("clear-version")
		valuesJSON, _ := cmd.Flags().GetString("values-json")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")

		if clearVersion && cmd.Flags().Changed("version") {
			return fmt.Errorf("--version and --clear-version are mutually exclusive")
		}

		body := map[string]interface{}{}
		switch {
		case clearVersion:
			empty := ""
			body["version"] = empty
		case cmd.Flags().Changed("version"):
			body["version"] = ver
		}
		if valuesJSON != "" {
			var values map[string]interface{}
			if err := json.Unmarshal([]byte(valuesJSON), &values); err != nil {
				return fmt.Errorf("--values-json must be a valid JSON object: %w", err)
			}
			body["values"] = values
		}
		if dryRun {
			body["dry_run"] = true
		}
		if yes {
			body["yes"] = true
		}
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing enabling %s on %s...\n", addon, cluster)
		} else {
			fmt.Printf("Enabling %s on %s... ", addon, cluster)
		}

		path := "/api/v1/v4/clusters/" + url.PathEscape(cluster) + "/addons/" + url.PathEscape(addon)
		respBody, status, err := apiPost(path, body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}
		if status != 200 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}
		if !dryRun {
			fmt.Println("done")
		}

		var result cliGitResult
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		fmt.Println()
		printGitResult(&result)
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────
// disable-addon <cluster> <addon>
// ─────────────────────────────────────────────────────────────────────────

var disableAddonCmd = &cobra.Command{
	Use:   "disable-addon <cluster> <addon>",
	Short: "Disable an addon on a cluster (v4 format)",
	Long: `Disables an addon on a cluster by setting enabled=false in its
cluster-addons entry — the entry (and its version pin and settings) is kept
by default so re-enabling is a one-word change. Pass --remove to delete the
entry entirely instead.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, addon := args[0], args[1]
		remove, _ := cmd.Flags().GetBool("remove")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")

		body := map[string]interface{}{}
		if remove {
			body["remove"] = true
		}
		if dryRun {
			body["dry_run"] = true
		}
		if yes {
			body["yes"] = true
		}
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing disabling %s on %s...\n", addon, cluster)
		} else {
			fmt.Printf("Disabling %s on %s... ", addon, cluster)
		}

		path := "/api/v1/v4/clusters/" + url.PathEscape(cluster) + "/addons/" + url.PathEscape(addon)
		respBody, status, err := apiRequest("DELETE", path, body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}
		if status != 200 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}
		if !dryRun {
			fmt.Println("done")
		}

		var result cliGitResult
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		fmt.Println()
		printGitResult(&result)
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────
// upgrade-clusters <addon>
// ─────────────────────────────────────────────────────────────────────────

var upgradeClustersCmd = &cobra.Command{
	Use:   "upgrade-clusters <addon>",
	Short: "Upgrade an addon's version pin on a chosen subset of clusters (v4 format)",
	Long: `Bumps one addon's version pin to a new value on exactly the clusters
listed with --cluster, in one pull request. Clusters left out are untouched.
Every selected cluster must already have the addon enabled — this never
enables an addon as a side effect of upgrading it.

This is the fleet-upgrade route the UI uses, and the one 'sharko
upgrade-addon' transparently switches to on a v4 repo when you pass
--cluster.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addon := args[0]
		ver, _ := cmd.Flags().GetString("version")
		clusters, _ := cmd.Flags().GetStringArray("cluster")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")

		if len(clusters) == 0 {
			return fmt.Errorf("at least one --cluster is required")
		}

		body := map[string]interface{}{
			"version":  ver,
			"clusters": clusters,
		}
		if dryRun {
			body["dry_run"] = true
		}
		if yes {
			body["yes"] = true
		}
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing upgrade of %s to %s on %d cluster(s)...\n", addon, ver, len(clusters))
		} else {
			fmt.Printf("Upgrading %s to %s on %d cluster(s)... ", addon, ver, len(clusters))
		}

		path := "/api/v1/addons/" + url.PathEscape(addon) + "/upgrade-clusters"
		respBody, status, err := apiPost(path, body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}
		if status != 200 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}
		if !dryRun {
			fmt.Println("done")
		}

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
	},
}

// upgradeClustersCommand renders the exact `sharko upgrade-clusters`
// invocation for a given addon/version/cluster set — used by upgrade-addon
// when it declines to run on a v4 repo without --cluster, so the operator
// gets a copy-pasteable next step instead of a dead end.
func upgradeClustersCommand(addon, version string, clusters []string) string {
	parts := []string{"sharko", "upgrade-clusters", addon, "--version", version}
	for _, c := range clusters {
		parts = append(parts, "--cluster", c)
	}
	return strings.Join(parts, " ")
}
