package audit

// reason.go — an audit entry says WHAT KIND of thing went wrong, in Sharko's
// own words, and it has no way to say anything else.
//
// # The hole this closes
//
// The old shape was: a call site put an error's text into Entry.Error, and set
// a bool called CredentialFailure to tell Add whether that text was dangerous.
// Add believed the bool. Seventeen call sites set it and sixteen of them
// decided by calling credsafe.Is on a live error, so the audit log was safe
// exactly as far as every upstream author had remembered to mark their errors.
//
// Two of those sites recorded an ArgoCD error that nothing ever marks, so
// credsafe.Is was guaranteed false and the raw text was guaranteed to be
// stored. That is not a bug in those two sites. It is what a design that asks
// the caller "is this safe?" produces, eventually, at every site.
//
// # The shape now
//
// There is no text channel into an audit record any more.
//
//   - Entry.Error is a SafeText. Its only field is unexported and this package
//     exports no constructor, so no other package can build one. There is no
//     parameter for a backend's words to arrive through.
//   - Entry.Reason is an ENUM. A caller says which CATEGORY of thing failed.
//     An enum cannot carry a secret: the worst a wrong or missing Reason can
//     do is pick the wrong sentence out of the catalog below.
//   - sanitize sets Error from Reason and from nothing else, on every entry,
//     every time. A caller cannot switch that off because there is no switch —
//     the value a caller puts in Error (which it cannot) is not consulted.
//
// So safety no longer depends on the caller getting anything right. It depends
// on this file's catalog being safe, which is a property of thirteen constants
// that never see an error.
//
// # Why the caller still runs the classifier
//
// Classify takes an error and returns a Reason. It runs at the call site, where
// the typed error is still alive — that part of the old design was right, and
// credsafe's whole doctrine depends on it: classification is by TYPE, and a
// type is only reachable while the error is.
//
// What changed is what travels. It used to be "the answer, plus the text".
// Now it is the answer and nothing else. A call site that forgets to call
// Classify, or calls it and gets the wrong answer, or hand-writes the wrong
// constant, produces a less useful record — never an unsafe one.
//
// # Why not verify.ClassifyError
//
// verify.ClassifyError matches on the error's WORDS ("connection refused",
// "x509", "AssumeRole"). That is sanctioned there: it is a fixed lookup table
// that echoes no part of what it read, and it calls credsafe.Cause itself. But
// it is the wrong tool here. A word table silently stops matching the day a
// backend rephrases itself, and the whole point of this file is that the audit
// log must not depend on anything that can silently stop working. Classify
// below reads types and status codes only.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// Reason is the category of an audit failure: what KIND of thing went wrong.
//
// It is deliberately a small closed set. Which operation was being attempted is
// already on the entry three times over — Event, Action and Resource — so this
// field carries the one thing those cannot: what happened to it.
type Reason string

const (
	// ReasonCredentials — a credentials backend refused, failed or could not
	// be reached while Sharko was fetching a cluster's sign-in details.
	ReasonCredentials Reason = "credentials_source"
	// ReasonSecretValue — the same, for an addon's secret VALUE rather than a
	// cluster's sign-in details. Two reasons, because credsafe has two
	// sentences and they name different things to go and look at.
	ReasonSecretValue Reason = "secret_value_source"
	// ReasonNotFound — what the operation needed was not there.
	ReasonNotFound Reason = "not_found"
	// ReasonPermission — Sharko was refused permission.
	ReasonPermission Reason = "permission_denied"
	// ReasonConflict — something else changed the same thing at the same time.
	ReasonConflict Reason = "conflict"
	// ReasonInvalidData — data Sharko read did not pass its own checks.
	ReasonInvalidData Reason = "invalid_data"
	// ReasonUnreachable — the service could not be reached at all.
	ReasonUnreachable Reason = "unreachable"
	// ReasonTLS — a secure connection could not be agreed.
	ReasonTLS Reason = "tls"
	// ReasonTimeout — it ran out of time.
	ReasonTimeout Reason = "timed_out"
	// ReasonCanceled — it was stopped before it finished.
	ReasonCanceled Reason = "canceled"
	// ReasonNotConverged — the write landed but the result is still not what
	// was asked for. Not an error from anybody: a disagreement with reality.
	ReasonNotConverged Reason = "not_converged"
	// ReasonUpstream — the service Sharko depends on refused or failed the
	// call, and none of the more specific answers fit.
	ReasonUpstream Reason = "upstream_failure"
	// ReasonUnspecified — a failure whose writer knows it failed and does not
	// know what kind. This is the ONLY answer that tells a reader nothing, so
	// reach for Classify(err) instead wherever there is an error to classify.
	ReasonUnspecified Reason = "unspecified"
)

// safeSentences is the whole vocabulary of Entry.Error. Nothing outside this
// map can ever be stored in that field.
//
// Every sentence points at the server log for this request id, because that is
// where the real cause goes and stays — the same contract credsafe.Message has
// had since it was written.
var safeSentences = map[Reason]string{
	ReasonCredentials: credsafe.Message,
	ReasonSecretValue: credsafe.SecretValueMessage,
	ReasonNotFound:    "What this operation needed was not there. The server log for this request id says what was missing.",
	ReasonPermission:  "Sharko was refused permission for this operation. The server log for this request id says which call was refused.",
	ReasonConflict:    "Something else changed this at the same moment, so Sharko did not write over it. The server log for this request id has the details.",
	ReasonInvalidData: "Data Sharko read did not pass its own checks. The server log for this request id says which file and which check.",
	ReasonUnreachable: "Sharko could not reach the service this operation needed. The server log for this request id says which one.",
	ReasonTLS:         "Sharko could not agree a secure connection with the service this operation needed. The server log for this request id says which one.",
	ReasonTimeout:     "This operation ran out of time before it finished. The server log for this request id says which step.",
	ReasonCanceled:    "This operation was stopped before it finished. The server log for this request id says at which step.",
	ReasonNotConverged: "Sharko wrote the change but the result still does not match what was asked for. " +
		"The server log for this request id says what is still different.",
	ReasonUpstream:    "The service this operation depends on refused or failed the call. The server log for this request id says which call.",
	ReasonUnspecified: "This operation failed. The server log for this request id says what went wrong.",
}

// Valid reports whether r is one of the defined reasons. The empty value is
// deliberately NOT valid — it means "nobody said", which is a different thing
// from ReasonUnspecified ("the writer said it does not know").
func (r Reason) Valid() bool {
	_, ok := safeSentences[r]
	return ok
}

// hidesDetail reports whether an entry with this reason must have its Detail
// dropped.
//
// The two credential reasons do, and nothing else does. A credentials failure
// has ONE answer and it lives in Error; repeating or elaborating it in Detail
// made the row look like it carried two facts when it carried one, and any
// elaboration a writer added there was by definition written next to a live
// credentials error.
//
// This is not "the caller said it was dangerous". It is the sink acting on its
// OWN classification of its OWN enum, and it runs on every entry.
func (r Reason) hidesDetail() bool {
	return r == ReasonCredentials || r == ReasonSecretValue
}

// SafeText is the type of Entry.Error, and the reason a backend's words cannot
// get into an audit record.
//
// Its only field is unexported and this package exports no way to build one, so
// no other package can produce a non-zero SafeText — not with a struct literal,
// not with a conversion, not with a cast. The single producer is sentenceFor
// below, which reads the catalog map and can therefore only ever return one of
// the thirteen constants above.
//
// This is the shape verify.SafeMessage established: a finished sentence chosen
// by code, in a type that has no opening for raw text.
//
// UnmarshalJSON exists so a stored entry can be decoded back (tests, clients).
// It is NOT a hole: sanitize rebuilds Error from Reason unconditionally, so a
// value that arrived by decoding is discarded the moment it reaches Add.
type SafeText struct{ sentence string }

// String is what the sentence reads as. The zero SafeText is the empty string.
func (t SafeText) String() string { return t.sentence }

// IsZero is what `json:",omitzero"` calls, so an entry with no failure carries
// no "error" key at all — the same wire shape the old `omitempty` string had.
func (t SafeText) IsZero() bool { return t.sentence == "" }

// MarshalJSON writes the sentence as a plain JSON string, so the wire format is
// unchanged from when this field was a string.
func (t SafeText) MarshalJSON() ([]byte, error) { return json.Marshal(t.sentence) }

// UnmarshalJSON reads a plain JSON string back.
func (t *SafeText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t.sentence = s
	return nil
}

// sentenceFor is the ONLY producer of a non-zero SafeText.
//
// It is a pure function of the reason: same reason in, same sentence out, no
// error consulted and no state read. That is what makes sanitize idempotent
// without anybody comparing message text — running it twice runs the same map
// lookup twice.
func sentenceFor(r Reason) SafeText {
	if s, ok := safeSentences[r]; ok {
		return SafeText{sentence: s}
	}
	return SafeText{}
}

// Classify says which Reason an error belongs to, by TYPE and by status code —
// never by reading the error's words.
//
// Call it at the boundary where the error is still a live typed value, and put
// the ANSWER on the entry. The error itself must not travel to the audit log:
// audit.Entry has no error-typed field and TestEntry_HasNoErrorTypedFieldAtAll
// keeps it that way.
//
// An UNMARKED error gets exactly the same protection as a marked one. It is
// classified into one of the reasons below and the sink writes the catalog
// sentence for that reason. There is no branch anywhere that reaches for
// err.Error() — not for git errors, not for Kubernetes errors, not for the
// fallback. That is the whole difference from the old design, where anything
// nobody had remembered to mark went in verbatim.
//
// nil in, empty out: an entry that is not about a failure has no reason.
func Classify(err error) Reason {
	if err == nil {
		return ""
	}

	// Credentials first, always. A Kubernetes or git error that WRAPS a
	// credentials error is a credentials failure, and asking the k8s helpers
	// first would classify it as whatever the wrapper looked like.
	if credsafe.Is(err) {
		// Which of credsafe's two sentences is on the error decides which of
		// the two credential reasons this is. This compares against a package
		// constant Sharko wrote — it is not reading a backend's words, and
		// there are no backend words left on a marked error anyway.
		if credsafe.Sentence(err) == credsafe.SecretValueMessage {
			return ReasonSecretValue
		}
		return ReasonCredentials
	}

	switch {
	case errors.Is(err, context.Canceled):
		return ReasonCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ReasonTimeout
	}

	// Kubernetes API errors. These helpers walk the chain with errors.As and
	// read the Status reason code, not the message.
	switch {
	case apierrors.IsNotFound(err), apierrors.IsGone(err):
		return ReasonNotFound
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return ReasonPermission
	case apierrors.IsConflict(err), apierrors.IsAlreadyExists(err):
		return ReasonConflict
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err), apierrors.IsRequestEntityTooLargeError(err):
		return ReasonInvalidData
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err):
		return ReasonTimeout
	case apierrors.IsServiceUnavailable(err):
		return ReasonUnreachable
	}

	// Transport-level failures, by type.
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameErr) || errors.As(err, &certInvalid) {
		return ReasonTLS
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ReasonTimeout
		}
		return ReasonUnreachable
	}
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		return ReasonUnreachable
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ReasonUnreachable
	}

	// Everything else. Not "an error occurred" — "the thing Sharko was talking
	// to refused or failed the call", which together with Event, Action and
	// Resource still tells a reader what to go and look at.
	return ReasonUpstream
}
