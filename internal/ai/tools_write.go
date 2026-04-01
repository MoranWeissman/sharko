package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/moran/argocd-addons-platform/internal/gitops"
)

var branchNameRe = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// sanitizeBranchName replaces non-alphanumeric, non-hyphen characters with hyphens.
func sanitizeBranchName(s string) string {
	return branchNameRe.ReplaceAllString(s, "-")
}

// GetWriteToolDefinitions returns tool definitions for write operations.
func GetWriteToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "enable_addon",
				Description: "Enable an addon on a cluster by creating a pull request that sets the addon label to enabled in cluster-addons.yaml.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"cluster_name":{"type":"string","description":"Name of the cluster"},"addon_name":{"type":"string","description":"Name of the addon to enable"},"connection":{"type":"string","description":"Optional: connection name to target a specific Git repo (for multi-repo operations)"}},"required":["cluster_name","addon_name"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "disable_addon",
				Description: "Disable an addon on a cluster by creating a pull request that sets the addon label to disabled in cluster-addons.yaml.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"cluster_name":{"type":"string","description":"Name of the cluster"},"addon_name":{"type":"string","description":"Name of the addon to disable"},"connection":{"type":"string","description":"Optional: connection name to target a specific Git repo (for multi-repo operations)"}},"required":["cluster_name","addon_name"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "update_addon_version",
				Description: "Update the version of an addon in the catalog by creating a pull request that modifies addons-catalog.yaml.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"addon_name":{"type":"string","description":"Name of the addon"},"version":{"type":"string","description":"New version to set"},"connection":{"type":"string","description":"Optional: connection name to target a specific Git repo (for multi-repo operations)"}},"required":["addon_name","version"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "sync_argocd_app",
				Description: "Trigger an ArgoCD sync operation on an application to deploy the latest desired state.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"app_name":{"type":"string","description":"Name of the ArgoCD application to sync"}},"required":["app_name"]}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "refresh_argocd_app",
				Description: "Trigger an ArgoCD refresh to re-fetch the application state from Git. Use hard=true to invalidate the cache.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"app_name":{"type":"string","description":"Name of the ArgoCD application to refresh"},"hard":{"type":"string","description":"Set to 'true' for a hard refresh that invalidates the cache (default false)"}},"required":["app_name"]}`),
			},
		},
	}
}

func (e *ToolExecutor) enableAddon(ctx context.Context, connectionName, clusterName, addonName string) (string, error) {
	if clusterName == "" {
		return "Please specify a cluster name.", nil
	}
	if addonName == "" {
		return "Please specify an addon name.", nil
	}

	gp := e.resolveProvider(connectionName)
	data, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return "", fmt.Errorf("reading cluster-addons.yaml: %w", err)
	}

	mutated, err := gitops.EnableAddonLabel(data, clusterName, addonName)
	if err != nil {
		return "", fmt.Errorf("enabling addon label: %w", err)
	}

	branch := fmt.Sprintf("aap/enable-addon/%s/%s/%d",
		sanitizeBranchName(addonName), sanitizeBranchName(clusterName), time.Now().Unix())

	if err := gp.CreateBranch(ctx, branch, "main"); err != nil {
		return "", fmt.Errorf("creating branch: %w", err)
	}

	commitMsg := fmt.Sprintf("Enable %s on %s", addonName, clusterName)
	if err := gp.CreateOrUpdateFile(ctx, "configuration/cluster-addons.yaml", mutated, branch, commitMsg); err != nil {
		return "", fmt.Errorf("updating file: %w", err)
	}

	title := fmt.Sprintf("[AAP] Enable %s on %s", addonName, clusterName)
	body := fmt.Sprintf("Automated by ArgoCD Addons Platform.\n\n**Change:** Enable addon %s on cluster %s.", addonName, clusterName)

	pr, err := gp.CreatePullRequest(ctx, title, body, branch, "main")
	if err != nil {
		return "", fmt.Errorf("creating pull request: %w", err)
	}

	return fmt.Sprintf("Pull request created: %s", pr.URL), nil
}

func (e *ToolExecutor) disableAddon(ctx context.Context, connectionName, clusterName, addonName string) (string, error) {
	if clusterName == "" {
		return "Please specify a cluster name.", nil
	}
	if addonName == "" {
		return "Please specify an addon name.", nil
	}

	gp := e.resolveProvider(connectionName)
	data, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return "", fmt.Errorf("reading cluster-addons.yaml: %w", err)
	}

	mutated, err := gitops.DisableAddonLabel(data, clusterName, addonName)
	if err != nil {
		return "", fmt.Errorf("disabling addon label: %w", err)
	}

	branch := fmt.Sprintf("aap/disable-addon/%s/%s/%d",
		sanitizeBranchName(addonName), sanitizeBranchName(clusterName), time.Now().Unix())

	if err := gp.CreateBranch(ctx, branch, "main"); err != nil {
		return "", fmt.Errorf("creating branch: %w", err)
	}

	commitMsg := fmt.Sprintf("Disable %s on %s", addonName, clusterName)
	if err := gp.CreateOrUpdateFile(ctx, "configuration/cluster-addons.yaml", mutated, branch, commitMsg); err != nil {
		return "", fmt.Errorf("updating file: %w", err)
	}

	title := fmt.Sprintf("[AAP] Disable %s on %s", addonName, clusterName)
	body := fmt.Sprintf("Automated by ArgoCD Addons Platform.\n\n**Change:** Disable addon %s on cluster %s.", addonName, clusterName)

	pr, err := gp.CreatePullRequest(ctx, title, body, branch, "main")
	if err != nil {
		return "", fmt.Errorf("creating pull request: %w", err)
	}

	return fmt.Sprintf("Pull request created: %s", pr.URL), nil
}

func (e *ToolExecutor) updateAddonVersion(ctx context.Context, connectionName, addonName, version string) (string, error) {
	if addonName == "" {
		return "Please specify an addon name.", nil
	}
	if version == "" {
		return "Please specify a version.", nil
	}

	gp := e.resolveProvider(connectionName)
	data, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return "", fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	mutated, err := gitops.UpdateCatalogVersion(data, addonName, version)
	if err != nil {
		return "", fmt.Errorf("updating catalog version: %w", err)
	}

	branch := fmt.Sprintf("aap/update-version/%s/%d",
		sanitizeBranchName(addonName), time.Now().Unix())

	if err := gp.CreateBranch(ctx, branch, "main"); err != nil {
		return "", fmt.Errorf("creating branch: %w", err)
	}

	commitMsg := fmt.Sprintf("Update %s to version %s", addonName, version)
	if err := gp.CreateOrUpdateFile(ctx, "configuration/addons-catalog.yaml", mutated, branch, commitMsg); err != nil {
		return "", fmt.Errorf("updating file: %w", err)
	}

	title := fmt.Sprintf("[AAP] Update %s to %s", addonName, version)
	body := fmt.Sprintf("Automated by ArgoCD Addons Platform.\n\n**Change:** Update addon %s version to %s.", addonName, version)

	pr, err := gp.CreatePullRequest(ctx, title, body, branch, "main")
	if err != nil {
		return "", fmt.Errorf("creating pull request: %w", err)
	}

	return fmt.Sprintf("Pull request created: %s", pr.URL), nil
}

func (e *ToolExecutor) syncArgocdApp(ctx context.Context, appName string) (string, error) {
	if appName == "" {
		return "Please specify an application name.", nil
	}

	if err := e.ac.SyncApplication(ctx, appName); err != nil {
		return "", fmt.Errorf("syncing application %q: %w", appName, err)
	}

	return fmt.Sprintf("Sync triggered for application %s.", appName), nil
}

func (e *ToolExecutor) refreshArgocdApp(ctx context.Context, appName string, hard bool) (string, error) {
	if appName == "" {
		return "Please specify an application name.", nil
	}

	app, err := e.ac.RefreshApplication(ctx, appName, hard)
	if err != nil {
		return "", fmt.Errorf("refreshing application %q: %w", appName, err)
	}

	refreshType := "normal"
	if hard {
		refreshType = "hard"
	}

	return fmt.Sprintf("Application %s refreshed (%s). Health: %s, Sync: %s.",
		appName, refreshType, app.HealthStatus, app.SyncStatus), nil
}
