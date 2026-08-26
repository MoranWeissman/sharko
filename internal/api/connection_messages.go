package api

// connection_messages.go — the JSON body of a failed connection test
// (v4-wave2 8.1).
//
// The connection test endpoints (POST /connections/test,
// POST /connections/test-credentials, POST /providers/test,
// POST /providers/test-config) previously returned err.Error() verbatim on
// failure — raw Go/HTTP-client/AWS-SDK/K8s-client text ("dial tcp: connect:
// connection refused", "unexpected status 404"). That is not a message an
// operator who isn't a Go developer can act on. So the failure is reclassified
// through the same verify.ClassifyError machinery internal/verify already uses
// for cluster connectivity checks (Stage1/Stage2, doctor, diagnose), and the
// body carries one plain sentence naming what failed plus an actionable next
// step — never the raw error text.
//
// THE WORDS ARE NOT HERE. Story P2c moved every fragment, hint, join and tail
// into connection_failure_messages.go, which renders the FINISHED sentence and
// catalogues every sentence it can produce. This file assembles nothing: it
// asks for the sentence and puts it on the wire beside the identifier that
// names it. TestConnectionMessages_HoldNoProse fails if a half-sentence ever
// comes back to this file.

import (
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// connectionErrorFields builds the JSON body for a failed connection test.
//
// The wire carries the COMPLETED sentence as "message" (unchanged, so existing
// UI reads of `.message` keep working) plus "message_id", the stable
// identifier that names it — so the browser can route on the message without
// ever matching on its text, and without ever assembling it. Then the same
// cause/hint/code fields the top-level error boundary uses (error review
// package 2). Omitted (not sent as "") when there's nothing to say, matching
// shapeError.
//
// err is nil here only means "nothing failed" is unreachable — callers only
// invoke this once they already know the test failed, but it's still
// defensive: nil in means a plain "ok" body out.
func connectionErrorFields(kind connectionKind, err error) map[string]interface{} {
	if err == nil {
		return map[string]interface{}{"status": "ok"}
	}
	failureCode := verify.ClassifyError(err)
	msg := renderConnectionFailure(kind, failureCode)
	body := map[string]interface{}{
		"status":     "error",
		"message":    msg,
		"message_id": connectionFailureIDFor(kind, failureCode),
	}
	_, cause, hint, code := shapeError(err, msg)
	// PUBLIC BOUNDARY. "cause" is the one field here that can carry an
	// underlying error's own text (classifyBoundaryError returns err.Error()
	// for its service.ErrValidation and Kubernetes StatusError branches). A
	// credentials-backend failure gets the fixed sentence instead; every other
	// kind of cause is untouched, so a git or ArgoCD test keeps its diagnosis.
	if cause != "" && credsafe.Is(err) {
		cause = credsafe.Message
	}
	// Review findings r1, M9: shapeError's hint comes from verify.Hint, which
	// is written for cluster-registration credentials ("regenerate the
	// kubeconfig/token", "the cluster accepted the credentials") — correct for
	// that flow, wrong for a Git or secrets-provider connection test. Take the
	// hint from the same catalog the message came from wherever that catalog
	// has words of its own for this (kind, code) pair; every other pair
	// (including "argocd", which has its own sentinel-based hints in
	// classifyBoundaryError) keeps shapeError's answer untouched.
	//
	// This used to ask only about ERR_AUTH. That left the Git 403 telling a
	// Git operator about "the cluster" in the hint field after the ruled
	// sentence had already stopped saying it in the message.
	if hint != "" && connectionFailureFamilyOwnsHint(kind, verify.ErrorCode(code)) {
		hint = connectionFailureHint(normalizeConnectionKind(kind), normalizeConnectionFailureCode(verify.ErrorCode(code)))
	}
	if cause != "" {
		body["cause"] = cause
	}
	if hint != "" {
		body["hint"] = hint
	}
	if code != "" {
		body["code"] = code
	}
	return body
}

// isKindAwareAuthKind reports whether a kind has its own ERR_AUTH wording, and
// so must not be handed verify.Hint's cluster-credential text through the hint
// FIELD (the message half is already handled by connectionFailureHint).
func isKindAwareAuthKind(kind connectionKind) bool {
	switch normalizeConnectionKind(kind) {
	case connectionKindGit, connectionKindVault, connectionKindSecrets:
		return true
	}
	return false
}
