package argocd

// unreachable.go — the ONE error every ArgoCD call returns when Sharko never
// got an answer at all, and the boundary the dialled address does not cross
// (BF8).
//
// # What used to happen
//
// All four HTTP verbs did the same thing when the round trip failed:
//
//	return nil, fmt.Errorf("executing request to %s: %w", path, err)
//
// The %w is the problem, not the %s. net/http wraps every failed round trip in
// a *url.Error, and a *url.Error's text is the FULL address it dialled. So the
// wrapped error's words carried Sharko's ArgoCD server address, and every
// boundary that wrote err.Error() into a 502 body or an operation record
// handed that address to whoever was looking.
//
// # Why an address is credential material
//
// Because operators write credentials into it, and nothing stopped them:
//
//	https://<token>@argocd.example            the token IS the username
//	https://user:<token>@argocd.example       the token is the password
//	https://argocd.example?access_token=...   the token is a query parameter
//	https://argocd.example#<token>            the token is a fragment
//
// net/http rewrites the PASSWORD slot to "***" before it prints. It does
// nothing about the other three. So "net/http already redacts it" was true for
// one shape out of four, and the three it misses are the three where the whole
// credential travels in the clear.
//
// # What replaces it
//
// The same shape BF6 gave the write path's non-2xx branch: an error type of
// Sharko's own, whose every field Sharko itself chose. Verb is the HTTP method
// Sharko picked. Endpoint is the API path Sharko built out of a cluster name,
// an application name or a fixed string — never the base address, which is the
// only part the operator's credential can be in. Code is one of the constants
// below, all spelled out in this file.
//
// There is deliberately no field for the cause and no option that puts one
// back. A caller must not be able to switch this off, and a cause reachable
// through Unwrap is one fmt.Errorf away from being printed again.
//
// # What is NOT lost
//
// Three things a caller could learn from the old chain still work, and each is
// decided by a type or a sentinel, never by reading words:
//
//   - errors.Is against ErrTLSCertificateNotTrusted still matches, so the
//     "add the certificate authority" sentence still reaches the operator who
//     needs it;
//   - errors.Is against context.Canceled and context.DeadlineExceeded still
//     matches when that is genuinely what happened, so internal/audit still
//     records a cancel as a cancel and a deadline as a deadline;
//   - this type IS a net.Error, so internal/audit's transport branch still
//     tells a timeout apart from an unreachable host.
//
// The full Go type chain of the real cause still reaches the server log, where
// internal/logging's RedactHandler prints it through credsafe.LogClass —
// *net.DNSError, *os.SyscallError, *tls.CertificateVerificationError — which
// is what triage actually needs and carries no address at all.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// UnreachableCode is the stable class of a call that got no answer. Every
// value is written in this file; none is derived from anything a server said.
type UnreachableCode string

const (
	// UnreachableNoAnswer is the ordinary case: a refused dial, a name that
	// would not resolve, a connection that dropped.
	UnreachableNoAnswer UnreachableCode = "argocd_no_answer"

	// UnreachableTLSUntrusted is a certificate Sharko would not accept.
	UnreachableTLSUntrusted UnreachableCode = "argocd_tls_untrusted"

	// UnreachableTimedOut is a context deadline that expired mid-call.
	UnreachableTimedOut UnreachableCode = "argocd_timed_out"

	// UnreachableCanceled is the caller giving up mid-call.
	UnreachableCanceled UnreachableCode = "argocd_canceled"
)

// UnreachableError is what every ArgoCD call returns when the round trip
// failed. See the file comment for why it carries no cause.
type UnreachableError struct {
	// Verb is the HTTP method Sharko used.
	Verb string
	// Endpoint is the ArgoCD API path Sharko built for the call. It is the
	// path only — never the base address, which is the part an operator's
	// credential can be written into.
	Endpoint string
	// Code is the stable class of the failure.
	Code UnreachableCode
	// timedOut records whether the underlying transport called this a
	// timeout. It is a bool, not a message, and it exists so this type can
	// answer net.Error honestly.
	timedOut bool
}

// Error returns one of Sharko's own fixed sentences followed by Sharko's own
// facts, in the same key=value shape *WriteRefusedError uses so the two read
// alike. Nothing here comes from the transport.
func (e *UnreachableError) Error() string {
	lead := credsafe.ArgocdReadUnreachableMessage
	if e.Code == UnreachableTLSUntrusted {
		// The certificate case has a fix an operator can act on, and the
		// sentinel already spells it out. It is Sharko's own constant.
		lead = ErrTLSCertificateNotTrusted.Error()
	}
	return fmt.Sprintf("%s (code=%s call=%s %s)", lead, e.Code, e.Verb, e.Endpoint)
}

// Is lets errors.Is keep matching the three things callers legitimately branch
// on. It compares the CODE — a value out of a closed set — never any text, and
// it only says yes when that really is what happened.
func (e *UnreachableError) Is(target error) bool {
	switch target {
	case ErrTLSCertificateNotTrusted:
		return e.Code == UnreachableTLSUntrusted
	case context.DeadlineExceeded:
		return e.Code == UnreachableTimedOut
	case context.Canceled:
		return e.Code == UnreachableCanceled
	}
	return false
}

// Timeout reports whether the transport called this a timeout. Together with
// Error it makes *UnreachableError a net.Error, which is how internal/audit
// still tells "ArgoCD took too long" apart from "ArgoCD was not there".
func (e *UnreachableError) Timeout() bool { return e.timedOut }

// Temporary is required by net.Error and is deprecated in the standard
// library. Sharko does not classify anything by it.
func (e *UnreachableError) Temporary() bool { return false }

// unreachableCode classifies a transport failure by sentinel and by interface.
// It never calls cause.Error().
func unreachableCode(cause error) (UnreachableCode, bool) {
	var netErr net.Error
	timedOut := errors.As(cause, &netErr) && netErr.Timeout()

	switch {
	case errors.Is(cause, ErrTLSCertificateNotTrusted):
		return UnreachableTLSUntrusted, timedOut
	case errors.Is(cause, context.Canceled):
		return UnreachableCanceled, timedOut
	case errors.Is(cause, context.DeadlineExceeded):
		return UnreachableTimedOut, true
	default:
		return UnreachableNoAnswer, timedOut
	}
}

// unreachableCallError turns a failed round trip into the error callers see.
//
// It takes the cause so it can classify it and hand it to the log sink, and it
// returns an error that does not carry it. That asymmetry is the whole design:
// the log sink prints causes through credsafe.LogClass, which is built from
// types and sentinels and cannot print an address; a returned error's words go
// to people.
func unreachableCallError(verb, path string, cause error) error {
	slog.Error("argocd call got no answer", "verb", verb, "endpoint", path, "error", cause)
	code, timedOut := unreachableCode(cause)
	return &UnreachableError{
		Verb:     verb,
		Endpoint: path,
		Code:     code,
		timedOut: timedOut,
	}
}

// SafeReadFailure is the sentence a boundary may show a person for a failed
// ArgoCD READ. It is the sibling of SafeWriteFailure and picks between fixed
// sentences by TYPE, never by reading the words of the error it was given.
//
// Four outcomes:
//
//   - the chain carries a *UnreachableError — Sharko never got an answer, and
//     the sentence carries Sharko's own facts about which call went nowhere;
//   - ArgoCD answered 401 or 403 — the matching sentinel's own sentence, which
//     names what to fix;
//   - anything else — the fixed "no answer" sentence, which is the safe
//     assumption for an error this function does not recognise.
//
// Why it does not simply reuse credsafe.ArgocdWriteUnreachableMessage: that
// sentence ends "so it does not know whether anything was applied", which is
// untrue of a read and would tell an operator their fleet may have changed
// when nothing did.
func SafeReadFailure(err error) string {
	var unreachable *UnreachableError
	if errors.As(err, &unreachable) {
		return unreachable.Error()
	}
	switch {
	case errors.Is(err, ErrTokenInvalid):
		return ErrTokenInvalid.Error()
	case errors.Is(err, ErrPermissionDenied):
		return ErrPermissionDenied.Error()
	}
	return credsafe.ArgocdReadUnreachableMessage
}
