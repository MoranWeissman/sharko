// Package lifecycleevents is the one list of connection-lifecycle audit event
// names, and the runtime value a generator can read.
//
// # Why it exists
//
// The connection page's activity feed renders lifecycle events by name. It
// holds an allowlist with a human title for each one, and NO fallback: an
// event it does not recognise is skipped rather than shown as a raw
// identifier. That is the right call for a reader — and it means a rename on
// the server turns a visible lifecycle event into silence, which looks
// exactly like nothing having happened. With enough renames the feed empties
// out altogether and the page states, as a fact, "Nothing recorded since
// Sharko started." while things did happen.
//
// The browser had a hand-written copy of these names, and its test had a
// SECOND hand-written copy to compare the first against. Two copies typed by
// the same person on the same afternoon agree with each other and with
// nothing else: no .go file and no CI job referred to either of them, so the
// server could rename an event and both copies would sit there, green.
//
// So the names live here once, the code that writes the audit entries refers
// to these constants, cmd/gen-lifecycle-events reads Declared() at RUNTIME
// and writes ui/src/generated/lifecycle-events.ts, and CI's "Lifecycle Events
// Up To Date" job regenerates and diffs. Rename an event and either the Go
// build breaks, or the generated file changes and the job fails the PR until
// the feed's table is updated with it.
//
// That is the same shape as the five generators already in this repo
// (provider types, connection sentences, notification codes, schemas, engine
// version) — deliberately, rather than inventing a sixth mechanism.
//
// # Why a package of its own
//
// The writers are spread across two packages: the reconciler owns the
// cluster-Secret lifecycle, and internal/api owns repair, the background
// credential check, and the fan-out endpoints. internal/api already imports
// internal/clusterreconciler, so neither of them can hold the list without
// the other importing something it should not. A leaf package both can
// import, and that imports nothing itself, is the shape with no cycle in it.
package lifecycleevents

// Event is one audit event name as it appears on the wire and in the audit
// ring.
//
// It is an ALIAS, not a distinct type: `Event` and `string` are the same type
// to the compiler, so a plain string passes anywhere an Event is wanted and
// nothing warns. That is the right choice here — every writer, the audit ring
// and the JSON on the wire all handle these as plain strings, and a distinct
// type would mean a conversion at every one of those boundaries for no safety
// gained. The comment used to claim the opposite ("a plain string cannot be
// passed where the catalog is expected without saying so"), which was simply
// not true of an alias. What actually keeps the catalog honest is the guard
// tests over this list, not the type.
type Event = string

// The reconciler's connection-Secret lifecycle.
const (
	ClusterSecretCreate          Event = "cluster_secret_create"
	ClusterSecretDelete          Event = "cluster_secret_delete"
	ClusterSecretUserLabelSync   Event = "cluster_secret_user_label_sync"
	ClusterSecretManagedSelfHeal Event = "cluster_secret_managed_self_heal"
	ClusterConnectionRepair      Event = "cluster_connection_repair"

	ClusterSecretCreateFailed          Event = "cluster_secret_create_failed"
	ClusterSecretDeleteFailed          Event = "cluster_secret_delete_failed"
	ClusterSecretUserLabelSyncFailed   Event = "cluster_secret_user_label_sync_failed"
	ClusterSecretManagedSelfHealFailed Event = "cluster_secret_managed_self_heal_failed"
	ClusterConnectionRepairFailed      Event = "cluster_connection_repair_failed"
)

// The API repair handler.
const (
	ClusterConnectionRepairRequested Event = "cluster_connection_repair_requested"
	ClusterConnectionRepairRefused   Event = "cluster_connection_repair_refused"
)

// The background credential check.
const (
	ConnectionCredentialDriftDetected  Event = "connection_credential_drift_detected"
	ConnectionCredentialDriftCleared   Event = "connection_credential_drift_cleared"
	ConnectionCredentialCheckFailed    Event = "connection_credential_check_failed"
	ConnectionCredentialCheckRecovered Event = "connection_credential_check_recovered"
)

// The fan-out endpoints, and takeover.
const (
	ClusterRegistered Event = "cluster_registered"
	ClusterAdopted    Event = "cluster_adopted"
	ClusterTakenOver  Event = "cluster_taken_over"
)

// declared is the catalog, in the order the connection page's feed groups
// them. Declaration order and not sorted: the generator emits this order
// straight through, so a reordering shows up in the diff instead of being
// hidden by a sort here.
var declared = []Event{
	ClusterSecretCreate,
	ClusterSecretCreateFailed,
	ClusterSecretDelete,
	ClusterSecretDeleteFailed,
	ClusterSecretUserLabelSync,
	ClusterSecretUserLabelSyncFailed,
	ClusterSecretManagedSelfHeal,
	ClusterSecretManagedSelfHealFailed,

	ClusterConnectionRepair,
	ClusterConnectionRepairRequested,
	ClusterConnectionRepairRefused,
	ClusterConnectionRepairFailed,

	ConnectionCredentialDriftDetected,
	ConnectionCredentialDriftCleared,
	ConnectionCredentialCheckFailed,
	ConnectionCredentialCheckRecovered,

	ClusterRegistered,
	ClusterAdopted,
	ClusterTakenOver,
}

// Declared returns every connection-lifecycle audit event the server can
// write and the activity feed may render.
//
// It hands back a COPY. The generator and the tests both range over it, and a
// caller that sorted or appended to the package's own slice would change what
// every later caller sees — including what gets written into the browser's
// generated contract.
func Declared() []Event {
	out := make([]Event, len(declared))
	copy(out, declared)
	return out
}
