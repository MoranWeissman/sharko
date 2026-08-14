package clusterreconciler

import "strings"

// failure_sentence.go — P1-B B2: a failed check is not drift, and a failed
// check's reason is never the raw error text.
//
// recordReconcile's Message field is written from many call sites in
// reconciler.go and check_pass.go, and several of them build the message by
// appending a Kubernetes API or git-provider error straight onto a fixed
// English prefix (e.g. "Sharko couldn't converge git-desired addon labels
// on this drifted managed-cluster Secret: " + err.Error()). That raw text
// must never reach the API response (finding #123): a misbehaving
// client-go or gitprovider error could in principle echo something a
// reader shouldn't see, and even when it can't, raw Go error text (a
// wrapped %w chain, "dial tcp: i/o timeout") is not a sentence a person
// outside this codebase can act on.
//
// FailureSentence is the single choke point both consumers of a Failed
// record's Message go through:
//   - LastError() (reconcile_status.go) — the engine-level summary the
//     managed-secrets page's top strip shows.
//   - api.connectionSecretRow.LastCheckError (system_managed_secrets.go) —
//     the per-row "why didn't the last check finish" fact (B1).
//
// Every branch matches on the FIXED prefix each call site always writes,
// never on the appended error text itself — so classification is correct
// regardless of what the underlying error actually says, and the mapped
// sentence can never contain it. Keyed by failing stage per the sprint
// plan: git read failed, cluster API unreachable, secret write failed,
// unknown. Mirrors the reasoning internal/api's
// addonValuesSecretCheckFailureSentence already established for the values
// engine's per-item errors — same "canned sentence per stage, never the
// raw string" choice, applied here to the connection engine's per-cluster
// and whole-pass failures.
//
// The raw error text is never dropped — every call site above still logs
// it via slog at warn/error with full detail, and the audit entry's Error
// field still carries it too. This function only governs what reaches the
// browser.
// DefaultFailureSentence is the generic fallback FailureSentence returns
// when a Failed record's message does not match any of the recognized fixed
// prefixes below — R2-2: promoted from an inline literal so a caller (and a
// test) can reference this exact sentence by name instead of copying the
// string, in particular to assert it can never appear paired with
// OutcomeSucceeded (see applyLastReconcile in api/clusters_reconcile.go).
// The text itself is unchanged.
const DefaultFailureSentence = "The last check didn't finish. Click Refresh to try again."

func FailureSentence(raw string) string {
	switch {
	case raw == "":
		return ""

	// A whole pass aborted before it reached any per-cluster work
	// (stampAbortedTick) — every cluster this server knows about gets the
	// SAME record this tick, so the sentence has to be honest about what
	// that means: Sharko couldn't look, not that every cluster drifted.
	//
	// pollOnce and checkOnce spell the same two reasons slightly
	// differently ("git read failed" vs "git_read failed", using the
	// desiredStateReadKind constant directly) — match both spellings.
	case strings.Contains(raw, "git_read failed") || strings.Contains(raw, "git read failed"):
		return "Sharko could not read git, so this check did not finish. Check that Sharko can reach your git host, then click Refresh."
	case strings.Contains(raw, "schema_validation failed") || strings.Contains(raw, "schema validation failed"):
		return "The managed-clusters file in git failed validation, so this check did not finish. Fix the file in git, then click Refresh."
	case strings.Contains(raw, "reconciler pass aborted"):
		// Any other whole-pass abort reason (today: listing the ArgoCD
		// cluster secrets failed) — the cluster API, not git, is what
		// Sharko couldn't reach.
		return "Sharko could not reach the cluster to check it, so this check did not finish. Check that Sharko can reach that cluster, then click Refresh."

	// Per-cluster failures. Matched on the fixed prefix each call site in
	// reconciler.go / check_pass.go always writes — never on the wrapped
	// error text that follows it.
	case strings.Contains(raw, "credentials"):
		return "Sharko couldn't get credentials for this cluster. Check that Sharko can reach that cluster, then click Refresh."
	case strings.Contains(raw, "check whether an ArgoCD cluster secret already exists"),
		strings.Contains(raw, "read this cluster's ArgoCD secret to check it"),
		strings.Contains(raw, "re-read the cluster Secret"):
		return "Sharko could not reach the cluster to look, so this check did not finish. Check that Sharko can reach that cluster, then click Refresh."
	case strings.Contains(raw, "couldn't build"),
		strings.Contains(raw, "couldn't create"),
		strings.Contains(raw, "couldn't sync"),
		strings.Contains(raw, "couldn't converge"),
		strings.Contains(raw, "couldn't remove"):
		return "Sharko tried to fix this cluster's connection secret and the write failed. Click Refresh to try again."

	default:
		// Covers every other Failed record this package writes today —
		// including the honest-but-jargon-laden self-heal verification
		// messages ("self-heal did not fully converge…", "…WITHOUT its
		// Sharko ownership label") — with one safe, generic sentence rather
		// than teaching this function every internal wording by hand.
		return DefaultFailureSentence
	}
}
