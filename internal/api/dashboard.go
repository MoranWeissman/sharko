package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleGetDashboardStats(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.dashboardSvc.GetStats(r.Context(), gp, ac)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetAttentionItems(w http.ResponseWriter, r *http.Request) {
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	apps, err := ac.ListApplications(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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

func (s *Server) handleGetPullRequests(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.dashboardSvc.GetPullRequests(r.Context(), gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
