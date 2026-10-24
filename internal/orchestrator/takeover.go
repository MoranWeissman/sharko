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
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
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
	// PreserveLegacyLabels is the person's choice to keep the previous
	// owner's labels. It is saved on the tracked pull request, so the step
	// that runs after the merge does what was asked (EP3 dry run round R,
	// R5).
	PreserveLegacyLabels bool `json:"-"`
}

// TakeoverGitResult is what the Git half produced.
type TakeoverGitResult struct {
	Cluster        string        `json:"cluster"`
	Git            *GitResult    `json:"git,omitempty"`
	FilesWritten   []string      `json:"files_written,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
	AlreadyInFleet bool          `json:"already_in_fleet,omitempty"`
	// AlreadyWaiting is true when a take over (or adopt) pull request for
	// this cluster is already open. Git then points at that pull request
	// and no second one is opened.
	AlreadyWaiting bool `json:"already_waiting,omitempty"`
	// NothingToWrite is true when the fleet record in Git already has
	// everything a take over writes, so no pull request was opened.
	NothingToWrite bool `json:"nothing_to_write,omitempty"`
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
//
// The two-file build below (buildTakeoverFiles) is shared with
// AdoptClusters' v4 branch (adopt_v4.go, v4-coherence-closure lane D):
// adopting an already-ArgoCD-managed cluster on a v4 repo writes the exact
// same pair of files a takeover does — the difference is what gates the
// write (the adopt door's own preflight + confirmation contract, not
// takeover's acknowledged-findings protocol) and what happens to the
// ArgoCD Secret afterward (adopt sets the v3-style adopted annotation in
// addition to the ownership swap).
func (o *Orchestrator) TakeoverClusterGit(ctx context.Context, req TakeoverClusterRequest) (*TakeoverGitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	// Resolve the commit path before anything else, so a name that could
	// climb out of the cluster-addons/ folder fails with a plain-English message
	// and zero side effects.
	if _, err := v4ClusterAddonsPath(req.Name); err != nil {
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

	// Taking over twice must not open two pull requests (episode 3 dry
	// run, design call 8). An open take over or adopt pull request for
	// this cluster is the answer. An open register pull request is
	// refused: it writes the same files (round R, R7). If the list cannot
	// be read, the take over stops rather than risk a second pull request.
	open, _, err := o.findOpenJoinPR(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if open != nil {
		result.AlreadyWaiting = true
		result.Git = &GitResult{PRUrl: open.URL, PRID: open.ID, Branch: open.SourceBranch}
		if !req.DryRun {
			return result, nil
		}
	}

	build, err := o.buildTakeoverFiles(ctx, req.Name, req.Region)
	if err != nil {
		return nil, err
	}
	result.AlreadyInFleet = build.alreadyInFleet
	result.NothingToWrite = build.unchanged()

	if req.DryRun {
		result.DryRun = &DryRunResult{
			EffectiveAddons: []string{},
			FilesToWrite:    o.takeoverFilePreviews(build),
			PRTitle:         fmt.Sprintf("%s take over cluster %s", o.gitops.CommitPrefix, req.Name),
			SecretsToCreate: []string{},
		}
		return result, nil
	}

	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	if result.NothingToWrite {
		// The fleet record already has this cluster and its addon file.
		// A pull request with no changes in it would only be noise.
		return result, nil
	}

	files := build.files()
	result.FilesWritten = []string{V4ManagedClustersPath, build.clusterPath}

	// Tracked under the adopt operation code: takeover is the operation
	// adopt grew into, and reusing the code keeps the pull request in the
	// dashboard bucket operators already look in for "a cluster joined the
	// fleet" changes.
	//
	// The follow-up names the finish kind, so the step that runs after the
	// merge swaps the owner as a take over (round R, R5), with the
	// person's own choice about the old labels.
	meta := o.prMeta(req.AutoMerge, "adopt-cluster", fmt.Sprintf("Take over cluster %s", req.Name), req.Name, "")
	meta.FollowUp = &ClusterFollowUp{Finish: FinishTakeover, PreserveLegacyLabels: req.PreserveLegacyLabels}
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil,
		fmt.Sprintf("take over cluster %s", req.Name), meta)
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

// takeoverFilesBuild holds the before/after state of the two v4 files a
// takeover (or a v4 adopt) writes, so the dry-run preview and the real
// commit share one computation.
type takeoverFilesBuild struct {
	clusterPath string

	connectionsData    []byte // before — never empty; defaulted to "clusters:\n" when absent
	connectionsExists  bool
	updatedConnections []byte // after

	existingClusterAddons []byte // before — genuinely empty/absent when the file doesn't exist yet
	clusterAddonsExists   bool
	updatedClusterAddons  []byte // after

	alreadyInFleet bool
}

// unchanged reports whether both files would be written back exactly as
// they already are.
func (b *takeoverFilesBuild) unchanged() bool {
	return b.connectionsExists && b.clusterAddonsExists &&
		bytes.Equal(b.connectionsData, b.updatedConnections) &&
		bytes.Equal(b.existingClusterAddons, b.updatedClusterAddons)
}

// findOpenJoinPR returns an open take over or adopt pull request for the
// cluster and which of the two it is, or nil. Both write the same two files, so either one means the
// cluster is already on its way into the fleet.
//
// An open register pull request writes the same files too, but it is a
// different change, so it comes back as a *ClusterPRWaitingError and the
// caller refuses (EP3 dry run round R, R7). A list that cannot be read is an
// error: carrying on could open a second pull request for the same files.
func (o *Orchestrator) findOpenJoinPR(ctx context.Context, name string) (*gitprovider.PullRequest, string, error) {
	prs, err := o.git.ListPullRequests(ctx, "open")
	if err != nil {
		slog.Warn("adopt or take over: could not list open pull requests", "cluster", name, "error", err)
		return nil, "", OpenPRsUnreadableError(name)
	}
	for _, op := range []string{"register", "take over", "adopt"} {
		pattern := fmt.Sprintf("%s %s cluster %s", o.gitops.CommitPrefix, op, name)
		for i := range prs {
			if !titleNamesCluster(prs[i].Title, pattern) {
				continue
			}
			found := prs[i]
			found.ID = PRNumber(found.ID, found.URL)
			if op == "register" {
				return nil, op, &ClusterPRWaitingError{Cluster: name, PRID: found.ID, PRUrl: found.URL}
			}
			return &found, op, nil
		}
	}
	return nil, "", nil
}

// ClusterPRWaitingError means a register pull request for the cluster is
// still open. Adopt and take over refuse, because they would write the
// same files in a second pull request.
type ClusterPRWaitingError struct {
	Cluster string
	PRID    int
	PRUrl   string
}

func (e *ClusterPRWaitingError) Error() string {
	return fmt.Sprintf("Cluster %s is already waiting in %s: %s. That pull request registers it. Merge it or close it first, then try again.",
		e.Cluster, PullRequestPhrase(e.PRID), e.PRUrl)
}

// OpenPRsUnreadableError is the answer when the open pull requests cannot be
// read before an adopt or a take over.
func OpenPRsUnreadableError(name string) error {
	return fmt.Errorf("Sharko could not read the open pull requests, so it cannot tell whether %s is already waiting in one. Nothing has changed. Try again.", name)
}

// files returns the map ready for commitChangesWithMeta / BatchCreateFiles.
func (b *takeoverFilesBuild) files() map[string][]byte {
	return map[string][]byte{
		V4ManagedClustersPath: b.updatedConnections,
		b.clusterPath:         b.updatedClusterAddons,
	}
}

// buildTakeoverFiles computes the two v4 files that make a cluster part of
// Sharko's fleet — managed-clusters.yaml (connection record, no addon
// labels) and an empty cluster-addons/<name>.yaml (nothing turned on) —
// WITHOUT touching git. It does not check isV4Repo; callers do that first
// (this is the shared "what to write" step both TakeoverClusterGit and
// AdoptClusters' v4 branch call after their own repo-format and
// confirmation gates pass).
//
// Both reads are fail-closed rewrite bases (readFileForRewrite): the fleet
// record and the cluster's addon file are both edited and written back
// whole, so a swallowed read error would open a pull request that drops
// every OTHER cluster out of the fleet, or wipes an existing addon
// assignment. Only a genuinely absent file starts from an empty document.
func (o *Orchestrator) buildTakeoverFiles(ctx context.Context, name, region string) (*takeoverFilesBuild, error) {
	clusterPath, err := v4ClusterAddonsPath(name)
	if err != nil {
		return nil, err
	}

	connectionsData, connectionsExists, readErr := o.readFileForRewrite(ctx, V4ManagedClustersPath)
	if readErr != nil {
		return nil, readErr
	}
	if !connectionsExists || len(connectionsData) == 0 {
		connectionsData = []byte("clusters:\n")
	}

	b := &takeoverFilesBuild{
		clusterPath:       clusterPath,
		connectionsData:   connectionsData,
		connectionsExists: connectionsExists,
	}

	// Is the cluster already in the fleet record? Checked before the write
	// so the result can say so honestly rather than reporting a no-op diff
	// as a fresh registration/adoption.
	if spec, loadErr := models.LoadManagedClusters(connectionsData); loadErr == nil {
		for _, c := range spec.Clusters {
			if c.Name == name {
				b.alreadyInFleet = true
				break
			}
		}
	}

	// The fleet entry carries no addon labels: in v4 those live in
	// cluster-addons/<name>.yaml, and a takeover/adopt enables nothing.
	updatedConnections, addErr := gitops.AddClusterEntry(connectionsData, gitops.ClusterEntryInput{
		Name:   name,
		Region: region,
	})
	if addErr != nil {
		return nil, fmt.Errorf("adding %q to %s: %w", name, V4ManagedClustersPath, addErr)
	}
	// v4 naming polish item 3: managed-clusters.yaml was genuinely absent
	// before this write — headers ride creation only.
	if !connectionsExists {
		updatedConnections = append([]byte(managedClustersFileHeader), updatedConnections...)
	}
	b.updatedConnections = updatedConnections

	existingClusterAddons, clusterAddonsExists, addonsReadErr := o.readFileForRewrite(ctx, clusterPath)
	if addonsReadErr != nil {
		return nil, addonsReadErr
	}
	b.existingClusterAddons = existingClusterAddons
	b.clusterAddonsExists = clusterAddonsExists

	updatedClusterAddons := existingClusterAddons
	if !clusterAddonsExists || len(existingClusterAddons) == 0 {
		// An empty assignment file, not a missing one: it is the anchor
		// the per-addon adoption steps write into later, and having it in
		// the repo from the start makes the takeover visible in the diff.
		updatedClusterAddons, err = models.SaveClusterAddons(models.ClusterAddonsSpec{
			Cluster: name,
			Addons:  map[string]models.ClusterAddonsAddon{},
		})
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", clusterPath, err)
		}
		// v4 naming polish item 3: this file is genuinely new — headers
		// ride creation only.
		updatedClusterAddons = append([]byte(clusterAddonsFileHeader(name)), updatedClusterAddons...)
	}
	b.updatedClusterAddons = updatedClusterAddons

	return b, nil
}

// takeoverFilePreviews renders the dry-run FilePreview pair for a
// takeoverFilesBuild — shared by TakeoverClusterGit and AdoptClusters' v4
// dry-run branch.
func (o *Orchestrator) takeoverFilePreviews(b *takeoverFilesBuild) []FilePreview {
	connAction := fileActionFromExists(b.connectionsData)
	if !b.connectionsExists {
		connAction = "create"
	}
	clusterAction := fileActionFromExists(b.existingClusterAddons)
	return []FilePreview{
		{
			Path:   V4ManagedClustersPath,
			Action: connAction,
			Diff:   o.buildFileDiff(V4ManagedClustersPath, b.connectionsData, b.updatedConnections, connAction),
		},
		{
			Path:   b.clusterPath,
			Action: clusterAction,
			Diff:   o.buildFileDiff(b.clusterPath, b.existingClusterAddons, b.updatedClusterAddons, clusterAction),
		},
	}
}
