package api

// connection_messages.go — plain-English failure messages for the
// connections-you-can-test surface (v4-wave2 8.1).
//
// The connection test endpoints (POST /connections/test,
// POST /connections/test-credentials, POST /providers/test,
// POST /providers/test-config) previously returned err.Error() verbatim on
// failure — raw Go/HTTP-client/AWS-SDK/K8s-client text ("dial tcp: connect:
// connection refused", "unexpected status 404"). That is not a message an
// operator who isn't a Go developer can act on. plainConnectionError
// reclassifies the failure through the same verify.ClassifyError /
// verify.Hint machinery internal/verify already uses for cluster
// connectivity checks (Stage1/Stage2, doctor, diagnose) and returns one
// plain sentence naming what failed plus an actionable next step — never
// the raw error text.

import (
	"github.com/MoranWeissman/sharko/internal/verify"
)

// connectionErrorFields builds the JSON body for a failed connection test —
// the plain sentence from plainConnectionError as "message" (unchanged, so
// existing UI reads of `.message` keep working), plus the same cause/hint/
// code fields the top-level error boundary uses (error review package 2).
// Omitted (not sent as "") when there's nothing to say, matching shapeError.
//
// err is nil here only means "nothing failed" is unreachable — callers only
// invoke this once they already know the test failed, but it's still
// defensive: nil in means a plain "ok" body out.
func connectionErrorFields(kind string, err error) map[string]interface{} {
	if err == nil {
		return map[string]interface{}{"status": "ok"}
	}
	msg := plainConnectionError(kind, err)
	body := map[string]interface{}{"status": "error", "message": msg}
	_, cause, hint, code := shapeError(err, msg)
	// Review findings r1, M9: shapeError's hint for ERR_AUTH comes from
	// verify.Hint, which is written for cluster-registration credentials
	// ("regenerate the kubeconfig/token") — correct for that flow, wrong for
	// a Git or secrets-provider connection test. Override with a kind-aware
	// hint for exactly those two kinds; every other kind (including
	// "argocd", which has its own sentinel-based hints above) keeps
	// shapeError's answer untouched.
	if hint != "" && code == string(verify.ERR_AUTH) && (kind == "git" || kind == "vault" || kind == "secrets") {
		hint = kindAwareAuthHint(kind)
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

// kindAwareAuthHint returns the ERR_AUTH hint text for a Git or
// secrets-provider ("vault"/"secrets") connection kind — see
// connectionErrorFields and plainConnectionError, the two places verify.Hint's
// cluster-credential wording would otherwise leak into a non-cluster
// connection kind (review findings r1, M9).
func kindAwareAuthHint(kind string) string {
	switch kind {
	case "git":
		return "the Git host rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the Git token/PAT in Settings → Connections and try again."
	case "vault", "secrets":
		return "the secrets store rejected the credentials (HTTP 401) — check the secrets-provider credentials and try again."
	default:
		return verify.Hint(verify.ERR_AUTH)
	}
}

// plainConnectionError returns a plain-English, non-technical description
// of why a connection test failed. kind identifies which connection was
// being tested ("git", "argocd", or "vault" — the secrets/cluster-creds
// provider). Returns "" when err is nil (nothing to report).
func plainConnectionError(kind string, err error) string {
	if err == nil {
		return ""
	}

	var what string
	switch kind {
	case "git":
		what = "Sharko can't reach your Git host"
	case "argocd":
		what = "Sharko can't reach ArgoCD"
	case "vault":
		what = "Sharko can't reach your secrets store"
	default:
		what = "Sharko can't reach this connection"
	}

	code := verify.ClassifyError(err)
	// M9: kind-aware for ERR_AUTH (git/vault get their own wording, every
	// other kind falls through to verify.Hint's existing text below).
	if code == verify.ERR_AUTH && (kind == "git" || kind == "vault" || kind == "secrets") {
		return what + " — " + kindAwareAuthHint(kind)
	}
	if hint := verify.Hint(code); hint != "" {
		return what + " — " + hint
	}

	switch code {
	case verify.ERR_NETWORK:
		return what + " — the host could not be reached. Check the URL and network access."
	case verify.ERR_TLS:
		return what + " — a certificate error occurred while connecting. Check the server's TLS configuration."
	case verify.ERR_TIMEOUT:
		return what + " — the request timed out. Check the URL and network access."
	default:
		return what + ". Check the connection settings and try again."
	}
}
