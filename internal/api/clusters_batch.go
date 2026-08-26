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

// handleBatchRegisterClusters godoc
//
// @Summary Batch register clusters
// @Description Registers multiple clusters in a single operation.
// @Description CLIENTS MUST INSPECT THE RESPONSE BODY. The HTTP status says whether Sharko accepted and processed the batch, not what happened to the individual clusters. Read `outcome` — `completed`, `partly_completed` and `failed` — and treat anything other than `completed == total` as work that did not finish. The older top-level `failed` counter has a different, deliberately unchanged meaning: it counts every cluster that did not FULLY succeed, partials included, and it is what the 207 is derived from. A `partly_completed` cluster was NOT rolled back, so real changes may already be in Git and on the cluster.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Batch registration request with clusters array"
// @Success 200 {object} orchestrator.BatchResult "Batch processed. Read outcome/results — a 200 only means Sharko accepted and processed the request."
// @Success 207 {object} orchestrator.BatchResult "At least one cluster did not fully succeed. Read outcome/results."
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/batch [post]
// handleBatchRegisterClusters handles POST /api/v1/clusters/batch — register multiple clusters.
//
// Credentials are OPTIONAL for every cluster in the batch, same as single
// registration (V2-cleanup-88.3 — lazy credentials): this handler no longer
// requires a configured secrets provider up front. Each cluster degrades to
// a connection-only registration when Sharko has no credentials for it.
func (s *Server) handleBatchRegisterClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.register") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	var req struct {
		Clusters []orchestrator.RegisterClusterRequest `json:"clusters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, "at least one cluster is required")
		return
	}
	if len(req.Clusters) > orchestrator.MaxBatchSize {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size exceeds maximum of %d clusters", orchestrator.MaxBatchSize))
		return
	}

	// Validate all cluster names before processing.
	for _, c := range req.Clusters {
		if c.Name == "" {
			writeError(w, http.StatusBadRequest, "cluster name is required for all entries")
			return
		}
		if !validClusterNameRe.MatchString(c.Name) {
			writeError(w, http.StatusBadRequest, "invalid cluster name "+c.Name+": must be alphanumeric with hyphens, starting with alphanumeric")
			return
		}
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
	orch.SetAllowInlineCredentialsFn(s.settingsStore.IsInlineCredentialsAllowed)
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

	result := orch.RegisterClusterBatch(r.Context(), req.Clusters)

	auditResult, changes := batchAuditOutcome(result)
	audit.Enrich(r.Context(), audit.Fields{
		Event:    lifecycleevents.ClusterRegistered,
		Resource: fmt.Sprintf("clusters:%d", len(req.Clusters)),
		Detail:   fmt.Sprintf("batch of %d", len(req.Clusters)),
		Result:   auditResult,
		Changes:  changes,
	})

	writeJSON(w, batchHTTPStatus(result), result)
}

// batchAuditOutcome says what the batch really did, for the audit trail.
//
// RULING (f), C9: the audit result says what happened, not what the status
// code implies. A batch where EVERY registration failed returned 207 and was
// recorded as "partial" — partial success, with no success in it.
//
// R2-7 fixes the mirror image of that, which was worse. A cluster that comes
// back "partial" had its pull request merged and its Secrets written, and
// nothing was rolled back — real things changed. The orchestrator had nowhere
// to put a partial except the Failed counter, so a batch where every cluster
// came back partial satisfied "everything failed" and was written into the
// audit trail as failure with no changes: "it all failed, nothing changed",
// when in fact a pull request had merged. An operator reading the audit trail
// to work out what happened was told the opposite of the truth.
//
// The rule itself is fanoutAuditOutcome — see that function for the four
// cases and for why a cluster that stopped part-way gets "changes may have
// been applied" rather than either certainty.
//
// The HTTP status is not decided here and is deliberately unchanged — see
// batchHTTPStatus.
//
// R2-8: the rule moved to fanoutAuditOutcome because the adopt handler needed
// the same answer and had written its own version of it — wrong in the
// opposite direction. This function is now only the counting.
//
// R2-9: the counting now reads the per-cluster answers through fanout.Count
// rather than the older Succeeded/Failed/Partial counters. Those counters put
// any status they did not recognise on the success side; the count that goes
// out on the wire, into the audit trail and into the CLI's exit code puts it
// in its own bucket, where it cannot be read as a clean completion.
func batchAuditOutcome(result *orchestrator.BatchResult) (string, audit.ChangeResult) {
	// Counted from the per-cluster answers rather than from the older
	// counters, so a cluster status this build does not recognise lands in
	// the Unrecognized bucket instead of being read as a clean success. The
	// same count goes out in the response body and drives the CLI's exit
	// code — one count, every surface.
	return fanoutAuditOutcome(fanoutCountsFrom(result.Summarize()))
}

// batchHTTPStatus is the response code for a batch registration.
//
// POST /api/v1/clusters/batch is a stable endpoint, so this is EXACTLY what
// it has always been: any cluster that did not fully succeed — a hard failure
// or a partial — makes the batch a 207. Changing it would be a major version
// bump and is not this story's to make.
func batchHTTPStatus(result *orchestrator.BatchResult) int {
	if result.Failed > 0 {
		return http.StatusMultiStatus
	}
	return http.StatusOK
}
