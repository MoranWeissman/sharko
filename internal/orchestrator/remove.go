package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ownershipLabelStripper is the OPTIONAL capability RemoveCluster uses for
// the handover-at-removal-time label strip (V2-cleanup-60.1). It is declared
// as a separate single-method interface (asserted at the call site) rather
// than added to ArgoSecretManager so existing implementations and test mocks
// keep compiling unchanged. The production adapter in internal/api
// (argo_adapter.go) implements it by delegating to
// argosecrets.Manager.StripOwnershipLabel.
type ownershipLabelStripper interface {
	// StripOwnershipLabel removes Sharko's ownership label
	// (app.kubernetes.io/managed-by: sharko) from the named ArgoCD cluster
	// Secret without deleting it or touching anything else. Returns
	// (stripped, error); a missing secret or an absent/foreign label is a
	// (false, nil) no-op.
	StripOwnershipLabel(ctx context.Context, name string) (bool, error)
}

// keptConnectionReleaser is the OPTIONAL capability the after-merge half of
// a removal uses for a kept connection: in one write it takes Sharko's
// ownership label and the remove-keep mark off (EP3 dry run round R, R1).
// The production adapter (internal/api/argo_adapter.go) delegates to
// argosecrets.Manager.ReleaseKeptConnection. Without it, finishRemove falls
// back to ownershipLabelStripper.
type keptConnectionReleaser interface {
	ReleaseKeptConnection(ctx context.Context, name string, prID int) (bool, error)
}

// clusterSecretDeleter is the OPTIONAL capability that deletes the ArgoCD
// cluster Secret the shared lookup found, by its own Kubernetes name. It
// replaces deleting through the ArgoCD API by server address, which can
// hit a different connection that points at the same server (R8). The
// production adapter delegates to argosecrets.Manager.Delete, which also
// refuses a Secret without Sharko's label.
type clusterSecretDeleter interface {
	DeleteClusterSecret(ctx context.Context, name string) error
}

// annotationValueRemover is the OPTIONAL capability that takes a mark off
// only when it still holds the given value, so an older request never
// takes off a mark a newer one wrote. Production delegates to
// argosecrets.Manager.RemoveAnnotationIfValue.
type annotationValueRemover interface {
	RemoveAnnotationIfValue(ctx context.Context, name, key, value string) (bool, error)
}

// sharkoManagedByValue mirrors argosecrets.ManagedByValue — the value of the
// app.kubernetes.io/managed-by label Sharko stamps on secrets it owns. It is
// duplicated here (like the literal in adopt.go's FR-4.6 check) because the
// orchestrator must not import argosecrets.
const sharkoManagedByValue = "sharko"

// RemoveCluster orchestrates cluster removal with configurable cleanup scope.
//
// Cleanup scopes:
//   - "all" (default): remove from managed-clusters.yaml + delete the cluster's
//     files via PR; after merge, delete addon secrets on remote + ArgoCD cluster secret.
//   - "git": same Git changes, but skip remote addon secret deletion.
//   - "none": only remove the managed-clusters.yaml entry (the cluster's other
//     files are kept, ArgoCD secret kept).
//
// Steps:
//  1. Validate confirmation (yes: true required).
//  2. Create PR: remove the managed-clusters entry + delete the cluster's
//     other files (except cleanup=none). What "the cluster's other files"
//     means diverges by repo layout: on a v3 repo (below) it is the one
//     combined values file; on a v4 repo (v4-coherence-closure lane K,
//     below) it is cluster-addons/<name>.yaml plus every Helm values file
//     under values/clusters/<name>/, computed by the same buildUnadoptV4Plan
//     helper UnadoptCluster's v4 branch uses (unadopt_v4.go, lane D) — a
//     full removal and a full unadopt delete the exact same set of files,
//     they just differ in what happens to the ArgoCD connection afterward.
//  3. ONLY ONCE THE PR IS MERGED (EP3-DRYRUN-FIXES call 1): if the PR
//     merged at once (auto-merge) this runs now; if it waits for a person,
//     nothing on the ArgoCD connection changes and the PR tracker's merge
//     hook runs FinishRemoveAfterMerge later with the same choices.
//     If cleanup=all: delete addon secrets from remote cluster + delete ArgoCD cluster secret.
//     Otherwise the connection is kept and loses Sharko's ownership label.
//     The ArgoCD secret delete is gated on Sharko's ownership label
//     (V2-cleanup-60.1): a Secret that does not carry
//     app.kubernetes.io/managed-by: sharko is NEVER deleted — the git entry
//     may already be gone (retry of a partially-failed removal), in which
//     case the mode check above cannot see connectionManagedBy: user and the
//     Secret itself is the only ownership record left.
//
// Steps 1 and 3 are layout-agnostic and run unchanged on any repo — they
// never look at a file path, only at cleanup, selfManagedConnection, and
// the resolved credential-routing key. Only step 2's Git write diverges.
func (o *Orchestrator) RemoveCluster(ctx context.Context, req RemoveClusterRequest) (*RemoveClusterResult, error) {
	log := logging.LoggerFromContext(ctx)
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	// Fail closed: everything from here can write, and answering "not v4"
	// wrongly recreates configuration/managed-clusters.yaml on a v4 repo —
	// the second-registry hijack v4_guard.go describes. Same stance as
	// AdoptClusters' and UnadoptCluster's v4 probes.
	v4Repo, v4ProbeErr := o.isV4Repo(ctx)
	if v4ProbeErr != nil {
		return nil, fmt.Errorf("Sharko stopped before removing a cluster: %w", v4ProbeErr)
	}

	// Normalize cleanup scope.
	cleanup := req.Cleanup
	if cleanup == "" {
		cleanup = "all"
	}
	if cleanup != "all" && cleanup != "git" && cleanup != "none" {
		return nil, fmt.Errorf("invalid cleanup scope %q: must be all, git, or none", cleanup)
	}

	result := &RemoveClusterResult{
		Name:    req.Name,
		Cleanup: cleanup,
	}

	// ─── v3: compute what the Git write removes, layout-specific ──────────
	var valuesPath, clusterAddonsPath string
	var clusterAddonsData, updatedClusterAddons []byte

	// ─── v4: compute what the Git write removes, layout-specific ──────────
	// registryData is whichever pre-mutation registry bytes were actually
	// read (v3's configuration/managed-clusters.yaml or v4's root
	// managed-clusters.yaml) — both kinds share the same envelope shape
	// (models.LoadManagedClusters / config.NewParser().ParseClusterAddons
	// are path-agnostic), so the credential-routing and connection-mode
	// resolution below reads whichever one applies without caring which.
	var registryData, v4UpdatedRegistry []byte
	var v4DeletePaths []string

	if v4Repo {
		if cleanup == "none" {
			// Registry-only: skip the values-folder enumeration entirely —
			// it is not needed for this cleanup scope and must not block a
			// removal that touches nothing else. An unrelated ListDirectory
			// hiccup would otherwise refuse a "none" removal for a reason
			// it has nothing to do with.
			data, exists, readErr := o.readFileForRewrite(ctx, V4ManagedClustersPath)
			if readErr != nil {
				return nil, readErr
			}
			registryData = data
			if exists && len(data) > 0 {
				if updated, removeErr := gitops.RemoveClusterEntry(data, req.Name); removeErr == nil {
					v4UpdatedRegistry = updated
				}
				// removeErr != nil means the cluster is not (or no longer)
				// in the fleet record — an idempotent no-op.
			}
		} else {
			// Reuse the exact plan UnadoptCluster's v4 branch computes
			// (unadopt_v4.go, lane D): removal and unadopt delete the same
			// set of files (the fleet entry + cluster-addons/<name>.yaml +
			// every file under values/clusters/<name>/), fail-closed on the
			// values-folder listing.
			plan, planErr := o.buildUnadoptV4Plan(ctx, req.Name)
			if planErr != nil {
				return nil, planErr
			}
			registryData = plan.managedClustersData
			v4UpdatedRegistry = plan.updatedManagedClusters
			v4DeletePaths = plan.deletePaths
		}
	} else {
		valuesPath = path.Join(o.paths.ClusterValues, req.Name+".yaml")
		clusterAddonsPath = o.paths.ManagedClusters
		if clusterAddonsPath == "" {
			clusterAddonsPath = "configuration/managed-clusters.yaml"
		}

		// Generate file content (shared between dry-run and real path).
		var err error
		clusterAddonsData, err = o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
		if err != nil && !req.DryRun {
			log.Warn("managed-clusters.yaml not found — skipping removal from it", "cluster", req.Name)
		}
		registryData = clusterAddonsData

		if clusterAddonsData != nil {
			var removeErr error
			updatedClusterAddons, removeErr = gitops.RemoveClusterEntry(clusterAddonsData, req.Name)
			if removeErr != nil && !req.DryRun {
				log.Warn("failed to remove cluster entry from managed-clusters.yaml",
					"cluster", req.Name, "error", removeErr)
				updatedClusterAddons = nil
			}
		}
	}

	// Dry-run exit point: return a preview of what would happen.
	if req.DryRun {
		var filePreviews []FilePreview

		if v4Repo {
			if v4UpdatedRegistry != nil {
				filePreviews = append(filePreviews, FilePreview{
					Path:   V4ManagedClustersPath,
					Action: "update",
					Diff:   o.buildFileDiff(V4ManagedClustersPath, registryData, v4UpdatedRegistry, "update"),
				})
			}
			for _, p := range v4DeletePaths {
				old, _ := o.readFileIfExists(ctx, p)
				filePreviews = append(filePreviews, FilePreview{
					Path:   p,
					Action: "delete",
					Diff:   o.buildFileDiff(p, old, nil, "delete"),
				})
			}
		} else {
			if updatedClusterAddons != nil {
				filePreviews = append(filePreviews, FilePreview{
					Path:   clusterAddonsPath,
					Action: "update",
					Diff:   o.buildFileDiff(clusterAddonsPath, clusterAddonsData, updatedClusterAddons, "update"),
				})
			}

			if cleanup != "none" {
				oldValues, _ := o.readFileIfExists(ctx, valuesPath)
				filePreviews = append(filePreviews, FilePreview{
					Path:   valuesPath,
					Action: "delete",
					Diff:   o.buildFileDiff(valuesPath, oldValues, nil, "delete"),
				})
			}
		}

		prTitle := fmt.Sprintf("%s remove cluster %s", o.gitops.CommitPrefix, req.Name)

		var secretsToDelete []string
		if cleanup == "all" {
			secretsToDelete = o.listSecretsToCreate(map[string]bool{}) // all known secrets
			if o.secretDefs != nil {
				secretsToDelete = make([]string, 0, len(o.secretDefs))
				for _, def := range o.secretDefs {
					secretsToDelete = append(secretsToDelete, def.SecretName)
				}
			}
		}

		result.Status = "success"
		result.DryRun = &DryRunResult{
			FilesToWrite:    filePreviews,
			PRTitle:         prTitle,
			SecretsToCreate: secretsToDelete, // reused field for "secrets to delete" in dry-run
		}
		return result, nil
	}

	// Require confirmation.
	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	var steps []string

	// Step 1: Create PR to remove from the fleet registry and (except
	// cleanup=none) delete the cluster's other files. Reuse whichever
	// layout-specific plan was computed above.

	// Resolve the credential routing NOW, from the bytes we just read —
	// the PR below removes this cluster's entry (and may auto-merge), so a
	// post-PR resolution would no longer find the stored secretPath
	// override (V2-cleanup-55.1) or per-cluster roleArn (V2-cleanup-62.2).
	credLookupKey, credsSource, credRoleARN := config.ResolveCredentialRoutingFromData(registryData, req.Name)

	// Resolve the connection-ownership mode from the SAME pre-mutation bytes
	// (V2-cleanup-57.2). A self-managed connection (connectionManagedBy:
	// user) means the ArgoCD cluster Secret is the USER's — removal must
	// leave it in place even under cleanup=all. Parse failures degrade to
	// the Sharko-managed default, which matches pre-field behavior.
	selfManagedConnection := false
	if registryData != nil {
		if parsed, parseErr := config.NewParser().ParseClusterAddons(registryData); parseErr == nil {
			selfManagedConnection = models.IsUserManagedConnection(models.ConnectionManagedByFor(parsed, req.Name))
		}
	}

	// What has to happen to the ArgoCD connection once the PR merges
	// (EP3-DRYRUN-FIXES call 1). It travels with the tracked PR so the
	// merge hook can do it when a person merges the PR later.
	followUp := ClusterFollowUp{
		Cleanup:           cleanup,
		KeepConnection:    removeKeepsConnection(cleanup, selfManagedConnection),
		CredentialKey:     credLookupKey,
		CredentialsSource: credsSource,
		CredentialRoleARN: credRoleARN,
	}

	var files map[string][]byte
	var deletePaths []string

	if v4Repo {
		if v4UpdatedRegistry != nil {
			files = map[string][]byte{V4ManagedClustersPath: v4UpdatedRegistry}
			steps = append(steps, "remove_managed_clusters_entry")
		}
		if cleanup != "none" {
			deletePaths = v4DeletePaths
		}
	} else {
		if updatedClusterAddons != nil {
			files = map[string][]byte{
				clusterAddonsPath: updatedClusterAddons,
			}
			steps = append(steps, "remove_managed_clusters_entry")
		}
		if cleanup != "none" {
			deletePaths = append(deletePaths, valuesPath)
		}
	}

	// Only create a PR if there are changes to commit.
	if len(files) > 0 || len(deletePaths) > 0 {
		// A connection this removal keeps, and that still carries Sharko's
		// label, gets a harmless mark BEFORE the pull request opens. While
		// the mark is on, the orphan sweep and the leftovers list leave the
		// Secret alone, even if the after-merge step fails or the tracker
		// loses the pull request (EP3 dry run round R, R1).
		markedKeep, markErr := o.markKeptConnection(ctx, req.Name, followUp)
		if markErr != nil {
			result.Status = "failed"
			result.FailedStep = "protect_kept_connection"
			result.Error = markErr.Error()
			result.Message = RemoveKeepMarkFailedMessage(req.Name)
			var other *OtherRemovalWaitingError
			if errors.As(markErr, &other) {
				result.FailedStep = "other_pr_open"
				result.Message = other.Error()
			}
			return result, nil
		}

		meta := o.prMeta(req.AutoMerge, "remove-cluster", fmt.Sprintf("Remove cluster %s", req.Name), req.Name, "")
		fu := followUp
		meta.FollowUp = &fu
		gitResult, gitErr := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("remove cluster %s", req.Name), meta)
		if markedKeep {
			if gitResult == nil {
				// No pull request: take the mark back off.
				o.removeMarkIfValue(ctx, req.Name, AnnotationRemoveKeepPendingPR, pendingMarkValue)
			} else if setErr := o.argoSecretManager.SetAnnotation(ctx, req.Name, AnnotationRemoveKeepPendingPR, keepMarkValue(models.PendingMarkOpRemove, gitResult.PRID, gitResult.PRUrl)); setErr != nil {
				// The "pending" mark still protects the connection.
				log.Warn("could not write the pull request number on the kept connection's mark; the mark stays on as it is",
					"cluster", req.Name, "pr_id", gitResult.PRID, "error", setErr)
			}
		}
		if gitErr != nil && gitResult != nil && errors.Is(gitErr, ErrPRNotRecorded) {
			result.Status = "partial"
			result.CompletedSteps = steps
			result.FailedStep = "track_pr"
			result.Error = gitErr.Error()
			result.Message = PRNotRecordedMessage("removal", gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		if gitErr != nil {
			if gitResult != nil {
				result.Status = "partial"
				result.CompletedSteps = steps
				result.FailedStep = "pr_merge"
				result.Error = gitErr.Error()
				result.Message = fmt.Sprintf("PR created but merge failed: %s. Nothing on the cluster's ArgoCD connection was changed — Sharko finishes the removal after the PR is merged.", gitResult.PRUrl)
				result.Git = gitResult
				return result, nil
			}
			result.Status = "failed"
			result.CompletedSteps = steps
			result.FailedStep = "git_commit"
			result.Error = gitErr.Error()
			result.Message = "Git commit failed during cluster removal"
			return result, nil
		}
		result.Git = gitResult
		steps = append(steps, "git_commit")
		if cleanup != "none" {
			if v4Repo {
				if len(v4DeletePaths) > 0 {
					steps = append(steps, "delete_cluster_files")
				}
			} else {
				steps = append(steps, "delete_values_file")
			}
		}

		// NOTHING THAT CUTS A CLUSTER OFF HAPPENS BEFORE ITS PR IS MERGED
		// (EP3-DRYRUN-FIXES call 1). When the PR waits for a person to
		// merge it, the ArgoCD connection and the addon secrets on the
		// cluster stay exactly as they are; the PR tracker's merge hook
		// runs FinishRemoveAfterMerge with the same choices once it merges.
		// If the PR is closed without merging, nothing was changed at all.
		if !gitResult.Merged {
			steps = append(steps, "waiting_for_merge")
			result.Status = "success"
			result.CompletedSteps = steps
			result.Message = RemoveWaitingForMergeMessage(req.Name, gitResult.PRUrl)
			return result, nil
		}
	}

	// The PR merged at once (or there was nothing left in Git to remove):
	// finish now, exactly as the merge hook would.
	if result.Git != nil {
		followUp.PRID = result.Git.PRID
	}
	o.finishRemove(ctx, req.Name, followUp, result, &steps)
	if result.Status == "" {
		result.Status = "success"
	}
	result.CompletedSteps = steps
	return result, nil
}

// RemoveWaitingForMergeMessage is the message a removal answers with while
// its pull request waits to be merged. Exported so tests pin it by exact
// text.
func RemoveWaitingForMergeMessage(name, prURL string) string {
	return fmt.Sprintf("Pull request opened, waiting for merge: %s. Nothing has changed on cluster %s yet — its ArgoCD connection stays as it is until the pull request is merged. Sharko finishes the removal after the merge.", prURL, name)
}

// RemoveKeepMarkFailedMessage is the answer when Sharko could not put the
// protecting mark on a connection the removal keeps. Exported so tests pin
// it by exact text.
func RemoveKeepMarkFailedMessage(name string) string {
	return fmt.Sprintf("Sharko did not remove cluster %s. Before it opens the pull request, it puts a mark on the cluster's ArgoCD connection so nothing deletes that connection after the merge, and that step failed. Nothing has changed. Try again.", name)
}

// markKeptConnection puts the remove-keep mark ("pending" for now) on a
// connection the removal keeps, when that connection carries Sharko's
// label. A connection without Sharko's label needs no mark: the sweep and
// the leftovers list only ever touch Sharko-labelled Secrets. Returns
// whether it put the mark on.
func (o *Orchestrator) markKeptConnection(ctx context.Context, name string, followUp ClusterFollowUp) (bool, error) {
	if !followUp.KeepConnection || o.argoSecretManager == nil {
		return false, nil
	}
	detail, err := o.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		return false, fmt.Errorf("reading the ArgoCD connection of cluster %q: %w", name, err)
	}
	if !detail.Found || detail.ManagedBy != sharkoManagedByValue {
		return false, nil
	}
	// Another removal's keep mark is never written over: that pull
	// request still has to finish with it (EP3 dry run round S, S4).
	cur, err := o.argoSecretManager.GetAnnotation(ctx, name, AnnotationRemoveKeepPendingPR)
	if err != nil {
		return false, fmt.Errorf("reading the keep mark on the ArgoCD connection of cluster %q: %w", name, err)
	}
	if cur != "" {
		other, _ := models.ParsePendingPRMark(cur)
		return false, &OtherRemovalWaitingError{Cluster: name, PRID: other.PR}
	}
	if err := o.argoSecretManager.SetAnnotation(ctx, name, AnnotationRemoveKeepPendingPR, keepMarkValue(models.PendingMarkOpRemove, 0, "")); err != nil {
		return false, fmt.Errorf("marking the ArgoCD connection of cluster %q as kept: %w", name, err)
	}
	return true, nil
}

// keepMarkValue is the keep mark for a removal or un-adopt (op is
// models.PendingMarkOpRemove or PendingMarkOpUnadopt) and pull request
// prID; 0 before the pull request exists.
func keepMarkValue(op string, prID int, prURL string) string {
	return models.PendingPRMark{PR: prID, Repo: models.RepoKeyFromPRURL(prURL), Op: op, At: time.Now().UTC().Format(time.RFC3339)}.String()
}

// OtherRemovalWaitingError is returned when another removal (or un-adopt)
// of the same cluster still waits in its pull request. Its text is the
// answer the person sees.
type OtherRemovalWaitingError struct {
	Cluster string
	PRID    int
}

func (e *OtherRemovalWaitingError) Error() string {
	return OtherRemovalWaitingMessage(e.Cluster, e.PRID)
}

// OtherRemovalWaitingMessage is the answer when another removal or
// un-adopt of the cluster still waits in its pull request. It never says
// "#0". Exported so tests pin it by exact text.
func OtherRemovalWaitingMessage(name string, prID int) string {
	pr := "a pull request"
	if prID > 0 {
		pr = fmt.Sprintf("pull request #%d", prID)
	}
	return fmt.Sprintf("Sharko did not change cluster %s: another removal or un-adopt of it is still waiting in %s. Merge or close that pull request first, then try again.", name, pr)
}

// removeMarkIfValue takes a mark off when it still holds value. Best-effort:
// a failure is logged, and a mark left on only protects the connection.
func (o *Orchestrator) removeMarkIfValue(ctx context.Context, name, key, value string) {
	remover, ok := o.argoSecretManager.(annotationValueRemover)
	if !ok {
		return
	}
	if _, err := remover.RemoveAnnotationIfValue(ctx, name, key, value); err != nil {
		logging.LoggerFromContext(ctx).Warn("could not take a waiting mark off a cluster's ArgoCD connection; it only keeps the connection safe from deletion",
			"cluster", name, "annotation", key, "error", err)
	}
}

// removeKeepsConnection says whether a removal keeps the cluster's ArgoCD
// connection. Only cleanup=all on a connection Sharko manages deletes it;
// every other removal keeps it, and a kept connection must lose Sharko's
// ownership label so neither the orphan sweep nor the leftovers list can
// pick it up later (EP3-DRYRUN-FIXES call 4).
func removeKeepsConnection(cleanup string, selfManaged bool) bool {
	return cleanup != "all" || selfManaged
}

// FinishRemoveAfterMerge does the ArgoCD side of a removal whose pull
// request was merged after RemoveCluster returned. The PR tracker's merge
// hook calls it (internal/api). The caller must have checked that the
// cluster is not back in Git. It is safe to run twice: every step is a
// no-op once done.
func (o *Orchestrator) FinishRemoveAfterMerge(ctx context.Context, name string, followUp ClusterFollowUp) *RemoveClusterResult {
	result := &RemoveClusterResult{Name: name, Cleanup: followUp.Cleanup}
	var steps []string
	o.finishRemove(ctx, name, followUp, result, &steps)
	if result.Status == "" {
		result.Status = "success"
	}
	result.CompletedSteps = steps
	return result
}

// finishRemove is the after-merge half of a removal: the addon secrets on
// the cluster (cleanup=all), then either Sharko's ownership label comes off
// the kept connection, or the Sharko-owned ArgoCD cluster Secret is
// deleted. It sets result.Status only on a failure.
func (o *Orchestrator) finishRemove(ctx context.Context, name string, followUp ClusterFollowUp, result *RemoveClusterResult, steps *[]string) {
	log := logging.LoggerFromContext(ctx)
	cleanup := followUp.Cleanup
	if cleanup == "" {
		cleanup = "all"
	}

	// Step 2: If cleanup=all, delete addon secrets from remote cluster
	// (best-effort). The credentials come through the same router the
	// request path uses, so a cluster whose connection is kept only in
	// ArgoCD is reached too (R13).
	if cleanup == "all" && (o.credProvider != nil || o.credsRouter != nil) {
		creds, credErr := o.followUpCredentials(name, followUp)
		if credErr == nil && creds != nil {
			deleted, _ := o.deleteAllAddonSecrets(ctx, name, creds.Raw)
			if len(deleted) > 0 {
				*steps = append(*steps, "delete_remote_secrets")
			}
		} else {
			log.Warn("could not fetch credentials for remote secret cleanup",
				"cluster", name, "error", credErr)
		}
	}

	// KEEP THE CONNECTION: take Sharko's ownership label (and the
	// remove-keep mark) off so the Secret is no longer Sharko's. Without
	// it, the orphan sweep would see a sharko-labeled Secret with no git
	// entry and delete a connection the person asked to keep, and the
	// leftovers list could offer it for discard.
	//
	// Which connections are kept: every removal with cleanup git or none
	// (whether Sharko registered, adopted or took over the cluster), and a
	// connection the user manages (connectionManagedBy: user) under any
	// cleanup (V2-cleanup-60.1 handover, EP3-DRYRUN-FIXES call 4).
	// cleanup=all on any other connection, adopted and taken-over ones
	// included, is NOT kept: the ArgoCD cluster Secret is deleted below,
	// and the unregister screen says so.
	//
	// A failure makes the result "partial", so the after-merge step is
	// tried again; the remove-keep mark keeps the Secret safe meanwhile.
	if followUp.KeepConnection && o.argoSecretManager != nil {
		var released bool
		var releaseErr error
		if releaser, ok := o.argoSecretManager.(keptConnectionReleaser); ok {
			released, releaseErr = releaser.ReleaseKeptConnection(ctx, name, followUp.PRID)
		} else if stripper, ok := o.argoSecretManager.(ownershipLabelStripper); ok {
			released, releaseErr = stripper.StripOwnershipLabel(ctx, name)
		}
		switch {
		case releaseErr != nil:
			log.Error("could not take Sharko's ownership label off a kept ArgoCD cluster Secret during removal; Sharko will try again, and its keep mark protects the Secret meanwhile",
				"cluster", name, "error", releaseErr)
			result.Status = "partial"
			result.FailedStep = "strip_sharko_ownership_label"
			result.Error = releaseErr.Error()
			result.Message = fmt.Sprintf("Cluster %s was removed from Git, but Sharko could not yet hand its ArgoCD connection back. The connection is kept and nothing deletes it. Sharko tries again by itself.", name)
		case released:
			*steps = append(*steps, "strip_sharko_ownership_label")
		}
	}

	// Step 3: If cleanup=all, delete ArgoCD cluster secret.
	//
	// SELF-MANAGED GUARD (V2-cleanup-57.2): the user created and maintains
	// this cluster's ArgoCD Secret; deleting it would kill THEIR connection.
	// Leave it in place and say so.
	if cleanup == "all" && followUp.KeepConnection {
		log.Info("cluster connection is managed by the user — leaving the ArgoCD cluster Secret in place",
			"cluster", name)
		*steps = append(*steps, "skip_argocd_secret_user_managed")
		if result.Status == "" { // a failed hand-back above keeps its own message
			result.Message = fmt.Sprintf(
				"Cluster %s removed from Sharko. Its ArgoCD cluster Secret was left in place because the connection is managed by you — delete it yourself if you no longer want ArgoCD connected to this cluster.",
				name)
		}
	}
	if cleanup == "all" && !followUp.KeepConnection && o.argoSecretManager != nil {
		if deleter, ok := o.argoSecretManager.(clusterSecretDeleter); ok {
			o.deleteOwnedClusterSecret(ctx, deleter, name, result, steps)
			return
		}
	}
	if cleanup == "all" && !followUp.KeepConnection && o.argoSecretManager != nil && o.argocd != nil {
		// Find the server URL so we can delete from ArgoCD.
		clusters, listErr := o.argocd.ListClusters(ctx)
		if listErr == nil {
			for _, c := range clusters {
				if c.Name != name {
					continue
				}

				// OWNERSHIP GATE (V2-cleanup-60.1): never delete a Secret
				// that does not carry Sharko's ownership label. The mode
				// check reads the cluster's git entry — but on a retry of
				// a removal whose PR already merged, the entry is gone and
				// the self-managed answer silently defaults to false. The
				// label on the Secret itself is the ownership record that
				// survives the entry's removal, so it has the final say.
				// Any doubt (read error, missing secret, absent or foreign
				// label) means refuse.
				managedBy, labelErr := o.argoSecretManager.GetManagedByLabel(ctx, name)
				if labelErr != nil || managedBy != sharkoManagedByValue {
					if labelErr != nil {
						log.Warn("could not confirm Sharko's ownership label on the ArgoCD cluster Secret — refusing to delete it",
							"cluster", name, "error", labelErr)
					} else {
						log.Info("ArgoCD cluster Secret does not carry Sharko's ownership label — refusing to delete it",
							"cluster", name, "managed_by", managedBy)
					}
					*steps = append(*steps, "skip_argocd_secret_not_sharko_labeled")
					result.Message = notSharkoLabelledMessage(name)
					break
				}

				if !o.noWaitingMarkOn(ctx, name, result, steps) {
					break
				}
				if delErr := o.argocd.DeleteCluster(ctx, c.Server); delErr != nil {
					log.Error("failed to delete ArgoCD cluster during removal",
						"cluster", name, "error", delErr)
					result.Status = "partial"
					result.FailedStep = "delete_argocd_cluster"
					result.Error = delErr.Error()
					result.Message = "Git changes committed but ArgoCD cluster deletion failed"
					return
				}
				*steps = append(*steps, "delete_argocd_cluster")
				break
			}
		}
	}
}

// notSharkoLabelledMessage is the answer when cleanup=all finds a
// connection without Sharko's label and leaves it in place.
func notSharkoLabelledMessage(name string) string {
	return fmt.Sprintf(
		"Cluster %s was removed from Sharko, but its ArgoCD cluster Secret was left in place: the Secret does not carry Sharko's ownership label (app.kubernetes.io/managed-by: sharko), so Sharko cannot confirm it created it and will not delete it. If you manage that connection yourself this is exactly right — delete the Secret yourself if you no longer want ArgoCD connected to this cluster.",
		name)
}

// deleteOwnedClusterSecret deletes the ArgoCD cluster Secret the shared
// lookup finds for name, by its own Kubernetes name, and only when it
// carries Sharko's label (R8). A read or delete failure makes the result
// "partial", so the after-merge step is tried again.
func (o *Orchestrator) deleteOwnedClusterSecret(ctx context.Context, deleter clusterSecretDeleter, name string, result *RemoveClusterResult, steps *[]string) {
	log := logging.LoggerFromContext(ctx)
	detail, err := o.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		log.Error("could not read the ArgoCD cluster Secret during removal", "cluster", name, "error", err)
		result.Status = "partial"
		result.FailedStep = "delete_argocd_cluster"
		result.Error = err.Error()
		result.Message = "Git changes committed but ArgoCD cluster deletion failed"
		return
	}
	if !detail.Found {
		return // already gone
	}
	if detail.ManagedBy != sharkoManagedByValue {
		log.Info("ArgoCD cluster Secret does not carry Sharko's ownership label — refusing to delete it",
			"cluster", name, "managed_by", detail.ManagedBy)
		*steps = append(*steps, "skip_argocd_secret_not_sharko_labeled")
		result.Message = notSharkoLabelledMessage(name)
		return
	}
	if !o.noWaitingMarkOn(ctx, name, result, steps) {
		return
	}
	if delErr := deleter.DeleteClusterSecret(ctx, name); delErr != nil {
		log.Error("failed to delete ArgoCD cluster during removal",
			"cluster", name, "error", delErr)
		result.Status = "partial"
		result.FailedStep = "delete_argocd_cluster"
		result.Error = delErr.Error()
		result.Message = "Git changes committed but ArgoCD cluster deletion failed"
		return
	}
	*steps = append(*steps, "delete_argocd_cluster")
}

// waitingMarks are the marks that say another pull request still has to
// finish with a connection. Kept in lockstep with
// argosecrets.PendingPRMarks.
var waitingMarks = []string{AnnotationAdoptPendingPR, AnnotationTakeoverPendingPR, AnnotationRemoveKeepPendingPR}

// noWaitingMarkOn says whether the connection carries none of the waiting
// marks, so a cleanup=all removal may delete it. A connection another
// removal keeps, or an adopt or take over still waits on, is left in place
// (EP3 dry run round S, S4). A failed read is not a "no": the result turns
// partial and the finish tries again.
func (o *Orchestrator) noWaitingMarkOn(ctx context.Context, name string, result *RemoveClusterResult, steps *[]string) bool {
	log := logging.LoggerFromContext(ctx)
	for _, key := range waitingMarks {
		v, err := o.argoSecretManager.GetAnnotation(ctx, name, key)
		if err != nil {
			log.Error("could not read the waiting marks on the ArgoCD cluster Secret during removal; not deleting it, Sharko tries again",
				"cluster", name, "annotation", key, "error", err)
			result.Status = "partial"
			result.FailedStep = "delete_argocd_cluster"
			result.Error = err.Error()
			result.Message = "Git changes committed but ArgoCD cluster deletion failed"
			return false
		}
		if v != "" {
			log.Info("another pull request still has to finish with this ArgoCD cluster Secret — leaving it in place",
				"cluster", name, "annotation", key, "pr", v)
			*steps = append(*steps, "skip_argocd_secret_waiting_pr")
			result.Message = RemoveLeftWaitingConnectionMessage(name)
			return false
		}
	}
	return true
}

// RemoveLeftWaitingConnectionMessage is the answer when a cleanup=all
// removal leaves the connection because another pull request still has to
// finish with it. Exported so tests pin it by exact text.
func RemoveLeftWaitingConnectionMessage(name string) string {
	return fmt.Sprintf("Cluster %s was removed from Sharko, but its ArgoCD connection was left in place: another pull request still has to finish with it. Delete it yourself later if you no longer want ArgoCD connected to this cluster.", name)
}

// followUpCredentials fetches a cluster's credentials for the after-merge
// clean up, with the routing saved on the follow-up.
func (o *Orchestrator) followUpCredentials(name string, followUp ClusterFollowUp) (*providers.Kubeconfig, error) {
	lookupKey := followUp.CredentialKey
	if lookupKey == "" {
		lookupKey = name
	}
	if o.credsRouter != nil {
		return o.credsRouter.Fetch(name, lookupKey, followUp.CredentialsSource, followUp.CredentialRoleARN)
	}
	if o.credProvider == nil {
		return nil, fmt.Errorf("no credentials provider configured")
	}
	return providers.GetCredentialsWithOptionalRole(o.credProvider, lookupKey, followUp.CredentialRoleARN)
}

// ClusterInRegistry reports whether name is in the fleet registry on the
// base branch right now (v4 root managed-clusters.yaml, or the v3
// configuration/managed-clusters.yaml). The after-merge hooks call it so
// they never touch a connection whose cluster was registered again under
// the same name. A read or parse failure is an error, never a "no": the
// caller must not act when Sharko cannot tell.
func (o *Orchestrator) ClusterInRegistry(ctx context.Context, name string) (bool, error) {
	v4Repo, err := o.isV4Repo(ctx)
	if err != nil {
		return false, err
	}
	registryPath := V4ManagedClustersPath
	if !v4Repo {
		registryPath = o.paths.ManagedClusters
		if registryPath == "" {
			registryPath = "configuration/managed-clusters.yaml"
		}
	}
	data, exists, err := o.readFileForRewrite(ctx, registryPath)
	if err != nil {
		return false, err
	}
	if !exists || len(data) == 0 {
		return false, nil
	}
	clusters, err := config.NewParser().ParseClusterAddons(data)
	if err != nil {
		return false, fmt.Errorf("could not read the cluster list in %s: %w", registryPath, err)
	}
	for _, c := range clusters {
		if c.Name == name {
			return true, nil
		}
	}
	return false, nil
}
