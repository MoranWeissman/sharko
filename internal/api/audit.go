package api

import (
	"net/http"
	"strconv"
)

// handleListAuditLog godoc
//
// @Summary List audit log entries
// @Description Returns recent audit log entries (webhook pushes, cluster registrations, secret reconciliations, init runs).
// @Description Entries are ordered newest-first. Use ?limit=N to restrict the number returned (default 50).
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Maximum number of entries to return (default 50)"
// @Success 200 {object} map[string]interface{} "Audit log entries"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /audit [get]
func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	entries := s.auditLog.List(limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}
