package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// AnnotationAdopted is the key used to mark a cluster as adopted by Sharko.
// Writers stamp ONLY this key. Kept in lockstep with
// internal/argosecrets.AnnotationAdopted (the orchestrator cannot import
// argosecrets — dependency boundary, see internal/orchestrator/orchestrator.go).
//
// Canonical spelling as of V2-cleanup-60.5 (L10): the V2-cleanup-59 rename
// landed "sharko.sharko.dev/adopted", a historical doubled "sharko." prefix.
// Zero adopters existed while that spelling was live, so this is the one
// and only chance to correct it for free.
const AnnotationAdopted = "sharko.dev/adopted"

// AnnotationAdoptedDoubledPrefixLegacy is the short-lived V2-cleanup-59
// canonical spelling ("sharko.sharko.dev/adopted"), superseded by
// AnnotationAdopted (L10, V2-cleanup-60.5). Only ever READ. Kept in
// lockstep with internal/argosecrets.AnnotationAdoptedDoubledPrefixLegacy.
const AnnotationAdoptedDoubledPrefixLegacy = "sharko.sharko.dev/adopted"

// AnnotationAdoptedLegacy is the pre-V2-cleanup-59 adopted key (sharko.io —
// a domain the project never owned). Only ever READ: clusters adopted before
// the group rename still carry it, and Unadopt must keep recognising them.
// Kept in lockstep with internal/argosecrets.AnnotationAdoptedLegacy.
const AnnotationAdoptedLegacy = "sharko.sharko.io/adopted"

// AdoptClusters orchestrates the two-phase adoption of existing ArgoCD clusters.
//
// Phase 1: Per-cluster verification — Stage1 connectivity test.
// Phase 2: Per-cluster atomic adoption — create values file, add to managed-clusters.yaml, PR.
//
// If the ArgoCD secret manager is available:
//   - Rejects clusters that have a managed-by label set to something other than "sharko" (FR-4.6).
//   - Sets the adopted annotation on the ArgoCD secret after PR merge.
func (o *Orchestrator) AdoptClusters(ctx context.Context, req AdoptClustersRequest) (*AdoptClustersResult, error) {
	log := logging.LoggerFromContext(ctx)
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
				log.Warn("could not read managed-by label — proceeding with adoption",
					"cluster", clusterName, "error", labelErr)
			} else if managedBy != "" && managedBy != "sharko" {
				cr.Status = "failed"
				cr.Error = fmt.Sprintf("cluster %q is managed by %q, not sharko — cannot adopt", clusterName, managedBy)
				result.Results = append(result.Results, cr)
				continue
			}
		}

		// Phase 1: Verification (Stage1 connectivity test).
		//
		// Credentials are OPTIONAL for adoption, same as registration
		// (V2-cleanup-88.3 — lazy credentials): an adopted cluster already
		// has a working ArgoCD cluster Secret created out-of-band — that is
		// the whole point of Adopt, and adoptSingleCluster below records
		// connectionManagedBy: user for exactly that reason. A failed
		// credential lookup is therefore the NORMAL case, not a fatal one —
		// skip verification instead of failing the adoption. Sharko will
		// ask for credentials later if a secret-bearing addon is enabled on
		// this cluster.
		if o.credProvider != nil && o.remoteClientFn != nil {
			// Resolve the stored secretPath override (if any) — a re-adopted
			// cluster may already have a record with one (V2-cleanup-55.1).
			creds, credErr := o.fetchClusterCredentials(ctx, clusterName)
			if credErr != nil {
				log.Info("adopt: no credentials available — skipping connectivity verification",
					"cluster", clusterName, "error", credErr)
			} else if creds.Raw != nil {
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
					cr.Error = fmt.Sprintf("connectivity verification failed: %s",
						verify.FriendlyMessage(verifyResult))
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
			log.Info("Found existing open PR for cluster adoption — skipping",
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
		// AutoMerge is *bool; nil means "fall back to connection default".
		adoptResult := o.adoptSingleCluster(ctx, clusterName, serverURL, req.AutoMerge)
		// Preserve Phase 1 verification result.
		if cr.Verification != nil {
			adoptResult.Verification = cr.Verification
		}
		result.Results = append(result.Results, adoptResult)
	}

	return result, nil
}

// adoptSingleCluster performs the Git + ArgoCD secret operations for a
// single cluster. autoMergeOverride is the per-request auto-merge
// decision (nil = fall back to o.gitops.PRAutoMerge). Plumbed into
// commitChangesWithMeta via PRMetadata.AutoMergeOverride — never mutates
// o.gitops.PRAutoMerge, which would race against concurrent ops on the
// shared connection-level config.
func (o *Orchestrator) adoptSingleCluster(ctx context.Context, name, serverURL string, autoMergeOverride *bool) AdoptClusterResult {
	log := logging.LoggerFromContext(ctx)
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
		log.Info("managed-clusters.yaml not found, bootstrapping", "cluster", name)
		clusterAddonsData = []byte("clusters:\n")
	}

	clusterLabels := make(map[string]string, len(addons))
	for addon, enabled := range addons {
		clusterLabels[addon] = models.AddonLabelValue(enabled)
	}

	// Adopted clusters default to a SELF-MANAGED connection (V2-cleanup-57.2):
	// they already have a user-created ArgoCD cluster Secret — that is the
	// whole point of Adopt. Recording connectionManagedBy: user means the
	// reconcilers will only ever sync addon labels onto that Secret and
	// never rewrite or rotate its credential material, and the remove flow
	// will leave the Secret in place. This kills the "Sharko took over my
	// connection" failure mode at the source-of-truth level instead of
	// relying solely on the adopted annotation living on the Secret.
	updatedClusterAddons, addEntryErr := gitops.AddClusterEntry(clusterAddonsData, gitops.ClusterEntryInput{
		Name:                name,
		Labels:              clusterLabels,
		ConnectionManagedBy: models.ConnectionManagedByUser,
	})
	if addEntryErr != nil {
		log.Error("failed to add cluster entry", "cluster", name, "error", addEntryErr)
	}

	files := map[string][]byte{
		valuesPath: valuesContent,
	}
	if updatedClusterAddons != nil {
		files[clusterAddonsPath] = updatedClusterAddons
	}

	// Per-request auto-merge override flows through PRMetadata rather than
	// mutating o.gitops.PRAutoMerge (which would race against concurrent
	// ops on the shared connection-level config). nil here means "fall
	// back to the connection default".
	gitResult, gitErr := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("adopt cluster %s", name),
		o.prMeta(autoMergeOverride, "adopt-cluster", fmt.Sprintf("Adopt cluster %s", name), name, ""))

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
			log.Error("failed to set adopted annotation on ArgoCD secret — reconciler will retry",
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
