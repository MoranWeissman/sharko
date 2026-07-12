package verify

import (
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrorCode classifies a connectivity test failure.
type ErrorCode string

const (
	ERR_NETWORK    ErrorCode = "ERR_NETWORK"
	ERR_TLS        ErrorCode = "ERR_TLS"
	ERR_AUTH       ErrorCode = "ERR_AUTH"
	ERR_RBAC       ErrorCode = "ERR_RBAC"
	ERR_AWS_STS    ErrorCode = "ERR_AWS_STS"
	ERR_AWS_ASSUME ErrorCode = "ERR_AWS_ASSUME"
	ERR_QUOTA      ErrorCode = "ERR_QUOTA"
	ERR_NAMESPACE  ErrorCode = "ERR_NAMESPACE"
	ERR_TIMEOUT    ErrorCode = "ERR_TIMEOUT"
	ERR_UNKNOWN    ErrorCode = "ERR_UNKNOWN"
)

// ClassifyError determines the appropriate ErrorCode for a connectivity
// failure.
//
// Classification is two-tiered:
//
//  1. HTTP status (primary). When the error is a typed Kubernetes API error
//     (e.g. returned straight from client-go), we read its Status code via
//     apierrors.IsUnauthorized / apierrors.IsForbidden. This is robust — it
//     does not depend on the exact wording of the server's message, which
//     varies between client-go versions and cluster setups.
//
//  2. String match (fallback). For non-typed or wrapped errors where the
//     typed Status is no longer reachable, we fall back to matching known
//     phrases. The ERR_AUTH fallback includes the literal client-go 401
//     message "the server has asked for the client to provide credentials"
//     and a case-insensitive "unauthorized" so a stringified 401 is still
//     classified as auth rather than ERR_UNKNOWN.
func ClassifyError(err error) ErrorCode {
	if err == nil {
		return ERR_UNKNOWN
	}

	// Primary: typed HTTP status. Robust against message wording changes.
	if apierrors.IsUnauthorized(err) {
		return ERR_AUTH
	}
	if apierrors.IsForbidden(err) {
		return ERR_RBAC
	}

	// Fallback: string match for non-typed / wrapped errors.
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp"):
		return ERR_NETWORK
	case strings.Contains(msg, "x509") || strings.Contains(msg, "certificate"):
		return ERR_TLS
	case strings.Contains(lower, "unauthorized") ||
		strings.Contains(msg, "token expired") ||
		strings.Contains(msg, "the server has asked for the client to provide credentials"):
		return ERR_AUTH
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "Forbidden"):
		return ERR_RBAC
	case strings.Contains(msg, "GetToken") || strings.Contains(msg, "no identity provider"):
		return ERR_AWS_STS
	case strings.Contains(msg, "AssumeRole") || strings.Contains(msg, "not authorized to assume"):
		return ERR_AWS_ASSUME
	case strings.Contains(msg, "throttl") || strings.Contains(msg, "Too many requests"):
		return ERR_QUOTA
	case strings.Contains(msg, "admission webhook") || strings.Contains(msg, "namespace"):
		return ERR_NAMESPACE
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return ERR_TIMEOUT
	default:
		return ERR_UNKNOWN
	}
}

// Hint returns a plain, generic, actionable next step for an ErrorCode, or the
// empty string when no specific guidance applies. Hints are intentionally
// generic — they describe what the operator should do, not how any particular
// environment generates its credentials.
func Hint(code ErrorCode) string {
	switch code {
	case ERR_AUTH:
		return "the cluster rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the kubeconfig/token and try again."
	case ERR_RBAC:
		return "the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."
	case ERR_AWS_STS:
		return "AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."
	case ERR_AWS_ASSUME:
		return "the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."
	default:
		return ""
	}
}

// FriendlyMessage builds the single, consistent human-facing message for a
// failed verification Result. It appends an actionable hint when one exists for
// the result's ErrorCode, while always preserving the raw error cause
// (ErrorMessage) for diagnosis. Both the register-preview and adopt call sites
// use this helper so their wording can never drift apart.
//
// Shape: "[ERR_AUTH] <raw cause> — <hint>" when a hint exists, otherwise
// "[ERR_AUTH] <raw cause>".
func FriendlyMessage(r Result) string {
	base := "[" + string(r.ErrorCode) + "] " + r.ErrorMessage
	if hint := Hint(r.ErrorCode); hint != "" {
		return base + " — " + hint
	}
	return base
}

// AssumeRoleHint returns a cause-specific hint for an assume-role failure by
// matching known AWS error patterns. This is narrower than the generic
// ERR_AWS_ASSUME hint and distinguishes the three most actionable sub-types:
// trust policy rejection, missing sts:AssumeRole permission, and missing
// sts:TagSession permission. When the failure doesn't clearly match any
// sub-type, it falls back to the combined generic hint from Hint(ERR_AWS_ASSUME).
func AssumeRoleHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	// sts:TagSession denial — EKS Pod Identity sessions require session tags
	// Check this first since it's the most specific pattern
	if strings.Contains(msg, "TagSession") || strings.Contains(msg, "sts:TagSession") {
		return "Sharko's identity lacks sts:TagSession permission — EKS Pod Identity sessions carry session tags, so grant both sts:AssumeRole and sts:TagSession on this role."
	}

	// Trust policy rejection — the role won't be assumed by Sharko's identity
	// "not authorized to assume" is the clearest signal
	if strings.Contains(msg, "not authorized to assume") {
		return "The role's trust policy does not allow Sharko's identity to assume it — update the role's trust policy to trust Sharko's IAM principal."
	}

	// AccessDenied on AssumeRole with "not authorized to perform" and "sts:AssumeRole"
	// This pattern (AccessDenied + perform + AssumeRole) typically indicates a trust policy issue
	if (strings.Contains(lower, "accessdenied") || strings.Contains(lower, "access denied")) &&
		strings.Contains(lower, "not authorized to perform") &&
		strings.Contains(lower, "sts:assumerole") {
		return "The role's trust policy does not allow Sharko's identity to assume it — update the role's trust policy to trust Sharko's IAM principal."
	}

	// Fallback to the generic ERR_AWS_ASSUME hint
	return Hint(ERR_AWS_ASSUME)
}
