package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// UnadoptCluster reverses a cluster adoption:
//  1. Check that the cluster has the adopted annotation (error if not).
//  2. Create PR to remove the cluster from Sharko's records (step 4 below).
//  3. ONLY ONCE THAT PR IS MERGED (EP3-DRYRUN-FIXES call 9): remove the
//     managed-by label, the adopted annotation and the take-over marks
//     from the ArgoCD secret (keep secret), then delete Sharko-created
//     addon secrets from the remote cluster. If the PR merged at once this
//     runs now; else the PR tracker's merge hook runs it later.
//
// Steps 1 and 3 are layout-agnostic and run unchanged on any repo. Step 4
// diverges: a v3 repo removes the entry from
// configuration/managed-clusters.yaml and deletes the combined per-cluster
// values file (unchanged v3 behaviour, below); a v4 repo removes the entry
// from managed-clusters.yaml and deletes cluster-addons/<name>.yaml plus
// every Helm values file under values/clusters/<name>/ (unadopt_v4.go,
// v4-coherence-closure lane D) — writing the v3 file on a v4 repo would
// leave a rival registry behind (ErrV4RepoUnsupported).
func (o *Orchestrator) UnadoptCluster(ctx context.Context, name string, req UnadoptClusterRequest) (*UnadoptClusterResult, error) {
	log := logging.LoggerFromContext(ctx)
	if name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	// Fail closed: everything from here can write, and answering "not v4"
	// wrongly recreates configuration/managed-clusters.yaml on a v4 repo —
	// the second-registry hijack v4_guard.go describes. Same stance as
	// AdoptClusters' and RegisterCluster's v4 probes.
	v4Repo, v4ProbeErr := o.isV4Repo(ctx)
	if v4ProbeErr != nil {
		return nil, fmt.Errorf("Sharko stopped before un-adopting a cluster: %w", v4ProbeErr)
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

	// ─── v4: compute what the Git write removes, layout-specific ──────────
	var v4Plan *unadoptV4Plan

	// ─── v3: compute what the Git write removes, layout-specific ──────────
	var valuesPath, clusterAddonsPath string
	var clusterAddonsData, updatedClusterAddons []byte

	if v4Repo {
		var planErr error
		v4Plan, planErr = o.buildUnadoptV4Plan(ctx, name)
		if planErr != nil {
			return nil, planErr
		}
	} else {
		valuesPath = path.Join(o.paths.ClusterValues, name+".yaml")
		clusterAddonsPath = o.paths.ManagedClusters
		if clusterAddonsPath == "" {
			clusterAddonsPath = "configuration/managed-clusters.yaml"
		}

		// Generate file content (shared between dry-run and real path).
		var err error
		clusterAddonsData, err = o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
		if err != nil && !req.DryRun {
			log.Warn("managed-clusters.yaml not found — skipping removal", "cluster", name)
		}

		if clusterAddonsData != nil {
			var removeErr error
			updatedClusterAddons, removeErr = gitops.RemoveClusterEntry(clusterAddonsData, name)
			if removeErr != nil && !req.DryRun {
				log.Warn("failed to remove cluster entry from managed-clusters.yaml",
					"cluster", name, "error", removeErr)
				updatedClusterAddons = nil
			}
		}
	}

	// Dry-run exit point.
	if req.DryRun {
		var filePreviews []FilePreview
		if v4Repo {
			filePreviews = o.unadoptV4FilePreviews(ctx, v4Plan)
		} else {
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
		}

		prTitle := fmt.Sprintf("%s unadopt cluster %s", o.gitops.CommitPrefix, name)
		result.Status = "success"
		result.DryRun = &DryRunResult{
			FilesToWrite: filePreviews,
			PRTitle:      prTitle,
		}
		return result, nil
	}

	// Where the cluster's credentials live, read NOW from the pre-mutation
	// registry bytes: the PR below removes the cluster's entry, and the
	// addon secrets on the cluster are removed only after it merges
	// (V2-cleanup-55.1 secretPath override, EP3-DRYRUN-FIXES call 9).
	var registryData []byte
	if v4Repo {
		registryData = v4Plan.managedClustersData
	} else {
		registryData = clusterAddonsData
	}
	credLookupKey, credsSource, credRoleARN := config.ResolveCredentialRoutingFromData(registryData, name)
	followUp := ClusterFollowUp{
		KeepConnection:    true,
		CredentialKey:     credLookupKey,
		CredentialsSource: credsSource,
		CredentialRoleARN: credRoleARN,
	}

	// Step 4: Create PR removing the cluster from Sharko's records. Reuse
	// whichever layout-specific plan was computed above.
	var files map[string][]byte
	var deletePaths []string

	if v4Repo {
		if v4Plan.updatedManagedClusters != nil {
			files = map[string][]byte{V4ManagedClustersPath: v4Plan.updatedManagedClusters}
		}
		deletePaths = v4Plan.deletePaths
	} else {
		if updatedClusterAddons != nil {
			files = map[string][]byte{clusterAddonsPath: updatedClusterAddons}
		}
		deletePaths = append(deletePaths, valuesPath)
	}

	meta := o.prMeta(req.AutoMerge, "unadopt-cluster", fmt.Sprintf("Unadopt cluster %s", name), name, "")
	fu := followUp
	meta.FollowUp = &fu
	gitResult, gitErr := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("unadopt cluster %s", name), meta)
	if gitErr != nil {
		if gitResult != nil {
			result.Status = "partial"
			result.Error = gitErr.Error()
			result.Message = fmt.Sprintf("PR created but merge failed: %s. Nothing on the cluster's ArgoCD connection was changed — Sharko finishes the un-adopt after the PR is merged.", gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		result.Status = "failed"
		result.Error = gitErr.Error()
		result.Message = UnadoptGitFailedMessage
		return result, nil
	}

	result.Git = gitResult

	// NOTHING CHANGES ON THE CONNECTION BEFORE THE PR IS MERGED
	// (EP3-DRYRUN-FIXES call 9, same rule as call 1). While the PR waits,
	// the Secret keeps its label and its adopted mark — the orphan sweep
	// never deletes an adopted Secret, so it is safe. The PR tracker's
	// merge hook runs FinishUnadoptAfterMerge once a person merges it.
	if !gitResult.Merged {
		result.Status = "success"
		result.Message = UnadoptWaitingForMergeMessage(name, gitResult.PRUrl)
		return result, nil
	}

	if err := o.finishUnadopt(ctx, name, followUp); err != nil {
		result.Status = "partial"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("The pull request was merged, but Sharko could not take its marks off the ArgoCD cluster Secret for %s: %s", name, err.Error())
		return result, nil
	}
	result.Status = "success"
	return result, nil
}

// UnadoptGitFailedMessage is the message when the un-adopt pull request
// could not be opened. Nothing was changed anywhere at that point.
const UnadoptGitFailedMessage = "Git commit failed during unadoption — nothing was changed on the cluster's ArgoCD connection"

// UnadoptWaitingForMergeMessage is the message an un-adopt answers with
// while its pull request waits to be merged. Exported so tests pin it by
// exact text.
func UnadoptWaitingForMergeMessage(name, prURL string) string {
	return fmt.Sprintf("Pull request opened, waiting for merge: %s. Nothing has changed on cluster %s yet — Sharko takes its marks off the ArgoCD cluster Secret after the pull request is merged.", prURL, name)
}

// FinishUnadoptAfterMerge does the ArgoCD side of an un-adopt whose pull
// request was merged after UnadoptCluster returned. The PR tracker's merge
// hook calls it (internal/api). The caller must have checked that the
// cluster is not back in Git. Safe to run twice.
func (o *Orchestrator) FinishUnadoptAfterMerge(ctx context.Context, name string, followUp ClusterFollowUp) error {
	return o.finishUnadopt(ctx, name, followUp)
}

// finishUnadopt is the after-merge half of an un-adopt: take Sharko's
// label, the adopted mark and the take-over marks off the ArgoCD cluster
// Secret (the Secret itself stays), then remove the addon secrets Sharko
// put on the cluster (best-effort).
func (o *Orchestrator) finishUnadopt(ctx context.Context, name string, followUp ClusterFollowUp) error {
	log := logging.LoggerFromContext(ctx)
	if o.argoSecretManager != nil {
		if err := o.argoSecretManager.Unadopt(ctx, name); err != nil {
			return fmt.Errorf("removing managed-by label from ArgoCD secret for cluster %q: %w", name, err)
		}
		log.Info("ArgoCD secret unadopted — labels removed", "cluster", name)
	}

	if o.credProvider != nil {
		lookupKey := followUp.CredentialKey
		if lookupKey == "" {
			lookupKey = name
		}
		var creds *providers.Kubeconfig
		var credErr error
		if o.credsRouter != nil {
			creds, credErr = o.credsRouter.Fetch(name, lookupKey, followUp.CredentialsSource, followUp.CredentialRoleARN)
		} else {
			creds, credErr = providers.GetCredentialsWithOptionalRole(o.credProvider, lookupKey, followUp.CredentialRoleARN)
		}
		if credErr == nil && creds != nil {
			o.deleteAllAddonSecrets(ctx, name, creds.Raw) // best-effort
		} else {
			log.Warn("could not fetch credentials for remote secret cleanup", "cluster", name, "error", credErr)
		}
	}
	return nil
}
