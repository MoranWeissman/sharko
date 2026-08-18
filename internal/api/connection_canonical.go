package api

// connection_canonical.go — ONE derivation of a cluster connection's
// reconciliation state, shared by the connection page and the fleet list.
//
// # Why this file exists (B5, the product owner's ruling)
//
// The fleet list used to answer "is this connection synced?" from a
// completely different fact than the connection page did. Its state came
// from connectionSecretState, whose only input is the reconciler's
// in-memory record, whose drift comparison covers ONLY Sharko's bare
// addon-label keys. So a legacy pasted-credential connection whose
// credential was never compared at all rendered a green "Synced" on the
// list and "Verification incomplete" on its own page — the same connection,
// two answers.
//
// The ruling, verbatim: "The fleet and detail page must derive their state
// from the same canonical reconciliation semantics. Do not maintain a second
// legacy vocabulary or duplicate status derivation in the browser."
//
// So: one pure function, connectionCanonicalStateFor, maps one FINISHED
// comparison view onto the canonical answer — management mode, managed
// scope, sync state, verification scope, approval requirement, the reason
// sentence, and the two display strings the browser used to compute for
// itself. Both surfaces read the same struct.
//
// # It is pure, and it needs nothing but the comparison
//
// No ArgoCD health call, no self-heal ConfigMap read, no clock. Verified by
// reading the code, not assumed:
//
//   - buildReconciliationSync's inputs are the comparison view, the mode,
//     the grouped drift, and one bool: "is repair_connection actually
//     offered". ArgoCD health is never an input to it.
//   - the self-heal setting only ever reaches buildReconciliationPlan, and
//     there it only ever chooses between plan.Automatic's sentence and the
//     sync_addon_labels door. It can never produce or withdraw
//     repair_connection — repairConnectionOffered below is the whole rule,
//     and the setting is not in it.
//
// That is what makes it safe for the fleet row: the row's canonical fields
// are computed inside the credential-check store's record(), which already
// holds the finished comparison and has neither an ArgoCD client nor the
// settings store.
//
// # Zero new I/O, zero new minting
//
// Everything here is a mapping over values already in memory. There is no
// client of any kind in this file. The fleet list path gains no Kubernetes
// read, no git read, no secrets-backend read and no ArgoCD call per row.

import (
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────
// The display words. These are the exact strings the browser pinned in
// ui/src/views/ConnectionReconciliationView.tsx and the product owner
// approved. They move here so ONE derivation feeds both surfaces; the
// browser renders them verbatim and computes nothing.
// ─────────────────────────────────────────────────────────────────────────

const (
	headlineConnectionSynced        = "Connection synced"
	headlineAddonLabelsSynced       = "Addon labels synced"
	headlineAddonLabelsOutOfSync    = "Addon labels out of sync"
	headlineOutOfSync               = "Out of sync"
	headlineOutOfSyncApproval       = "Out of sync — approval required"
	headlineOutOfSyncCannotRestore  = "Out of sync — cannot be restored from Git"
	headlineBlocked                 = "Blocked"
	headlineVerificationIncomplete  = "Verification incomplete"
	headlineConfigurationMatchesEKS = "Configuration matches; credential content not compared"
	headlineUnknownCheckFailed      = "Unknown — check failed"
	headlineNotCheckedYet           = "Not checked yet"
)

const (
	// qualifierCredentialNotCompared rides beside a partial verification —
	// part of what Sharko owns was not compared.
	qualifierCredentialNotCompared = "Credential content not compared"
	// qualifierSelfManaged is the self-managed qualifier (product ruling 2).
	qualifierSelfManaged = "Connection data managed outside Sharko"
	// qualifierLegacyInline is the legacy-inline qualifier, the product
	// owner's exact ruled sentence (correction 3).
	qualifierLegacyInline = "The credential exists only in the live Secret. Sharko can check the connection's health and some non-sensitive fields, but it cannot verify or rebuild the complete connection from Git."
)

// connectionCanonicalState is the whole canonical answer for one connection.
// Both the reconciliation endpoint and the fleet row are projections of this
// struct — neither derives any of it a second time.
type connectionCanonicalState struct {
	ManagementMode    string
	ManagedScope      string
	SyncState         string
	VerificationScope string
	ApprovalRequired  bool
	// Reason is the fixed sentence explaining a state that is not clean.
	Reason string
	// Headline and Qualifier are the display strings. Qualifier is empty
	// when the state needs none.
	Headline  string
	Qualifier string
	// CheckedAt is the comparison's own timestamp, RFC3339. Empty means no
	// check has run — never a fabricated zero time.
	CheckedAt string
	// LiveSecretMissing is true when the comparison found no live connection
	// Secret at all. It is a fact of the comparison (StatusMissing), not a
	// re-derivation: the browser used to infer it from the blocked
	// live_secret condition, which is produced from exactly the same fact.
	LiveSecretMissing bool
}

// connectionCanonicalStateFor maps ONE finished comparison onto the
// canonical answer. Pure — no clients, no settings, no clock.
func connectionCanonicalStateFor(v connectionComparisonView) connectionCanonicalState {
	mode := managementModeFor(v)
	drift := groupConnectionDrift(v)
	sync := buildReconciliationSync(v, mode, drift, repairConnectionOffered(v, mode, drift))

	st := connectionCanonicalState{
		ManagementMode:    mode,
		ManagedScope:      managedScopeFor(mode),
		SyncState:         sync.State,
		VerificationScope: sync.VerificationScope,
		ApprovalRequired:  sync.ApprovalRequired,
		Reason:            sync.Reason,
		CheckedAt:         sync.CheckedAt,
		LiveSecretMissing: connectioncompare.Status(v.Status) == connectioncompare.StatusMissing,
	}

	// THE INVARIANT, enforced here so BOTH surfaces inherit it and neither
	// can be the one that forgets: synced is only ever shipped at full
	// verification scope, of a scope Sharko actually owns. This guard should
	// never fire — the two paths that produce synced both set full, and both
	// own something — but a false "synced" headline is the one lie this whole
	// feature exists to prevent, so the rule does not depend on every future
	// edit remembering it.
	//
	// The managed_scope: none half matters as much as the full half: a
	// connection Sharko owns NOTHING on (another tool's) has nothing that
	// could be called synced, and without this clause an incoherent input
	// would have come out as "Connection synced".
	if st.SyncState == syncStateSynced &&
		(st.VerificationScope != verificationScopeFull || st.ManagedScope == managedScopeNone) {
		st.SyncState = syncStateUnknown
		st.Reason = reasonInvariantFailClosed
	}

	st.Headline = connectionSyncHeadline(st)
	st.Qualifier = connectionSyncQualifier(st)
	return st
}

// connectionCanonicalStateNotChecked is the canonical answer for a
// connection no check has reached yet on this server instance.
//
// HONEST, AND DELIBERATELY NOT CHEERFUL. Sharko has no comparison for this
// connection, so it has no verdict: unknown, nothing verified, "Not checked
// yet". It is NOT reported as synced, and it is not back-filled from the
// cluster reconciler's addon-label-only record — that record is exactly the
// second vocabulary the ruling removed.
//
// The management mode still comes from the git record, because that is a
// git-declared fact and not a comparison result: the entry says who
// maintains the connection (connectionManagedBy) and where its credential
// is kept (credsSource). foreign_owned can never be produced here — knowing
// another tool owns the live Secret requires reading it.
func connectionCanonicalStateNotChecked(userManagedConnection bool, credsSource string) connectionCanonicalState {
	mode := managementModeSharkoManaged
	switch {
	case userManagedConnection:
		mode = managementModeSelfManaged
	case credsSource == models.CredsSourceInlineKubeconfig:
		mode = managementModeLegacyInline
	}
	st := connectionCanonicalState{
		ManagementMode:    mode,
		ManagedScope:      managedScopeFor(mode),
		SyncState:         syncStateUnknown,
		VerificationScope: verificationScopeNone,
	}
	st.Headline = connectionSyncHeadline(st)
	st.Qualifier = connectionSyncQualifier(st)
	return st
}

// connectionSyncHeadline is the page's and the row's leading word, named by
// scope per matrix v3/v4. This is the Go home of what
// ConnectionReconciliationView.tsx's reconSyncHeadline used to compute in
// the browser; the strings are unchanged, and the browser now renders the
// returned value verbatim.
//
// Bare "Synced" is not in the vocabulary at all, and "Connection synced"
// can never be produced for a self-managed connection — Sharko owns only
// that connection's addon labels, so that is all it may claim.
func connectionSyncHeadline(st connectionCanonicalState) string {
	switch st.SyncState {
	case syncStateBlocked:
		return headlineBlocked
	case syncStateSynced:
		// The canonical core only ever produces synced at full verification
		// of the OWNED scope, and the headline names THAT scope — so the
		// branch keys on managed_scope, not on the mode.
		//
		// The browser's version of this function keyed on
		// management_mode == self_managed, which is the same answer for
		// every state the handler can actually produce (self_managed is the
		// only mode that both owns addon_labels only AND can reach synced).
		// Keying on the scope closes the hole anyway: if a future comparison
		// ever let another addon-labels-only mode reach synced, "Connection
		// synced" would have been a claim about a connection Sharko does not
		// own. TestConnectionReconciliation_BareSyncedIsUnproducible drives
		// the incoherent combinations too and caught exactly that.
		if st.ManagedScope == managedScopeAddonLabels {
			return headlineAddonLabelsSynced
		}
		return headlineConnectionSynced
	case syncStateOutOfSync:
		if st.ManagementMode == managementModeLegacyInline && st.LiveSecretMissing {
			return headlineOutOfSyncCannotRestore
		}
		if st.ApprovalRequired {
			return headlineOutOfSyncApproval
		}
		if st.ManagementMode == managementModeSelfManaged {
			return headlineAddonLabelsOutOfSync
		}
		return headlineOutOfSync
	default:
		// unknown — four honest shades, split by what was recorded.
		if st.CheckedAt == "" {
			return headlineNotCheckedYet
		}
		if st.VerificationScope == verificationScopeNone {
			return headlineUnknownCheckFailed
		}
		if st.ManagementMode == managementModeLegacyInline {
			return headlineVerificationIncomplete
		}
		// Sharko-managed, checked, partial: everything compared matched, the
		// credential content could not be (EKS, an unreadable backend).
		return headlineConfigurationMatchesEKS
	}
}

// connectionSyncQualifier is the scope qualifier beside the headline —
// rendered whenever the verification is narrower than the full connection.
// The Go home of ConnectionReconciliationView.tsx's reconSyncQualifier.
// Empty string means "no qualifier" (the browser's null).
func connectionSyncQualifier(st connectionCanonicalState) string {
	if st.ManagementMode == managementModeLegacyInline {
		return qualifierLegacyInline
	}
	if st.ManagementMode == managementModeSelfManaged {
		return qualifierSelfManaged
	}
	if st.VerificationScope == verificationScopePartial {
		// Already the headline's own words on the clean-EKS row — don't say
		// it twice.
		if connectionSyncHeadline(st) == headlineConfigurationMatchesEKS {
			return ""
		}
		return qualifierCredentialNotCompared
	}
	return ""
}

// repairConnectionOffered is the ONE answer to "is the guarded repair door
// actually open for this connection", extracted so the plan builder and the
// canonical core cannot drift apart about it — and so the canonical core
// provably does not need the self-heal setting to know it.
//
// Read it as the whole rule: only a Sharko-managed connection, only when
// the comparison itself offers a full-connection repair, and only for drift
// the repair would actually address (connection configuration or credential
// material) or for the clean-EKS row where the repair stays offered by
// policy. legacy_inline and self_managed never qualify — there is no
// durable credential reference to rebuild an inline connection from
// (product ruling 1), and Sharko does not rewrite a connection it does not
// manage.
func repairConnectionOffered(v connectionComparisonView, mode string, drift connectionReconciliationDrift) bool {
	if mode != managementModeSharkoManaged {
		return false
	}
	if !v.RepairAvailable || connectioncompare.RepairScope(v.RepairScope) != connectioncompare.RepairScopeFullConnection {
		return false
	}
	switch connectioncompare.Status(v.Status) {
	case connectioncompare.StatusOutOfSync:
		return len(drift.ConnectionConfiguration) > 0 || len(drift.CredentialMaterial) > 0
	case connectioncompare.StatusLimited:
		return true
	}
	return false
}

// argoHealthWordFor maps ArgoCD's OWN connection-state string onto the
// health vocabulary both surfaces render. One function, so the fleet row and
// the connection page can never disagree about the same cluster's health.
//
// The input is whatever ArgoCD's cluster listing reported: "Successful",
// "Failed", "Unknown", "" — or Sharko's own "missing" marker for a
// git-listed cluster ArgoCD has no entry for at all. Everything that is not
// an explicit success or an explicit failure is not_checked: ArgoCD only
// probes a cluster once an application is scheduled on it, and "Sharko could
// not ask" is the same honest not-an-answer.
func argoHealthWordFor(argoConnectionState string) string {
	switch argoConnectionState {
	case "Successful":
		return healthStateConnected
	case "Failed":
		return healthStateUnavailable
	default:
		return healthStateNotChecked
	}
}
