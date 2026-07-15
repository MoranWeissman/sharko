package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/gitops"
)

// UnadoptCluster reverses a cluster adoption:
//  1. Check that the cluster has the adopted annotation (error if not).
//  2. Remove the managed-by label and adopted annotation from the ArgoCD secret (keep secret).
//  3. Delete Sharko-created addon secrets from the remote cluster.
//  4. Create PR to remove from managed-clusters.yaml and delete values file.
func (o *Orchestrator) UnadoptCluster(ctx context.Context, name string, req UnadoptClusterRequest) (*UnadoptClusterResult, error) {
	log := logging.LoggerFromContext(ctx)
	if name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	result := &UnadoptClusterResult{Name: name}

	// Step 1: Check adopted annotation. Recognise both older key spellings
	// too (V2-cleanup-59, V2-cleanup-60.5 L10): a cluster adopted before
	// either rename carries only an older annotation and must stay
	// unadoptable.
	if o.argoSecretManager != nil {
		adopted, err := o.argoSecretManager.GetAnnotation(ctx, name, AnnotationAdopted)
		if err != nil {
			return nil, fmt.Errorf("checking adopted annotation for cluster %q: %w", name, err)
		}
		if adopted != "true" {
			doubledPrefix, doubledPrefixErr := o.argoSecretManager.GetAnnotation(ctx, name, AnnotationAdoptedDoubledPrefixLegacy)
			if doubledPrefixErr != nil {
				return nil, fmt.Errorf("checking adopted annotation for cluster %q: %w", name, doubledPrefixErr)
			}
			if doubledPrefix != "true" {
				legacy, legacyErr := o.argoSecretManager.GetAnnotation(ctx, name, AnnotationAdoptedLegacy)
				if legacyErr != nil {
					return nil, fmt.Errorf("checking adopted annotation for cluster %q: %w", name, legacyErr)
				}
				if legacy != "true" {
					return nil, fmt.Errorf("cluster %q was not adopted (missing %s annotation) — use remove-cluster instead", name, AnnotationAdopted)
				}
			}
		}
	}

	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
	clusterAddonsPath := o.paths.ManagedClusters
	if clusterAddonsPath == "" {
		clusterAddonsPath = "configuration/managed-clusters.yaml"
	}

	// Generate file content (shared between dry-run and real path).
	clusterAddonsData, err := o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
	if err != nil && !req.DryRun {
		log.Warn("managed-clusters.yaml not found — skipping removal", "cluster", name)
	}

	var updatedClusterAddons []byte
	if clusterAddonsData != nil {
		var removeErr error
		updatedClusterAddons, removeErr = gitops.RemoveClusterEntry(clusterAddonsData, name)
		if removeErr != nil && !req.DryRun {
			log.Warn("failed to remove cluster entry from managed-clusters.yaml",
				"cluster", name, "error", removeErr)
			updatedClusterAddons = nil
		}
	}

	// Dry-run exit point.
	if req.DryRun {
		var filePreviews []FilePreview

		oldValues, _ := o.readFileIfExists(ctx, valuesPath)
		filePreviews = append(filePreviews, FilePreview{
			Path:   valuesPath,
			Action: "delete",
			Diff:   o.buildFileDiff(valuesPath, oldValues, nil, "delete"),
		})

		if updatedClusterAddons != nil {
			filePreviews = append(filePreviews, FilePreview{
				Path:   clusterAddonsPath,
				Action: "update",
				Diff:   o.buildFileDiff(clusterAddonsPath, clusterAddonsData, updatedClusterAddons, "update"),
			})
		}

		prTitle := fmt.Sprintf("%s unadopt cluster %s", o.gitops.CommitPrefix, name)
		result.Status = "success"
		result.DryRun = &DryRunResult{
			FilesToWrite: filePreviews,
			PRTitle:      prTitle,
		}
		return result, nil
	}

	// Step 2: Remove managed-by label and adopted annotation from ArgoCD secret.
	// Do this BEFORE the Git PR so the reconciler won't treat the removal as an orphan.
	if o.argoSecretManager != nil {
		if err := o.argoSecretManager.Unadopt(ctx, name); err != nil {
			return nil, fmt.Errorf("removing managed-by label from ArgoCD secret for cluster %q: %w", name, err)
		}
		log.Info("ArgoCD secret unadopted — labels removed", "cluster", name)
	}

	// Step 3: Delete Sharko-created addon secrets from remote cluster (best-effort).
	// Resolve the stored secretPath override BEFORE step 4 removes the
	// cluster's managed-clusters.yaml entry (V2-cleanup-55.1).
	if o.credProvider != nil {
		creds, credErr := o.fetchClusterCredentials(ctx, name)
		if credErr == nil {
			o.deleteAllAddonSecrets(ctx, creds.Raw) // best-effort
		} else {
			log.Warn("could not fetch credentials for remote secret cleanup", "cluster", name, "error", credErr)
		}
	}

	// Step 4: Create PR to remove from managed-clusters.yaml and delete values file.
	// Reuse updatedClusterAddons already computed above.

	var files map[string][]byte
	var deletePaths []string

	if updatedClusterAddons != nil {
		files = map[string][]byte{
			clusterAddonsPath: updatedClusterAddons,
		}
	}

	deletePaths = append(deletePaths, valuesPath)

	gitResult, gitErr := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("unadopt cluster %s", name),
		o.prMeta(req.AutoMerge, "unadopt-cluster", fmt.Sprintf("Unadopt cluster %s", name), name, ""))
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
