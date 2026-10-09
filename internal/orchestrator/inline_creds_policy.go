package orchestrator

import "errors"

// InlineCredentialsDisabledError is returned by RegisterCluster when the
// server-wide allow_inline_credentials setting is false (the default since
// the connection-reconciliation epic, product correction 5) and the request
// actually supplied inline kubeconfig bytes. It is an admin-policy
// rejection, not a malformed-request error — mirrors the
// *InvalidCredsSourceError / *AddonNotInCatalogError pattern: a typed error
// plus an Is-helper the API layer uses to choose a 4xx status without
// string-matching.
type InlineCredentialsDisabledError struct{}

// Error is the explicit, fixed refusal (product correction 5): plain
// English, names the supported credential providers, and says why the
// pasted path is off by default. Pinned by exact-equality tests — do not
// paraphrase.
func (e *InlineCredentialsDisabledError) Error() string {
	return "This server does not accept pasted credentials. A pasted credential would exist only inside the live connection and could never be restored from Git. Store the cluster's kubeconfig in a supported credentials provider instead — a Kubernetes Secret or AWS Secrets Manager — and register the cluster pointing at it. An admin can allow legacy inline credentials in Settings."
}

// IsInlineCredentialsDisabled reports whether err is (or wraps) an
// *InlineCredentialsDisabledError. The API layer uses this to choose a 403
// status (an admin policy blocked the request, not bad input).
func IsInlineCredentialsDisabled(err error) bool {
	var target *InlineCredentialsDisabledError
	return errors.As(err, &target)
}
