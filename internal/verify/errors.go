package verify

import (
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/MoranWeissman/sharko/internal/credsafe"
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
//
// # Why it unwraps the credentials marker first
//
// A credentials-backend error's Error() is the one fixed safe sentence, so the
// string arms below would see that sentence and classify every credential
// failure the same way. credsafe.Cause steps past the marker to the real error
// underneath, which is what the arms were written against.
//
// That is safe HERE and only here: this function reads the words to pick one of
// a fixed set of codes and echoes not one character of them. The same is true of
// AssumeRoleHint below, which chooses between Sharko-written hints. Anything
// that builds a MESSAGE must call credsafe.Sentence instead.
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
	msg := credsafe.Cause(err).Error()
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
	// ERR_NAMESPACE (tightened — error review package 2): a bare "namespace"
	// substring used to be enough to classify here, which misfired on almost
	// any Kubernetes error that happens to mention a namespace in passing
	// (e.g. "listing pods in namespace sharko-system: connection refused" —
	// that's a network failure, not a namespace problem). Now this arm
	// requires either the admission-webhook phrase, or "namespace" PLUS a
	// genuine not-found/already-exists/terminating signal, so a namespace
	// mention alone falls through to ERR_UNKNOWN (or an earlier, more
	// specific arm) instead of being misclassified.
	case strings.Contains(msg, "admission webhook"),
		strings.Contains(lower, "namespace") && strings.Contains(lower, "not found"),
		strings.Contains(lower, "namespace") && strings.Contains(lower, "already exists"),
		strings.Contains(lower, "namespace") && strings.Contains(lower, "terminating"):
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

// ── the safe sentence catalog ───────────────────────────────────────────

// SafeMessage is a finished sentence from the catalog below, and the only
// thing this package hands out as a message for a person.
//
// # Why it is a type and not a string
//
// The product owner asked for "an API that makes unsafe construction
// difficult" rather than a rule somebody has to remember. A string return type
// made the unsafe thing the DEFAULT: the old FriendlyMessage returned one, and
// what it returned was the raw error text with a hint glued on. Nothing in the
// signature said that was wrong, and the doc comment above it described the
// leak as the design ("<raw cause> — <hint>").
//
// SafeMessage carries an unexported field, so no package outside this one can
// build one. The only way to get one is FriendlyMessage, and FriendlyMessage
// takes an ErrorCode — a closed set of ten constants. There is no parameter
// for raw text to ride in on. A caller who wants to put a cluster's own words
// in front of a person now has to write code that visibly does that, instead
// of getting it by accident.
type SafeMessage struct{ sentence string }

// String renders the sentence. Satisfying fmt.Stringer is what lets the call
// sites keep using %s without reaching for anything raw.
func (m SafeMessage) String() string { return m.sentence }

// safeSentences is the catalog: one complete, Sharko-written sentence per
// ErrorCode. Every character a person reads about a failed connectivity check
// is declared right here.
//
// Each entry says two things on purpose — what went wrong, and which category
// of thing to go and look at. That second half is the counterweight: the raw
// Kubernetes text is gone, and a bare "something went wrong" would trade one
// defect for another, so no entry is allowed to stop at the first half.
//
// The raw cause is not thrown away. It goes on Result.diagnostic, which is
// unexported and therefore unreachable by encoding/json, and the handlers log
// it server-side against the request id.
//
// TestSafeSentences_EveryDeclaredErrorCodeHasOne parses the const block above
// and fails BY NAME on any code missing an entry — a LIST, not a count, so a
// new code cannot ship with a blank message.
var safeSentences = map[ErrorCode]string{
	ERR_NETWORK:    "Sharko could not reach this cluster's API server over the network. Check that the server address in this cluster's connection details is correct and that the API server is reachable from Sharko.",
	ERR_TLS:        "Sharko reached this cluster's API server but could not verify its certificate. Check that the certificate authority in this cluster's connection details matches the one the API server presents.",
	ERR_AUTH:       "This cluster rejected Sharko's credentials (HTTP 401). The token may be expired or invalid — regenerate the kubeconfig or token for this cluster and try again.",
	ERR_RBAC:       "This cluster accepted Sharko's credentials but refused the action (HTTP 403). Grant Sharko's service account the RBAC permissions it needs on this cluster and try again.",
	ERR_AWS_STS:    "AWS would not mint a token for this cluster. Check that Sharko's identity is allowed to mint tokens for it and that the cluster's region is set correctly.",
	ERR_AWS_ASSUME: "Sharko could not assume the role configured for this cluster. Check the role's trust policy, that Sharko's identity has sts:AssumeRole on it, and that sts:TagSession is granted if you use EKS Pod Identity.",
	ERR_QUOTA:      "This cluster's API server is rate-limiting Sharko's requests. Wait for the limit to reset and run the check again.",
	ERR_NAMESPACE:  "Sharko could not use its test namespace on this cluster. Check that the namespace is not stuck terminating and that no admission webhook is blocking it.",
	ERR_TIMEOUT:    "This cluster's API server did not answer in time. Check that it is reachable from Sharko and not overloaded, then run the check again.",
	ERR_UNKNOWN:    "Sharko's connectivity check on this cluster did not finish. The server log for this request id names the step that failed and the reason the cluster gave.",
}

// FriendlyMessage returns the catalog sentence for a classified failure.
//
// It takes the CODE — not the Result, not the error. That is the whole point:
// with no error and no string in the signature, there is nothing for raw
// provider, credential-store, Git or Kubernetes text to ride in on, and "never
// put err.Error() in a response message" stops being a rule someone can forget
// and becomes a thing that cannot be written.
//
// Both the register-preview and adopt call sites use this helper, so their
// wording still cannot drift apart. An unrecognised code — including stage 2's
// ERR_NOT_IMPLEMENTED — renders as ERR_UNKNOWN rather than as an empty string,
// so no path can produce a blank message.
//
// A bracketed machine token used to lead this string ("[ERR_AUTH] ...") —
// retired (error review package 2): a machine code has no business leading
// a sentence meant for a person, and a caller that needs the code has
// Result.ErrorCode directly rather than parsing it back out of prose.
func FriendlyMessage(code ErrorCode) SafeMessage {
	if sentence, ok := safeSentences[code]; ok {
		return SafeMessage{sentence: sentence}
	}
	return SafeMessage{sentence: safeSentences[ERR_UNKNOWN]}
}

// AssumeRoleHint returns a cause-specific hint for an assume-role failure by
// matching known AWS error patterns. This is narrower than the generic
// ERR_AWS_ASSUME hint and distinguishes the three most actionable sub-types:
// trust policy rejection, missing sts:AssumeRole permission, and missing
// sts:TagSession permission. When the failure doesn't clearly match any
// sub-type, it falls back to the combined generic hint from Hint(ERR_AWS_ASSUME).
//
// Like ClassifyError, it steps past the credentials marker with credsafe.Cause
// so it reads the real AWS message rather than the fixed safe sentence. Every
// string it can RETURN is written here in this file — no part of the AWS
// message is ever echoed — which is what makes reading it acceptable.
func AssumeRoleHint(err error) string {
	if err == nil {
		return ""
	}
	msg := credsafe.Cause(err).Error()
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
