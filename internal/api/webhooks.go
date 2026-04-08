package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// gitHubPushEvent is the minimal GitHub push event payload we need.
type gitHubPushEvent struct {
	Ref    string `json:"ref"`
	Pusher struct {
		Name string `json:"name"`
	} `json:"pusher"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
}

// handleGitWebhook handles POST /api/v1/webhooks/git
//
// @Summary Git webhook receiver
// @Description Receives GitHub push event webhooks. Verifies HMAC-SHA256 signature
// @Description when SHARKO_WEBHOOK_SECRET is set, then triggers a cache refresh on
// @Description pushes to the configured base branch.
// @Tags system
// @Accept json
// @Produce json
// @Param X-Hub-Signature-256 header string false "GitHub HMAC-SHA256 signature"
// @Param X-GitHub-Event header string false "GitHub event type (push, ping, etc.)"
// @Param body body gitHubPushEvent false "GitHub push event payload"
// @Success 200 {object} map[string]string "Webhook accepted"
// @Failure 400 {object} map[string]string "Bad request or unreadable body"
// @Failure 401 {object} map[string]string "Invalid signature"
// @Router /webhooks/git [post]
func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	// Read body first — needed for HMAC verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	// Verify HMAC-SHA256 signature if a secret is configured.
	secret := os.Getenv("SHARKO_WEBHOOK_SECRET")
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if sig == "" {
			writeError(w, http.StatusUnauthorized, "missing X-Hub-Signature-256 header")
			return
		}
		if !verifyGitHubSignature(body, sig, secret) {
			writeError(w, http.StatusUnauthorized, "invalid webhook signature")
			return
		}
	}

	// GitHub sends a ping event on webhook creation — accept it gracefully.
	if r.Header.Get("X-GitHub-Event") == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}

	// Parse push event payload.
	var event gitHubPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid push event payload")
		return
	}

	// Determine the base branch to watch (mirrors gitopsCfg.BaseBranch; default "main").
	baseBranch := s.gitopsCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	expectedRef := "refs/heads/" + baseBranch

	if event.Ref == expectedRef {
		pusher := event.Pusher.Name
		if pusher == "" {
			pusher = "unknown"
		}
		slog.Info("External push detected",
			"commits", len(event.Commits),
			"pusher", pusher,
			"branch", baseBranch,
		)

		// Trigger cache refresh so the next read picks up the latest state.
		// The services fetch data directly from Git on every request (no persistent
		// cache today), so the log is sufficient to signal the event. A dedicated
		// cache layer can hook in here later.

		// Trigger secret reconciliation on catalog changes.
		if s.secretReconciler != nil {
			s.secretReconciler.Trigger()
			slog.Info("[webhooks] triggered secret reconcile from push event")
		}

		// Record audit entry.
		details := fmt.Sprintf("%d commit(s) pushed to %s", len(event.Commits), baseBranch)
		if len(event.Commits) > 0 {
			details = fmt.Sprintf("%s — %s", details, event.Commits[0].Message)
		}
		s.auditLog.Add(audit.Entry{
			Source:  "webhook",
			Action:  "push",
			Actor:   pusher,
			Details: details,
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// verifyGitHubSignature verifies the HMAC-SHA256 signature sent by GitHub in the
// X-Hub-Signature-256 header.  It uses a constant-time comparison to prevent
// timing attacks.
func verifyGitHubSignature(payload []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
