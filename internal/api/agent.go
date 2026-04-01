package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/moran/argocd-addons-platform/internal/ai"
)

// agentSession wraps an agent with creation time for cleanup.
type agentSession struct {
	agent     *ai.Agent
	createdAt time.Time
}

const (
	agentSessionMaxAge   = 1 * time.Hour
	agentSessionMaxCount = 100
)

// agentSessions stores per-session agents (in-memory, simple approach).
var (
	agentSessions = make(map[string]*agentSession)
	agentMu       sync.Mutex
)

func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"session_id"`
		Message     string `json:"message"`
		PageContext string `json:"page_context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Prepend page context to help the agent understand where the user is
	message := req.Message
	if req.PageContext != "" {
		message = fmt.Sprintf("[User is currently viewing %s]\n\n%s", req.PageContext, req.Message)
	}

	// Get or create agent session
	agentMu.Lock()

	// Prune expired sessions and enforce max count
	pruneAgentSessions()

	sess, exists := agentSessions[req.SessionID]
	if !exists || req.SessionID == "" {
		// Create new agent with active connection's providers
		gp, err := s.connSvc.GetActiveGitProvider()
		if err != nil {
			agentMu.Unlock()
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		ac, err := s.connSvc.GetActiveArgocdClient()
		if err != nil {
			agentMu.Unlock()
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		executor := ai.NewToolExecutor(gp, ac, s.agentMemory, nil)
		agent := ai.NewAgent(s.aiClient, executor, s.agentMemory)

		if req.SessionID == "" {
			req.SessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
		}
		sess = &agentSession{agent: agent, createdAt: time.Now()}
		agentSessions[req.SessionID] = sess
	}
	agentMu.Unlock()

	response, err := sess.agent.Chat(r.Context(), message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"session_id": req.SessionID,
		"response":   response,
	})
}

func (s *Server) handleAgentReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	agentMu.Lock()
	if sess, exists := agentSessions[req.SessionID]; exists {
		sess.agent.Reset()
	}
	agentMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// pruneAgentSessions removes expired sessions and evicts oldest if over cap.
// Must be called with agentMu held.
func pruneAgentSessions() {
	now := time.Now()

	// Remove expired sessions
	for id, sess := range agentSessions {
		if now.Sub(sess.createdAt) > agentSessionMaxAge {
			delete(agentSessions, id)
		}
	}

	// If still over cap, evict oldest
	if len(agentSessions) > agentSessionMaxCount {
		type entry struct {
			id        string
			createdAt time.Time
		}
		entries := make([]entry, 0, len(agentSessions))
		for id, sess := range agentSessions {
			entries = append(entries, entry{id: id, createdAt: sess.createdAt})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].createdAt.Before(entries[j].createdAt)
		})
		toRemove := len(agentSessions) - agentSessionMaxCount
		for i := 0; i < toRemove; i++ {
			delete(agentSessions, entries[i].id)
		}
	}
}
