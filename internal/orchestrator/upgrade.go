package orchestrator

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

// UpgradeAddonGlobal changes the version in the addons-catalog.yaml.
// Affects every cluster using the global version.
func (o *Orchestrator) UpgradeAddonGlobal(ctx context.Context, addonName, newVersion string) (*GitResult, error) {
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if newVersion == "" {
		return nil, fmt.Errorf("new version is required")
	}

	catalogPath := o.paths.Catalog
	content, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	updated, err := gitops.UpdateCatalogVersion(content, addonName, newVersion)
	if err != nil {
		return nil, fmt.Errorf("updating version for addon %q: %w", addonName, err)
	}

	files := map[string][]byte{
		catalogPath: updated,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("upgrade addon %s to %s", addonName, newVersion))
	if err != nil {
		return nil, fmt.Errorf("committing upgrade of addon %q to %s: %w", addonName, newVersion, err)
	}

	return gitResult, nil
}

// UpgradeAddonCluster changes the version in a specific cluster's values file.
// Only affects that cluster (per-cluster override).
func (o *Orchestrator) UpgradeAddonCluster(ctx context.Context, addonName, clusterName, newVersion string) (*GitResult, error) {
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if newVersion == "" {
		return nil, fmt.Errorf("new version is required")
	}

	valuesPath := path.Join(o.paths.ClusterValues, clusterName+".yaml")
	content, err := o.git.GetFileContent(ctx, valuesPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading values for cluster %q: %w", clusterName, err)
	}

	updated := setAddonVersionInClusterValues(string(content), addonName, newVersion)

	files := map[string][]byte{
		valuesPath: []byte(updated),
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("upgrade addon %s to %s on cluster %s", addonName, newVersion, clusterName))
	if err != nil {
		return nil, fmt.Errorf("committing upgrade of addon %q to %s on cluster %q: %w", addonName, newVersion, clusterName, err)
	}

	return gitResult, nil
}

// UpgradeAddons upgrades multiple addons in one PR (global catalog changes).
func (o *Orchestrator) UpgradeAddons(ctx context.Context, upgrades map[string]string) (*GitResult, error) {
	if len(upgrades) == 0 {
		return nil, fmt.Errorf("at least one addon upgrade is required")
	}

	// Read the catalog once, apply all version changes, commit as single PR.
	catalogPath := o.paths.Catalog
	content, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	var names []string
	for addonName, newVersion := range upgrades {
		if addonName == "" || newVersion == "" {
			return nil, fmt.Errorf("addon name and version are required for each upgrade")
		}
		content, err = gitops.UpdateCatalogVersion(content, addonName, newVersion)
		if err != nil {
			return nil, fmt.Errorf("updating version for addon %q: %w", addonName, err)
		}
		names = append(names, fmt.Sprintf("%s=%s", addonName, newVersion))
	}

	files := map[string][]byte{
		catalogPath: content,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("upgrade addons: %s", strings.Join(names, ", ")))
	if err != nil {
		return nil, fmt.Errorf("committing batch addon upgrade: %w", err)
	}

	return gitResult, nil
}

// setAddonVersionInClusterValues updates or inserts a version field under an addon section
// in a cluster values YAML file.
func setAddonVersionInClusterValues(content, addonName, newVersion string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inAddonSection := false
	versionSet := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && strings.HasSuffix(trimmed, ":") {
			if inAddonSection && !versionSet {
				result = append(result, fmt.Sprintf("  version: %s", newVersion))
				versionSet = true
			}
			inAddonSection = strings.TrimSuffix(trimmed, ":") == addonName
		}

		if inAddonSection && !versionSet && strings.HasPrefix(trimmed, "version:") {
			result = append(result, fmt.Sprintf("  version: %s", newVersion))
			versionSet = true
			continue
		}

		result = append(result, line)

		if i == len(lines)-1 && inAddonSection && !versionSet {
			result = append(result, fmt.Sprintf("  version: %s", newVersion))
			versionSet = true
		}
	}

	if !versionSet {
		result = append(result, fmt.Sprintf("%s:", addonName))
		result = append(result, fmt.Sprintf("  version: %s", newVersion))
	}

	return strings.Join(result, "\n")
}
