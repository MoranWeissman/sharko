package main

// catalog_delta.go — CLI door for the v4 catalog delta model (v4 wave 1
// Story 3.3 "internal addons"). Adds a first-class in-house addon to the
// caller's catalog.yaml (kind AddonCatalog), same shape as
// `sharko add-addon` for the v3 catalog. Mirrors POST
// /api/v1/catalog/delta/addons (internal/api/catalog_delta.go).

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	addInternalAddonCmd.Flags().String("chart", "", "Helm chart name inside repo (required)")
	addInternalAddonCmd.Flags().String("repo", "", "Chart repository URL — https(s):// or oci:// (required)")
	addInternalAddonCmd.Flags().String("version", "", "Chart version (required)")
	addInternalAddonCmd.Flags().String("namespace", "", "Target namespace")
	addInternalAddonCmd.MarkFlagRequired("chart")
	addInternalAddonCmd.MarkFlagRequired("repo")
	addInternalAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(addInternalAddonCmd)
}

var addInternalAddonCmd = &cobra.Command{
	Use:   "add-internal-addon <name>",
	Short: "Add an in-house addon to your v4 catalog delta (catalog.yaml)",
	Long: `Adds (or updates) one first-class in-house addon in your v4
catalog.yaml (kind AddonCatalog), committed via a pull request
like every other Sharko write. repo_url, chart, and version are all
required — nothing else can supply them for an addon with no shipped
catalog entry. Once the PR merges, the addon is assignable to clusters and
appears in the merged catalog view (origin=internal).

Use this for a private OCI chart (e.g. --repo oci://your-registry/charts)
or any other chart Sharko doesn't ship in its curated catalog.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		chart, _ := cmd.Flags().GetString("chart")
		repo, _ := cmd.Flags().GetString("repo")
		ver, _ := cmd.Flags().GetString("version")
		namespace, _ := cmd.Flags().GetString("namespace")

		body := map[string]interface{}{
			"name":      name,
			"chart":     chart,
			"repo_url":  repo,
			"version":   ver,
			"namespace": namespace,
		}

		fmt.Printf("Adding internal addon %s... ", name)
		respBody, status, err := apiPost("/api/v1/catalog/delta/addons", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		fmt.Println("done")

		// The handler returns *orchestrator.GitResult flat (pr_url/branch at
		// the top level) — NOT nested under a "git" key — unless the
		// attribution-fallback wrapper kicks in, in which case the same
		// fields live under "result". Try flat first, then the wrapped shape,
		// so this keeps working either way.
		var flat struct {
			PRUrl  string `json:"pr_url"`
			Branch string `json:"branch"`
		}
		var wrapped struct {
			Result struct {
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"result"`
			AttributionWarning string `json:"attribution_warning"`
		}
		_ = json.Unmarshal(respBody, &flat)
		_ = json.Unmarshal(respBody, &wrapped)

		fmt.Printf("Addon %s added to catalog.yaml.\n", name)
		switch {
		case flat.PRUrl != "":
			fmt.Printf("  Git: PR %s\n", flat.PRUrl)
		case wrapped.Result.PRUrl != "":
			fmt.Printf("  Git: PR %s\n", wrapped.Result.PRUrl)
		}
		if wrapped.AttributionWarning != "" {
			fmt.Printf("  Note: %s\n", wrapped.AttributionWarning)
		}
		return nil
	},
}
