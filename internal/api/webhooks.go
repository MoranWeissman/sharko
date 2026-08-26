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
//
// IT DELIBERATELY HAS NOWHERE TO PUT THE SENDER'S TEXT. There used to be a
// Pusher.Name and a Commits[].Message here, and both were copied straight into
// an audit record — so the sender of a request decided what the audit log said
// an actor was called, and what it said had been done. Neither field is parsed
// any more. Commits is a list of empty structs because the only thing read off
// it is how many there are, and a shape with no fields cannot carry a sentence
// into anything downstream even if a future edit tries.
type gitHubPushEvent struct {
	Ref     string     `json:"ref"`
	Commits []struct{} `json:"commits"`
}

// webhookRefusal is the ONE thing a refused caller is ever told, and it is
// the same words for every reason a call is refused.
//
// THREE CASES COLLAPSE INTO THIS SENTENCE ON PURPOSE. No shared secret has
// been configured; a signature header is absent; a signature header is present
// and does not match. If those answered differently, an outsider could tell
// which case they were in — and "no secret configured" is the one answer that
// must never be obtainable, because it says the endpoint is unprotected and
// says it to anybody who asks. One sentence for all three means the response
// carries no information about how the server is set up.
//
// It also says nothing about the secret itself — not its length, not whether
// one exists, not any part of its value. The sentence is a constant; there is
// no formatting verb anywhere near it and nothing derived from configuration
// can reach it.
const webhookRefusal = "the Git webhook accepts only a request signed with the shared secret set by an operator"

// webhookAuditUser is the actor recorded for a webhook-sourced audit entry.
// It is a constant because a signed webhook has no person behind it that
// Sharko can name — see the audit call below.
const webhookAuditUser = "webhook"

// handleGitWebhook handles POST /api/v1/webhooks/git
//
// CLOSED UNLESS AN OPERATOR OPENS IT. This endpoint is the one route past the
// session/token check in basicAuthMiddleware, so the signature IS its
// authentication — there is nothing else in front of it. It used to verify a
// signature only when SHARKO_WEBHOOK_SECRET happened to be set, and the chart
// shipped that value empty, so a default install answered this route to
// anybody on the network. Anybody could therefore choose when Sharko pushed
// Secret material to remote clusters, and could write audit records carrying
// their own text.
//
// So the check is no longer conditional on configuration. With no shared
// secret the endpoint does no work at all and refuses every call. There is
// deliberately NO setting that turns the check off: a caller cannot switch
// safety off, and neither can an operator who leaves a value blank.
//
// @Summary Git webhook receiver
// @Description Receives GitHub push event webhooks. A request is accepted only when it
// @Description carries an HMAC-SHA256 signature matching the shared secret an operator
// @Description configured. With no shared secret configured the endpoint refuses every
// @Description request. An accepted push to the configured base branch triggers a refresh.
// @Tags system
// @Accept json
// @Produce json
// @Param X-Hub-Signature-256 header string true "HMAC-SHA256 signature of the request body"
// @Param X-GitHub-Event header string false "GitHub event type (push, ping, etc.)"
// @Param body body gitHubPushEvent false "GitHub push event payload"
// @Success 200 {object} map[string]string "Webhook accepted"
// @Failure 400 {object} map[string]string "Bad request or unreadable body"
// @Failure 401 {object} map[string]string "Request refused"
// @Router /webhooks/git [post]
func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	// Read body first — needed for HMAC verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	// Verify the HMAC-SHA256 signature. UNCONDITIONAL: an empty secret is not
	// "verification off", it is "nothing can ever match", and the request is
	// refused with the same words as a wrong signature.
	secret := os.Getenv("SHARKO_WEBHOOK_SECRET")
	sig := r.Header.Get("X-Hub-Signature-256")
	if secret == "" || sig == "" || !verifyGitHubSignature(body, sig, secret) {
		writeError(w, http.StatusUnauthorized, webhookRefusal)
		return
	}

	// GitHub sends a ping event on webhook creation — accept it gracefully.
	// This sits BEHIND the signature check, because a ping is signed too.
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
	baseBranch := s.gitopsConfig().BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	expectedRef := "refs/heads/" + baseBranch

	if event.Ref == expectedRef {
		// NOTHING FROM THE REQUEST BODY IS RECORDED AS TEXT, anywhere below.
		// The pusher name and the commit message used to travel from the
		// payload into the audit record's User and Resource fields, so
		// whoever sent the call chose what the audit log said an actor was
		// called and what it said had happened. A signature check in front
		// makes that harder to reach; it does not make it correct. Every
		// value recorded here is either a constant, a count Sharko computed,
		// or the branch name out of Sharko's own configuration.
		slog.Info("External push detected",
			"commits", len(event.Commits),
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

		// Record audit entry. User is the constant below rather than a name
		// off the payload: a signed webhook proves the sender holds the
		// shared secret, and nothing more — it does not establish who a
		// person is, so claiming one in the User column would be a made-up
		// identity sitting next to real ones. Resource names the branch out
		// of Sharko's configuration, and Detail is a count.
		s.auditLog.Add(audit.Entry{
			Level:    "info",
			Event:    "push",
			User:     webhookAuditUser,
			Action:   "push",
			Resource: "branch:" + baseBranch,
			Source:   audit.SourceWebhook,
			Result:   "success",
			Detail:   fmt.Sprintf("%d commit(s)", len(event.Commits)),
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
