// Package orchestrator — brownfield takeover, the Git half (v4 Wave 2,
// Epic 6, story 6.3).
//
// Takeover is registration's brownfield sibling. Registration says "here is
// a cluster you have never seen, set it up"; takeover says "here is a
// cluster ArgoCD is already running, become its owner without disturbing
// anything". Both end with the same two v4 files in the repo, written
// through a real pull request:
//
//   - managed-clusters.yaml       — the cluster's entry in Sharko's fleet
//   - cluster-addons/<name>.yaml  — the cluster's addon assignment file
//
// The addon file is created EMPTY. A takeover deliberately turns nothing
// on: whatever is running on the cluster today keeps running exactly as it
// is, and each addon is adopted afterwards, one at a time, at whatever pace
// the operator wants.
//
// This file writes Git only. Swapping the ArgoCD cluster Secret's owner is
// a separate, single K8s write that lives in internal/argosecrets
// (TakeOverClusterSecret) — see that function's comment for why the swap
// is an in-place relabel and not a create-then-delete.

package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TakeoverClusterRequest is the input for TakeoverClusterGit.
type TakeoverClusterRequest struct {
	// Name is the cluster name, kept EXACTLY as ArgoCD already has it.
	// Takeover never renames anything.
	Name string `json:"name"`
	// Region is optional, recorded on the fleet entry the same way
	// registration records it.
	Region string `json:"region,omitempty"`

	DryRun bool `json:"dry_run,omitempty"`
	// Yes is the explicit confirmation. Without it nothing is written.
	Yes       bool  `json:"yes"`
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// TakeoverGitResult is what the Git half produced.
type TakeoverGitResult struct {
	Cluster        string        `json:"cluster"`
	Git            *GitResult    `json:"git,omitempty"`
	FilesWritten   []string      `json:"files_written,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
	AlreadyInFleet bool          `json:"already_in_fleet,omitempty"`
}

// ErrTakeoverNeedsV4Repo is returned when the connected repo is still in
// the v3 format. Takeover writes only v4 files; there is no v3 shape for
// it, and quietly writing the v3 registry instead would leave the operator
// with a cluster that neither format fully describes.
var ErrTakeoverNeedsV4Repo = fmt.Errorf(
	"taking over a cluster writes the v4 files (managed-clusters.yaml and cluster-addons/<name>.yaml), and this repo is still in the older format — migrate the repo first, then take the cluster over")

// TakeoverClusterGit writes the two v4 files that make a cluster part of
// Sharko's fleet, via a pull request. It is idempotent: a cluster already
// in managed-clusters.yaml is left as it is (AddClusterEntry skips
// duplicates silently), so a retry after a half-finished takeover is safe.
//
// Nothing here deletes anything, in any branch. A takeover that is
// abandoned half way leaves the repo with an extra fleet entry and an empty
// addon file — both harmless, both removable through the ordinary
// unregister flow.
func (o *Orchestrator) TakeoverClusterGit(ctx context.Context, req TakeoverClusterRequest) (*TakeoverGitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	// Resolve the commit path before anything else, so a name that could
	// climb out of the cluster-addons/ folder fails with a plain-English message
	// and zero side effects.
	clusterPath, err := v4ClusterAddonsPath(req.Name)
	if err != nil {
		return nil, err
	}
	// Fail closed: a layout probe that cannot get an answer stops the
	// takeover rather than guessing. Everything below this line writes.
	v4Repo, v4Err := o.isV4Repo(ctx)
	if v4Err != nil {
		return nil, v4Err
	}
	if !v4Repo {
		return nil, ErrTakeoverNeedsV4Repo
	}

	result := &TakeoverGitResult{Cluster: req.Name}

	// Fail-closed read. The fleet record is edited and written back, so a
	// read error that quietly became "empty fleet" would open a pull request
	// replacing every registered cluster with this one — and the orphan sweep
	// would delete the rest of the fleet's connections once it merged. Only a
	// genuinely absent file starts from an empty document.
	connectionsData, connectionsExists, readErr := o.readFileForRewrite(ctx, V4ManagedClustersPath)
	if readErr != nil {
		return nil, readErr
	}
	if !connectionsExists || len(connectionsData) == 0 {
		connectionsData = []byte("clusters:\n")
	}
	managedClustersIsNew := !connectionsExists

	// Is the cluster already in the fleet record? Checked before the write
	// so the result can say so honestly rather than reporting a no-op diff
	// as a fresh registration.
	if spec, loadErr := models.LoadManagedClusters(connectionsData); loadErr == nil {
		for _, c := range spec.Clusters {
			if c.Name == req.Name {
				result.AlreadyInFleet = true
				break
			}
		}
	}

	// The fleet entry carries no addon labels: in v4 those live in
	// cluster-addons/<name>.yaml, and a takeover enables nothing anyway.
	updatedConnections, addErr := gitops.AddClusterEntry(connectionsData, gitops.ClusterEntryInput{
		Name:   req.Name,
		Region: req.Region,
	})
	if addErr != nil {
		return nil, fmt.Errorf("adding %q to %s: %w", req.Name, V4ManagedClustersPath, addErr)
	}
	// v4 naming polish item 3: managed-clusters.yaml was genuinely absent
	// before this write — headers ride creation only.
	if managedClustersIsNew {
		updatedConnections = append([]byte(managedClustersFileHeader), updatedConnections...)
	}

	existingClusterAddons, clusterAddonsExists, addonsReadErr := o.readFileForRewrite(ctx, clusterPath)
	if addonsReadErr != nil {
		return nil, addonsReadErr
	}
	updatedClusterAddons := existingClusterAddons
	if !clusterAddonsExists || len(existingClusterAddons) == 0 {
		// An empty assignment file, not a missing one: it is the anchor
		// the per-addon adoption steps write into later, and having it in
		// the repo from the start makes the takeover visible in the diff.
		updatedClusterAddons, err = models.SaveClusterAddons(models.ClusterAddonsSpec{
			Cluster: req.Name,
			Addons:  map[string]models.ClusterAddonsAddon{},
		})
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", clusterPath, err)
		}
		// v4 naming polish item 3: this file is genuinely new — headers
		// ride creation only.
		updatedClusterAddons = append([]byte(clusterAddonsFileHeader(req.Name)), updatedClusterAddons...)
	}

	if req.DryRun {
		var previews []FilePreview
		connAction := fileActionFromExists(connectionsData)
		if !connectionsExists {
			connAction = "create"
		}
		previews = append(previews, FilePreview{
			Path:   V4ManagedClustersPath,
			Action: connAction,
			Diff:   o.buildFileDiff(V4ManagedClustersPath, connectionsData, updatedConnections, connAction),
		})
		clusterAction := fileActionFromExists(existingClusterAddons)
		previews = append(previews, FilePreview{
			Path:   clusterPath,
			Action: clusterAction,
			Diff:   o.buildFileDiff(clusterPath, existingClusterAddons, updatedClusterAddons, clusterAction),
		})
		result.DryRun = &DryRunResult{
			EffectiveAddons: []string{},
			FilesToWrite:    previews,
			PRTitle:         fmt.Sprintf("%s take over cluster %s", o.gitops.CommitPrefix, req.Name),
			SecretsToCreate: []string{},
		}
		return result, nil
	}

	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	files := map[string][]byte{
		V4ManagedClustersPath: updatedConnections,
		clusterPath:       updatedClusterAddons,
	}
	result.FilesWritten = []string{V4ManagedClustersPath, clusterPath}

	// Tracked under the adopt operation code: takeover is the operation
	// adopt grew into, and reusing the code keeps the pull request in the
	// dashboard bucket operators already look in for "a cluster joined the
	// fleet" changes.
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil,
		fmt.Sprintf("take over cluster %s", req.Name),
		o.prMeta(req.AutoMerge, "adopt-cluster", fmt.Sprintf("Take over cluster %s", req.Name), req.Name, ""))
	if err != nil {
		if gitResult != nil {
			result.Git = gitResult
		}
		return result, err
	}
	result.Git = gitResult

	// No reconciler nudge here on purpose. The takeover is not finished when
	// the pull request is open — the ownership swap on the ArgoCD Secret
	// still has to happen, and the handler triggers the reconciler once,
	// after that. Firing here as well would only make the reconciler look at
	// a fleet that has not changed yet.
	return result, nil
}
