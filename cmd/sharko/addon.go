package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	addAddonCmd.Flags().String("chart", "", "Helm chart name (required)")
	addAddonCmd.Flags().String("repo", "", "Helm chart repository URL (required)")
	addAddonCmd.Flags().String("version", "", "Chart version (required)")
	addAddonCmd.Flags().String("namespace", "", "Target namespace")
	addAddonCmd.MarkFlagRequired("chart")
	addAddonCmd.MarkFlagRequired("repo")
	addAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(addAddonCmd)

	removeAddonCmd.Flags().Bool("confirm", false, "Execute removal (without this flag, shows dry-run impact)")
	rootCmd.AddCommand(removeAddonCmd)

	configureAddonCmd.Flags().String("version", "", "Update chart version")
	configureAddonCmd.Flags().String("self-heal", "", "Auto-revert manual changes (true/false)")
	configureAddonCmd.Flags().StringSlice("sync-option", nil, "ArgoCD sync option (repeatable)")
	configureAddonCmd.Flags().String("ignore-differences", "", "ArgoCD ignoreDifferences JSON array")
	configureAddonCmd.Flags().StringSlice("extra-helm-value", nil, "Extra Helm parameter key=value (repeatable)")
	rootCmd.AddCommand(configureAddonCmd)
	rootCmd.AddCommand(describeAddonCmd)

	listAddonsCmd.Flags().Bool("show-config", false, "Show advanced config columns (self-heal, sync-options)")
	rootCmd.AddCommand(listAddonsCmd)
}

var addAddonCmd = &cobra.Command{
	Use:   "add-addon <name>",
	Short: "Add a new addon to the catalog",
	Args:  cobra.ExactArgs(1),
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

		fmt.Printf("Adding addon %s... ", name)
		respBody, status, err := apiPost("/api/v1/addons", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")

		var result struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Git     *struct {
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"git"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Addon %s added to catalog.\n", name)
		if result.Git != nil && result.Git.PRUrl != "" {
			fmt.Printf("  Git: PR %s\n", result.Git.PRUrl)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}

var removeAddonCmd = &cobra.Command{
	Use:   "remove-addon <name>",
	Short: "Remove an addon from the catalog",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		confirm, _ := cmd.Flags().GetBool("confirm")

		path := "/api/v1/addons/" + url.PathEscape(name)
		if confirm {
			path += "?confirm=true"
		}

		if confirm {
			fmt.Printf("Removing addon %s... ", name)
		} else {
			fmt.Printf("Checking impact of removing addon %s...\n", name)
		}

		respBody, status, err := apiRequest("DELETE", path, nil)
		if err != nil {
			if confirm {
				fmt.Println("failed")
			}
			return err
		}

		// Without --confirm, the server returns 400 with a dry-run impact report.
		if !confirm && status == 400 {
			var dryRun struct {
				Error  string `json:"error"`
				Impact struct {
					Addon                    string   `json:"addon"`
					AffectedClusters         []string `json:"affected_clusters"`
					TotalDeploymentsToRemove int      `json:"total_deployments_to_remove"`
					Warning                  string   `json:"warning"`
				} `json:"impact"`
			}
			if err := json.Unmarshal(respBody, &dryRun); err != nil {
				return printAPIError(respBody, status)
			}

			fmt.Println()
			fmt.Printf("Impact of removing %s:\n", name)
			fmt.Printf("  Affected clusters: %d\n", len(dryRun.Impact.AffectedClusters))
			if len(dryRun.Impact.AffectedClusters) > 0 {
				fmt.Printf("  Clusters:          %s\n", strings.Join(dryRun.Impact.AffectedClusters, ", "))
			}
			if dryRun.Impact.Warning != "" {
				fmt.Printf("  Warning:           %s\n", dryRun.Impact.Warning)
			}
			fmt.Println()
			fmt.Println("Run with --confirm to execute removal.")
			return nil
		}

		// 404 — addon not found.
		if status == 404 {
			if confirm {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		if status != 200 {
			if confirm {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		fmt.Println("done")
		fmt.Printf("Addon %s removed from catalog.\n", name)
		return nil
	},
}

var configureAddonCmd = &cobra.Command{
	Use:   "configure-addon <name>",
	Short: "Update configuration for an existing addon",
	Long: `Update one or more configuration fields for an existing addon.
Only flags that are explicitly provided are sent to the server.

Examples:
  sharko configure-addon kyverno --sync-option ServerSideApply=true
  sharko configure-addon prometheus --self-heal=false
  sharko configure-addon cert-manager --version 1.15.0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		body := map[string]interface{}{}

		if cmd.Flags().Changed("version") {
			v, _ := cmd.Flags().GetString("version")
			body["version"] = v
		}
		if cmd.Flags().Changed("self-heal") {
			s, _ := cmd.Flags().GetString("self-heal")
			body["self_heal"] = s
		}
		if cmd.Flags().Changed("sync-option") {
			opts, _ := cmd.Flags().GetStringSlice("sync-option")
			body["sync_options"] = opts
		}
		if cmd.Flags().Changed("ignore-differences") {
			raw, _ := cmd.Flags().GetString("ignore-differences")
			var parsed interface{}
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				return fmt.Errorf("--ignore-differences must be a valid JSON array: %w", err)
			}
			body["ignore_differences"] = parsed
		}
		if cmd.Flags().Changed("extra-helm-value") {
			vals, _ := cmd.Flags().GetStringSlice("extra-helm-value")
			body["extra_helm_values"] = vals
		}

		if len(body) == 0 {
			return fmt.Errorf("no flags provided — specify at least one field to update")
		}

		fmt.Printf("Configuring addon %s... ", name)
		respBody, status, err := apiPatch("/api/v1/addons/"+url.PathEscape(name), body)
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
			Status  string `json:"status"`
			Message string `json:"message"`
			Git     *struct {
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"git"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Addon %s updated.\n", name)
		if result.Git != nil && result.Git.PRUrl != "" {
			fmt.Printf("  Git: PR %s\n", result.Git.PRUrl)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return nil
	},
}

// addonDetailResponse mirrors models.AddonDetailResponse — the body GET
// /api/v1/addons/{name} actually returns (NOT /api/v1/addons/{name}/detail,
// which does not exist — a pre-existing CLI bug fixed alongside this
// struct: the old code called the wrong route and got a 404 every time).
type addonDetailResponse struct {
	Addon          addonCatalogItem           `json:"addon"`
	ApplicationSet *addonApplicationSetStatus `json:"application_set,omitempty"`
}

// addonCatalogItem mirrors models.AddonCatalogItem.
type addonCatalogItem struct {
	AddonName               string                `json:"addon_name"`
	Chart                   string                `json:"chart"`
	RepoURL                 string                `json:"repo_url"`
	Namespace               string                `json:"namespace,omitempty"`
	Version                 string                `json:"version"`
	TotalClusters           int                   `json:"total_clusters"`
	EnabledClusters         int                   `json:"enabled_clusters"`
	HealthyApplications     int                   `json:"healthy_applications"`
	DegradedApplications    int                   `json:"degraded_applications"`
	MissingApplications     int                   `json:"missing_applications"`
	DeployedClusterCount    int                   `json:"deployed_cluster_count"`
	TotalTargetClusterCount int                   `json:"total_target_cluster_count"`
	Applications            []addonDeploymentInfo `json:"applications"`
}

// addonDeploymentInfo mirrors models.AddonDeploymentInfo.
type addonDeploymentInfo struct {
	ClusterName       string `json:"cluster_name"`
	Enabled           bool   `json:"enabled"`
	ConfiguredVersion string `json:"configured_version,omitempty"`
	DeployedVersion   string `json:"deployed_version,omitempty"`
	SyncStatus        string `json:"sync_status,omitempty"`
	HealthStatus      string `json:"health_status,omitempty"`
	Status            string `json:"status"`
}

// addonApplicationSetStatus mirrors models.ApplicationSetStatusInfo.
type addonApplicationSetStatus struct {
	Name          string `json:"name"`
	GeneratedApps int    `json:"generated_apps"`
}

var describeAddonCmd = &cobra.Command{
	Use:   "describe-addon <name>",
	Short: "Show full details of an addon including deployment status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiGet("/api/v1/addons/" + url.PathEscape(name))
		if err != nil {
			return err
		}

		if status != 200 {
			return printAPIError(respBody, status)
		}

		var detail addonDetailResponse
		if err := json.Unmarshal(respBody, &detail); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		a := detail.Addon

		fmt.Printf("Addon: %s\n", a.AddonName)
		fmt.Printf("  Chart:     %s\n", a.Chart)
		fmt.Printf("  Repo:      %s\n", a.RepoURL)
		fmt.Printf("  Version:   %s\n", a.Version)
		if a.Namespace != "" {
			fmt.Printf("  Namespace: %s\n", a.Namespace)
		}
		fmt.Printf("  Clusters:  %d/%d enabled, %d/%d running (healthy)\n",
			a.EnabledClusters, a.TotalClusters, a.DeployedClusterCount, a.TotalTargetClusterCount)
		if a.DegradedApplications > 0 || a.MissingApplications > 0 {
			fmt.Printf("  Health:    %d healthy, %d degraded, %d missing\n",
				a.HealthyApplications, a.DegradedApplications, a.MissingApplications)
		}
		if detail.ApplicationSet != nil {
			fmt.Printf("  ApplicationSet: %s (%d generated app(s))\n",
				detail.ApplicationSet.Name, detail.ApplicationSet.GeneratedApps)
		}
		if len(a.Applications) > 0 {
			fmt.Println("  Deployments:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "    CLUSTER\tENABLED\tCONFIGURED\tDEPLOYED\tSYNC\tHEALTH")
			for _, d := range a.Applications {
				fmt.Fprintf(w, "    %s\t%v\t%s\t%s\t%s\t%s\n",
					d.ClusterName, d.Enabled, d.ConfiguredVersion, d.DeployedVersion, d.SyncStatus, d.HealthStatus)
			}
			w.Flush()
		}

		return nil
	},
}

// addonCatalogEntry mirrors models.AddonCatalogEntry — the catalog entry
// shape GET /api/v1/addons/list returns.
type addonCatalogEntry struct {
	Name        string   `json:"name"`
	RepoURL     string   `json:"repoURL"`
	Chart       string   `json:"chart"`
	Version     string   `json:"version"`
	Namespace   string   `json:"namespace,omitempty"`
	SelfHeal    *bool    `json:"selfHeal,omitempty"`
	SyncOptions []string `json:"syncOptions,omitempty"`
}

var listAddonsCmd = &cobra.Command{
	Use:   "list-addons",
	Short: "List addons in the catalog",
	Long: `List all addons in the catalog.

Examples:
  sharko list-addons
  sharko list-addons --show-config`,
	RunE: func(cmd *cobra.Command, args []string) error {
		showConfig, _ := cmd.Flags().GetBool("show-config")

		// Route fixed alongside this command: the old code called GET
		// /api/v1/addons, which does not exist (a pre-existing 404 bug) —
		// the real route is /api/v1/addons/list, and its response is keyed
		// "applicationsets" (the server's field name, carried over verbatim
		// here rather than reshaped, since this is a CLI-only lane).
		respBody, status, err := apiGet("/api/v1/addons/list")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var resp struct {
			Addons []addonCatalogEntry `json:"applicationsets"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.Addons) == 0 {
			fmt.Println("No addons in catalog.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if showConfig {
			fmt.Fprintln(w, "NAME\tCHART\tVERSION\tNAMESPACE\tSELF-HEAL\tSYNC-OPTIONS")
		} else {
			fmt.Fprintln(w, "NAME\tCHART\tVERSION\tNAMESPACE")
		}

		for _, a := range resp.Addons {
			if showConfig {
				selfHeal := "false"
				if a.SelfHeal != nil && *a.SelfHeal {
					selfHeal = "true"
				}
				syncOpts := "(none)"
				if len(a.SyncOptions) > 0 {
					syncOpts = strings.Join(a.SyncOptions, ",")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					a.Name, a.Chart, a.Version, a.Namespace, selfHeal, syncOpts)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					a.Name, a.Chart, a.Version, a.Namespace)
			}
		}
		w.Flush()
		return nil
	},
}
