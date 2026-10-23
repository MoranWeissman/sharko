package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// availableCluster is one cluster ArgoCD already connects to that Sharko
// does not manage yet.
type availableCluster struct {
	// Name is the cluster name ArgoCD shows.
	Name string `json:"name"`
	// ServerURL is the cluster's API server address in ArgoCD.
	ServerURL string `json:"server_url"`
	// SecretName is the Kubernetes name of ArgoCD's connection Secret for
	// this cluster. Left out when Sharko could not read it.
	SecretName string `json:"secret_name,omitempty"`
	// Reachable is ArgoCD's own last word on the connection: true when
	// ArgoCD says Successful, false when it says Failed. Left out when
	// ArgoCD has not said yet.
	Reachable *bool `json:"reachable,omitempty"`
}

// availableClustersResponse is the body of GET /clusters/available.
type availableClustersResponse struct {
	Clusters []availableCluster `json:"clusters"`
}

// reachableFromConnectionState turns ArgoCD's connection state into the
// reachable field. Anything other than Successful or Failed is unknown.
func reachableFromConnectionState(state string) *bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "successful":
		v := true
		return &v
	case "failed":
		v := false
		return &v
	}
	return nil
}

// handleDiscoverClusters godoc
//
// @Summary List clusters ArgoCD knows that Sharko does not manage
// @Description Reads the clusters ArgoCD already connects to and leaves out the ones in Sharko's fleet record in Git, ArgoCD's own in-cluster entry, and the ones being registered (an open register pull request, or a connection with the registration-pending mark).
// @Description Each one can be adopted or taken over. reachable is ArgoCD's own connection state and is left out when ArgoCD has not checked yet.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} availableClustersResponse "Clusters ArgoCD knows that Sharko does not manage"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Sharko could not read ArgoCD or Git"
// @Router /clusters/available [get]
func (s *Server) handleDiscoverClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.discover") {
		return
	}

	// The orchestrator-interface accessor honours the test override, so
	// the ArgoCD-failure path below is unit-testable with a fake client.
	ac, err := s.connSvc.GetActiveOrchestratorArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}
	argoClusters, ok := s.listArgoClustersOrFail(w, r, ac, "cluster-discover")
	if !ok {
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}
	managed, err := s.clusterSvc.ManagedClusterNames(r.Context(), gp)
	if err != nil {
		writeUpstreamError(w, "list_available_clusters", err)
		return
	}

	// Secret names come from one list of ArgoCD's cluster Secrets. When
	// that cannot be read the list still goes out, without the names.
	var secretNames map[string]string
	var registering map[string]bool
	if s.argoSecretManager != nil {
		if secretNames, registering, err = s.argoSecretManager.ClusterSecretIndex(r.Context()); err != nil {
			slog.Warn("[cluster-discover] could not read ArgoCD's cluster Secrets, sending the list without secret names", "error", err)
			secretNames, registering = nil, nil
		}
	}
	// A cluster whose register pull request is still open is not
	// available: it shows in the pending list, and adopt or take over
	// would open a second pull request for it (EP3 dry run round R, R7).
	for _, p := range resolvePendingRegistrations(r.Context(), gp, s.gitopsConfig().CommitPrefix) {
		if p.Operation == models.PendingOperationRegister {
			if registering == nil {
				registering = map[string]bool{}
			}
			registering[p.ClusterName] = true
		}
	}

	out := make([]availableCluster, 0, len(argoClusters))
	for _, c := range argoClusters {
		if c.Name == "in-cluster" || strings.HasPrefix(c.Server, "https://kubernetes.default") {
			continue
		}
		if _, isManaged := managed[c.Name]; isManaged {
			continue
		}
		if registering[c.Name] {
			continue
		}
		out = append(out, availableCluster{
			Name:       c.Name,
			ServerURL:  c.Server,
			SecretName: secretNames[c.Name],
			Reachable:  reachableFromConnectionState(c.ConnectionState),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, availableClustersResponse{Clusters: out})
}

// listArgoClustersOrFail lists ArgoCD's clusters. On failure it raises the
// Kubernetes Warning event, writes the 502 with the fixed sentence, and
// returns false.
func (s *Server) listArgoClustersOrFail(w http.ResponseWriter, r *http.Request, ac orchestrator.ArgocdClient, logTag string) ([]models.ArgocdCluster, bool) {
	argoClusters, err := ac.ListClusters(r.Context())
	if err == nil {
		return argoClusters, true
	}
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
	// PUBLIC BOUNDARY. err can be a *url.Error carrying the full ArgoCD
	// base URL, so the answer is the classified sentence, never err's text.
	slog.Error("listing ArgoCD clusters failed", "handler", logTag, "step", "list-argocd-clusters")
	writeError(w, http.StatusBadGateway, argoListFailureSentence(argoReadClusters, err))
	return nil, false
}

// providerClusterEntry is one cluster in GET /providers/clusters.
type providerClusterEntry struct {
	Name       string `json:"name"`
	Region     string `json:"region"`
	Registered bool   `json:"registered"`
}

// handleListProviderClusters godoc
//
// @Summary List clusters in the credentials backend
// @Description Lists the clusters the credentials backend has sign-in details for, and marks which ones ArgoCD already has.
// @Description This is what GET /clusters/available used to send. The CLI's batch register --from-provider uses it.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Clusters in the credentials backend"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /providers/clusters [get]
func (s *Server) handleListProviderClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.discover") {
		return
	}
	if s.credProvider() == nil {
		writeMissingProviderError(w)
		return
	}

	ac, err := s.connSvc.GetActiveOrchestratorArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}

	// PUBLIC BOUNDARY. A credentials-backend error gets the fixed sentence
	// instead of the backend's raw text; anything else keeps its text.
	providerClusters, err := s.credProvider().ListClusters()
	if err != nil {
		slog.Error("[provider-clusters] listing provider clusters failed", "step", "list-provider-clusters")
		writeError(w, http.StatusBadGateway, "failed to list provider clusters: "+credsafe.Sentence(err))
		return
	}

	argoClusters, ok := s.listArgoClustersOrFail(w, r, ac, "provider-clusters")
	if !ok {
		return
	}

	registered := make(map[string]bool, len(argoClusters))
	for _, c := range argoClusters {
		registered[c.Name] = true
	}
	entries := make([]providerClusterEntry, 0, len(providerClusters))
	for _, pc := range providerClusters {
		entries = append(entries, providerClusterEntry{
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

// verifyStageCredentials is the `stage` this endpoint reports when fetching
// the cluster's sign-in details failed, before anything was asked of the
// cluster itself.
//
// IT IS A WIRE VALUE THE BROWSER ROUTES ON, so it is named here rather than
// typed inline. The adopt dialog keeps a cluster selected and adoptable when
// it sees this stage, because a failed credential lookup is the normal case
// for adoption (internal/orchestrator/adopt.go) and nothing is known against
// a cluster that was never contacted. It used to work that out by searching
// ErrorMessage for words like "not found" — and once a credentials-backend
// failure started carrying one fixed sentence, that search matched nothing
// and the contract silently stopped working.
//
// Both halves are pinned to a written-out literal:
// TestTestCluster_CredentialFailureStageIsPinned here, and
// VERIFY_STAGE_CREDENTIALS in ui/src/services/api.ts.
const verifyStageCredentials = "credentials"

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
	// Warnings lists test objects Sharko made for this check and could not
	// remove afterwards. A warning never turns a passing test into a failure.
	Warnings []string      `json:"warnings,omitempty"`
	Result   verify.Result `json:"result"`
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
		Warnings:      result.Warnings,
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
		// PUBLIC BOUNDARY. err came from a credentials backend, so neither the
		// log line nor the response body carries its text: credsafe.Sentence
		// returns the one fixed sentence for a credentials-backend failure
		// (recognised by TYPE, through the %w chain) and leaves every other
		// error's text alone.
		safeMsg := credsafe.Sentence(err)
		slog.Error("[cluster-test] failed", "name", name, "step", "fetch-credentials", "error", safeMsg)
		result := verify.Result{
			Success:      false,
			Stage:        verifyStageCredentials,
			ErrorCode:    "ERR_AUTH",
			ErrorMessage: safeMsg,
			Steps: []verify.Step{
				{Name: "Fetch credentials", Status: "fail", Detail: safeMsg},
				{Name: "Fetch server version", Status: "skipped"},
				{Name: "Ensure namespace", Status: "skipped"},
				{Name: "Create test secret", Status: "skipped"},
				{Name: "Read back test secret", Status: "skipped"},
				{Name: "Delete test secret", Status: "skipped"},
			},
		}
		resp := newTestClusterResponse(name, result)

		// If the credentials are genuinely MISSING, search for similarly-named
		// secrets and offer them, so the operator can fix a typo in one click.
		//
		// This asks credsafe.IsNotFound, which is errors.Is against a marker the
		// PROVIDER set at the point it actually knew: the AWS SDK returned
		// ResourceNotFoundException, or the Kubernetes read was a real NotFound.
		//
		// It used to be strings.Contains(err.Error(), "not found"), and that was
		// wrong in both directions. It could not survive a backend rephrasing
		// itself, and it fired on any failure whose text happened to contain the
		// words — an AccessDenied that mentions a missing permission got offered
		// a list of secret names, sending the operator to fix a typo that was
		// never there. Now "we were not allowed to look" and "it is not there"
		// give different answers, which is what they always were.
		//
		// Nothing from err reaches the response — only the suggestion list,
		// which is secret NAMES from the operator's own configured prefix.
		if credsafe.IsNotFound(err) {
			suggestions, searchErr := s.credProvider().SearchSecrets(name)
			if searchErr != nil {
				slog.Warn("[cluster-test] SearchSecrets failed", "name", name, "error", credsafe.Sentence(searchErr))
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
		// PUBLIC BOUNDARY. The client is built FROM the credential material, so
		// the parse error can quote part of it.
		//
		// The response says the catalog sentence for the classified code, the
		// same wording Stage 1 would use for the same code — a person should
		// not be able to tell from the message whether the check failed while
		// building the client or while talking to the cluster.
		//
		// The cause still reaches the server log, and credsafe.Sentence swaps
		// in the fixed sentence when the error came from the credentials
		// backend.
		code := verify.ClassifyError(err)
		safeMsg := verify.FriendlyMessage(code).String()
		slog.Error("[cluster-test] failed", "name", name, "step", "build-client",
			"code", code, "diagnostic", credsafe.Sentence(err))
		result := verify.Result{
			Success:      false,
			Stage:        "client",
			ErrorCode:    code,
			ErrorMessage: safeMsg,
			Steps: []verify.Step{
				{Name: "Fetch credentials", Status: "pass"},
				{Name: "Fetch server version", Status: "fail", Detail: safeMsg},
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
		// ErrorMessage is the catalog sentence now, which would make every
		// failure of a given code log identically. Diagnostic() is the real
		// cause — this log line is the one place it is meant to appear.
		slog.Error("[cluster-test] Stage 1 failed", "name", name,
			"code", result.ErrorCode, "diagnostic", result.Diagnostic())
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
		// Stage 1 already ran and may have left something behind; keep
		// saying so even though the response now describes Stage 2.
		resp.Warnings = result.Warnings
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
