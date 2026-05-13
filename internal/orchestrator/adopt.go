package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// AnnotationAdopted is the key used to mark a cluster as adopted by Sharko.
const AnnotationAdopted = "sharko.sharko.io/adopted"

// AdoptClusters orchestrates the two-phase adoption of existing ArgoCD clusters.
//
// Phase 1: Per-cluster verification — Stage1 connectivity test.
// Phase 2: Per-cluster atomic adoption — create values file, add to managed-clusters.yaml, PR.
//
// If the ArgoCD secret manager is available:
//   - Rejects clusters that have a managed-by label set to something other than "sharko" (FR-4.6).
//   - Sets the adopted annotation on the ArgoCD secret after PR merge.
func (o *Orchestrator) AdoptClusters(ctx context.Context, req AdoptClustersRequest) (*AdoptClustersResult, error) {
	if len(req.Clusters) == 0 {
		return nil, fmt.Errorf("at least one cluster name is required")
	}

	result := &AdoptClustersResult{
		Results: make([]AdoptClusterResult, 0, len(req.Clusters)),
	}

	// Resolve ArgoCD cluster list once for server URL lookups.
	argoClusters, err := o.argocd.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing ArgoCD clusters: %w", err)
	}
	clusterServerMap := make(map[string]string, len(argoClusters))
	for _, c := range argoClusters {
		clusterServerMap[c.Name] = c.Server
	}

	for _, clusterName := range req.Clusters {
		cr := AdoptClusterResult{Name: clusterName}

		// Validate: cluster must exist in ArgoCD (it's an adoption, not a registration).
		serverURL, exists := clusterServerMap[clusterName]
		if !exists {
			cr.Status = "failed"
			cr.Error = fmt.Sprintf("cluster %q not found in ArgoCD — cannot adopt", clusterName)
			result.Results = append(result.Results, cr)
			continue
		}

		// FR-4.6: Reject if managed-by is set to something other than "sharko".
		if o.argoSecretManager != nil {
			managedBy, labelErr := o.argoSecretManager.GetManagedByLabel(ctx, clusterName)
			if labelErr != nil {
				slog.Warn("could not read managed-by label — proceeding with adoption",
					"cluster", clusterName, "error", labelErr)
			} else if managedBy != "" && managedBy != "sharko" {
				cr.Status = "failed"
				cr.Error = fmt.Sprintf("cluster %q is managed by %q, not sharko — cannot adopt", clusterName, managedBy)
				result.Results = append(result.Results, cr)
				continue
			}
		}

		// Phase 1: Verification (Stage1 connectivity test).
		if o.credProvider != nil && o.remoteClientFn != nil {
			creds, credErr := o.credProvider.GetCredentials(clusterName)
			if credErr != nil {
				cr.Status = "failed"
				cr.Error = fmt.Sprintf("fetching credentials for cluster %q: %v", clusterName, credErr)
				result.Results = append(result.Results, cr)
				continue
			}
			if creds.Raw != nil {
				remoteClient, clientErr := o.remoteClientFn(creds.Raw)
				if clientErr != nil {
					cr.Status = "failed"
					cr.Error = fmt.Sprintf("building remote client for cluster %q: %v", clusterName, clientErr)
					result.Results = append(result.Results, cr)
					continue
				}
				verifyResult := verify.Stage1(ctx, remoteClient, verify.TestNamespace())
				cr.Verification = &verifyResult
				if !verifyResult.Success {
					cr.Status = "failed"
					cr.Error = fmt.Sprintf("connectivity verification failed: [%s] %s",
						verifyResult.ErrorCode, verifyResult.ErrorMessage)
					result.Results = append(result.Results, cr)
					continue
				}
			}
		}

		// Dry-run exit point.
		if req.DryRun {
			valuesPath := path.Join(o.paths.ClusterValues, clusterName+".yaml")
			clusterAddonsPath := o.paths.ManagedClusters
			if clusterAddonsPath == "" {
				clusterAddonsPath = "configuration/managed-clusters.yaml"
			}
			prTitle := fmt.Sprintf("%s adopt cluster %s", o.gitops.CommitPrefix, clusterName)
			cr.Status = "success"
			cr.DryRun = &DryRunResult{
				FilesToWrite: []FilePreview{
					{Path: valuesPath, Action: o.fileAction(ctx, valuesPath)},
					{Path: clusterAddonsPath, Action: o.fileAction(ctx, clusterAddonsPath)},
				},
				PRTitle: prTitle,
			}
			if cr.Verification != nil {
				cr.DryRun.Verification = cr.Verification
			}
			result.Results = append(result.Results, cr)
			continue
		}

		// Idempotency (Story 6.3): check if an open PR already exists for this cluster adoption.
		existingPR, existingPRErr := o.findOpenPRForCluster(ctx, clusterName, "adopt")
		if existingPRErr == nil && existingPR != nil {
			slog.Info("Found existing open PR for cluster adoption — skipping",
				"cluster", clusterName, "pr", existingPR.URL)
			cr.Status = "success"
			cr.Git = &GitResult{
				PRUrl:  existingPR.URL,
				PRID:   existingPR.ID,
				Branch: existingPR.SourceBranch,
			}
			cr.Message = "Existing open PR found — skipped PR creation (idempotent retry)"
			result.Results = append(result.Results, cr)
			continue
		}

		// Phase 2: Atomic adoption — create values file + add to managed-clusters.yaml in one PR.
		// BUG-031: AutoMerge is now *bool; nil means "fall back to connection default".
		adoptResult := o.adoptSingleCluster(ctx, clusterName, serverURL, req.AutoMerge)
		// Preserve Phase 1 verification result.
		if cr.Verification != nil {
			adoptResult.Verification = cr.Verification
		}
		result.Results = append(result.Results, adoptResult)
	}

	return result, nil
}

// adoptSingleCluster performs the Git + ArgoCD secret operations for a single cluster.
//
// BUG-031: autoMergeOverride is the per-request auto-merge decision (nil =
// fall back to o.gitops.PRAutoMerge). Plumbed into commitChangesWithMeta
// via PRMetadata.AutoMergeOverride — replaces the pre-BUG-031 pattern that
// mutated o.gitops.PRAutoMerge in-place (a race against concurrent ops on
// the shared connection-level config).
func (o *Orchestrator) adoptSingleCluster(ctx context.Context, name, serverURL string, autoMergeOverride *bool) AdoptClusterResult {
	cr := AdoptClusterResult{Name: name}

	// Generate values file (empty addons for adopted clusters — user can update later).
	addons := make(map[string]bool)
	if len(o.defaultAddons) > 0 {
		for k, v := range o.defaultAddons {
			addons[k] = v
		}
	}
	valuesContent := generateClusterValues(name, "", addons, nil)
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")

	// Build managed-clusters.yaml entry.
	clusterAddonsPath := o.paths.ManagedClusters
	if clusterAddonsPath == "" {
		clusterAddonsPath = "configuration/managed-clusters.yaml"
	}
	clusterAddonsData, err := o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
	if err != nil {
		slog.Info("managed-clusters.yaml not found, bootstrapping", "cluster", name)
		clusterAddonsData = []byte("clusters:\n")
	}

	clusterLabels := make(map[string]string, len(addons))
	for addon, enabled := range addons {
		if enabled {
			clusterLabels[addon] = "true"
		} else {
			clusterLabels[addon] = "false"
		}
	}

	updatedClusterAddons, addEntryErr := gitops.AddClusterEntry(clusterAddonsData, gitops.ClusterEntryInput{
		Name:   name,
		Labels: clusterLabels,
	})
	if addEntryErr != nil {
		slog.Error("failed to add cluster entry", "cluster", name, "error", addEntryErr)
	}

	files := map[string][]byte{
		valuesPath: valuesContent,
	}
	if updatedClusterAddons != nil {
		files[clusterAddonsPath] = updatedClusterAddons
	}

	// BUG-031: per-request auto-merge override flows through PRMetadata
	// rather than mutating o.gitops.PRAutoMerge (which would race against
	// concurrent ops on the shared connection-level config). nil here
	// means "fall back to the connection default" — preserves back-compat
	// for clients that don't send auto_merge.
	gitResult, gitErr := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("adopt cluster %s", name), PRMetadata{
		OperationCode:     "adopt-cluster",
		Cluster:           name,
		Title:             fmt.Sprintf("Adopt cluster %s", name),
		AutoMergeOverride: autoMergeOverride,
	})

	if gitErr != nil {
		if gitResult != nil {
			cr.Status = "partial"
			cr.Error = gitErr.Error()
			cr.Message = fmt.Sprintf("PR created but merge failed for cluster %s: %s", name, gitResult.PRUrl)
			cr.Git = gitResult
		} else {
			cr.Status = "failed"
			cr.Error = gitErr.Error()
			cr.Message = "Git commit failed during adoption"
		}
		return cr
	}
	cr.Git = gitResult

	// If the ArgoCD secret manager is available and PR was merged (or auto-merge),
	// set the adopted annotation on the ArgoCD cluster secret.
	if o.argoSecretManager != nil && gitResult.Merged {
		if annotErr := o.argoSecretManager.SetAnnotation(ctx, name, AnnotationAdopted, "true"); annotErr != nil {
			slog.Error("failed to set adopted annotation on ArgoCD secret — reconciler will retry",
				"cluster", name, "error", annotErr)
			cr.Status = "partial"
			cr.Error = annotErr.Error()
			cr.Message = "Adoption PR merged but failed to set adopted annotation on ArgoCD secret"
			return cr
		}
	}

	cr.Status = "success"
	if !gitResult.Merged {
		cr.Message = fmt.Sprintf("PR created — adopted annotation will be set after PR is merged: %s", gitResult.PRUrl)
	}
	return cr
}
