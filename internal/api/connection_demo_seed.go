package api

// connection_demo_seed.go — the demo estate's fabricated connection-check
// outcomes.
//
// # Why this exists
//
// Since B5 a connection row's state IS the canonical answer from a real
// comparison, held in the credential-check store. Demo mode never runs a
// real comparison — its clusters do not exist — so without a seam every row
// on the demo's Managed Secrets page would read "Not checked yet", which is
// technically honest and completely useless for showing what the page does.
//
// This is the exact counterpart of
// clusterreconciler.SeedReconcileRecordForDemo, and it is here for the same
// reason that one exists: getting a chosen state out of a real pass would
// need fake Secrets, git-rendered desired labels and a live backend to all
// agree, which is a lot of machinery to arrange a picture that a direct,
// honest record write produces just as legitimately.
//
// # It cannot be used to fake a green row
//
// Everything seeded here goes through the SAME derivation the real path
// uses: the display words come from connectionSyncHeadline /
// connectionSyncQualifier, and the fail-closed invariant is applied here too,
// so a seed asking for "synced" without full verification of an owned scope
// comes back downgraded exactly as a real answer would. Demo mode can
// choose the facts; it cannot choose a headline the facts do not support.
//
// # Demo only
//
// Nothing in the product calls this. It is exported solely so internal/demo
// can reach it, alongside the other Set*ForDemo seams on Server.

import "time"

// The canonical vocabulary, exported so demo mode does not retype the words
// and cannot drift from them.
const (
	DemoManagementModeSharkoManaged = managementModeSharkoManaged
	DemoManagementModeSelfManaged   = managementModeSelfManaged
	DemoManagementModeLegacyInline  = managementModeLegacyInline
	DemoManagementModeForeignOwned  = managementModeForeignOwned

	DemoSyncStateSynced    = syncStateSynced
	DemoSyncStateOutOfSync = syncStateOutOfSync
	DemoSyncStateBlocked   = syncStateBlocked
	DemoSyncStateUnknown   = syncStateUnknown

	DemoVerificationScopeFull    = verificationScopeFull
	DemoVerificationScopePartial = verificationScopePartial
	DemoVerificationScopeNone    = verificationScopeNone
)

// DemoConnectionCheck is one cluster's fabricated canonical check outcome.
// The FACTS only — the display words are derived, never supplied.
type DemoConnectionCheck struct {
	Cluster           string
	ManagementMode    string
	SyncState         string
	VerificationScope string
	ApprovalRequired  bool
	// LiveSecretMissing is "there is no connection Secret at all", the fact
	// behind the row's "missing" word.
	LiveSecretMissing bool
	// Reason is the fixed sentence for a state that is not clean. Empty is
	// fine; it is not rendered as a headline anywhere.
	Reason string
	// CheckedAt is when this fabricated check "ran". A zero value means the
	// cluster has never been checked, and the row says so.
	CheckedAt time.Time
}

// SeedConnectionChecksForDemo writes fabricated canonical answers into the
// same store the real background loop and the real Check button write, so the
// demo's fleet rows read through exactly the production code path.
func (s *Server) SeedConnectionChecksForDemo(checks []DemoConnectionCheck) {
	if s == nil || s.connCredChecks == nil {
		return
	}
	for _, c := range checks {
		st := connectionCanonicalState{
			ManagementMode:    c.ManagementMode,
			ManagedScope:      managedScopeFor(c.ManagementMode),
			SyncState:         c.SyncState,
			VerificationScope: c.VerificationScope,
			ApprovalRequired:  c.ApprovalRequired,
			Reason:            c.Reason,
			LiveSecretMissing: c.LiveSecretMissing,
		}
		if !c.CheckedAt.IsZero() {
			st.CheckedAt = c.CheckedAt.UTC().Format(time.RFC3339)
		}
		// The same fail-closed invariant the real derivation applies. A demo
		// seed gets no shortcut to a green row.
		if st.SyncState == syncStateSynced &&
			(st.VerificationScope != verificationScopeFull || st.ManagedScope == managedScopeNone) {
			st.SyncState = syncStateUnknown
			st.Reason = reasonInvariantFailClosed
		}
		st.Headline = connectionSyncHeadline(st)
		st.Qualifier = connectionSyncQualifier(st)

		s.connCredChecks.mu.Lock()
		rec := s.connCredChecks.records[c.Cluster]
		rec.CheckedAt = st.CheckedAt
		rec.Canonical = st
		s.connCredChecks.records[c.Cluster] = rec
		s.connCredChecks.mu.Unlock()
	}
}
