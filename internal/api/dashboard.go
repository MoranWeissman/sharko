package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
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

// handleGetAttentionItems godoc
//
// @Summary Get attention items
// @Description Returns ArgoCD applications that are unhealthy or have conditions requiring attention
// @Tags dashboard
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Attention items"
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
	apps, err := ac.ListApplications(ctx)
	if err != nil {
		// Upstream call (ArgoCD): classify so an ArgoCD timeout reads as
		// 504 rather than 500.
		writeUpstreamError(w, "dashboard_attention", err)
		return
	}

	type AttentionItem struct {
		AppName    string `json:"app_name"`
		AddonName  string `json:"addon_name"`
		Cluster    string `json:"cluster"`
		Health     string `json:"health"`
		Sync       string `json:"sync"`
		Error      string `json:"error,omitempty"`
		ErrorType  string `json:"error_type,omitempty"`
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
		// Extract addon and cluster from app name (convention: addon-cluster)
		addonName := app.Name
		cluster := ""
		if app.DestinationName != "" {
			cluster = app.DestinationName
		}

		errMsg := ""
		errType := ""
		for _, c := range app.Conditions {
			if errMsg == "" {
				errMsg = c.Message
				errType = c.Type
			}
		}

		if app.HealthStatus != "Healthy" || len(app.Conditions) > 0 {
			items = append(items, AttentionItem{
				AppName:   app.Name,
				AddonName: addonName,
				Cluster:   cluster,
				Health:    app.HealthStatus,
				Sync:      app.SyncStatus,
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
