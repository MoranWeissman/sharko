package api

import (
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/diagnose"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// handleDiagnoseCluster godoc
//
// @Summary Diagnose cluster permissions
// @Description Runs a series of permission checks against a cluster and returns
// @Description a diagnostic report with pass/fail results and suggested RBAC fixes.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} diagnose.DiagnosticReport "Diagnostic report"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "No credentials provider configured"
// @Router /clusters/{name}/diagnose [post]
// handleDiagnoseCluster handles POST /api/v1/clusters/{name}/diagnose — run IAM diagnostics.
func (s *Server) handleDiagnoseCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.diagnose") {
		return
	}

	name := r.PathValue("name")
	slog.Info("[cluster-diagnose] starting diagnostics", "name", name)

	if s.credProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "no credentials provider configured")
		return
	}

	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		slog.Error("[cluster-diagnose] failed to fetch credentials", "name", name, "error", err)
		writeError(w, http.StatusBadGateway, "failed to fetch credentials: "+err.Error())
		return
	}

	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		slog.Error("[cluster-diagnose] failed to build client", "name", name, "error", err)
		writeError(w, http.StatusBadGateway, "failed to build k8s client: "+err.Error())
		return
	}

	// The caller ARN and role ARN are not available from the credential provider;
	// pass the cluster name as identity context and mark role as N/A.
	callerARN := name
	roleARN := "N/A"

	namespace := verify.TestNamespace()
	report := diagnose.DiagnoseCluster(r.Context(), client, namespace, callerARN, roleARN)

	slog.Info("[cluster-diagnose] completed", "name", name, "checks", len(report.NamespaceAccess), "fixes", len(report.SuggestedFixes))
	writeJSON(w, http.StatusOK, report)
}
