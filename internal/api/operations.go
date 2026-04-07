package api

import (
	"net/http"
)

// handleGetOperation godoc
//
// @Summary Get operation status
// @Description Returns the current status and step progress of a long-running operation
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param id path string true "Operation session ID"
// @Success 200 {object} map[string]interface{} "Operation session"
// @Failure 404 {object} map[string]interface{} "Operation not found"
// @Router /operations/{id} [get]
func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.opsStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

// handleOperationHeartbeat godoc
//
// @Summary Send operation heartbeat
// @Description Records a client heartbeat so the server knows the client is still polling.
// @Description Without heartbeats, a waiting operation may be abandoned.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param id path string true "Operation session ID"
// @Success 200 {object} map[string]interface{} "Heartbeat recorded"
// @Failure 404 {object} map[string]interface{} "Operation not found"
// @Router /operations/{id}/heartbeat [post]
func (s *Server) handleOperationHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.opsStore.Heartbeat(id) {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCancelOperation godoc
//
// @Summary Cancel an operation
// @Description Cancels a pending or waiting operation. Running operations may not stop immediately.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param id path string true "Operation session ID"
// @Success 200 {object} map[string]interface{} "Operation cancelled"
// @Failure 404 {object} map[string]interface{} "Operation not found"
// @Router /operations/{id}/cancel [post]
func (s *Server) handleCancelOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.opsStore.Cancel(id) {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
