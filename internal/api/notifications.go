package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/authz"
)

// handleListNotifications godoc
// @Summary List notifications
// @Description Returns recent notifications (upgrades, drift, security)
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /notifications [get]
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notificationStore == nil {
		setPaginationHeaders(w, 0, parsePagination(r))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"notifications": []interface{}{},
			"unread_count":  0,
		})
		return
	}
	all := s.notificationStore.List()
	p := parsePagination(r)
	setPaginationHeaders(w, len(all), p)
	paged := applyPagination(all, p)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"notifications": paged,
		"unread_count":  s.notificationStore.UnreadCount(),
	})
}

// handleMarkAllNotificationsRead godoc
// @Summary Mark all notifications as read
// @Description Marks all notifications as read
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /notifications/read-all [post]
func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	// Mutates shared, platform-wide notification state. No dedicated
	// notification.* action exists in the table; gate it at operator level via
	// the closest existing operational-housekeeping action so a read-only
	// viewer cannot flip everyone's notifications to read.
	if !authz.RequireWithResponse(w, r, "reconciler.trigger") {
		return
	}
	if s.notificationStore != nil {
		s.notificationStore.MarkAllRead()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}
