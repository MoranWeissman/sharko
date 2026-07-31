package main

// catalog_add.go — the command-line door into the org's catalog.
//
// Adding an addon to catalog.yaml is the approval step: it opens a pull
// request somebody reviews, exactly like the button in the UI and exactly
// like hand-editing the file. Same operation behind all three
// (POST /api/v1/catalog/addons — internal/api/catalog_org.go).

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	addToCatalogCmd.Flags().String("chart", "", "Chart name inside the repo (required unless --from-marketplace)")
	addToCatalogCmd.Flags().String("repo", "", "Chart repository URL — https:// or oci:// (required unless --from-marketplace)")
	addToCatalogCmd.Flags().String("version", "", "Chart version (always required)")
	addToCatalogCmd.Flags().String("namespace", "", "Namespace to install into")
	addToCatalogCmd.Flags().Bool("from-marketplace", false, "Copy the chart location, namespace and needed-secrets list from the Marketplace entry of the same name")
	addToCatalogCmd.Flags().String("enable-on", "", "Also switch the addon on for this cluster, in the same pull request")
	addToCatalogCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(addToCatalogCmd)
}

var addToCatalogCmd = &cobra.Command{
	Use:   "add-to-catalog <name>",
	Short: "Add an addon to your org's catalog (catalog.yaml)",
	Long: `Adds an addon to catalog.yaml — the list of addons your org allows on
its clusters — and opens a pull request. Nothing runs anywhere until the
addon is in that file, so this is the approval step.

The entry written is self-contained: chart, chart repo, version, namespace.
Use --from-marketplace to copy the chart location and needed-secrets list
from the Marketplace entry of the same name; you still choose the version,
because the Marketplace deliberately carries none.

  sharko add-to-catalog cert-manager --from-marketplace --version 1.14.5
  sharko add-to-catalog billing-api --repo oci://your-registry/charts \
      --chart billing-api --version 2.4.0 --namespace billing

Add --enable-on <cluster> to switch it on for a cluster at the same time —
one pull request touching both files, so the reviewer sees the whole change
in one diff.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		chart, _ := cmd.Flags().GetString("chart")
		repo, _ := cmd.Flags().GetString("repo")
		ver, _ := cmd.Flags().GetString("version")
		namespace, _ := cmd.Flags().GetString("namespace")
		fromMarketplace, _ := cmd.Flags().GetBool("from-marketplace")
		enableOn, _ := cmd.Flags().GetString("enable-on")

		addon := map[string]interface{}{
			"name":             name,
			"version":          ver,
			"from_marketplace": fromMarketplace,
		}
		if chart != "" {
			addon["chart"] = chart
		}
		if repo != "" {
			addon["repo_url"] = repo
		}
		if namespace != "" {
			addon["namespace"] = namespace
		}
		body := map[string]interface{}{"addons": []interface{}{addon}}
		if enableOn != "" {
			body["enable_on_cluster"] = enableOn
		}

		fmt.Printf("Adding %s to your catalog... ", name)
		respBody, status, err := apiPost("/api/v1/catalog/addons", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		fmt.Println("done")

		// The handler returns the result flat (pr_url/branch at the top
		// level) unless the attribution-fallback wrapper kicks in, in which
		// case the same fields live under "result". Try flat first, then the
		// wrapped shape, so this keeps working either way.
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

		if enableOn != "" {
			fmt.Printf("%s added to catalog.yaml and switched on for %s.\n", name, enableOn)
		} else {
			fmt.Printf("%s added to catalog.yaml.\n", name)
		}
		switch {
		case flat.PRUrl != "":
			fmt.Printf("  Review and merge: %s\n", flat.PRUrl)
		case wrapped.Result.PRUrl != "":
			fmt.Printf("  Review and merge: %s\n", wrapped.Result.PRUrl)
		}
		if wrapped.AttributionWarning != "" {
			fmt.Printf("  Note: %s\n", wrapped.AttributionWarning)
		}
		return nil
	},
}
