package secrets

import "strings"

// failure_sentence.go — P1-B B2, the values engine's half of "a failed
// check is not drift, and its reason is never the raw error text."
//
// reconcile()'s engine-level lastErrors entries are either a plan-level
// read failure (the catalog or managed-clusters file itself couldn't be
// read — err.Error() verbatim) or a per-item failure formatted as
// "cluster=%s addon=%s secret=%s: %v", where %v wraps a
// reconcileSecret error ("getting credentials: %w", "connecting to
// cluster: %w", "fetching %q from provider: %w", and so on). Neither shape
// is safe to hand back to a browser: a misbehaving credentials or
// secrets-provider SDK could in principle echo a fragment of a value into
// its own error text (the same concern LastItemError's doc comment already
// raises for the per-row case), and raw Go error text is not a sentence a
// person outside this codebase can act on either way.
//
// FailureSentence is the choke point LastError() goes through before its
// text reaches addonValuesEngineInfo (internal/api/system_managed_secrets.go)
// and the page's top strip. It matches on the FIXED prefix each failure
// path always writes, never on the wrapped error text that follows it —
// mirrors clusterreconciler.FailureSentence's reasoning and
// internal/api's addonValuesSecretCheckFailureSentence's existing
// per-stage-canned-sentence choice for this same package's per-item
// errors, just applied to the engine-level (whole-pass) summary instead of
// one row.
//
// The raw text is never dropped: every reconcile() failure branch still
// logs it via slog with full detail, and ItemRecord.Error still carries it
// for LastItemError's existing mapped-at-the-API-layer contract. This
// function only governs what LastError() itself returns.
func FailureSentence(raw string) string {
	switch {
	case raw == "":
		return ""
	case strings.Contains(raw, "the secret definition in the catalog has no"):
		return "One of the secret definitions in the catalog is incomplete. Fix it in the catalog, then click Refresh."
	case strings.Contains(raw, "getting credentials"):
		return "Sharko couldn't get credentials for one of the clusters. Check that Sharko can reach that cluster, then click Refresh."
	case strings.Contains(raw, "connecting to cluster"):
		return "Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh."
	case strings.Contains(raw, "provider"):
		return "Sharko couldn't fetch a secret's value from the secrets store. Click Refresh to try again."
	case strings.Contains(raw, "existing secret"):
		return "Sharko couldn't read an existing secret on one of the clusters. Click Refresh to try again."
	case strings.Contains(raw, "creating secret"), strings.Contains(raw, "updating secret"):
		return "Sharko tried to write a secret on a cluster and the write failed. Click Refresh to try again."
	case strings.Contains(raw, "could not read") || strings.Contains(raw, "no addon catalog could be read"):
		// Plan-level: the catalog or the managed-clusters file itself
		// couldn't be read — nothing cluster-specific went wrong yet.
		return "Sharko couldn't read the addon catalog or the managed-clusters file in git. Check that Sharko can reach your git host, then click Refresh."
	default:
		return "The last check didn't finish. Click Refresh to try again."
	}
}
