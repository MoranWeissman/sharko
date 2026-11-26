package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	upgradeAddonCmd.Flags().String("version", "", "Target version (required)")
	upgradeAddonCmd.Flags().String("cluster", "", "Upgrade on this one cluster only (the other clusters stay as they are)")
	upgradeAddonCmd.Flags().Bool("dry-run", false, "Show the files the pull request would change, without opening it")
	upgradeAddonCmd.Flags().Bool("auto-merge", false, "Auto-merge the PR (overrides the server default; only sent when you set this flag)")
	upgradeAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(upgradeAddonCmd)

	upgradeAddonsCmd.Flags().Bool("auto-merge", false, "Auto-merge the PR (overrides the server default; only sent when you set this flag)")
	rootCmd.AddCommand(upgradeAddonsCmd)
}

// UpgradeAddonLong is the --help text of upgrade-addon. A test pins it,
// because it is the only place the command line says what "for all
// clusters" changes.
const UpgradeAddonLong = `Upgrades one addon in one pull request.

Without --cluster the addon is upgraded for ALL clusters: the version in the
catalog changes, and a cluster that had its own version for this addon goes
back to following the catalog. A cluster that is already on a newer version
is left as it is.

With --cluster only that cluster moves. The catalog and the other clusters
stay as they are. To move several clusters at once, use 'sharko
upgrade-clusters'.

The version must exist in the addon's chart repository. A version that is
not there is refused and no pull request is opened.`

var upgradeAddonCmd = &cobra.Command{
	Use:   "upgrade-addon <name>",
	Short: "Upgrade an addon version (all clusters, or one with --cluster)",
	Long:  UpgradeAddonLong,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ver, _ := cmd.Flags().GetString("version")
		cluster, _ := cmd.Flags().GetString("cluster")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		// One route for every repo layout. The server decides what an
		// upgrade writes; the command line used to guess the layout first
		// and, on the current layout, refuse an all-clusters upgrade.
		body := map[string]interface{}{
			"version": ver,
		}
		if cluster != "" {
			body["cluster"] = cluster
		}
		if dryRun {
			body["dry_run"] = true
		}
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		target := "on all clusters"
		if cluster != "" {
			target = fmt.Sprintf("on cluster %s", cluster)
		}
		if dryRun {
			fmt.Printf("Dry-run: previewing upgrade of %s to %s %s...\n", name, ver, target)
		} else {
			fmt.Printf("Upgrading addon %s to %s %s... ", name, ver, target)
		}

		path := "/api/v1/addons/" + url.PathEscape(name) + "/upgrade"
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
		return printUpgradeResponse(respBody)
	},
}

var upgradeAddonsCmd = &cobra.Command{
	Use:   "upgrade-addons <addon=version,...>",
	Short: "Upgrade multiple addons for all clusters in one PR",
	Long:  "Upgrade multiple addons at once, each for all clusters. Format: cert-manager=1.15.0,metrics-server=0.7.1",
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
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		fmt.Printf("Upgrading %d addons on all clusters... ", len(upgrades))
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
		return printUpgradeResponse(respBody)
	},
}

// printUpgradeResponse prints what an upgrade call answered: the preview or
// the pull request, then anything the server said the upgrade left on the
// old chart's values, then the shared-credential note when there is one.
func printUpgradeResponse(respBody []byte) error {
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

// uniqueSorted drops repeats and empty names, so a cluster named twice on
// the command line is counted once.
func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
