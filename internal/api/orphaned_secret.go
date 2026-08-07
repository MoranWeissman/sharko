package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/secrets"
)

// orphaned_secret.go — DELETE /clusters/{name}/orphaned-secrets/{namespace}/
// {secret} (leftover-secrets S1). The addon-values engine's own scan passes
// (internal/secrets.Reconciler.scanOrphans) find leftover values secrets;
// this is the ONLY path that deletes one, and only after the reconciler has
// re-verified everything against the live world (see
// secrets.Reconciler.DeleteOrphanedSecret's own doc comment for the exact
// checks). Modeled closely on addon_secret_single.go's single-item actions:
// authz check first, 503 when no engine is wired, canned plain-English
// responses, never the secret's own content.

// orphanedSecretDeleteResult is the response body for a successful delete.
type orphanedSecretDeleteResult struct {
	Status    string `json:"status"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Message   string `json:"message"`
}

// handleDeleteOrphanedSecret godoc
//
// @Summary Delete a leftover addon-values secret
// @Description Deletes one leftover values secret the addon-values engine found on a cluster — a Secret Sharko once wrote whose catalog push definition no longer exists. Never auto-deleted: this is the only path that removes one, and it re-verifies the secret is still an unclaimed, Sharko-owned orphan (against a fresh read of the catalog and the live secret's own labels/annotations) immediately before deleting — a reclaim or an ownership-marker change since the last scan refuses the delete instead of guessing.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param namespace path string true "Namespace the secret lives in"
// @Param secret path string true "Secret name"
// @Success 200 {object} orphanedSecretDeleteResult "Deleted"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "No leftover secret is on record for that cluster, namespace, and name"
// @Failure 409 {object} map[string]interface{} "This secret is claimed by the catalog again, or its ownership marker changed since it was found — nothing was deleted"
// @Failure 422 {object} map[string]interface{} "Could not delete — no Git connection, cluster unreachable, or the delete itself failed"
// @Failure 503 {object} map[string]interface{} "The addon-values secrets engine is not running on this server"
// @Router /clusters/{name}/orphaned-secrets/{namespace}/{secret} [delete]
func (s *Server) handleDeleteOrphanedSecret(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "orphaned-secret.delete") {
		return
	}

	cluster := r.PathValue("name")
	namespace := r.PathValue("namespace")
	name := r.PathValue("secret")

	if s.secretReconciler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the addon-values secrets engine is not running on this server (no Git connection or credentials provider configured) — there is nothing to delete",
		})
		return
	}

	err := s.secretReconciler.DeleteOrphanedSecret(r.Context(), cluster, namespace, name)
	if err != nil {
		status, message := orphanedSecretDeleteStatus(err)
		writeError(w, status, message)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "orphaned_secret_deleted",
		Resource: fmt.Sprintf("cluster:%s", cluster),
		Detail:   fmt.Sprintf("deleted leftover secret %s/%s", namespace, name),
	})

	writeJSON(w, http.StatusOK, orphanedSecretDeleteResult{
		Status:    "deleted",
		Cluster:   cluster,
		Namespace: namespace,
		Name:      name,
		Message:   fmt.Sprintf("deleted leftover secret %q from cluster %q.", namespace+"/"+name, cluster),
	})
}

// orphanedSecretDeleteStatus maps DeleteOrphanedSecret's error to an HTTP
// status and an honest, plain-English message. secrets.ErrOrphanUnknown /
// ErrOrphanReclaimed / ErrOrphanRefused are this package's own sentinels —
// their text is already written to be shown to a person verbatim (same
// convention as secrets.ErrForeignSecret). Anything else — no Git
// connection, a credentials/connectivity failure, a genuine K8s API error —
// is a 422: the check itself could not be completed, never the raw wrapped
// error text (S8 — mirrors addonValuesSecretCheckFailureSentence's
// "canned sentence, never the raw string" rule, but these three sentinels
// are this package's OWN text, not a provider SDK's, so they are safe to
// pass through directly).
func orphanedSecretDeleteStatus(err error) (int, string) {
	switch {
	case errors.Is(err, secrets.ErrOrphanUnknown):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, secrets.ErrOrphanReclaimed), errors.Is(err, secrets.ErrOrphanRefused):
		return http.StatusConflict, err.Error()
	case errors.Is(err, secrets.ErrNoGitConnection):
		return http.StatusUnprocessableEntity, err.Error()
	default:
		return http.StatusUnprocessableEntity, "Sharko could not delete this secret — check that it can reach the cluster, then try again."
	}
}
