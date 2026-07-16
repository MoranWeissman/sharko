package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// discoverClusterEntry is a single cluster in the discover response.
type discoverClusterEntry struct {
	Name       string `json:"name"`
	Region     string `json:"region"`
	Registered bool   `json:"registered"`
}

// handleDiscoverClusters godoc
//
// @Summary Discover available clusters
// @Description Lists clusters from the credentials provider and marks which are registered in ArgoCD
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Available clusters"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/available [get]
// handleDiscoverClusters handles GET /api/v1/clusters/available — list provider clusters
// and mark which are already registered in ArgoCD.
func (s *Server) handleDiscoverClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.discover") {
		return
	}
	if s.credProvider() == nil {
		writeMissingProviderError(w)
		return
	}

	// Use the orchestrator-interface accessor (honours the test override) so
	// this handler's ArgoCD-failure event path (V3 E1) is unit-testable with a
	// fake client — behaviourally identical to GetActiveArgocdClient in
	// production (same underlying *argocd.Client).
	ac, err := s.connSvc.GetActiveOrchestratorArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Get all clusters from the credentials provider.
	providerClusters, err := s.credProvider().ListClusters()
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list provider clusters: "+err.Error())
		return
	}

	// Get all clusters registered in ArgoCD.
	argoClusters, err := ac.ListClusters(r.Context())
	if err != nil {
		// V3 E1: surface the host-ArgoCD API failure as a k8s Warning event.
		// A 403 (ErrPermissionDenied) means the token lacks RBAC permission
		// (auth failure); anything else is treated as unreachable. The message
		// carries no token or URL — only the operational condition.
		if errors.Is(err, argocd.ErrPermissionDenied) {
			s.emitWarning(events.ReasonArgoCDAuthFailed,
				"Host ArgoCD rejected Sharko's token: the account does not have permission to list clusters.")
		} else {
			s.emitWarning(events.ReasonArgoCDUnreachable,
				"Host ArgoCD is unreachable: Sharko could not list clusters from the ArgoCD API.")
		}
		writeError(w, http.StatusBadGateway, "failed to list ArgoCD clusters: "+err.Error())
		return
	}

	// Build a set of registered cluster names.
	registered := make(map[string]bool, len(argoClusters))
	for _, c := range argoClusters {
		registered[c.Name] = true
	}

	// Cross-reference provider clusters with ArgoCD.
	entries := make([]discoverClusterEntry, 0, len(providerClusters))
	for _, pc := range providerClusters {
		entries = append(entries, discoverClusterEntry{
			Name:       pc.Name,
			Region:     pc.Region,
			Registered: registered[pc.Name],
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clusters": entries,
	})
}

// testClusterRequest is the optional JSON body for POST /clusters/{name}/test.
type testClusterRequest struct {
	Deep bool `json:"deep"`
}

// testClusterResponse wraps verify.Result with top-level fields so the UI can
// read error details without drilling into a nested "result" object.
type testClusterResponse struct {
	Name          string                 `json:"name"`
	Reachable     bool                   `json:"reachable"`
	Success       bool                   `json:"success"`
	Stage         string                 `json:"stage"`
	ErrorCode     verify.ErrorCode       `json:"error_code,omitempty"`
	ErrorMessage  string                 `json:"error_message,omitempty"`
	DurationMs    int64                  `json:"duration_ms"`
	ServerVersion string                 `json:"server_version,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	Suggestions   []string               `json:"suggestions,omitempty"`
	Steps         []verify.Step          `json:"steps,omitempty"`
	Result        verify.Result          `json:"result"`
}

// newTestClusterResponse builds a testClusterResponse from a verify.Result,
// copying key fields to the top level for UI consumption.
func newTestClusterResponse(name string, result verify.Result) testClusterResponse {
	return testClusterResponse{
		Name:          name,
		Reachable:     result.Success,
		Success:       result.Success,
		Stage:         result.Stage,
		ErrorCode:     result.ErrorCode,
		ErrorMessage:  result.ErrorMessage,
		DurationMs:    result.DurationMs,
		ServerVersion: result.ServerVersion,
		Details:       result.Details,
		Steps:         result.Steps,
		Result:        result,
	}
}

// handleTestCluster godoc
//
// @Summary Test cluster connectivity
// @Description Verifies connectivity to a cluster by performing a secret CRUD cycle (Stage 1).
// @Description Optionally runs an ArgoCD round-trip test (Stage 2) when {"deep": true} is sent.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body testClusterRequest false "Optional test options"
// @Success 200 {object} testClusterResponse "Connectivity result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Service unavailable. error_code one of: no_secrets_backend, argocd_provider_iam_required, argocd_provider_exec_unsupported, argocd_provider_unsupported_auth"
// @Router /clusters/{name}/test [post]
// handleTestCluster handles POST /api/v1/clusters/{name}/test — test connectivity to a cluster.
func (s *Server) handleTestCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.test") {
		return
	}

	name := r.PathValue("name")
	slog.Info("[cluster-test] testing cluster", "name", name)

	// Parse optional request body.
	var req testClusterRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if s.credProvider() == nil {
		// A bare "no credentials provider configured" message would be
		// surfaced by the UI as "Unreachable" — but the cluster isn't
		// unreachable, the *test feature* is unavailable because no
		// secrets backend is wired up on the active connection. Return a
		// structured payload so the UI can render a dedicated "test
		// unavailable" state with a path to Settings → Connections.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":      "Cluster connectivity test requires a secrets backend (Vault / AWS Secrets Manager / file-store) on the active connection. Configure one in Settings → Connections to enable testing.",
			"error_code": "no_secrets_backend",
			"hint":       "configure a secrets backend on the active connection via Settings → Connections",
		})
		return
	}

	// Routed fetch (V2-cleanup-60.4): resolves the stored secretPath
	// override (V2-cleanup-55.1) AND routes by the cluster's stored
	// creds_source — an inline-registered cluster is read from the ArgoCD
	// cluster Secret regardless of the configured backend type, so Test
	// works on mixed inline + backend fleets.
	slog.Info("[cluster-test] fetching credentials", "name", name)
	creds, err := s.fetchClusterCredentials(r.Context(), name)
	if err != nil {
		// When the active credentials provider is ArgoCDProvider (the
		// built-in default for in-cluster installs), it returns a typed
		// *ArgoCDProviderError for any cluster Secret whose auth shape
		// is not the bearerToken happy-path (awsAuthConfig → IAM
		// required; execProviderConfig → exec-plugin auth not supported;
		// unknown shape). Surface those via the same structured-503
		// envelope as the missing-backend case so the UI can render
		// branch-specific copy off the stable error_code field.
		var argoErr *providers.ArgoCDProviderError
		if errors.As(err, &argoErr) {
			slog.Warn("[cluster-test] argocd provider unavailable shape",
				"name", name, "code", argoErr.Code, "cluster", argoErr.ClusterName, "server", argoErr.Server)
			// V3 E1: the IAM-required code is returned when Sharko parsed the
			// cluster's AWS-IAM connection shape but could not MINT an EKS
			// token with its own identity — surface that as a Warning event.
			// The message names the cluster only (no server URL, no role ARN).
			if argoErr.Code == providers.ArgoCDProviderCodeIAMRequired {
				s.emitWarning(events.ReasonAWSTokenMintFailed,
					fmt.Sprintf("EKS token mint failed for cluster %q: Sharko has no usable AWS identity to authenticate to this cluster.", name))
			}
			writeStructuredError(w, http.StatusServiceUnavailable, argoErr.Code, argoErr.Detail)
			return
		}
		slog.Error("[cluster-test] failed", "name", name, "step", "fetch-credentials", "error", err)
		result := verify.Result{
			Success:      false,
			Stage:        "credentials",
			ErrorCode:    "ERR_AUTH",
			ErrorMessage: err.Error(),
			Steps: []verify.Step{
				{Name: "Fetch credentials", Status: "fail", Detail: err.Error()},
				{Name: "Fetch server version", Status: "skipped"},
				{Name: "Ensure namespace", Status: "skipped"},
				{Name: "Create test secret", Status: "skipped"},
				{Name: "Read back test secret", Status: "skipped"},
				{Name: "Delete test secret", Status: "skipped"},
			},
		}
		resp := newTestClusterResponse(name, result)

		// If credential fetch failed with "not found", search for similar secrets
		// and include them as suggestions so the UI can offer one-click correction.
		if strings.Contains(err.Error(), "not found") {
			suggestions, searchErr := s.credProvider().SearchSecrets(name)
			if searchErr != nil {
				slog.Warn("[cluster-test] SearchSecrets failed", "name", name, "error", searchErr)
			}
			if len(suggestions) > 0 {
				resp.Suggestions = suggestions
				slog.Info("[cluster-test] found secret suggestions", "name", name, "count", len(suggestions))
			}
		}

		writeJSON(w, http.StatusOK, resp)
		return
	}
	slog.Info("[cluster-test] credentials obtained", "name", name, "server", creds.Server)

	slog.Info("[cluster-test] building k8s client", "name", name)
	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		slog.Error("[cluster-test] failed", "name", name, "step", "build-client", "error", err)
		result := verify.Result{
			Success:      false,
			Stage:        "client",
			ErrorCode:    verify.ClassifyError(err),
			ErrorMessage: "failed to build client: " + err.Error(),
			Steps: []verify.Step{
				{Name: "Fetch credentials", Status: "pass"},
				{Name: "Fetch server version", Status: "fail", Detail: "failed to build client: " + err.Error()},
				{Name: "Ensure namespace", Status: "skipped"},
				{Name: "Create test secret", Status: "skipped"},
				{Name: "Read back test secret", Status: "skipped"},
				{Name: "Delete test secret", Status: "skipped"},
			},
		}
		writeJSON(w, http.StatusOK, newTestClusterResponse(name, result))
		return
	}

	// Stage 1: secret CRUD cycle.
	slog.Info("[cluster-test] running Stage 1 verification", "name", name)
	result := verify.Stage1(r.Context(), client, verify.TestNamespace())

	// Prepend the "Fetch credentials" step to the result steps.
	credStep := verify.Step{Name: "Fetch credentials", Status: "pass"}
	result.Steps = append([]verify.Step{credStep}, result.Steps...)

	resp := newTestClusterResponse(name, result)

	if result.Success {
		slog.Info("[cluster-test] Stage 1 passed", "name", name, "version", result.ServerVersion)
	} else {
		slog.Error("[cluster-test] Stage 1 failed", "name", name, "error", result.ErrorMessage)
		// V3 E1: surface the connectivity-test failure as a k8s Warning event.
		// The message names the cluster and the verify error CODE (an
		// enum like ERR_NETWORK / ERR_RBAC), never the raw error string —
		// keeps secret material and internal detail out of the event.
		s.emitWarning(events.ReasonClusterTestFailed,
			fmt.Sprintf("Connectivity test failed for cluster %q (%s): Sharko could not complete the secret read/write probe.", name, result.ErrorCode))
	}

	// Stage 2: ArgoCD round-trip (stub).
	if req.Deep {
		slog.Info("[cluster-test] running Stage 2 (deep) verification", "name", name)
		stage2Result := verify.Stage2(r.Context(), nil, name, 0)
		resp = newTestClusterResponse(name, stage2Result)
	}

	// Record observation for cluster status tracking.
	if s.obsStore != nil {
		if err := s.obsStore.RecordTestResult(r.Context(), name, resp.Result); err != nil {
			slog.Error("[cluster-test] failed to record observation", "name", name, "error", err)
		}
	}

	testResult := "pass"
	if !resp.Success {
		testResult = "fail"
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_tested",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   fmt.Sprintf("result=%s", testResult),
	})

	writeJSON(w, http.StatusOK, resp)
}

// writeStructuredError emits the {"error", "error_code"} envelope used by
// callers (UI, CLI) that dispatch on a stable machine-readable code
// instead of parsing the human-readable error string. The shape covers
// the no_secrets_backend response and argocd_provider_* codes for the
// ArgoCDProvider's IAM / exec-plugin / unknown-auth branches.
//
// Note: writeMissingProviderError in router.go uses a parallel envelope
// with a "code"+"hint" pair. The cluster-test endpoint deliberately
// uses the "error_code" key — that is what the UI keys off for this
// surface, so a rename would silently break the UI dispatch.
func writeStructuredError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":      message,
		"error_code": code,
	})
}

