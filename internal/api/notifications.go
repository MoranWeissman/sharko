package api

import (
	"net/http"
)

// handleListNotifications godoc
// @Summary List notifications
// @Description Returns recent notifications (upgrades, drift, security)
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /notifications [get]
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notificationStore == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"notifications": []interface{}{},
			"unread_count":  0,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"notifications": s.notificationStore.List(),
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
// @Router /notifications/read-all [post]
func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if s.notificationStore != nil {
		s.notificationStore.MarkAllRead()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}
