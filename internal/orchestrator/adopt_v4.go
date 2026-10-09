// Package orchestrator — adopting an already-ArgoCD-managed cluster on a v4
// repo (v4-coherence-closure, lane D).
//
// v3's AdoptClusters writes configuration/managed-clusters.yaml — on a v4
// repo that would create a rival registry file and orphan every
// v4-registered cluster (see ErrV4RepoUnsupported). This file is the v4
// replacement: the Git write is the same two files TakeoverClusterGit
// writes (buildTakeoverFiles, takeover.go), gated by the same takeover
// preflight the brownfield-takeover door runs — a cluster that is not
// Ready fails outright, one that is Ready-but-warned proceeds and carries
// the warnings back on the per-cluster result. Unlike the takeover door,
// there is no acknowledged_findings protocol here: the adopt door's
// existing confirmation contract (dry_run / not-dry_run) is what gates the
// write, matching v3 AdoptClusters, which never asked for a separate "yes"
// either.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// adoptLegacyLabelKey is the legacy-label classifier handed to the
// ownership swap: it decides which of the live Secret labels belonged to
// whoever had the connection before Sharko. Mirrors
// internal/api/clusters_takeover.go's takeoverLegacyLabelKey — duplicated
// rather than shared because the orchestrator cannot import internal/api
// (and must not import internal/argosecrets), but both wrap the same pure
// takeover.SharkoOwnedLabelKey predicate, so the two can never disagree
// about which labels are Sharko's.
func adoptLegacyLabelKey(key string) bool {
	if key == takeover.ArgoCDSecretTypeLabel {
		return false
	}
	return !takeover.SharkoOwnedLabelKey(key)
}

// v4RegisteredClusterNames reads managed-clusters.yaml and returns the
// cluster names in it, for the takeover preflight's name-collision check.
//
// A fleet record that is not there yet is an ordinary answer: no names, no
// error. Anything else — an unreadable file, a file that does not parse —
// comes back as an error, and the caller's preflight then reports "could
// not check" rather than "the name is free" (same reasoning as
// internal/api's registeredClusterNames, which this mirrors for the
// orchestrator side of the adopt door).
func (o *Orchestrator) v4RegisteredClusterNames(ctx context.Context) ([]string, error) {
	data, err := o.git.GetFileContent(ctx, V4ManagedClustersPath, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read Sharko's own list of clusters from %s: %w", V4ManagedClustersPath, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	spec, err := models.LoadManagedClusters(data)
	if err != nil {
		return nil, fmt.Errorf("could not make sense of Sharko's own list of clusters in %s: %w", V4ManagedClustersPath, err)
	}
	out := make([]string, 0, len(spec.Clusters))
	for _, c := range spec.Clusters {
		out = append(out, c.Name)
	}
	return out, nil
}

// gatherAdoptPreflightInputs assembles the takeover.Inputs snapshot for one
// cluster in AdoptClusters' v4 branch — the same four facts
// takeover.Preflight needs, gathered from what the orchestrator has direct
// access to rather than the API layer's connSvc/argocd.Service wiring
// (internal/api/clusters_takeover.go's gatherPreflightInputs is the
// handler-side twin; this is the orchestrator-side equivalent, duplicated
// rather than shared because the two layers reach their facts through
// different plumbing).
//
// Every source degrades in the direction that makes a person look rather
// than assume: a failure to read the ApplicationSets is reported as
// "Sharko could not check", never as "all clear".
func (o *Orchestrator) gatherAdoptPreflightInputs(ctx context.Context, clusterName, serverURL string) (takeover.Inputs, error) {
	in := takeover.Inputs{Cluster: clusterName}

	// (a) who owns the existing cluster Secret. Adopting on a v4 repo
	// requires the same in-cluster install the brownfield-takeover door
	// requires — without it there is no way to answer "is this cluster
	// ready", so the cluster fails rather than being adopted blind.
	if o.argoSecretManager == nil {
		return in, fmt.Errorf(
			"Sharko is not connected to the cluster it runs in, so it cannot check whether %q is ready to adopt — adopting on this repo needs an in-cluster install",
			clusterName)
	}
	detail, err := o.argoSecretManager.GetClusterSecretDetail(ctx, clusterName)
	if err != nil {
		return in, err
	}
	in.SecretFound = detail.Found
	in.Server = detail.Server
	in.SecretLabels = detail.Labels
	in.SecretAnnotations = detail.Annotations
	in.ManagedBy = detail.ManagedBy
	in.ForeignOwnerAppName = detail.ForeignOwnerAppName
	in.ForeignOwnerConfidence = detail.ForeignOwnerConfidence
	in.ForeignOwnerFound = detail.ForeignOwnerFound
	in.AlreadyTakenOver = detail.ManagedBy == "sharko"

	// (b) the ApplicationSets and how they behave on deletion.
	if o.appSets == nil {
		in.ApplicationSetsError = "Sharko has no read access to ApplicationSets in this install"
	} else if list, listErr := o.appSets.List(ctx); listErr != nil {
		in.ApplicationSetsError = listErr.Error()
	} else {
		in.ApplicationSets = list
	}

	// (c) the Applications pointed at this cluster. Matched the same way
	// argocd.Service.GetClusterApplications does (destination server, then
	// destination name, then the "-<cluster>" app-name suffix) — replicated
	// here rather than reused because that helper takes the concrete
	// *argocd.Client the orchestrator's ArgocdClient interface deliberately
	// does not expose. Best-effort: a listing failure leaves Applications
	// empty rather than failing the whole preflight, same as the API-layer
	// twin.
	if apps, appsErr := o.argocd.ListApplications(ctx); appsErr == nil {
		for _, app := range apps {
			if app.DestinationServer == serverURL || app.DestinationName == clusterName || strings.HasSuffix(app.Name, "-"+clusterName) {
				in.Applications = append(in.Applications, app.Name)
			}
		}
	}

	// (d) a name clash with a cluster Sharko already has.
	names, namesErr := o.v4RegisteredClusterNames(ctx)
	if namesErr != nil {
		in.RegisteredClustersError = namesErr.Error()
	}
	in.RegisteredClusters = names

	return in, nil
}

// adoptSingleClusterV4 is the v4-repo counterpart to adoptSingleCluster:
// runs the takeover preflight, then (dry-run) previews or (real) writes the
// same two v4 files a takeover writes, then swaps ArgoCD Secret ownership
// and stamps the v3-style adopted annotation — gated on the adoption PR
// having actually merged, exactly as adoptSingleCluster gates its Ensure +
// SetAnnotation calls today.
func (o *Orchestrator) adoptSingleClusterV4(ctx context.Context, name, serverURL string, req AdoptClustersRequest, extraWarnings []string) AdoptClusterResult {
	log := logging.LoggerFromContext(ctx)
	cr := AdoptClusterResult{Name: name}
	cr.Warnings = append(cr.Warnings, extraWarnings...)

	in, err := o.gatherAdoptPreflightInputs(ctx, name, serverURL)
	if err != nil {
		cr.Status = "failed"
		cr.Error = err.Error()
		return cr
	}
	report := takeover.Preflight(in)
	if !report.Ready {
		cr.Status = "failed"
		cr.Error = report.Summary
		return cr
	}
	for _, f := range report.Findings {
		if f.Status == takeover.StatusWarning {
			cr.Warnings = append(cr.Warnings, fmt.Sprintf("%s: %s", f.Title, f.Detail))
		}
	}

	build, err := o.buildTakeoverFiles(ctx, name, "")
	if err != nil {
		cr.Status = "failed"
		cr.Error = err.Error()
		return cr
	}

	if req.DryRun {
		cr.Status = "success"
		cr.DryRun = &DryRunResult{
			EffectiveAddons: []string{},
			FilesToWrite:    o.takeoverFilePreviews(build),
			PRTitle:         fmt.Sprintf("%s adopt cluster %s", o.gitops.CommitPrefix, name),
			SecretsToCreate: []string{},
		}
		return cr
	}

	// Idempotency (mirrors adoptSingleCluster / RegisterCluster): a previous
	// attempt may have left an open adopt PR for this cluster.
	existingPR, existingPRErr := o.findOpenPRForCluster(ctx, name, "adopt")
	if existingPRErr == nil && existingPR != nil {
		log.Info("Found existing open PR for v4 cluster adoption — skipping",
			"cluster", name, "pr", existingPR.URL)
		cr.Status = "success"
		cr.Git = &GitResult{PRUrl: existingPR.URL, PRID: existingPR.ID, Branch: existingPR.SourceBranch}
		cr.Message = "Existing open PR found — skipped PR creation (idempotent retry)"
		return cr
	}

	files := build.files()
	gitResult, gitErr := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("adopt cluster %s", name),
		o.prMeta(req.AutoMerge, "adopt-cluster", fmt.Sprintf("Adopt cluster %s", name), name, ""))
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

	// The ownership swap and the adopted annotation both wait for the PR to
	// merge — same gate adoptSingleCluster uses for Ensure + SetAnnotation.
	// Swapping ArgoCD's Secret ownership before the fleet record (the
	// source of truth) has actually landed would let a merge failure leave
	// Sharko owning a connection its own records don't yet know about.
	if o.argoSecretManager != nil && gitResult.Merged {
		swap, swapErr := o.argoSecretManager.TakeOverClusterSecret(
			ctx, name, true, time.Now().UTC().Format(time.RFC3339), adoptLegacyLabelKey)
		if swapErr != nil {
			log.Error("failed to take ownership of the ArgoCD cluster secret during v4 adoption",
				"cluster", name, "error", swapErr)
			cr.Status = "partial"
			cr.Error = swapErr.Error()
			cr.Message = "Adoption PR merged but Sharko could not take ownership of the ArgoCD connection. Nothing was deleted; fix the problem and adopt again — it is safe to repeat."
			return cr
		}
		if !swap.Found {
			cr.Status = "partial"
			cr.Message = fmt.Sprintf(
				"Adoption PR merged, but %s's ArgoCD connection disappeared between the check and the swap. Check the cluster still exists in ArgoCD, then adopt again.", name)
			return cr
		}
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
		cr.Message = fmt.Sprintf("PR created — ArgoCD connection ownership and the adopted annotation will be set after the PR is merged: %s", gitResult.PRUrl)
	}
	return cr
}
