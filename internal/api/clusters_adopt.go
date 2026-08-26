package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleAdoptClusters godoc
//
// @Summary Adopt existing ArgoCD clusters
// @Description Adopts one or more existing ArgoCD clusters under Sharko management.
// @Description Phase 1 verifies connectivity per cluster, Phase 2 creates GitOps config via PR.
// @Description On a v4 repo, phase 1 is replaced per cluster by the same takeover preflight the brownfield-takeover door runs: a cluster the preflight blocks fails with the preflight's summary as its error (the batch continues with the rest); a cluster it only warns about proceeds, and the warnings are listed on that cluster's result. The write is the same two v4 files a takeover writes (managed-clusters.yaml plus an empty cluster-addons/{name}.yaml), followed by the ArgoCD connection ownership swap and the adopted annotation once the pull request merges — no separate confirmation step beyond dry_run.
// @Description CLIENTS MUST INSPECT THE RESPONSE BODY. HTTP 200 does NOT mean every cluster was adopted: an adoption in which every single cluster failed also answers 200, and so does one in which every cluster stopped part-way. Read `outcome` — `completed`, `partly_completed` and `failed` — and treat anything other than `completed == total` as work that did not finish. A `partly_completed` cluster was NOT rolled back, so real changes may already be in Git and on the cluster.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AdoptClustersRequest true "Adoption request"
// @Success 200 {object} orchestrator.AdoptClustersResult "Adoption processed. Read outcome/results — 200 does NOT mean every cluster was adopted."
// @Success 207 {object} orchestrator.AdoptClustersResult "Some clusters were adopted and some failed outright. Read outcome/results."
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 409 {object} map[string]interface{} "The repo carries both the old and the new layout (code repo_layout)"
// @Router /clusters/adopt [post]
func (s *Server) handleAdoptClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.adopt") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	// Validate body BEFORE any upstream call.
	var req orchestrator.AdoptClustersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, "at least one cluster name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if len(s.defaultAddons) > 0 {
		orch.SetDefaultAddons(s.defaultAddons)
	}
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}
	// On a v4 repo, AdoptClusters runs the takeover preflight per cluster,
	// which reads the ApplicationSets — same wiring migration.go uses for
	// the runtime handoff. nil is a valid, commonly-hit state (out-of-cluster
	// / no ApplicationSet access); the preflight then reports "could not
	// check" instead of claiming they are fine.
	orch.SetApplicationSetManager(s.appSetManager)

	result, err := orch.AdoptClusters(r.Context(), req)
	if err != nil {
		// A v4 repo is now a supported adopt door (v4-coherence-closure lane
		// D) — this stays only as a defensive mapping, matching the coded
		// shape the rest of the repo-layout family uses (CodeRepoLayout,
		// catalog_org.go).
		if orchestrator.IsV4RepoUnsupported(err) {
			writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger canonical reconciler after adoption.
	if !req.DryRun && s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	// The accurate counts go out in the body. AdoptClusters fills them in
	// already; recounting here means the wire is right even if a future
	// path builds a result some other way.
	result.Summarize()

	auditResult, changes := adoptAuditOutcome(result, req.DryRun)
	audit.Enrich(r.Context(), audit.Fields{
		Event:    lifecycleevents.ClusterAdopted,
		Resource: adoptAuditResource(result),
		Result:   auditResult,
		Changes:  changes,
	})

	writeJSON(w, adoptHTTPStatus(result), result)
}

// adoptAuditOutcome says what the adoption really did, for the audit trail.
//
// RULING (f), C8 fixed one half of this: the audit result stopped being
// derived from the HTTP status, so an adoption where EVERY cluster failed no
// longer reads as "Cluster adopted · success".
//
// R2-8 fixes the other half, which is worse, because it claims success rather
// than failure. A cluster that comes back "partial" had its pull request
// opened or merged and its ArgoCD connection half swapped, and nothing was
// rolled back — but the fold that decided the audit line only ever asked "did
// this one fail?", so every partial landed on the success side. An adoption
// where EVERY cluster stopped halfway was written into the audit trail as
// "Cluster adopted · success · changes applied": full completion claimed for
// work that did not complete. False success is the direction that hides a
// problem, and the audit trail is the thing an operator opens to find out
// what happened.
//
// The rule is shared with batch registration — see fanoutAuditOutcome.
//
// A dry run is the one override. It plans and writes nothing, so whatever the
// per-cluster answers are, no change was applied and none was going to be:
// the change answer is not_applicable. Before this, a dry run was recorded as
// "Cluster adopted · success · changes applied" — a preview claiming a write.
//
// The HTTP status is NOT decided here and is deliberately unchanged — see
// adoptHTTPStatus.
func adoptAuditOutcome(result *orchestrator.AdoptClustersResult, dryRun bool) (string, audit.ChangeResult) {
	// Summarize recounts from the per-cluster answers, so the audit trail
	// and the counts that go out in the response body are the same count —
	// made once, by fanout.Count, and read by both.
	outcome, changes := fanoutAuditOutcome(fanoutCountsFrom(result.Summarize()))
	if dryRun {
		return outcome, audit.ChangesNotApplicable
	}
	return outcome, changes
}

// adoptAuditResource names what the audit entry is about: the clusters the
// adoption actually touched. A partial counts as touched — its pull request
// exists — which is why it is named here and why the outcome above must say
// "partial" rather than "success".
func adoptAuditResource(result *orchestrator.AdoptClustersResult) string {
	touched := []string{}
	for _, cr := range result.Results {
		if cr.Status == "success" || cr.Status == "partial" {
			touched = append(touched, cr.Name)
		}
	}
	switch len(touched) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("cluster:%s", touched[0])
	default:
		return fmt.Sprintf("clusters:%d", len(touched))
	}
}

// adoptHTTPStatus is the response code for an adoption.
//
// POST /api/v1/clusters/adopt is a stable endpoint, so this is EXACTLY what
// it has always been: 207 only when at least one cluster failed AND at least
// one did not. Everything else — including an adoption where every cluster
// failed, and one where every cluster came back partial — answers 200.
//
// That is inconsistent with batch registration, which answers 207 for any
// cluster that did not fully succeed. The inconsistency is real and is an
// open product-owner question; changing it is a major version bump and was
// NOT this story's to make. R2-8 corrected the audit trail and nothing else.
func adoptHTTPStatus(result *orchestrator.AdoptClustersResult) int {
	hasFailure := false
	hasSuccess := false
	for _, cr := range result.Results {
		if cr.Status == "failed" {
			hasFailure = true
		} else {
			hasSuccess = true
		}
	}
	if hasFailure && hasSuccess {
		return http.StatusMultiStatus
	}
	return http.StatusOK
}

// handleUnadoptCluster godoc
//
// @Summary Un-adopt a cluster
// @Description Reverses adoption of a cluster — removes Sharko management but keeps the ArgoCD secret.
// @Description The cluster must have been adopted (has sharko.sharko.dev/adopted annotation).
// @Description On a v4 repo, the pull request removes the cluster from managed-clusters.yaml and deletes cluster-addons/{name}.yaml plus every Helm values file under values/clusters/{name}/ — everything this cluster owns in the repo.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body orchestrator.UnadoptClusterRequest true "Unadopt request (requires yes: true)"
// @Success 200 {object} orchestrator.UnadoptClusterResult "Cluster unadopted"
// @Success 207 {object} orchestrator.UnadoptClusterResult "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 409 {object} map[string]interface{} "Cluster was not adopted, or the repo carries both the old and the new layout (code repo_layout)"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/unadopt [post]
func (s *Server) handleUnadoptCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.unadopt") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	// Decode + validate body BEFORE upstream call.
	var req orchestrator.UnadoptClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Require confirmation unless dry-run.
	if !req.DryRun && !req.Yes {
		writeError(w, http.StatusBadRequest, "confirmation required: set yes: true in request body")
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	result, err := orch.UnadoptCluster(r.Context(), name, req)
	if err != nil {
		// A v4 repo is now a supported unadopt door (v4-coherence-closure
		// lane D) — kept as a defensive mapping, coded shape like the rest
		// of the repo-layout family.
		if orchestrator.IsV4RepoUnsupported(err) {
			writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
			return
		}
		// Check if this is a "not adopted" error.
		if contains(err.Error(), "was not adopted") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger canonical reconciler.
	if !req.DryRun && s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_unadopted",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// contains is a simple string contains helper to avoid importing strings in this file.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
