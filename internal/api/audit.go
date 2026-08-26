package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// auditLogResponse is the body of GET /audit.
//
// IT EXISTS BECAUSE THE HANDLER USED TO SHIP AN UNTYPED MAP. The endpoint
// declared `map[string]interface{}` and wrote a bare map, so the published
// OpenAPI spec described the response as an object with arbitrary
// properties and carried no audit.Entry definition at all. Every field of
// every audit entry was undocumented — including `changes`, the field ruling
// (f) added so a reader can tell "this operation wrote something" from "this
// operation deliberately wrote nothing" from "this was a read-only check".
// The field was real on the wire and invisible in the contract, which is the
// same as not shipping it for anyone writing a client from the spec.
//
// Naming the type is the whole fix: swag then walks audit.Entry and emits
// every field, and the enums tag on Changes publishes its three values.
type auditLogResponse struct {
	// Entries are the matching audit entries, newest first.
	Entries []audit.Entry `json:"entries"`
	// Count is len(Entries) — how many entries this response carries, NOT
	// how many exist. The ring buffer holds a bounded window and the query
	// applies a limit.
	Count int `json:"count"`
}

// handleListAuditLog godoc
//
// @Summary List audit log entries
// @Description Returns recent audit log entries (webhook pushes, cluster registrations, secret reconciliations, init runs).
// @Description Entries are ordered newest-first. Supports filtering by user, action, source, result, cluster, and time range.
// @Description Each entry carries a "changes" field saying whether the operation actually changed anything: "applied" (something was written), "none" (it ran and deliberately wrote nothing) or "not_applicable" (a read-only check, which neither changed anything nor failed to). The field is absent on entries recorded before it existed, and an absent value means "not stated" — never "no changes made".
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param user query string false "Filter by user"
// @Param action query string false "Filter by action"
// @Param source query string false "Filter by source (\"api\", \"webhook\", \"reconciler\", etc.)"
// @Param result query string false "Filter by result (success, failure, partial)"
// @Param since query string false "Filter entries after this RFC3339 timestamp"
// @Param cluster query string false "Filter by cluster name (matches cluster:NAME in resource)"
// @Param limit query int false "Maximum number of entries to return (default 50)"
// @Success 200 {object} auditLogResponse "Audit log entries"
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
	if entries == nil {
		// ListFiltered returns a nil slice when nothing matched. A nil slice
		// marshals to null, and the declared type says array — so an empty
		// result is an empty array, matching what the spec now promises.
		entries = []audit.Entry{}
	}
	writeJSON(w, http.StatusOK, auditLogResponse{
		Entries: entries,
		Count:   len(entries),
	})
}

// handleAuditStream godoc
//
// @Summary Stream audit log entries via SSE
// @Description Opens a Server-Sent Events stream that pushes each new audit entry as it is recorded.
// @Description The connection stays open until the client disconnects.
// @Description Each SSE "data:" line is one JSON audit entry with exactly the shape below — the same shape GET /audit returns in its entries array, "changes" field included.
// @Tags system
// @Produce text/event-stream
// @Security BearerAuth
// @Success 200 {object} audit.Entry "One JSON audit entry per SSE data line"
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
