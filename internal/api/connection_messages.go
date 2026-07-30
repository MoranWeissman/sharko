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
