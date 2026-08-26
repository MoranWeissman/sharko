package connectioncompare

// checkfailure.go — WHY a connection check could not finish, as a typed fact.
//
// # The ruling this implements
//
//	"Presentation structure must follow typed facts, never equality between
//	 human sentences."
//
// # What it replaces, and why that was a defect waiting to happen
//
// This used to be a bare string carrying the whole sentence a person reads.
// The connection page then routed on it — a switch whose case values were
// full paragraphs:
//
//	switch v.FailureReason {
//	case failGitRead, failNoGitConnection:
//	    ...
//
// where failGitRead is "Sharko could not read this cluster's record from Git,
// so it cannot tell what the connection should look like. Check the Git
// connection and try again."
//
// So a COPY EDIT decided which branch ran. Change a comma in that sentence and
// the page falls through to default and names the wrong failed step — no
// compiler error, no test failure, nothing to notice. The words a person reads
// and the structure of what they are shown were the same object.
//
// They are two objects now. This type is the fact; the sentence is produced
// FROM it, in one place, and every routing decision keys on the fact.
//
// # These values are internal, not a wire contract
//
// Unlike notifications.Code, a CheckFailure is never serialised and never
// reaches a browser. The wire still carries the sentence (failure_reason), so
// no consumer changes. What the values must be is STABLE ACROSS ONE BUILD and
// exhaustively mapped to a sentence — which is what CheckFailures() and the
// guards in internal/api check, by name, in both directions.

// CheckFailure names the reason a check could not finish. The empty value
// means "nothing failed" — it is not a reason, and Compare treats it as such.
type CheckFailure string

const (
	// CheckFailureNone is the zero value: the check was not refused.
	CheckFailureNone CheckFailure = ""

	// CheckFailureNoReconciler — the part of Sharko that manages cluster
	// connections is not running on this server.
	CheckFailureNoReconciler CheckFailure = "no_reconciler"
	// CheckFailureNoGitConnection — no Git repository is connected, so the
	// desired connection cannot be looked up at all.
	CheckFailureNoGitConnection CheckFailure = "no_git_connection"
	// CheckFailureNoHubClient — Sharko has no client for its own cluster, so
	// the live connection Secret cannot be read.
	CheckFailureNoHubClient CheckFailure = "no_hub_client"
	// CheckFailureGitRead — the Git read itself failed.
	CheckFailureGitRead CheckFailure = "git_read"
	// CheckFailureLiveRead — reading the live connection Secret failed.
	CheckFailureLiveRead CheckFailure = "live_read"
	// CheckFailureBackendRead — reading the configured credentials source from
	// the secrets backend failed.
	CheckFailureBackendRead CheckFailure = "backend_read"
	// CheckFailureNotManaged — the cluster has no entry in the Git-managed
	// cluster list, so there is nothing to compare against. A whole-check
	// refusal, answered before a view is ever built.
	CheckFailureNotManaged CheckFailure = "not_managed"
	// CheckFailureCredentialsUnavailable — no credentials router is wired, so
	// the configured credentials source cannot be read.
	CheckFailureCredentialsUnavailable CheckFailure = "credentials_unavailable"
	// CheckFailureAddonLabelsUnknown — Sharko could not read which addons
	// should be on for this cluster, so it cannot judge the labels. Raised
	// inside Compare.
	CheckFailureAddonLabelsUnknown CheckFailure = "addon_labels_unknown"
	// CheckFailureExpectedBuild — building the expected connection from the
	// Git record failed, so there is nothing to compare against. Raised inside
	// Compare.
	CheckFailureExpectedBuild CheckFailure = "expected_build"
)

// checkFailures is the closed set, in a stable order.
//
// It is a SLICE and CheckFailures() returns it in this order, so a guard can
// report which reasons are missing a sentence BY NAME rather than telling
// somebody a count is wrong. CheckFailureNone is deliberately not in it: it is
// the absence of a reason, and a sentence for "nothing failed" would be a
// sentence nobody can ever be shown.
var checkFailures = []CheckFailure{
	CheckFailureNoReconciler,
	CheckFailureNoGitConnection,
	CheckFailureNoHubClient,
	CheckFailureGitRead,
	CheckFailureLiveRead,
	CheckFailureBackendRead,
	CheckFailureNotManaged,
	CheckFailureCredentialsUnavailable,
	CheckFailureAddonLabelsUnknown,
	CheckFailureExpectedBuild,
}

// CheckFailures returns every reason a check can be refused for, in a stable
// order.
//
// SOURCE OF TRUTH for the guard in internal/api that fails when a reason has no
// sentence and when a sentence has no reason. It returns a copy so a caller
// cannot reach in and edit the closed set.
func CheckFailures() []CheckFailure {
	out := make([]CheckFailure, len(checkFailures))
	copy(out, checkFailures)
	return out
}

// IsDeclared reports whether c is one of the reasons Sharko owns. The empty
// CheckFailure is not declared.
func IsDeclared(c CheckFailure) bool {
	for _, declared := range checkFailures {
		if c == declared {
			return true
		}
	}
	return false
}

// String makes a CheckFailure printable in a log without a conversion at every
// call site. It is NOT a display string — never show one to a person.
func (c CheckFailure) String() string { return string(c) }
