package api

import (
	"encoding/json"
	"net/http"
)

// handleCreateOperation godoc
//
// @Summary Create an operation session
// @Description Creates a new long-running operation session and returns its ID and initial state
// @Tags operations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Operation type and step names"
// @Success 201 {object} operations.Session "Session created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /operations [post]
func (s *Server) handleCreateOperation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type  string   `json:"type"`
		Steps []string `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}

	sess := s.opsStore.Create(req.Type, req.Steps)
	writeJSON(w, http.StatusCreated, sess)
}

// handleGetOperation godoc
//
// @Summary Get operation status
// @Description Returns the full state of an operation session by ID
// @Tags operations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Session ID"
// @Success 200 {object} operations.Session "Session state"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Not found"
// @Router /operations/{id} [get]
func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.opsStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleCancelOperation godoc
//
// @Summary Cancel an operation
// @Description Marks the operation session as cancelled
// @Tags operations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Session ID"
// @Success 200 {object} map[string]interface{} "Cancelled"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Not found"
// @Router /operations/{id} [delete]
func (s *Server) handleCancelOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, ok := s.opsStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	s.opsStore.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleOperationHeartbeat godoc
//
// @Summary Send operation heartbeat
// @Description Keeps the operation session alive. Sessions with no heartbeat for 2 minutes are removed.
// @Tags operations
// @Produce json
// @Security BearerAuth
// @Param id path string true "Session ID"
// @Success 200 {object} map[string]interface{} "Heartbeat acknowledged"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Not found"
// @Router /operations/{id}/heartbeat [post]
func (s *Server) handleOperationHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ok := s.opsStore.Heartbeat(id)
	if !ok {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"alive": true})
}
