package notifications

// reason.go — a notification says WHAT KIND of thing went wrong, in Sharko's
// own words, and it has no way to say anything else.
//
// # The hole this closes
//
// A notification's Description used to be built as
//
//	lead + " Reason: " + detail
//
// where `detail` was a plain string handed in from outside the package. Two
// callers filled it, and both filled it with a backend's own words: the Git
// health probe passed err.Error() straight off gitprovider.TestConnection, and
// the ArgoCD health probe passed the detail string out of ProbeBootstrapApp,
// which is err.Error() on a rejected token and a formatted %v on any other
// listing failure.
//
// That description is PERSISTED. The notification store writes every mutation
// into the sharko-notifications ConfigMap so alerts survive a pod restart, so
// whatever a backend said about a failed connection was written to disk, served
// back on every GET /notifications, and restored again on the next restart.
// Every other leak found in this round was transient; this one outlived the
// process.
//
// # The shape now
//
// There is no text channel into a connection notification any more.
//
//   - HealthResult carries a Reason, which is an ENUM. A probe says which
//     CATEGORY of thing failed. An enum cannot carry a secret: the worst a
//     wrong Reason can do is pick the wrong sentence out of the catalog below.
//   - The description is built by descriptionFor, a pure function of two
//     enums — the code picks the lead sentence, the reason picks the second
//     sentence. Neither lookup echoes any part of what it read.
//   - Store.Add re-derives the description of every connection notification
//     from those same two enums before storing it, so the sink does not depend
//     on its callers having built the description correctly.
//
// So safety no longer depends on a caller getting anything right. It depends on
// this file's catalog being safe, which is a property of nine constants that
// never see an error.
//
// # Reason is a string type, so a caller CAN convert raw text into one
//
// Reason(err.Error()) compiles. That is why sanitised() exists and why
// Store.Add calls it on every notification: a Reason outside the declared set
// is replaced with ReasonUnspecified before anything is stored or rendered. The
// value is never printed, only looked up — an undeclared reason selects no
// sentence at all. A struct with an unexported field (the audit.SafeText shape)
// would make the conversion itself impossible, but it cannot be a map key and
// cannot survive the JSON round-trip through the ConfigMap, which this field
// must do. The sink check is what closes that gap instead.
//
// # Why Classify is borrowed from internal/audit
//
// audit.Classify already reads an error's TYPE and status code, never its
// words, and it is the only classifier in the tree that does. Writing a second
// one here would mean two lists to keep in step, and the day they drift is the
// day one of them stops recognising a credentials failure. This package uses
// audit for Classify and for nothing else — no notification writes to the audit
// log, and a guard in reason_guard_test.go keeps it that way.

import (
	"github.com/MoranWeissman/sharko/internal/audit"
)

// Reason is the category of a connection failure: what KIND of thing went
// wrong. It is a named type rather than a bare string so the compiler helps,
// and it is deliberately a small closed set — which connection this is about is
// already carried by the Code.
//
// The empty Reason means "this notification is not about a failure with a
// category" (the addon upgrade/drift alerts), which is a different thing from
// ReasonUnspecified ("something failed and nobody could say what kind").
type Reason string

const (
	// ReasonUnreachable — Sharko got no answer at all: the address did not
	// resolve, the connection was refused, or the network dropped it.
	ReasonUnreachable Reason = "unreachable"
	// ReasonTLS — a secure connection could not be agreed.
	ReasonTLS Reason = "tls"
	// ReasonTimeout — it ran out of time before answering.
	ReasonTimeout Reason = "timed_out"
	// ReasonCredentials — the sign-in details were refused, or Sharko could
	// not read them from the configured source.
	ReasonCredentials Reason = "credentials"
	// ReasonPermission — Sharko signed in but was refused permission.
	ReasonPermission Reason = "permission_denied"
	// ReasonNotFound — what Sharko looked for is not there.
	ReasonNotFound Reason = "not_found"
	// ReasonNotSynced — the service answered and reported the thing Sharko
	// asked about as out of sync or unhealthy. Nobody errored; the answer was
	// simply not the one Sharko wants.
	ReasonNotSynced Reason = "not_synced"
	// ReasonUpstream — the service refused or failed the call, and none of the
	// more specific answers fit.
	ReasonUpstream Reason = "upstream_failure"
	// ReasonUnspecified — something failed and the probe could not say what
	// kind. This is the only answer that tells a reader nothing, so reach for
	// ClassifyReason(err) instead wherever there is a live error to classify.
	ReasonUnspecified Reason = "unspecified"
)

// declaredReasons is the closed set, as a SLICE so DeclaredReasons returns a
// stable order and a guard can report which reasons are missing BY NAME rather
// than telling somebody a count is wrong.
var declaredReasons = []Reason{
	ReasonUnreachable,
	ReasonTLS,
	ReasonTimeout,
	ReasonCredentials,
	ReasonPermission,
	ReasonNotFound,
	ReasonNotSynced,
	ReasonUpstream,
	ReasonUnspecified,
}

// reasonSentences is the whole vocabulary the reason half of a connection
// description can be written in. Nothing outside this map can reach a stored
// description.
//
// Each sentence points at the server log, because that is where the real cause
// goes and stays — the same contract credsafe.Message has had since it was
// written.
var reasonSentences = map[Reason]string{
	ReasonUnreachable: "Sharko got no answer at all — the address may be wrong, or the network may be blocking it. " +
		"The server log for this check says what Sharko tried.",
	ReasonTLS: "Sharko could not agree a secure connection with it. " +
		"The server log for this check says what it objected to.",
	ReasonTimeout: "It ran out of time before answering. " +
		"The server log for this check says which step waited.",
	ReasonCredentials: "The sign-in details Sharko uses were refused, or Sharko could not read them. " +
		"The server log for this check says which step failed.",
	ReasonPermission: "Sharko signed in, but was refused permission to do what it needed. " +
		"The server log for this check says which call was refused.",
	ReasonNotFound: "What Sharko looked for is not there. " +
		"The server log for this check says what was missing.",
	ReasonNotSynced: "It answered, but reported the thing Sharko asked about as out of sync or unhealthy. " +
		"The server log for this check says what it reported.",
	ReasonUpstream: "It refused or failed the call. " +
		"The server log for this check says which call.",
	ReasonUnspecified: "Sharko could not work out what kind of problem this is. " +
		"The server log for this check says what happened.",
}

// DeclaredReasons returns every reason Sharko may emit, in a stable order. It
// returns a copy so a caller cannot reach in and edit the closed set.
func DeclaredReasons() []Reason {
	out := make([]Reason, len(declaredReasons))
	copy(out, declaredReasons)
	return out
}

// Valid reports whether r is one of the reasons Sharko owns. The empty Reason
// is deliberately NOT valid — see the type comment.
func (r Reason) Valid() bool {
	_, ok := reasonSentences[r]
	return ok
}

// String makes a Reason printable in a log without a conversion at every call
// site. It is not a display string — never show a Reason to a person.
func (r Reason) String() string { return string(r) }

// sanitised is the sink's own check on its own enum, run by Store.Add on every
// notification.
//
// Empty stays empty (an addon alert has no failure category). Anything else
// that is not declared becomes ReasonUnspecified — which is what stops
// Reason(someBackendsWords) from being stored, served or restored, even though
// the conversion itself compiles.
func (r Reason) sanitised() Reason {
	if r == "" {
		return ""
	}
	if r.Valid() {
		return r
	}
	return ReasonUnspecified
}

// sentence is the words for this reason. An undeclared or empty reason gets the
// unspecified sentence rather than anything derived from its value, so this
// function never echoes what it was given.
func (r Reason) sentence() string {
	if s, ok := reasonSentences[r]; ok {
		return s
	}
	return reasonSentences[ReasonUnspecified]
}

// ClassifyReason says which Reason a live error belongs to.
//
// Call it at the boundary where the error is still a typed value and put the
// ANSWER on the HealthResult. The error itself must not travel into this
// package: HealthResult has no error-typed and no string-typed failure field,
// and TestHealthResult_HasNoTextChannel keeps it that way.
//
// It delegates to audit.Classify, which matches on TYPE and on Kubernetes
// status codes and never on an error's words — see that function for why
// reading the words would be the wrong tool. The mapping below is the only
// thing this package adds: audit's thirteen categories are written for an audit
// record, and several of them cannot describe a connection probe at all.
//
// nil in, empty out: a probe that did not fail has no reason.
func ClassifyReason(err error) Reason {
	if err == nil {
		return ""
	}
	switch audit.Classify(err) {
	case audit.ReasonCredentials, audit.ReasonSecretValue:
		return ReasonCredentials
	case audit.ReasonNotFound:
		return ReasonNotFound
	case audit.ReasonPermission:
		return ReasonPermission
	case audit.ReasonUnreachable:
		return ReasonUnreachable
	case audit.ReasonTLS:
		return ReasonTLS
	case audit.ReasonTimeout, audit.ReasonCanceled:
		return ReasonTimeout
	case audit.ReasonUpstream:
		return ReasonUpstream
	default:
		// audit's conflict / invalid-data / not-converged / unspecified
		// categories describe a write that went wrong, not a connection that
		// could not be made. None of them says anything useful about a health
		// probe, so they collapse into the honest answer.
		return ReasonUnspecified
	}
}
