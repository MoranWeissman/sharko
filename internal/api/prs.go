package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// PRListResponse is the response for GET /api/v1/prs.
type PRListResponse struct {
	PRs []PRItem `json:"prs"`
}

// PRItem is a single PR in list/detail responses.
type PRItem struct {
	PRID       int    `json:"pr_id"`
	PRUrl      string `json:"pr_url"`
	PRBranch   string `json:"pr_branch"`
	PRTitle    string `json:"pr_title"`
	PRBase     string `json:"pr_base"`
	Cluster    string `json:"cluster,omitempty"`
	Addon      string `json:"addon,omitempty"`
	Operation  string `json:"operation"`
	User       string `json:"user"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at"`
	LastStatus string `json:"last_status"`
	LastPolled string `json:"last_polled_at"`
}

// handleListPRs handles GET /api/v1/prs
//
// @Summary List tracked pull requests
// @Description Returns all tracked pull requests with optional filters
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param status query string false "Filter by status (open, merged, closed)"
// @Param cluster query string false "Filter by cluster name"
// @Param addon query string false "Filter by addon name"
// @Param user query string false "Filter by user"
// @Success 200 {object} PRListResponse "List of tracked PRs"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs [get]
func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.list") {
		return
	}

	if s.prTracker == nil {
		writeJSON(w, http.StatusOK, PRListResponse{PRs: []PRItem{}})
		return
	}

	status := r.URL.Query().Get("status")
	cluster := r.URL.Query().Get("cluster")
	addon := r.URL.Query().Get("addon")
	user := r.URL.Query().Get("user")

	prs, err := s.prTracker.ListPRs(r.Context(), status, cluster, addon, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]PRItem, 0, len(prs))
	for _, pr := range prs {
		items = append(items, PRItem{
			PRID:       pr.PRID,
			PRUrl:      pr.PRUrl,
			PRBranch:   pr.PRBranch,
			PRTitle:    pr.PRTitle,
			PRBase:     pr.PRBase,
			Cluster:    pr.Cluster,
			Addon:      pr.Addon,
			Operation:  pr.Operation,
			User:       pr.User,
			Source:     pr.Source,
			CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
			LastStatus: pr.LastStatus,
			LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, PRListResponse{PRs: items})
}

// handleGetPR handles GET /api/v1/prs/{id}
//
// @Summary Get a tracked pull request
// @Description Returns details for a single tracked PR
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} PRItem "PR details"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 404 {object} map[string]interface{} "PR not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id} [get]
func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.detail") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	pr, err := s.prTracker.GetPR(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pr == nil {
		writeError(w, http.StatusNotFound, "PR not tracked")
		return
	}

	writeJSON(w, http.StatusOK, PRItem{
		PRID:       pr.PRID,
		PRUrl:      pr.PRUrl,
		PRBranch:   pr.PRBranch,
		PRTitle:    pr.PRTitle,
		PRBase:     pr.PRBase,
		Cluster:    pr.Cluster,
		Operation:  pr.Operation,
		User:       pr.User,
		Source:     pr.Source,
		CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
		LastStatus: pr.LastStatus,
		LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
	})
}

// handleRefreshPR handles POST /api/v1/prs/{id}/refresh
//
// @Summary Force refresh a tracked PR
// @Description Immediately polls the Git provider for this PR's current status
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} PRItem "Updated PR details"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 404 {object} map[string]interface{} "PR not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id}/refresh [post]
func (s *Server) handleRefreshPR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.refresh") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	pr, err := s.prTracker.PollSinglePR(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "pr_refreshed",
		Resource: fmt.Sprintf("pr:%d", id),
	})
	writeJSON(w, http.StatusOK, PRItem{
		PRID:       pr.PRID,
		PRUrl:      pr.PRUrl,
		PRBranch:   pr.PRBranch,
		PRTitle:    pr.PRTitle,
		PRBase:     pr.PRBase,
		Cluster:    pr.Cluster,
		Operation:  pr.Operation,
		User:       pr.User,
		Source:     pr.Source,
		CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
		LastStatus: pr.LastStatus,
		LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
	})
}

// handleDeletePR handles DELETE /api/v1/prs/{id}
//
// @Summary Stop tracking a pull request
// @Description Removes a PR from tracking (admin only)
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} map[string]string "PR removed from tracking"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id} [delete]
func (s *Server) handleDeletePR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.delete") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	if err := s.prTracker.StopTracking(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "pr_deleted",
		Resource: fmt.Sprintf("pr:%d", id),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
