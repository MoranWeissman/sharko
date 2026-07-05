package models

import "strings"

// Per-cluster ArgoCD connection ownership (V2-cleanup-57.2).
//
// Sharko is a GUEST on the user's ArgoCD. By default Sharko owns the ArgoCD
// cluster Secret for every cluster it registers (writes it, rotates its
// credentials, deletes it when the cluster leaves managed-clusters.yaml).
// The `connectionManagedBy` field on a managed-clusters.yaml entry makes the
// OTHER stance first-class: the USER creates and maintains the ArgoCD
// cluster Secret by hand, and Sharko only syncs addon labels onto it —
// never writing, rotating, or deleting the credential material.
//
//   - Absent / empty / "sharko" → Sharko-managed connection (today's
//     behavior, byte-for-byte; zero migration for existing files).
//   - "user" → self-managed connection: Sharko never writes the Secret's
//     config/credential keys and never deletes the Secret; it only merges
//     addon labels (enabled/disabled) onto the existing user-created Secret.
//     Clusters ADOPTED from an existing ArgoCD default to this mode — they
//     already have a user-created Secret; that is the whole point of Adopt.
//
// One vocabulary, one predicate: every consumer (orchestrator registration,
// adopt flow, both cluster-secret reconcilers, the remove flow, the API
// surface) routes through IsUserManagedConnection so the modes can never
// drift between writers and readers.
const (
	// ConnectionManagedBySharko is the default mode: Sharko owns the ArgoCD
	// cluster Secret lifecycle (create / rotate / delete). Writers OMIT the
	// field for this mode so existing files stay byte-identical.
	ConnectionManagedBySharko = "sharko"

	// ConnectionManagedByUser is the self-managed mode: the user creates and
	// maintains the ArgoCD cluster Secret; Sharko only manages addon labels
	// on it.
	ConnectionManagedByUser = "user"
)

// IsUserManagedConnection reports whether a stored connectionManagedBy value
// means "the user manages this cluster's ArgoCD connection". Only the
// canonical "user" (case-insensitive) counts; absent/empty and every other
// value mean Sharko-managed — the fail-safe default that preserves existing
// behavior for every file written before this field existed.
//
// Deliberate asymmetry (V2-cleanup-60 M4 — do NOT unify with the strict
// surfaces below): this predicate is the ONLY place ambiguous casing (e.g.
// "User", "USER") is tolerated, and only for the bare (pre-envelope,
// hand-edited) managed-clusters.yaml / cluster-addons.yaml path. The
// enveloped schema and the API both reject any non-lowercase
// connectionManagedBy value outright (schema validation failure / 400) —
// they do NOT case-fold. Both policies point in the same fail-safe
// direction, they just sit on opposite sides of "when in doubt, who should
// Sharko NOT touch":
//
//   - Here, a stray capital in a bare file resolves to self-managed, so an
//     odd casing errs toward Sharko touching LESS (it never silently claims
//     the connection as its own to write/rotate/delete).
//   - At the strict edges (API/schema), an odd casing is rejected outright
//     instead of being silently accepted in EITHER direction — a caller
//     typo on a fresh, structured request should be surfaced, not guessed.
//
// Keep both. Loosening the strict edges to case-fold would let a typo
// silently flip a Sharko-managed intent to self-managed (or vice versa) on
// a request that should have been rejected; tightening this predicate to
// reject ambiguous casing would turn a hand-edited legacy file's stray
// capital into an unrecoverable "Sharko now owns this Secret" surprise.
func IsUserManagedConnection(value string) bool {
	return strings.EqualFold(value, ConnectionManagedByUser)
}

// UserManagedConnection reports whether this cluster's ArgoCD connection is
// self-managed (user-created Secret; Sharko syncs addon labels only).
func (c Cluster) UserManagedConnection() bool {
	return IsUserManagedConnection(c.ConnectionManagedBy)
}

// UserManagedConnection is the ManagedClusterEntry twin of
// Cluster.UserManagedConnection.
func (e ManagedClusterEntry) UserManagedConnection() bool {
	return IsUserManagedConnection(e.ConnectionManagedBy)
}

// ConnectionManagedByFor returns the stored connectionManagedBy value for
// the named cluster in the parsed managed-clusters records. Returns ""
// (Sharko-managed default) when the cluster is not found — the fail-safe
// that preserves pre-field behavior.
func ConnectionManagedByFor(clusters []Cluster, name string) string {
	for _, c := range clusters {
		if c.Name == name {
			return c.ConnectionManagedBy
		}
	}
	return ""
}
