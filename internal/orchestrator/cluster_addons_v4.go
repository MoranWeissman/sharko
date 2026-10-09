// Package orchestrator — the v4-repo branch of UpdateClusterAddons
// (v4-coherence-closure lane D): PATCH /clusters/{name}'s label-patch door,
// rebuilt for the v4 data-file format.
//
// v3's UpdateClusterAddons writes addon on/off as labels inside
// configuration/managed-clusters.yaml and pushes them straight to ArgoCD
// via UpdateClusterLabels. In v4 that state lives in
// cluster-addons/<cluster>.yaml instead (design doc §2.1), and it is
// PR-only: the cluster reconciler applies the resulting addon labels to
// the ArgoCD cluster Secret FROM GIT once the pull request merges
// (applyV4AddonLabels) — this file never touches ArgoCD or remote secrets
// directly, mirroring EnableAddonV4 / DisableAddonV4 (addon_ops_v4.go).
package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

// updateClusterAddonsV4 applies the full desired addon on/off set from a
// PATCH /clusters/{name} request to cluster-addons/<name>.yaml, in one
// pull request.
//
// Every addon named in the request must already be in catalog.yaml — the
// same approval gate EnableAddonV4 enforces (catalogAddonForV4), reused
// here rather than duplicated. Unlike EnableAddonV4 this runs no further
// semantic validation (required values, declared secrets): the label-patch
// door has no values input to validate against, only on/off — the same
// scope v3's UpdateClusterAddons has always had.
func (o *Orchestrator) updateClusterAddonsV4(ctx context.Context, name string, addons map[string]bool, autoMergeOverride *bool, dryRun bool, result *RegisterClusterResult) (*RegisterClusterResult, error) {
	// Same half-converted-repo refusal as EnableAddonV4 / DisableAddonV4:
	// a repo carrying both layouts writes into the half nothing reads.
	if err := o.refuseOnMixedLayout(ctx); err != nil {
		return nil, err
	}

	// Refuse a cluster nobody registered BEFORE any git write — same guard
	// EnableAddonV4/DisableAddonV4 use, so a typo'd or never-registered
	// cluster name gets a clean 404 instead of bootstrapping a brand-new
	// cluster-addons/<name>.yaml.
	if !o.v4ClusterExists(ctx, name) {
		return nil, fmt.Errorf("%w: %q", ErrV4ClusterNotFound, name)
	}

	// The approval gate: every addon named in the request must be in
	// catalog.yaml. Checked before any git write, in the same order the
	// names will be applied, so the first bad name in sorted order is what
	// the caller sees.
	addonNames := sortedBoolKeys(addons)
	for _, addonName := range addonNames {
		if _, err := o.catalogAddonForV4(ctx, addonName); err != nil {
			return nil, err
		}
	}

	clusterPath, err := v4ClusterAddonsPath(name)
	if err != nil {
		return nil, err
	}

	// Rewrite base: the assignment file is edited and written back whole,
	// so a swallowed read error would open a pull request wiping every
	// OTHER addon already on this cluster.
	existing, exists, readErr := o.readFileForRewrite(ctx, clusterPath)
	if readErr != nil {
		return nil, readErr
	}

	// Nothing named: nothing to change. Return before touching git at all.
	//
	// This guard exists because of a bug a kind e2e run caught (the
	// PatchClusterLabels subtest sends `{"addons": {}}` as a no-op-patch
	// probe): with an empty addons map, the loop below never runs, so
	// `updated` never passes through gitops.SetClusterAddonsAddon (the
	// only thing that produces a valid apiVersion/kind envelope — see
	// models.SaveClusterAddons). On a cluster whose assignment file does
	// not exist yet (the ordinary case right after registration, since
	// RegisterCluster never creates cluster-addons/<name>.yaml), `updated`
	// stayed empty and the `!exists` branch below then prepended ONLY the
	// plain-English comment header to it — a file with no apiVersion/kind
	// at all, which every subsequent read of cluster-addons/<name>.yaml
	// then failed to parse ("not a Sharko document"). An empty request has
	// no addon to validate against the catalog gate either, so there is
	// nothing left to do but report success with no git write — the same
	// no-op contract the PatchClusterLabels subtest's own comment expects.
	if len(addonNames) == 0 {
		result.Status = "success"
		result.CompletedSteps = []string{}
		if dryRun {
			result.DryRun = &DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite:    []FilePreview{},
				PRTitle:         fmt.Sprintf("%s update addons for cluster %s", o.gitops.CommitPrefix, name),
				SecretsToCreate: []string{},
			}
		}
		return result, nil
	}

	// Apply the full desired on/off set over ONE in-memory buffer, in one
	// pass — the same loop-over-a-buffer-then-commit-once shape
	// AddToCatalog's add-and-enable combo branch uses (catalog_ops.go), so
	// a PATCH naming several addons lands as one pull request touching this
	// cluster's assignment file once, not once per addon.
	updated := existing
	for _, addonName := range addonNames {
		updated, err = gitops.SetClusterAddonsAddon(updated, name, addonName, addons[addonName], nil, nil)
		if err != nil {
			return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
		}
	}
	// v4 naming polish item 3: this cluster's assignment file did not exist
	// before this write — headers ride creation only. Reached only when
	// addonNames is non-empty (guarded above), so `updated` has always
	// been through gitops.SetClusterAddonsAddon at least once here and
	// already carries a valid apiVersion/kind envelope — the header is
	// purely a comment prepended in front of it, never a substitute for it.
	if !exists {
		updated = append([]byte(clusterAddonsFileHeader(name)), updated...)
	}

	if dryRun {
		result.Status = "success"
		result.DryRun = &DryRunResult{
			EffectiveAddons: v4EnabledAddonNames(addons),
			FilesToWrite: []FilePreview{{
				Path:   clusterPath,
				Action: fileActionFromExists(existing),
				Diff:   o.buildFileDiff(clusterPath, existing, updated, fileActionFromExists(existing)),
			}},
			PRTitle:         fmt.Sprintf("%s update addons for cluster %s", o.gitops.CommitPrefix, name),
			SecretsToCreate: []string{},
		}
		return result, nil
	}

	// PR-only on v4: no ArgoCD label push, no remote secret write. The
	// cluster reconciler applies the resulting addon labels to the ArgoCD
	// cluster Secret from git once this merges.
	gitResult, gitErr := o.commitChangesWithMeta(ctx, map[string][]byte{clusterPath: updated}, nil,
		fmt.Sprintf("update addons for cluster %s (v4)", name),
		o.prMeta(autoMergeOverride, "update-cluster", fmt.Sprintf("Update addons for cluster %s", name), name, ""))
	if gitErr != nil {
		if gitResult != nil {
			result.Status = "partial"
			result.FailedStep = "pr_merge"
			result.Error = gitErr.Error()
			result.Message = fmt.Sprintf("Pull request opened for cluster %s but auto-merge failed. Merge manually: %s", name, gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		result.Status = "partial"
		result.FailedStep = "git_commit"
		result.Error = gitErr.Error()
		result.Message = fmt.Sprintf("Sharko could not open a pull request updating addons for cluster %s.", name)
		return result, nil
	}

	result.Status = "success"
	result.CompletedSteps = []string{"git_commit"}
	result.Git = gitResult
	return result, nil
}

// v4EnabledAddonNames returns the sorted names of every addon set to
// enabled=true in the request, never nil (so the JSON response carries
// `[]`, not `null`, matching every other DryRunResult.EffectiveAddons).
func v4EnabledAddonNames(addons map[string]bool) []string {
	out := []string{}
	for _, name := range sortedBoolKeys(addons) {
		if addons[name] {
			out = append(out, name)
		}
	}
	return out
}
