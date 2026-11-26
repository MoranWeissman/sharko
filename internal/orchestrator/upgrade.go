package orchestrator

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

// UpgradeAddonRequest is the one request shape behind POST
// /addons/{name}/upgrade, the main Upgrade button and `sharko upgrade-addon`.
// Cluster empty means "for all clusters".
type UpgradeAddonRequest struct {
	Addon     string
	Version   string
	Cluster   string
	DryRun    bool
	AutoMerge *bool
}

// UpgradeAddon upgrades one addon, for all clusters or for one, on whichever
// layout the repo has. On a v4 repo it supports DryRun (the exact files the
// pull request would carry); the older layout has no preview for this call
// and refuses a DryRun rather than writing.
func (o *Orchestrator) UpgradeAddon(ctx context.Context, req UpgradeAddonRequest) (*GitResult, error) {
	if req.Addon == "" {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "addon name is required")
	}
	if strings.TrimSpace(req.Version) == "" {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "version is required")
	}
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return nil, fmt.Errorf("Sharko stopped before upgrading %s: %w", req.Addon, err)
	}
	if v4 {
		if req.Cluster != "" {
			return o.UpgradeAddonClustersV4(ctx, UpgradeAddonClustersV4Request{
				Addon: req.Addon, Clusters: []string{req.Cluster}, Version: req.Version,
				DryRun: req.DryRun, Yes: true, AutoMerge: req.AutoMerge,
			})
		}
		return o.UpgradeAddonsV4(ctx, []UpgradeAddonV4Request{{Addon: req.Addon, Version: req.Version}}, req.DryRun, req.AutoMerge)
	}
	if req.DryRun {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "a preview of this upgrade is not available on this repo layout")
	}
	if req.Cluster != "" {
		return o.UpgradeAddonCluster(ctx, req.Addon, req.Cluster, req.Version, req.AutoMerge)
	}
	return o.UpgradeAddonGlobal(ctx, req.Addon, req.Version, req.AutoMerge)
}

// UpgradeAddonGlobal changes the version in the addons-catalog.yaml.
// Affects every cluster using the global version.
func (o *Orchestrator) UpgradeAddonGlobal(ctx context.Context, addonName, newVersion string, autoMerge *bool) (*GitResult, error) {
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if newVersion == "" {
		return nil, fmt.Errorf("new version is required")
	}
	// On a v4 repo this is the all-clusters upgrade (upgrade_all_v4.go).
	// The code below writes the v3 addons-catalog.yaml, which a v4 repo does
	// not have and must never get. An unanswerable layout probe stops here
	// (fail closed), for the reason refuseV3ShapedWriteOnV4Repo gives.
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return nil, fmt.Errorf("Sharko stopped before upgrading %s: %w", addonName, err)
	}
	if v4 {
		return o.UpgradeAddonsV4(ctx, []UpgradeAddonV4Request{{Addon: addonName, Version: newVersion}}, false, autoMerge)
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

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("upgrade addon %s to %s", addonName, newVersion),
		o.prMeta(autoMerge, "addon-upgrade", fmt.Sprintf("Upgrade %s to %s", addonName, newVersion), "", addonName))
	if err != nil {
		return nil, fmt.Errorf("committing upgrade of addon %q to %s: %w", addonName, newVersion, err)
	}

	return gitResult, nil
}

// UpgradeAddonCluster changes the version in a specific cluster's values file.
// Only affects that cluster (per-cluster override).
func (o *Orchestrator) UpgradeAddonCluster(ctx context.Context, addonName, clusterName, newVersion string, autoMerge *bool) (*GitResult, error) {
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if newVersion == "" {
		return nil, fmt.Errorf("new version is required")
	}
	// On a v4 repo this is a one-cluster upgrade: the version pin in that
	// cluster's cluster-addons file (upgrade_v4.go). The code below writes
	// the v3 per-cluster values file, which a v4 repo does not read. An
	// unanswerable layout probe stops here (fail closed).
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return nil, fmt.Errorf("Sharko stopped before upgrading %s on %s: %w", addonName, clusterName, err)
	}
	if v4 {
		return o.UpgradeAddonClustersV4(ctx, UpgradeAddonClustersV4Request{
			Addon: addonName, Clusters: []string{clusterName}, Version: newVersion, Yes: true, AutoMerge: autoMerge,
		})
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

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("upgrade addon %s to %s on cluster %s", addonName, newVersion, clusterName),
		o.prMeta(autoMerge, "addon-upgrade", fmt.Sprintf("Upgrade %s to %s on cluster %s", addonName, newVersion, clusterName), clusterName, addonName))
	if err != nil {
		return nil, fmt.Errorf("committing upgrade of addon %q to %s on cluster %q: %w", addonName, newVersion, clusterName, err)
	}

	return gitResult, nil
}

// UpgradeAddons upgrades multiple addons in one PR (global catalog changes).
func (o *Orchestrator) UpgradeAddons(ctx context.Context, upgrades map[string]string, autoMerge *bool) (*GitResult, error) {
	if len(upgrades) == 0 {
		return nil, fmt.Errorf("at least one addon upgrade is required")
	}
	// On a v4 repo this is several all-clusters upgrades in ONE pull request
	// (upgrade_all_v4.go). The code below writes the v3 addons-catalog.yaml.
	// An unanswerable layout probe stops here (fail closed).
	v4, v4Err := o.isV4Repo(ctx)
	if v4Err != nil {
		return nil, fmt.Errorf("Sharko stopped before upgrading addons: %w", v4Err)
	}
	if v4 {
		reqs := make([]UpgradeAddonV4Request, 0, len(upgrades))
		for addonName, newVersion := range upgrades {
			reqs = append(reqs, UpgradeAddonV4Request{Addon: addonName, Version: newVersion})
		}
		return o.UpgradeAddonsV4(ctx, reqs, false, autoMerge)
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

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("upgrade addons: %s", strings.Join(names, ", ")),
		o.prMeta(autoMerge, "addon-upgrade", fmt.Sprintf("Batch upgrade addons: %s", strings.Join(names, ", ")), "", ""))
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
