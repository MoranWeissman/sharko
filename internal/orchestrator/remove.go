package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
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
//   - "all" (default): remove from managed-clusters.yaml + delete values file via PR;
//     after merge, delete addon secrets on remote + ArgoCD cluster secret.
//   - "git": same Git changes, but skip remote addon secret deletion.
//   - "none": only remove managed-clusters.yaml entry (values file kept, ArgoCD secret kept).
//
// Steps:
//  1. Validate confirmation (yes: true required).
//  2. Create PR: remove managed-clusters entry + delete values file (except cleanup=none).
//  3. If cleanup=all: delete addon secrets from remote cluster + delete ArgoCD cluster secret.
//     The ArgoCD secret delete is gated on Sharko's ownership label
//     (V2-cleanup-60.1): a Secret that does not carry
//     app.kubernetes.io/managed-by: sharko is NEVER deleted — the git entry
//     may already be gone (retry of a partially-failed removal), in which
//     case the mode check above cannot see connectionManagedBy: user and the
//     Secret itself is the only ownership record left.
func (o *Orchestrator) RemoveCluster(ctx context.Context, req RemoveClusterRequest) (*RemoveClusterResult, error) {
	log := logging.LoggerFromContext(ctx)
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
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

	valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")
	clusterAddonsPath := o.paths.ManagedClusters
	if clusterAddonsPath == "" {
		clusterAddonsPath = "configuration/managed-clusters.yaml"
	}

	// Dry-run exit point: return a preview of what would happen.
	if req.DryRun {
		filePreviews := []FilePreview{
			{Path: clusterAddonsPath, Action: "update"},
		}
		if cleanup != "none" {
			filePreviews = append(filePreviews, FilePreview{Path: valuesPath, Action: "delete"})
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

	// Step 1: Create PR to remove from managed-clusters.yaml and optionally delete values file.
	clusterAddonsData, err := o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
	if err != nil {
		log.Warn("managed-clusters.yaml not found — skipping removal from it", "cluster", req.Name)
	}

	// Resolve the credential lookup key NOW, from the bytes we just read —
	// the PR below removes this cluster's entry (and may auto-merge), so a
	// post-PR resolution would no longer find the stored secretPath
	// override (V2-cleanup-55.1).
	credLookupKey := config.ResolveCredentialLookupKeyFromData(clusterAddonsData, req.Name)

	// Resolve the connection-ownership mode from the SAME pre-mutation bytes
	// (V2-cleanup-57.2). A self-managed connection (connectionManagedBy:
	// user) means the ArgoCD cluster Secret is the USER's — removal must
	// leave it in place even under cleanup=all. Parse failures degrade to
	// the Sharko-managed default, which matches pre-field behavior.
	selfManagedConnection := false
	if clusterAddonsData != nil {
		if parsed, parseErr := config.NewParser().ParseClusterAddons(clusterAddonsData); parseErr == nil {
			selfManagedConnection = models.IsUserManagedConnection(models.ConnectionManagedByFor(parsed, req.Name))
		}
	}

	var files map[string][]byte
	var deletePaths []string

	if clusterAddonsData != nil {
		updatedData, removeErr := gitops.RemoveClusterEntry(clusterAddonsData, req.Name)
		if removeErr != nil {
			log.Warn("failed to remove cluster entry from managed-clusters.yaml",
				"cluster", req.Name, "error", removeErr)
		} else {
			files = map[string][]byte{
				clusterAddonsPath: updatedData,
			}
			steps = append(steps, "remove_managed_clusters_entry")
		}
	}

	if cleanup != "none" {
		deletePaths = append(deletePaths, valuesPath)
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
			steps = append(steps, "delete_values_file")
		}
	}

	// Step 2: If cleanup=all, delete addon secrets from remote cluster (best-effort).
	if cleanup == "all" && o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(credLookupKey)
		if credErr == nil {
			deleted, _ := o.deleteAllAddonSecrets(ctx, creds.Raw)
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
