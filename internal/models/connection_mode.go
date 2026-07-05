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
