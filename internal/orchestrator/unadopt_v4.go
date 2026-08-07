// Package orchestrator — un-adopting a cluster on a v4 repo
// (v4-coherence-closure, lane D).
//
// v3's UnadoptCluster writes configuration/managed-clusters.yaml — on a v4
// repo that would leave a rival registry file behind (see
// ErrV4RepoUnsupported). This file is the v4 replacement for the Git half
// of an unadopt: remove the cluster from managed-clusters.yaml
// (gitops.RemoveClusterEntry) and delete every file this cluster owns —
// cluster-addons/<name>.yaml plus every Helm values file under
// values/clusters/<name>/.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// unadoptV4Plan is what a v4 unadopt would change: the managed-clusters.yaml
// rewrite (nil when there is nothing to change — an idempotent re-run) and
// every path to delete.
type unadoptV4Plan struct {
	clusterAddonsPath string
	valuesDir         string

	managedClustersData    []byte // before; possibly empty (bootstrapped "clusters:\n")
	managedClustersExists  bool
	updatedManagedClusters []byte // after; nil when the cluster was already absent (idempotent no-op)

	// deletePaths is every file this unadopt removes: the cluster-addons
	// file (only when it exists) and every file ListDirectory found under
	// valuesDir. A file that is already absent is simply never added here
	// — idempotent by construction, not by swallowing a delete error.
	deletePaths []string
}

// buildUnadoptV4Plan computes what a v4 unadopt would remove, without
// touching git. Read-only, so it is safe to call for both the dry-run
// preview and the real write.
//
// FAIL CLOSED on the values-folder listing: a ListDirectory error that is
// NOT "the folder genuinely does not exist" aborts the whole plan rather
// than returning a partial delete list. A partial list would let
// UnadoptCluster silently leave some of a cluster's values files behind —
// files Sharko's own records say belong to a cluster that is gone, and
// that nothing will ever point at again. Costing a retry beats deleting
// fewer files than the operator asked for and never saying so.
func (o *Orchestrator) buildUnadoptV4Plan(ctx context.Context, name string) (*unadoptV4Plan, error) {
	clusterPath, err := v4ClusterAddonsPath(name)
	if err != nil {
		return nil, err
	}
	plan := &unadoptV4Plan{
		clusterAddonsPath: clusterPath,
		valuesDir:         path.Join(V4ClusterValuesDir, name),
	}

	// Fail-closed read: managed-clusters.yaml is a rewrite base — a
	// swallowed error here would open a pull request that (because
	// RemoveClusterEntry rewrites the whole file) removes every OTHER
	// cluster from the fleet along with this one.
	data, exists, readErr := o.readFileForRewrite(ctx, V4ManagedClustersPath)
	if readErr != nil {
		return nil, readErr
	}
	plan.managedClustersData = data
	plan.managedClustersExists = exists
	if exists && len(data) > 0 {
		if updated, removeErr := gitops.RemoveClusterEntry(data, name); removeErr == nil {
			plan.updatedManagedClusters = updated
		}
		// removeErr != nil means the cluster is not (or no longer) in the
		// fleet record — an idempotent no-op, not a failure: re-running
		// unadopt on an already-unadopted v4 cluster must not error.
	}

	// The cluster's addon-assignment file — deleted only if it is there.
	if _, addonsExists := o.readFileIfExists(ctx, clusterPath); addonsExists {
		plan.deletePaths = append(plan.deletePaths, clusterPath)
	}

	// Every Helm values file this cluster owns.
	names, listErr := o.git.ListDirectory(ctx, plan.valuesDir, o.gitops.BaseBranch)
	if listErr != nil {
		if !errors.Is(listErr, gitprovider.ErrFileNotFound) {
			return nil, fmt.Errorf(
				"could not list %s to work out which of %s's values files to remove, so Sharko stopped instead of deleting fewer than it should have: %w",
				plan.valuesDir, name, listErr)
		}
		// Genuinely no such folder — no per-cluster values overrides for
		// this cluster. Ordinary, not an error.
	} else {
		for _, n := range names {
			plan.deletePaths = append(plan.deletePaths, path.Join(plan.valuesDir, n))
		}
	}

	return plan, nil
}

// filePreviews renders the dry-run preview for a v4 unadopt plan: the
// managed-clusters.yaml diff (only when there is a change to show) and one
// delete preview per file in deletePaths.
func (o *Orchestrator) unadoptV4FilePreviews(ctx context.Context, plan *unadoptV4Plan) []FilePreview {
	var previews []FilePreview
	if plan.updatedManagedClusters != nil {
		previews = append(previews, FilePreview{
			Path:   V4ManagedClustersPath,
			Action: "update",
			Diff:   o.buildFileDiff(V4ManagedClustersPath, plan.managedClustersData, plan.updatedManagedClusters, "update"),
		})
	}
	for _, p := range plan.deletePaths {
		old, _ := o.readFileIfExists(ctx, p)
		previews = append(previews, FilePreview{
			Path:   p,
			Action: "delete",
			Diff:   o.buildFileDiff(p, old, nil, "delete"),
		})
	}
	return previews
}
