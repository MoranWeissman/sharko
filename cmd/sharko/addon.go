package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	addAddonCmd.Flags().String("chart", "", "Helm chart name (required)")
	addAddonCmd.Flags().String("repo", "", "Helm chart repository URL (required)")
	addAddonCmd.Flags().String("version", "", "Chart version (required)")
	addAddonCmd.Flags().String("namespace", "", "Target namespace")
	addAddonCmd.Flags().Int("sync-wave", 0, "ArgoCD sync wave (0 = default, negative = earlier)")
	addAddonCmd.MarkFlagRequired("chart")
	addAddonCmd.MarkFlagRequired("repo")
	addAddonCmd.MarkFlagRequired("version")
	rootCmd.AddCommand(addAddonCmd)

	removeAddonCmd.Flags().Bool("confirm", false, "Execute removal (without this flag, shows dry-run impact)")
	rootCmd.AddCommand(removeAddonCmd)

	configureAddonCmd.Flags().String("version", "", "Update chart version")
	configureAddonCmd.Flags().Int("sync-wave", 0, "Deployment ordering (-2=early, 0=default, 2=late)")
	configureAddonCmd.Flags().String("self-heal", "", "Auto-revert manual changes (true/false)")
	configureAddonCmd.Flags().StringSlice("sync-option", nil, "ArgoCD sync option (repeatable)")
	configureAddonCmd.Flags().String("ignore-differences", "", "ArgoCD ignoreDifferences JSON array")
	configureAddonCmd.Flags().StringSlice("extra-helm-value", nil, "Extra Helm parameter key=value (repeatable)")
	rootCmd.AddCommand(configureAddonCmd)
	rootCmd.AddCommand(describeAddonCmd)
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
		syncWave, _ := cmd.Flags().GetInt("sync-wave")

		body := map[string]interface{}{
			"name":      name,
			"chart":     chart,
			"repo_url":  repo,
			"version":   ver,
			"namespace": namespace,
		}
		if syncWave != 0 {
			body["sync_wave"] = syncWave
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
					Addon                   string   `json:"addon"`
					AffectedClusters        []string `json:"affected_clusters"`
					TotalDeploymentsToRemove int      `json:"total_deployments_to_remove"`
					Warning                 string   `json:"warning"`
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
  sharko configure-addon istio-base --sync-wave -1
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
		if cmd.Flags().Changed("sync-wave") {
			w, _ := cmd.Flags().GetInt("sync-wave")
			body["sync_wave"] = w
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

var describeAddonCmd = &cobra.Command{
	Use:   "describe-addon <name>",
	Short: "Show full details of an addon including defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiGet("/api/v1/addons/" + url.PathEscape(name) + "/detail")
		if err != nil {
			return err
		}

		if status != 200 {
			return printAPIError(respBody, status)
		}

		var detail struct {
			Name      string `json:"name"`
			Chart     string `json:"chart"`
			RepoURL   string `json:"repo_url"`
			Version   struct {
				Value     string `json:"value"`
				IsDefault bool   `json:"is_default"`
			} `json:"version"`
			Namespace struct {
				Value     string `json:"value"`
				IsDefault bool   `json:"is_default"`
			} `json:"namespace"`
			SyncWave struct {
				Value     int  `json:"value"`
				IsDefault bool `json:"is_default"`
			} `json:"sync_wave"`
			SelfHeal struct {
				Value     bool `json:"value"`
				IsDefault bool `json:"is_default"`
			} `json:"self_heal"`
			SyncOptions        []string `json:"sync_options"`
			IgnoreDifferences  []interface{} `json:"ignore_differences"`
			ExtraHelmValues    []string `json:"extra_helm_values"`
			AdditionalSources  []interface{} `json:"additional_sources"`
		}

		if err := json.Unmarshal(respBody, &detail); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		defaultTag := func(isDefault bool) string {
			if isDefault {
				return " (default)"
			}
			return ""
		}
		noneIfEmpty := func(s []string) string {
			if len(s) == 0 {
				return "(none)"
			}
			return strings.Join(s, ", ")
		}
		noneIfEmptyAny := func(s []interface{}) string {
			if len(s) == 0 {
				return "(none)"
			}
			b, _ := json.Marshal(s)
			return string(b)
		}

		fmt.Printf("Addon: %s\n", detail.Name)
		fmt.Printf("  Chart:     %s\n", detail.Chart)
		fmt.Printf("  Repo:      %s\n", detail.RepoURL)
		fmt.Printf("  Version:   %s%s\n", detail.Version.Value, defaultTag(detail.Version.IsDefault))
		fmt.Printf("  Namespace: %s%s\n", detail.Namespace.Value, defaultTag(detail.Namespace.IsDefault))
		fmt.Printf("  Sync Wave: %d%s\n", detail.SyncWave.Value, defaultTag(detail.SyncWave.IsDefault))
		fmt.Printf("  Self-Heal: %v%s\n", detail.SelfHeal.Value, defaultTag(detail.SelfHeal.IsDefault))
		fmt.Printf("  Sync Options: %s\n", noneIfEmpty(detail.SyncOptions))
		fmt.Printf("  Ignore Differences: %s\n", noneIfEmptyAny(detail.IgnoreDifferences))
		fmt.Printf("  Extra Helm Values: %s\n", noneIfEmpty(detail.ExtraHelmValues))
		fmt.Printf("  Additional Sources: %s\n", noneIfEmptyAny(detail.AdditionalSources))

		return nil
	},
}
