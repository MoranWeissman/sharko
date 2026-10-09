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
		return nil, fmt.Errorf("addon name is required")
	}
	if req.Version == "" {
		return nil, fmt.Errorf("version is required")
	}

	clusters := dedupSortedClusters(req.Clusters)
	if len(clusters) == 0 {
		return nil, fmt.Errorf("at least one cluster is required")
	}

	// Same half-converted-repo refusal as EnableAddonV4 (review F4).
	if err := o.refuseOnMixedLayout(ctx); err != nil {
		return nil, err
	}

	writes := make([]clusterAddonsWrite, 0, len(clusters))
	for _, cluster := range clusters {
		clusterPath, err := v4ClusterAddonsPath(cluster)
		if err != nil {
			return nil, err
		}
		existing, ok := o.readFileIfExists(ctx, clusterPath)
		if !ok {
			return nil, fmt.Errorf("cluster %q has no assignment file at %s — nothing to upgrade", cluster, clusterPath)
		}
		spec, err := models.LoadClusterAddons(existing)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", clusterPath, err)
		}
		entry, hasAddon := spec.Addons[req.Addon]
		if !hasAddon || !entry.Enabled {
			return nil, fmt.Errorf("addon %q is not enabled on cluster %q — enable it there first", req.Addon, cluster)
		}

		version := req.Version
		updated, err := gitops.SetClusterAddonsAddon(existing, cluster, req.Addon, true, &version, nil)
		if err != nil {
			return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
		}
		writes = append(writes, clusterAddonsWrite{cluster: cluster, path: clusterPath, existing: existing, updated: updated})
	}

	title := fmt.Sprintf("Upgrade %s to %s on %d cluster(s)", req.Addon, req.Version, len(clusters))

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
				PRTitle:         fmt.Sprintf("%s upgrade addon %s to %s on %s", o.gitops.CommitPrefix, req.Addon, req.Version, strings.Join(clusters, ", ")),
				SecretsToCreate: []string{},
			},
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
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil,
		fmt.Sprintf("upgrade addon %s to %s on clusters %s (v4)", req.Addon, req.Version, strings.Join(clusters, ", ")),
		o.prMeta(req.AutoMerge, "addon-upgrade", title, "", req.Addon))
	if err != nil {
		return nil, fmt.Errorf("committing subset upgrade: %w", err)
	}
	return gitResult, nil
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
