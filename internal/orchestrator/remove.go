package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

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
func (o *Orchestrator) RemoveCluster(ctx context.Context, req RemoveClusterRequest) (*RemoveClusterResult, error) {
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
		slog.Warn("managed-clusters.yaml not found — skipping removal from it", "cluster", req.Name)
	}

	var files map[string][]byte
	var deletePaths []string

	if clusterAddonsData != nil {
		updatedData, removeErr := gitops.RemoveClusterEntry(clusterAddonsData, req.Name)
		if removeErr != nil {
			slog.Warn("failed to remove cluster entry from managed-clusters.yaml",
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
		gitResult, gitErr := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("remove cluster %s", req.Name), PRMetadata{
			OperationCode: "remove-cluster",
			Cluster:       req.Name,
			Title:         fmt.Sprintf("Remove cluster %s", req.Name),
		})
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
		creds, credErr := o.credProvider.GetCredentials(req.Name)
		if credErr == nil {
			deleted, _ := o.deleteAllAddonSecrets(ctx, creds.Raw)
			if len(deleted) > 0 {
				steps = append(steps, "delete_remote_secrets")
			}
		} else {
			slog.Warn("could not fetch credentials for remote secret cleanup",
				"cluster", req.Name, "error", credErr)
		}
	}

	// Step 3: If cleanup=all, delete ArgoCD cluster secret.
	if cleanup == "all" && o.argoSecretManager != nil {
		// Find the server URL so we can delete from ArgoCD.
		clusters, listErr := o.argocd.ListClusters(ctx)
		if listErr == nil {
			for _, c := range clusters {
				if c.Name == req.Name {
					if delErr := o.argocd.DeleteCluster(ctx, c.Server); delErr != nil {
						slog.Error("failed to delete ArgoCD cluster during removal",
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
	}

	result.Status = "success"
	result.CompletedSteps = steps
	return result, nil
}
