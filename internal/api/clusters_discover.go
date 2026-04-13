package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
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
// @Failure 501 {object} map[string]interface{} "Provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/available [get]
// handleDiscoverClusters handles GET /api/v1/clusters/available — list provider clusters
// and mark which are already registered in ArgoCD.
func (s *Server) handleDiscoverClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.discover") {
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Get all clusters from the credentials provider.
	providerClusters, err := s.credProvider.ListClusters()
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list provider clusters: "+err.Error())
		return
	}

	// Get all clusters registered in ArgoCD.
	argoClusters, err := ac.ListClusters(r.Context())
	if err != nil {
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
// @Failure 503 {object} map[string]interface{} "No credentials provider configured"
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

	if s.credProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "no credentials provider configured")
		return
	}

	// Resolve the credential lookup name. If the cluster is registered and has
	// a SecretPath override, use that instead of the cluster name.
	credLookupName := name
	if s.clusterSvc != nil {
		if gp, gpErr := s.connSvc.GetActiveGitProvider(); gpErr == nil {
			if ac, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil {
				detail, detailErr := s.clusterSvc.GetClusterDetail(r.Context(), name, gp, ac)
				if detailErr == nil && detail != nil && detail.Cluster.SecretPath != "" {
					credLookupName = detail.Cluster.SecretPath
					slog.Info("[cluster-test] using secretPath override", "name", name, "secretPath", credLookupName)
				}
			}
		}
	}

	slog.Info("[cluster-test] fetching credentials", "name", name, "lookupName", credLookupName)
	creds, err := s.credProvider.GetCredentials(credLookupName)
	if err != nil {
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
			suggestions, searchErr := s.credProvider.SearchSecrets(name)
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

	writeJSON(w, http.StatusOK, resp)
}

// discoverEKSRequest is the request body for POST /api/v1/clusters/discover.
type discoverEKSRequest struct {
	Provider string   `json:"provider"`
	RoleARNs []string `json:"role_arns"`
	Region   string   `json:"region"`
}

// discoverEKSResponse wraps the discovery results.
type discoverEKSResponse struct {
	Clusters []providers.DiscoveredCluster `json:"clusters"`
	Error    string                        `json:"error,omitempty"`
}

// handleDiscoverEKS godoc
//
// @Summary Discover EKS clusters
// @Description Scans one or more AWS accounts for EKS clusters via the EKS API.
// @Description If role_arns is empty, uses the default IRSA identity.
// @Description For cross-account discovery, provide one or more IAM role ARNs to assume.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body discoverEKSRequest true "Discovery request"
// @Success 200 {object} discoverEKSResponse "Discovered clusters"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Discovery failed"
// @Router /clusters/discover [post]
// handleDiscoverEKS handles POST /api/v1/clusters/discover — scan AWS for EKS clusters.
func (s *Server) handleDiscoverEKS(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.discover") {
		return
	}

	var req discoverEKSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Provider != "eks" {
		writeError(w, http.StatusBadRequest, "unsupported provider: "+req.Provider+"; only \"eks\" is supported")
		return
	}

	// Default region from provider config if not specified in request.
	region := req.Region
	if region == "" && s.providerCfg != nil {
		region = s.providerCfg.Region
	}

	slog.Info("[discover-eks] starting EKS discovery", "roleARNs", req.RoleARNs, "region", region)

	clusters, err := providers.DiscoverEKSClusters(r.Context(), req.RoleARNs, region)

	resp := discoverEKSResponse{
		Clusters: clusters,
	}
	if resp.Clusters == nil {
		resp.Clusters = []providers.DiscoveredCluster{}
	}

	if err != nil {
		slog.Warn("[discover-eks] discovery completed with errors", "error", err)
		// If we have partial results, return 200 with error field.
		// If we have no results, return 502.
		if len(clusters) == 0 {
			writeError(w, http.StatusBadGateway, "EKS discovery failed: "+err.Error())
			return
		}
		resp.Error = err.Error()
	}

	slog.Info("[discover-eks] discovery complete", "clusterCount", len(clusters))

	// Emit audit event.
	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		username = "system"
	}
	s.auditLog.Add(audit.Entry{
		Level:    "info",
		Event:    "cluster_discovered",
		User:     username,
		Action:   "discover",
		Resource: "eks",
		Source:   "api",
		Result:   "success",
	})

	writeJSON(w, http.StatusOK, resp)
}

