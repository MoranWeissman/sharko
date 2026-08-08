package orchestrator

import (
	"context"
	"fmt"
	"path"

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
//  3. If cleanup=all: delete addon secrets from remote cluster + delete ArgoCD cluster secret.
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
	credLookupKey, _, credRoleARN := config.ResolveCredentialRoutingFromData(registryData, req.Name)

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
		gitResult, gitErr := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("remove cluster %s", req.Name),
			o.prMeta(req.AutoMerge, "remove-cluster", fmt.Sprintf("Remove cluster %s", req.Name), req.Name, ""))
		if gitErr != nil {
			if gitResult != nil {
				result.Status = "partial"
				result.CompletedSteps = steps
				result.FailedStep = "pr_merge"
				result.Error = gitErr.Error()
				result.Message = fmt.Sprintf("PR created but merge failed: %s", gitResult.PRUrl)
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
	}

	// Step 2: If cleanup=all, delete addon secrets from remote cluster (best-effort).
	if cleanup == "all" && o.credProvider != nil {
		creds, credErr := providers.GetCredentialsWithOptionalRole(o.credProvider, credLookupKey, credRoleARN)
		if credErr == nil {
			deleted, _ := o.deleteAllAddonSecrets(ctx, req.Name, creds.Raw)
			if len(deleted) > 0 {
				steps = append(steps, "delete_remote_secrets")
			}
		} else {
			log.Warn("could not fetch credentials for remote secret cleanup",
				"cluster", req.Name, "error", credErr)
		}
	}

	// HANDOVER AT REMOVAL TIME (V2-cleanup-60.1): when the pre-mutation
	// bytes say the connection is the user's, strip Sharko's ownership
	// label from the Secret NOW — the reconcile tick that normally does
	// this on a mode switch reads the git entry, and the PR above just
	// removed it, so no later tick can ever perform the handover. Without
	// the strip, the orphan sweep would see a sharko-labeled Secret with no
	// git entry and delete the user's connection. Applies to every cleanup
	// scope (the entry is removed in all of them). Best-effort: a failure
	// is logged loudly but never blocks the removal.
	if selfManagedConnection && o.argoSecretManager != nil {
		if stripper, ok := o.argoSecretManager.(ownershipLabelStripper); ok {
			stripped, stripErr := stripper.StripOwnershipLabel(ctx, req.Name)
			switch {
			case stripErr != nil:
				log.Error("could not strip Sharko's ownership label from the user's ArgoCD cluster Secret during removal — no reconcile tick can do it now that the git entry is gone; remove the app.kubernetes.io/managed-by label by hand or the orphan sweep may delete the Secret",
					"cluster", req.Name, "error", stripErr)
			case stripped:
				steps = append(steps, "strip_sharko_ownership_label")
			}
		}
	}

	// Step 3: If cleanup=all, delete ArgoCD cluster secret.
	//
	// SELF-MANAGED GUARD (V2-cleanup-57.2): the user created and maintains
	// this cluster's ArgoCD Secret; deleting it would kill THEIR connection.
	// Leave it in place and say so.
	if cleanup == "all" && selfManagedConnection {
		log.Info("cluster connection is managed by the user — leaving the ArgoCD cluster Secret in place",
			"cluster", req.Name)
		steps = append(steps, "skip_argocd_secret_user_managed")
		result.Message = fmt.Sprintf(
			"Cluster %s removed from Sharko. Its ArgoCD cluster Secret was left in place because the connection is managed by you — delete it yourself if you no longer want ArgoCD connected to this cluster.",
			req.Name)
	}
	if cleanup == "all" && !selfManagedConnection && o.argoSecretManager != nil {
		// Find the server URL so we can delete from ArgoCD.
		clusters, listErr := o.argocd.ListClusters(ctx)
		if listErr == nil {
			for _, c := range clusters {
				if c.Name != req.Name {
					continue
				}

				// OWNERSHIP GATE (V2-cleanup-60.1): never delete a Secret
				// that does not carry Sharko's ownership label. The mode
				// check above reads the cluster's git entry — but on a
				// retry of a removal whose PR already merged, the entry is
				// gone and selfManagedConnection silently defaults to
				// false. The label on the Secret itself is the ownership
				// record that survives the entry's removal, so it has the
				// final say. Any doubt (read error, missing secret, absent
				// or foreign label) means refuse.
				managedBy, labelErr := o.argoSecretManager.GetManagedByLabel(ctx, req.Name)
				if labelErr != nil || managedBy != sharkoManagedByValue {
					if labelErr != nil {
						log.Warn("could not confirm Sharko's ownership label on the ArgoCD cluster Secret — refusing to delete it",
							"cluster", req.Name, "error", labelErr)
					} else {
						log.Info("ArgoCD cluster Secret does not carry Sharko's ownership label — refusing to delete it",
							"cluster", req.Name, "managed_by", managedBy)
					}
					steps = append(steps, "skip_argocd_secret_not_sharko_labeled")
					result.Message = fmt.Sprintf(
						"Cluster %s was removed from Sharko, but its ArgoCD cluster Secret was left in place: the Secret does not carry Sharko's ownership label (app.kubernetes.io/managed-by: sharko), so Sharko cannot confirm it created it and will not delete it. If you manage that connection yourself this is exactly right — delete the Secret yourself if you no longer want ArgoCD connected to this cluster.",
						req.Name)
					break
				}

				if delErr := o.argocd.DeleteCluster(ctx, c.Server); delErr != nil {
					log.Error("failed to delete ArgoCD cluster during removal",
						"cluster", req.Name, "error", delErr)
					result.Status = "partial"
					result.FailedStep = "delete_argocd_cluster"
					result.Error = delErr.Error()
					result.CompletedSteps = steps
					result.Message = "Git changes committed but ArgoCD cluster deletion failed"
					return result, nil
				}
				steps = append(steps, "delete_argocd_cluster")
				break
			}
		}
	}

	result.Status = "success"
	result.CompletedSteps = steps
	return result, nil
}
