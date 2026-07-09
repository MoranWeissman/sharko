package orchestrator

import "errors"

// InlineCredentialsDisabledError is returned by RegisterCluster when the
// server-wide allow_inline_credentials setting is false and the request
// actually supplied inline kubeconfig bytes (V2-cleanup-89.6). It is an
// admin-policy rejection, not a malformed-request error — mirrors the
// *InvalidCredsSourceError / *AddonNotInCatalogError pattern: a typed error
// plus an Is-helper the API layer uses to choose a 4xx status without
// string-matching.
type InlineCredentialsDisabledError struct{}

func (e *InlineCredentialsDisabledError) Error() string {
	return "inline credential paste is disabled on this server — point at your secret store instead, or ask your admin to enable allow_inline_credentials"
}

// IsInlineCredentialsDisabled reports whether err is (or wraps) an
// *InlineCredentialsDisabledError. The API layer uses this to choose a 403
// status (an admin policy blocked the request, not bad input).
func IsInlineCredentialsDisabled(err error) bool {
	var target *InlineCredentialsDisabledError
	return errors.As(err, &target)
}
