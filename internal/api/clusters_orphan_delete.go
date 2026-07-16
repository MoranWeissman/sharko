package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// handleDeleteOrphanCluster godoc
//
// @Summary Delete an orphan cluster
// @Description Deletes an ArgoCD cluster Secret for a cluster that has no managed-clusters.yaml entry and no open registration PR. Refuses to delete a cluster that is genuinely managed (in git), pending (has an open register PR), or NOT owned by Sharko (missing the app.kubernetes.io/managed-by=sharko label) — externally-owned Secrets are Adopt territory.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 204 "Cluster Secret deleted"
// @Failure 400 {object} map[string]interface{} "Cluster is not orphaned (managed, pending, or not Sharko-owned)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "Cluster Secret not found in ArgoCD"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "K8s client not wired — ownership label cannot be verified"
// @Router /clusters/{name}/orphan [delete]
//
// Orphan-cluster recovery surface. The orphan resolver
// (resolveOrphanRegistrations) detects ArgoCD cluster Secrets with no
// matching managed-clusters.yaml entry and no open registration PR.
//
// SAFETY: this endpoint refuses to delete a cluster Secret unless it is
// genuinely an orphan — the same name must be (1) present in ArgoCD,
// (2) NOT in managed-clusters.yaml, and (3) NOT in pending_registrations.
// A user-initiated DELETE on a managed or pending cluster gets a 400
// with a remediation hint. This guard is the difference between a
// recovery action and a foot-gun.
func (s *Server) handleDeleteOrphanCluster(w http.ResponseWriter, r *http.Request) {
	// Reuse the cluster.remove permission — deleting a cluster Secret in
	// ArgoCD is no less destructive than deregistering a managed cluster.
	// The safety net is the orphan-only validation below.
	if !authz.RequireWithResponse(w, r, "cluster.remove") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}
	// Defensive: buildArgocdClient should never return (nil, nil) but guard
	// against future refactors so this code path never reaches ac.ListClusters
	// with a nil receiver and produces an opaque 500 instead of a clear message.
	if ac == nil {
		writeServerError(w, http.StatusBadGateway, "delete_orphan_cluster_no_argocd",
			fmt.Errorf("GetActiveArgocdClient returned nil client without error"))
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}
	// Defensive: same guard for the Git provider — a nil provider would
	// panic inside resolvePendingRegistrations and ListClusters.
	if gp == nil {
		writeServerError(w, http.StatusBadGateway, "delete_orphan_cluster_no_git",
			fmt.Errorf("GetActiveGitProvider returned nil provider without error"))
		return
	}

	// Fetch the current cluster picture so we can determine orphan status.
	// This MUST mirror the same data sources as /clusters: ArgoCD list +
	// managed-clusters.yaml + pending-registration PRs. Otherwise a TOCTOU
	// race could let the user delete a cluster that was managed at /clusters
	// fetch time but flipped to "managed" between fetch and delete.
	resp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
	if err != nil {
		writeUpstreamError(w, "delete_orphan_cluster_list", err)
		return
	}
	// Defensive: ListClusters should always return a non-nil response on
	// success (the service layer guarantees this), but guard against
	// unexpected refactors so a nil deref never becomes a bare 500.
	if resp == nil {
		writeServerError(w, http.StatusInternalServerError, "delete_orphan_cluster_list",
			fmt.Errorf("ListClusters returned nil response without error"))
		return
	}

	// Check #1: cluster must NOT be in managed-clusters.yaml.
	// The service-layer ListClusters response contains both managed and
	// not_in_git entries; managed clusters have Managed==true.
	var managed bool
	for _, c := range resp.Clusters {
		if c.Name == name && c.Managed {
			managed = true
			break
		}
	}
	if managed {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("cluster %q is managed (present in managed-clusters.yaml) — use DELETE /api/v1/clusters/%s instead", name, name))
		return
	}

	// Check #2: cluster must NOT have an open registration PR.
	pending := resolvePendingRegistrations(r.Context(), gp, s.gitopsConfig().CommitPrefix)
	for _, p := range pending {
		if p.ClusterName == name {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("cluster %q has an open registration PR (%s) — close the PR first, then retry orphan delete", name, p.PRURL))
			return
		}
	}

	// Check #3: cluster MUST be present in ArgoCD (otherwise nothing to
	// delete). Find its server URL while we're at it — DeleteCluster
	// addresses by URL, not by name.
	argoClusters, err := ac.ListClusters(r.Context())
	if err != nil {
		writeUpstreamError(w, "delete_orphan_cluster_argocd_list", err)
		return
	}
	var serverURL string
	for _, a := range argoClusters {
		if a.Name == name {
			// Defensive: never delete the in-cluster entry.
			if a.Name == "in-cluster" || strings.HasPrefix(a.Server, "https://kubernetes.default") {
				writeError(w, http.StatusBadRequest, "refusing to delete the in-cluster ArgoCD entry")
				return
			}
			serverURL = a.Server
			break
		}
	}
	if serverURL == "" {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("cluster %q not found in ArgoCD — nothing to delete", name))
		return
	}

	// Ownership-label gate (prevents Discard from destroying an
	// externally-created Secret). The Secret in the argocd namespace
	// MUST carry the app.kubernetes.io/managed-by=sharko label written
	// by the reconciler — otherwise it is Adopt territory and Discard
	// would silently destroy whatever tool (operator, GitOps, external
	// script) was supposed to own it.
	//
	// Failure modes:
	//   - k8s client not wired → 503; operator must restart Sharko with the
	//     in-cluster client configured (production always wires it via
	//     SetArgoReconcilerConfig in cmd/sharko/serve.go).
	//   - Secret disappeared between the ArgoCD check (step 6) and now →
	//     404. Race window is tiny but possible; ArgoCD's REST cache lags
	//     the underlying K8s API.
	//   - Secret lacks the sharko label → 400 with a remediation
	//     message pointing operators at the Adopt action.
	//   - K8s API error → 502 (upstream-classify so a transient blip reads
	//     as gateway error and not 500).
	//
	// On rejection we audit-log so the operator sees the rejection alongside
	// the success case (Event: cluster_orphan_delete_rejected). The success
	// case keeps its existing cluster_orphan_deleted event below.
	secret, secErr := s.getSecretIfPresent(r.Context(), name)
	if secErr != nil {
		if errors.Is(secErr, errNoK8sClient) {
			writeServerError(w, http.StatusServiceUnavailable, "delete_orphan_cluster_no_k8s_client", secErr)
			return
		}
		writeUpstreamError(w, "delete_orphan_cluster_get_secret", secErr)
		return
	}
	if secret == nil {
		// Race: ArgoCD said it existed at step 6, but K8s says it's gone
		// now. Idempotent: nothing to delete on the K8s side. Surface a 404
		// so the FE refreshes and the row drops off the orphan list.
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("cluster Secret %q no longer present in argocd namespace — nothing to delete (refresh and retry)", name))
		return
	}
	if !clusterreconciler.IsManagedBySharko(secret) {
		audit.Enrich(r.Context(), audit.Fields{
			Event:    "cluster_orphan_delete_rejected",
			Resource: fmt.Sprintf("cluster:%s", name),
		})
		writeError(w, http.StatusBadRequest,
			"this Secret was not created by Sharko (no managed-by label); refusing to delete. If you want to bring it under management, use the Adopt action.")
		return
	}

	// All safety checks passed — perform the delete.
	if err := ac.DeleteCluster(r.Context(), serverURL); err != nil {
		writeUpstreamError(w, "delete_orphan_cluster_argocd_delete", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_orphan_deleted",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

	w.WriteHeader(http.StatusNoContent)
}
