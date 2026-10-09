package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// observeDashboardRead is the V2-3 SLO instrumentation shared across the
// three dashboard read endpoints. phase matches a V2-1.2 baseline phase
// id where possible (fleet_status, pull_requests) so histogram
// dimensions line up with the baselines that sized the buckets in
// internal/metrics/buckets.go.
func observeDashboardRead(r *http.Request, w *http.ResponseWriter, phase string) func() {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: *w, statusCode: http.StatusOK}
	*w = rec
	return func() {
		code := strconv.Itoa(rec.statusCode)
		metrics.Observe(metrics.PathDashboardRead, phase, time.Since(start).Seconds(), logging.RequestID(r.Context()))
		metrics.IncTotal(metrics.PathDashboardRead, code)
		if rec.statusCode >= 400 {
			metrics.IncError(metrics.PathDashboardRead, code)
		}
	}
}

// handleGetDashboardStats handles GET /api/v1/dashboard/stats
//
// @Summary Dashboard stats
// @Description Returns dashboard statistics overview
// @Tags dashboard
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /dashboard/stats [get]
func (s *Server) handleGetDashboardStats(w http.ResponseWriter, r *http.Request) {
	defer observeDashboardRead(r, &w, "fleet_status")()

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	resp, err := s.dashboardSvc.GetStats(r.Context(), gp, ac)
	if err != nil {
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "dashboard_stats", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// AttentionItem is one row of the Dashboard's needs-attention feed
// (GET /api/v1/dashboard/attention).
//
// It was a type declared INSIDE the handler until B10. It is a package-level
// type now for one reason: internal/api/cluster_comparison_leak_test.go's
// field-by-field guard walks a response struct with reflection and fails on
// any field it has not been told about, and it cannot see a type that only
// exists inside a function body. The `error` field on this struct carried
// ArgoCD's own condition text, which is exactly the kind of addition the
// guard exists to catch, and the guard could not have caught it here.
type AttentionItem struct {
	AppName   string `json:"app_name"`
	AddonName string `json:"addon_name"`
	Cluster   string `json:"cluster"`
	Health    string `json:"health"`
	Sync      string `json:"sync"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
}

// handleGetAttentionItems godoc
//
// @Summary Get attention items
// @Description Returns ArgoCD applications that are unhealthy or have conditions requiring attention
// @Tags dashboard
// @Produce json
// @Security BearerAuth
// @Success 200 {array} AttentionItem "Attention items"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /dashboard/attention [get]
func (s *Server) handleGetAttentionItems(w http.ResponseWriter, r *http.Request) {
	defer observeDashboardRead(r, &w, "attention")()

	ac, err := s.connSvc.GetActiveOrchestratorArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Fetch applications and clusters concurrently. The cluster list is
	// only needed to split each app's "<addon>-<cluster>" name via
	// service.ExtractAddonCluster (same helper as
	// internal/service/observability.go's GetOverview) — a ListClusters
	// failure degrades to ExtractAddonCluster's last-hyphen fallback
	// (empty clusterNames set) rather than failing the whole endpoint,
	// since the apps list is what actually matters here.
	var (
		apps         []models.ArgocdApplication
		appsErr      error
		argoClusters []models.ArgocdCluster
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		apps, appsErr = ac.ListApplications(ctx)
	}()
	go func() {
		defer wg.Done()
		argoClusters, _ = ac.ListClusters(ctx) // best-effort; nil clusterNames on failure
	}()
	wg.Wait()

	if appsErr != nil {
		// Upstream call (ArgoCD): classify so an ArgoCD timeout reads as
		// 504 rather than 500.
		writeUpstreamError(w, "dashboard_attention", appsErr)
		return
	}

	clusterNames := make(map[string]bool, len(argoClusters))
	for _, c := range argoClusters {
		clusterNames[c.Name] = true
	}

	var items []AttentionItem
	for _, app := range apps {
		// Sharko's own ArgoCD system apps (bootstrap root + per-cluster
		// connectivity-check probes) are not catalog addons. The frontend renders
		// each item as a link to /addons/<name>, which 404s for these. Exclude them
		// from the Needs-Attention feed entirely (V2-cleanup-52).
		if orchestrator.IsSharkoSystemApp(app.Name) {
			continue
		}
		if app.HealthStatus == "Healthy" && len(app.Conditions) == 0 {
			continue
		}
		// Split "<addon>-<cluster>" the same way
		// internal/service/observability.go's GetOverview does (walk
		// finding from #691: this used to be `addonName := app.Name` —
		// the FULL ArgoCD app name, e.g. "metrics-server-spoke-eu" — with
		// a comment claiming it split the name, which it never actually
		// did).
		addonName, cluster := service.ExtractAddonCluster(app.Name, clusterNames)

		// B10: `error` used to be the ArgoCD application condition's own
		// message, quoted whole, on an ordinary 200 that the Dashboard calls
		// every time it opens. A ComparisonError condition is the one that
		// says "repository not accessible: authentication required" followed
		// by the repository address it was handed — token and all — so this
		// travelled to the browser with nothing having gone wrong. The prose
		// does not travel any more; the condition TYPE does, because that is
		// a closed enum, and so do the facts internal/credsafe will vouch for.
		errMsg := ""
		errType := ""
		for _, c := range app.Conditions {
			if errMsg == "" && c.Message != "" {
				errMsg = credsafe.SafeReportedDetail(true,
					credsafe.ArgocdAppConditionMessage, credsafe.OperationFacts{
						Phase:        app.OperationPhase,
						SyncStatus:   app.SyncStatus,
						HealthStatus: app.HealthStatus,
						RepoURL:      app.SourceRepoURL,
					})
				errType = credsafe.SafeConditionType(c.Type)
			}
		}

		if app.HealthStatus != "Healthy" || len(app.Conditions) > 0 {
			items = append(items, AttentionItem{
				AppName:   app.Name,
				AddonName: addonName,
				// B10: the two status words go through the same allow-lists
				// the cluster-comparison response uses.
				Cluster:   cluster,
				Health:    credsafe.SafeHealthStatus(app.HealthStatus),
				Sync:      credsafe.SafeSyncStatus(app.SyncStatus),
				Error:     errMsg,
				ErrorType: errType,
			})
		}
	}

	writeJSON(w, http.StatusOK, items)
}

// handleGetPullRequests godoc
//
// @Summary Get pull requests
// @Description Returns open pull requests from the GitOps repository
// @Tags dashboard
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Pull requests"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /dashboard/pull-requests [get]
func (s *Server) handleGetPullRequests(w http.ResponseWriter, r *http.Request) {
	defer observeDashboardRead(r, &w, "pull_requests")()

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	resp, err := s.dashboardSvc.GetPullRequests(r.Context(), gp)
	if err != nil {
		// Upstream call (Git provider): classify.
		writeUpstreamError(w, "dashboard_pull_requests", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
