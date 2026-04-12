package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// handleListAuditLog godoc
//
// @Summary List audit log entries
// @Description Returns recent audit log entries (webhook pushes, cluster registrations, secret reconciliations, init runs).
// @Description Entries are ordered newest-first. Supports filtering by user, action, source, result, cluster, and time range.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param user query string false "Filter by user"
// @Param action query string false "Filter by action"
// @Param source query string false "Filter by source (api, webhook, reconciler, etc.)"
// @Param result query string false "Filter by result (success, failure, partial)"
// @Param since query string false "Filter entries after this RFC3339 timestamp"
// @Param cluster query string false "Filter by cluster name (matches cluster:NAME in resource)"
// @Param limit query int false "Maximum number of entries to return (default 50)"
// @Success 200 {object} map[string]interface{} "Audit log entries"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /audit [get]
func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := audit.AuditFilter{
		User:   q.Get("user"),
		Action: q.Get("action"),
		Source: q.Get("source"),
		Result: q.Get("result"),
	}

	if raw := q.Get("since"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.Since = t
		}
	}

	filter.Cluster = q.Get("cluster")

	filter.Limit = 50
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	entries := s.auditLog.ListFiltered(filter)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}

// handleAuditStream godoc
//
// @Summary Stream audit log entries via SSE
// @Description Opens a Server-Sent Events stream that pushes each new audit entry as it is recorded.
// @Description The connection stays open until the client disconnects.
// @Tags system
// @Produce text/event-stream
// @Security BearerAuth
// @Success 200 {string} string "SSE stream of audit entries"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /audit/stream [get]
func (s *Server) handleAuditStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.auditLog.Subscribe()
	defer unsub()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry := <-ch:
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
