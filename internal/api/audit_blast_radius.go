package api

import "fmt"

// audit_blast_radius.go — P3-F1. Two "check everything" buttons on the
// Managed Secrets page each wrote an audit entry that was technically true
// and practically useless:
//
//   - POST /clusters/{name}/reconcile said "read-only check of every
//     cluster's connection secret". It takes ONE cluster name in its path
//     but starts a pass over all of them, so a reader was left guessing
//     whether "every" meant three clusters or three hundred.
//   - POST /secrets/check said the same kind of thing and carried NO
//     resource at all — an entry about nothing, sitting in a table whose
//     whole job is saying what was touched.
//
// Both are fixed the same way: state the real number when the engine can
// count, and say nothing numeric when it cannot. A count of 0 always means
// "no pass has run on this server instance yet" (see
// clusterreconciler.KnownClusterCount / secrets.KnownItemCount), never "no
// clusters exist" — so 0 falls back to the unnumbered sentence rather than
// printing "all 0 clusters", which would read as a claim that Sharko
// manages nothing.

// resourceAddonValuesSecrets is what a fleet-wide addon-values action names
// as its resource. Sharko's audit resources are "<kind>:<name>" strings
// ("cluster:prod-eu", "addon:datadog"); an action over a whole surface
// names the surface the same way, so the Resource column is never empty for
// an act that genuinely did something.
const resourceAddonValuesSecrets = "secrets:addon-values"

// clusterCheckBlastRadius describes what a connection-secret check pass
// covered. n is clusterreconciler.Reconciler.KnownClusterCount().
func clusterCheckBlastRadius(n int) string {
	switch {
	case n == 1:
		return "read-only check of 1 cluster's connection secret — nothing is written"
	case n > 1:
		return fmt.Sprintf("read-only check of all %d clusters' connection secrets — nothing is written", n)
	default:
		return "read-only check of every cluster's connection secret — nothing is written"
	}
}

// valuesCheckBlastRadius describes what an addon-values check pass covered.
// n is SecretReconciler.KnownItemCount() — cluster+addon pairs, which is
// the unit a values row actually is.
func valuesCheckBlastRadius(n int) string {
	switch {
	case n == 1:
		return "read-only check of 1 addon-values secret — nothing is written"
	case n > 1:
		return fmt.Sprintf("read-only check of all %d addon-values secrets — nothing is written", n)
	default:
		return "read-only check of every addon-values secret — nothing is written"
	}
}

// valuesReconcileBlastRadius describes what a full addon-values reconcile
// pass covers. Deliberately worded as a WRITE ("pushes values") — this is
// the one of the two that creates and rotates secrets, and an audit entry
// that read the same as the check's would hide exactly the difference an
// auditor is looking for.
func valuesReconcileBlastRadius(n int) string {
	switch {
	case n == 1:
		return "full pass over 1 addon-values secret — values are pushed where they differ"
	case n > 1:
		return fmt.Sprintf("full pass over all %d addon-values secrets — values are pushed where they differ", n)
	default:
		return "full pass over every addon-values secret — values are pushed where they differ"
	}
}
