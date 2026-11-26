// Package orchestrator — v4 subset upgrade (v4 Wave 2 Epic 7 Story 7.2).
//
// UpgradeAddonClustersV4 is the "one action, one PR" fleet-upgrade write:
// given an addon that is outdated on N of a fleet's M clusters, the caller
// picks exactly which of the N clusters to move, and Sharko writes ONE
// pull request that bumps the per-cluster version pin
// (cluster-addons/<cluster>.yaml, design doc §2.1's spec.addons.<name>.version)
// on those clusters only — every other cluster's file is untouched, so the
// diff is small and reviewable one block per cluster.
//
// This is deliberately narrower than UpgradeAddonGlobal/UpgradeAddonCluster
// (upgrade.go, the v3-format counterparts): those write the fleet-wide
// catalog default or a single cluster's override. This one is v4-only —
// there is no v3 equivalent of "pick a subset of clusters, one commit" —
// and it requires the addon to already be enabled on every selected
// cluster; it never enables an addon as a side effect of upgrading it.
package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
)

// UpgradeAddonClustersV4Request is the input to UpgradeAddonClustersV4.
type UpgradeAddonClustersV4Request struct {
	Addon string `json:"addon"`
	// Clusters is the chosen subset to upgrade. Duplicates are dropped and
	// the set is sorted before writing, so the resulting PR diff and dry
	// run preview are deterministic regardless of request order.
	Clusters []string `json:"clusters"`
	// Version is the new pin, written identically to every selected
	// cluster's cluster-addons/<cluster>.yaml. Required and non-empty — this
	// endpoint always sets an explicit pin; use DisableAddonV4/
	// EnableAddonV4(version: nil) to remove a pin and fall back to the
	// catalog default.
	Version   string `json:"version"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Yes       bool   `json:"yes"`
	AutoMerge *bool  `json:"auto_merge,omitempty"`
}

// clusterAddonsWrite is one cluster's before/after ClusterAddons file
// content, computed before any git write so DryRun and the real commit
// share identical diff-building logic.
type clusterAddonsWrite struct {
	cluster  string
	path     string
	existing []byte
	updated  []byte
}

// UpgradeAddonClustersV4 bumps req.Addon's version pin to req.Version on
// exactly req.Clusters, in a single PR. All-or-nothing: every selected
// cluster is validated (file exists, addon present and enabled) BEFORE any
// git write, so a bad cluster name in the middle of a large selection never
// leaves a partial PR — the same "sharpened pipeline" stance
// EnableAddonV4/addon_ops_v4.go established for the rest of the v4 write
// surface.
func (o *Orchestrator) UpgradeAddonClustersV4(ctx context.Context, req UpgradeAddonClustersV4Request) (*GitResult, error) {
	if req.Addon == "" {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "addon name is required")
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "version is required")
	}

	clusters := dedupSortedClusters(req.Clusters)
	if len(clusters) == 0 {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "at least one cluster is required")
	}

	// Same half-converted-repo refusal as EnableAddonV4 (review F4).
	if err := o.refuseOnMixedLayout(ctx); err != nil {
		return nil, err
	}

	// The catalog entry says where the chart lives and which version a
	// cluster with no pin of its own runs. An addon that is switched on
	// somewhere but missing from catalog.yaml (a hand edit) still upgrades
	// the way it always did; only the checks that need the entry are
	// skipped for it.
	entry, entryErr := o.catalogAddonForV4(ctx, req.Addon)
	inCatalog := entryErr == nil

	writes := make([]clusterAddonsWrite, 0, len(clusters))
	// currentVersions holds the version each chosen cluster runs today.
	currentVersions := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		clusterPath, err := v4ClusterAddonsPath(cluster)
		if err != nil {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid, err.Error())
		}
		existing, ok := o.readFileIfExists(ctx, clusterPath)
		if !ok {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid,
				fmt.Sprintf("cluster %q has no assignment file at %s — nothing to upgrade", cluster, clusterPath))
		}
		spec, err := models.LoadClusterAddons(existing)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", clusterPath, err)
		}
		addonEntry, hasAddon := spec.Addons[req.Addon]
		if !hasAddon || !addonEntry.Enabled {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid,
				fmt.Sprintf("addon %q is not enabled on cluster %q — enable it there first", req.Addon, cluster))
		}
		current := addonEntry.Version
		if current == "" && inCatalog {
			current = entry.Version
		}
		currentVersions[cluster] = current

		version := req.Version
		updated, err := gitops.SetClusterAddonsAddon(existing, cluster, req.Addon, true, &version, nil)
		if err != nil {
			return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
		}
		writes = append(writes, clusterAddonsWrite{cluster: cluster, path: clusterPath, existing: existing, updated: updated})
	}

	var warnings []string
	if inCatalog {
		// Every chosen cluster already runs this version: there is no
		// change to make, and an empty pull request helps nobody.
		allSame := true
		for _, cluster := range clusters {
			if currentVersions[cluster] != req.Version {
				allSame = false
				break
			}
		}
		if allSame {
			return nil, newUserFacingError(ErrUpgradeNothingToDo, UpgradeClustersNothingToDoMessage(req.Addon, req.Version, clusters))
		}

		// The version has to exist before anything is written.
		if err := o.checkChartVersion(ctx, req.Addon, entry.RepoURL, entry.Chart, req.Version); err != nil {
			return nil, err
		}

		warnings = append(warnings, o.sharedValuesWarnings(ctx, req.Addon, entry.RepoURL, entry.Chart, currentVersions, req.Version)...)
	}

	verb := "Upgrade"
	if allClustersGoDown(currentVersions, req.Version) {
		verb = "Downgrade"
	}
	title := fmt.Sprintf("%s %s to %s on %d cluster(s)", verb, req.Addon, req.Version, len(clusters))
	// One text for the preview title, the commit message and the real pull
	// request title, so the preview never promises a different title.
	operation := fmt.Sprintf("%s addon %s to %s on clusters %s", strings.ToLower(verb), req.Addon, req.Version, strings.Join(clusters, ", "))

	if req.DryRun {
		previews := make([]FilePreview, 0, len(writes))
		for _, w := range writes {
			previews = append(previews, FilePreview{
				Path:   w.path,
				Action: "update",
				Diff:   o.buildFileDiff(w.path, w.existing, w.updated, "update"),
			})
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{req.Addon},
				FilesToWrite:    previews,
				PRTitle:         o.gitops.CommitPrefix + " " + operation,
				SecretsToCreate: []string{},
			},
			Warnings: warnings,
		}, nil
	}

	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	files := make(map[string][]byte, len(writes))
	for _, w := range writes {
		files[w.path] = w.updated
	}

	// Cluster left "" on the tracked PR metadata (like UpgradeAddons'
	// multi-addon batch) because this PR spans multiple clusters — Addon
	// plus the PR body/title already disambiguate which upgrade this is.
	// One cluster is named, so the cluster page can show its pending PR.
	metaCluster := ""
	if len(clusters) == 1 {
		metaCluster = clusters[0]
	}
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, operation,
		o.prMeta(req.AutoMerge, "addon-upgrade", title, metaCluster, req.Addon))
	if err != nil {
		return nil, fmt.Errorf("committing subset upgrade: %w", err)
	}
	gitResult.Warnings = append(gitResult.Warnings, warnings...)
	return gitResult, nil
}

// UpgradeClustersNothingToDoMessage is said when every chosen cluster already
// runs the asked-for version.
func UpgradeClustersNothingToDoMessage(addon, version string, clusters []string) string {
	if len(clusters) == 1 {
		return fmt.Sprintf("%s is already on version %s on %s. Nothing to change.", addon, version, clusters[0])
	}
	return fmt.Sprintf("%s is already on version %s on every chosen cluster. Nothing to change.", addon, version)
}

// allClustersGoDown reports whether the change lowers the version on every
// chosen cluster — the only case the pull request is called a downgrade.
func allClustersGoDown(current map[string]string, target string) bool {
	if len(current) == 0 {
		return false
	}
	for _, cur := range current {
		cmp, ok := compareChartVersions(target, cur)
		if !ok || cmp >= 0 {
			return false
		}
	}
	return true
}

// sharedValuesWarnings covers the one thing a some-clusters upgrade cannot
// fix by itself. values/global/<addon>.yaml is shared by EVERY cluster, so it
// cannot follow the new chart's defaults while other clusters still run the
// old chart. When that file still sets one of the old chart's defaults (the
// image tag is the usual one), the chosen clusters get the new chart with the
// OLD setting — and Sharko says so here instead of reporting a clean upgrade.
func (o *Orchestrator) sharedValuesWarnings(ctx context.Context, addon, repoURL, chart string, currentVersions map[string]string, target string) []string {
	if o.chartDefaultsFn == nil {
		return nil
	}
	gPath, err := v4GlobalValuesPath(addon)
	if err != nil {
		return nil
	}
	global, exists := o.readFileIfExists(ctx, gPath)
	if !exists || yamlDocRoot(global) == nil {
		return nil
	}
	newDefaults, err := o.chartDefaultsFn(ctx, repoURL, chart, target)
	if err != nil {
		return []string{ValuesNotComparedWarning(addon, gPath)}
	}
	// One comparison per distinct version the chosen clusters run today.
	seen := map[string]bool{}
	byPath := map[string]KeptDefault{}
	for _, cur := range currentVersions {
		if cur == "" || cur == target || seen[cur] {
			continue
		}
		seen[cur] = true
		oldDefaults, oldErr := o.chartDefaultsFn(ctx, repoURL, chart, cur)
		if oldErr != nil {
			return []string{ValuesNotComparedWarning(addon, gPath)}
		}
		for _, k := range staleDefaultsReport(global, oldDefaults, newDefaults) {
			byPath[k.Path] = k
		}
	}
	if len(byPath) == 0 {
		return nil
	}
	kept := make([]KeptDefault, 0, len(byPath))
	for _, k := range byPath {
		kept = append(kept, k)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Path < kept[j].Path })
	return []string{SharedValuesKeepOldDefaultsWarning(gPath, addon, kept)}
}

// SharedValuesKeepOldDefaultsWarning is the sentence for sharedValuesWarnings.
func SharedValuesKeepOldDefaultsWarning(valuesPath, addon string, kept []KeptDefault) string {
	const maxListed = 5
	parts := make([]string, 0, maxListed)
	for i, k := range kept {
		if i == maxListed {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s, new chart default %s)", k.Path, k.Old, k.New))
	}
	more := ""
	if len(kept) > maxListed {
		more = fmt.Sprintf(" and %d more", len(kept)-maxListed)
	}
	return fmt.Sprintf(
		"%s is shared by every cluster and still sets the old chart's value for: %s%s. The chosen clusters get the new chart but keep these old values. To move them too, upgrade %s for all clusters, or set them in the cluster's own values file.",
		valuesPath, strings.Join(parts, ", "), more, addon)
}

// dedupSortedClusters drops empty/duplicate entries and sorts the rest, so
// the resulting file-write set and PR diff never depend on request order or
// accidental repeats.
func dedupSortedClusters(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, c := range in {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
