package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/notifications"
)

// NotificationListResponse is the body of GET /notifications.
//
// It is a declared struct rather than an inline map so the OpenAPI spec
// describes what a caller actually receives. The map version documented the
// response as an untyped object, which meant the one field a consumer must
// branch on — each notification's `code` — appeared nowhere in the spec and
// nowhere in a generated client. A stable identifier a client cannot discover
// is not much of a contract.
type NotificationListResponse struct {
	// Notifications is the requested page, newest first. Route on each
	// item's `code`; `title` and `description` are text to show a person and
	// may be reworded in any release.
	Notifications []notifications.Notification `json:"notifications"`
	// UnreadCount counts every unread notification, not just this page.
	UnreadCount int `json:"unread_count"`
}

// handleListNotifications godoc
// @Summary List notifications
// @Description Returns recent notifications (upgrades, drift, security, connection health).
// @Description Each notification carries a stable `code` — that is the field to branch on.
// @Description `title` and `description` are display text and may be reworded in any release,
// @Description so matching on their wording will break.
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} NotificationListResponse
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /notifications [get]
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notificationStore == nil {
		setPaginationHeaders(w, 0, parsePagination(r))
		writeJSON(w, http.StatusOK, NotificationListResponse{
			Notifications: []notifications.Notification{},
			UnreadCount:   0,
		})
		return
	}
	all := s.notificationStore.List()
	p := parsePagination(r)
	setPaginationHeaders(w, len(all), p)
	paged := applyPagination(all, p)
	writeJSON(w, http.StatusOK, NotificationListResponse{
		Notifications: paged,
		UnreadCount:   s.notificationStore.UnreadCount(),
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

// handleMarkNotificationRead godoc
// @Summary Mark a single notification as read
// @Description Marks one notification as read by id
// @Tags notifications
// @Produce json
// @Param id path string true "Notification ID"
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Notification not found"
// @Router /notifications/{id}/read [post]
func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	// Same authz stance as mark-all-read (S3, walk day 4): mutates shared,
	// platform-wide notification state, no dedicated notification.* action
	// exists, so it's gated at the closest existing operational action.
	if !authz.RequireWithResponse(w, r, "reconciler.trigger") {
		return
	}
	id := r.PathValue("id")
	if s.notificationStore == nil || !s.notificationStore.MarkRead(id) {
		writeError(w, http.StatusNotFound, "notification not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}
