package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleListClusterSecrets godoc
//
// @Summary List cluster secrets
// @Description Lists managed addon secrets on a remote cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Cluster secrets"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/secrets [get]
func (s *Server) handleListClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.secrets.list") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if s.credProvider() == nil {
		writeMissingProviderError(w)
		return
	}

	creds, err := s.fetchClusterCredentials(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching cluster credentials: "+err.Error())
		return
	}

	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connecting to cluster: "+err.Error())
		return
	}

	// Collect declared namespaces from addon secret definitions.
	allowedNamespaces := make(map[string]bool)
	s.addonSecretDefsMu.RLock()
	for _, def := range s.addonSecretDefs {
		if def.Namespace != "" {
			allowedNamespaces[def.Namespace] = true
		}
	}
	s.addonSecretDefsMu.RUnlock()

	var allSecrets []remoteclient.ManagedSecretInfo
	if len(allowedNamespaces) > 0 {
		// List secrets only in declared namespaces.
		for ns := range allowedNamespaces {
			secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, ns)
			if err != nil {
				// Log but continue — namespace may not exist yet.
				slog.Warn("listing secrets in namespace", "namespace", ns, "error", err)
				continue
			}
			allSecrets = append(allSecrets, secrets...)
		}
	} else {
		// No addon secret definitions configured — fall back to listing by label only.
		slog.Warn("no addon secret definitions — listing all managed secrets")
		secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, "")
		if err != nil {
			writeError(w, http.StatusBadGateway, "listing secrets: "+err.Error())
			return
		}
		allSecrets = secrets
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster": name,
		"secrets": allSecrets,
	})
}

// handleRefreshClusterSecrets godoc
//
// @Summary Refresh cluster secrets from Git
// @Description Delivers every addon-values secret the GIT CATALOG defines for this cluster, right now, through the same git-backed engine the periodic reconciler runs (task #152). What to deliver — backend paths, destination namespaces, Secret names, key lists — comes exclusively from Git; any request body is ignored entirely. Pass ?addon= to narrow the push to one addon, which must already be defined in Git for this cluster or the call is refused.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon query string false "Refresh only this addon's secrets (must be defined in the Git catalog for this cluster)"
// @Success 200 {object} map[string]interface{} "Secrets refreshed from Git"
// @Success 207 {object} map[string]interface{} "Some secrets refreshed, some failed"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "The cluster or addon is not defined in Git"
// @Failure 503 {object} map[string]interface{} "Secrets reconciler not configured"
// @Failure 502 {object} map[string]interface{} "Git or upstream error"
// @Router /clusters/{name}/secrets/refresh [post]
func (s *Server) handleRefreshClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.secrets.refresh") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	// The ONLY inputs this endpoint takes are the cluster name in the path
	// and an optional addon name in the query. The request body is
	// deliberately never read (task #152, story 152.A): a caller could
	// once smuggle a whole secret definition — backend path, destination
	// namespace, Secret name, key list — through this door, and the Git
	// catalog is the only source of those now.
	addon := r.URL.Query().Get("addon")

	if s.secretReconciler == nil {
		// Same structured 503 shape writeMissingProviderError uses (code +
		// hint + error), with the code naming the real missing precondition:
		// the git-backed secrets reconciler, which only exists once a
		// secrets provider is configured.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "secrets reconciler not configured",
			"code":  "secrets_reconciler_not_configured",
			"hint":  "configure a secrets provider via Settings → Connections (UI), or POST /api/v1/connections/ with provider config (API) — the git-backed secrets engine starts with it",
		})
		return
	}

	refreshed, failed, err := s.secretReconciler.SyncCluster(r.Context(), name, addon)
	if err != nil {
		status, msg := clusterSecretsRefreshRefusal(name, addon, err.Error())
		// AC: a refresh Git does not back is refused AND the refusal is
		// audited — the audit middleware emits this entry with the
		// failure result it derives from the status code.
		audit.Enrich(r.Context(), audit.Fields{
			Event:    "cluster_secret_refresh_refused",
			Resource: fmt.Sprintf("cluster:%s", name),
			Detail:   msg,
		})
		writeError(w, status, msg)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_secret_synced",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   fmt.Sprintf("refreshed %d secret(s) from the Git catalog, %d failed", len(refreshed), len(failed)),
	})

	if refreshed == nil {
		refreshed = []string{}
	}
	resp := map[string]interface{}{
		"cluster":           name,
		"secrets_refreshed": refreshed,
	}
	status := http.StatusOK
	if len(failed) > 0 {
		resp["failed_secrets"] = failed
		resp["note"] = "Some secrets could not be delivered — the Managed Secrets page shows the reason for each row."
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, resp)
}

// clusterSecretsRefreshRefusal maps a SyncCluster error to an HTTP status
// and a safe, canned sentence — the same substring convention
// addonValuesSecretSyncFailureSentence uses (see its doc comment for why
// raw reconciler error text must never reach a response body). The two
// not-in-Git refusals and the no-Git-connection case are Sharko's own
// fixed sentences, rebuilt here with the caller's own cluster/addon names
// (which came from the URL, so they leak nothing the caller didn't send).
func clusterSecretsRefreshRefusal(cluster, addon, errMsg string) (int, string) {
	switch {
	case strings.Contains(errMsg, "is not in the managed clusters list in Git"):
		return http.StatusNotFound, fmt.Sprintf(
			"Cluster %q is not in the managed clusters list in Git — nothing to refresh.", cluster)
	case strings.Contains(errMsg, "does not define an addon-values secret"):
		return http.StatusNotFound, fmt.Sprintf(
			"Git does not define an addon-values secret for addon %q on cluster %q — nothing to refresh. Add it to the catalog first.", addon, cluster)
	case strings.Contains(errMsg, "no Git connection is configured"):
		return http.StatusBadGateway, "Sharko has no Git connection configured — there is nothing to refresh."
	case strings.Contains(errMsg, "could not read the addon catalog or managed clusters list"):
		return http.StatusBadGateway, "Sharko couldn't read the addon catalog or managed-clusters file in git. Check that Sharko can reach your git host, then try again."
	default:
		return http.StatusBadGateway, "Sharko could not refresh this cluster's secrets. The Managed Secrets page shows the reason for each row."
	}
}
