package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// handleDeleteOrphanCluster godoc
//
// @Summary Delete an orphan cluster
// @Description Deletes an ArgoCD cluster Secret for a cluster that has no managed-clusters.yaml entry and no open registration PR. Refuses to delete a cluster that is genuinely managed (in git) or pending (has an open register PR) — those are not orphans.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 204 "Cluster Secret deleted"
// @Failure 400 {object} map[string]interface{} "Cluster is not orphaned (managed or pending)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "Cluster Secret not found in ArgoCD"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/orphan [delete]
//
// V125-1-7 / BUG-058 — orphan-cluster recovery surface. The orphan resolver
// (resolveOrphanRegistrations) detects ArgoCD cluster Secrets with no
// matching managed-clusters.yaml entry and no open registration PR — these
// are typically left behind when a manual-mode register PR was closed
// without merging (the orchestrator pre-creates the Secret in
// internal/orchestrator/cluster.go:408 before the PR opens).
//
// SAFETY: this endpoint refuses to delete a cluster Secret unless it is
// genuinely an orphan — the same name must be (1) present in ArgoCD,
// (2) NOT in managed-clusters.yaml, and (3) NOT in pending_registrations.
// A user-initiated DELETE on a managed or pending cluster gets a 400 with
// a remediation hint ("use DELETE /api/v1/clusters/{name}" for managed;
// "close the registration PR first" for pending). This guard is the
// difference between a recovery action and a foot-gun.
//
// V125-1-8 closes this bug class architecturally by deferring the ArgoCD
// register call to post-PR-merge so orphans are never created in the first
// place. This endpoint stays as the recovery surface for orphans created
// by older Sharko versions or by direct-API races.
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

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
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
	pending := resolvePendingRegistrations(r.Context(), gp, s.gitopsCfg.CommitPrefix)
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
