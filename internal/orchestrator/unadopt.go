package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

// UnadoptCluster reverses a cluster adoption:
//  1. Check that the cluster has the adopted annotation (error if not).
//  2. Remove the managed-by label and adopted annotation from the ArgoCD secret (keep secret).
//  3. Delete Sharko-created addon secrets from the remote cluster.
//  4. Create PR to remove from managed-clusters.yaml and delete values file.
func (o *Orchestrator) UnadoptCluster(ctx context.Context, name string, req UnadoptClusterRequest) (*UnadoptClusterResult, error) {
	if name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	result := &UnadoptClusterResult{Name: name}

	// Step 1: Check adopted annotation.
	if o.argoSecretManager != nil {
		adopted, err := o.argoSecretManager.GetAnnotation(ctx, name, AnnotationAdopted)
		if err != nil {
			return nil, fmt.Errorf("checking adopted annotation for cluster %q: %w", name, err)
		}
		if adopted != "true" {
			return nil, fmt.Errorf("cluster %q was not adopted (missing %s annotation) — use remove-cluster instead", name, AnnotationAdopted)
		}
	}

	// Dry-run exit point.
	if req.DryRun {
		valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
		clusterAddonsPath := o.paths.ManagedClusters
		if clusterAddonsPath == "" {
			clusterAddonsPath = "configuration/managed-clusters.yaml"
		}
		prTitle := fmt.Sprintf("%s unadopt cluster %s", o.gitops.CommitPrefix, name)
		result.Status = "success"
		result.DryRun = &DryRunResult{
			FilesToWrite: []FilePreview{
				{Path: valuesPath, Action: "delete"},
				{Path: clusterAddonsPath, Action: "update"},
			},
			PRTitle: prTitle,
		}
		return result, nil
	}

	// Step 2: Remove managed-by label and adopted annotation from ArgoCD secret.
	// Do this BEFORE the Git PR so the reconciler won't treat the removal as an orphan.
	if o.argoSecretManager != nil {
		if err := o.argoSecretManager.Unadopt(ctx, name); err != nil {
			return nil, fmt.Errorf("removing managed-by label from ArgoCD secret for cluster %q: %w", name, err)
		}
		slog.Info("ArgoCD secret unadopted — labels removed", "cluster", name)
	}

	// Step 3: Delete Sharko-created addon secrets from remote cluster (best-effort).
	if o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(name)
		if credErr == nil {
			o.deleteAllAddonSecrets(ctx, creds.Raw) // best-effort
		} else {
			slog.Warn("could not fetch credentials for remote secret cleanup", "cluster", name, "error", credErr)
		}
	}

	// Step 4: Create PR to remove from managed-clusters.yaml and delete values file.
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
	clusterAddonsPath := o.paths.ManagedClusters
	if clusterAddonsPath == "" {
		clusterAddonsPath = "configuration/managed-clusters.yaml"
	}

	// Read and update managed-clusters.yaml.
	clusterAddonsData, err := o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
	if err != nil {
		slog.Warn("managed-clusters.yaml not found — skipping removal", "cluster", name)
	}

	var files map[string][]byte
	var deletePaths []string

	if clusterAddonsData != nil {
		updatedData, removeErr := gitops.RemoveClusterEntry(clusterAddonsData, name)
		if removeErr != nil {
			slog.Warn("failed to remove cluster entry from managed-clusters.yaml",
				"cluster", name, "error", removeErr)
		} else {
			files = map[string][]byte{
				clusterAddonsPath: updatedData,
			}
		}
	}

	deletePaths = append(deletePaths, valuesPath)

	gitResult, gitErr := o.commitChanges(ctx, files, deletePaths, fmt.Sprintf("unadopt cluster %s", name))
	if gitErr != nil {
		if gitResult != nil {
			result.Status = "partial"
			result.Error = gitErr.Error()
			result.Message = fmt.Sprintf("PR created but merge failed: %s", gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		result.Status = "failed"
		result.Error = gitErr.Error()
		result.Message = "Git commit failed during unadoption — ArgoCD secret labels have already been removed"
		return result, nil
	}

	result.Status = "success"
	result.Git = gitResult
	return result, nil
}
