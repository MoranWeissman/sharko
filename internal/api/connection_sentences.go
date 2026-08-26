package api

// connection_sentences.go — the ONE catalog of every sentence the server
// authors for the connection surface, keyed by a stable symbolic identifier.
//
// # Why this exists
//
// Fifty-two server-authored sentences were hand-typed into twenty-one browser
// files. Fourteen of them reached shipped browser code. That produced four
// test fixtures describing responses the server cannot send, and two
// assertions that can never fail again — and nothing in the tree prevented
// any of it, because a re-typed sentence is just a string that happens to
// match until somebody edits one side.
//
// The product owner's ruling on the design point, verbatim:
//
//	"Give server-owned messages stable symbolic identifiers; do not make
//	 browser code locate them by sentence text."
//
// So the browser must never hold, match on, or search for a sentence's TEXT —
// only an identifier. That is the difference between drift being DETECTABLE
// and drift being IMPOSSIBLE: a browser that cannot name a sentence's words
// cannot get them wrong.
//
// # What this file is, and what it is not
//
// It is a map from identifier to the PRODUCTION CONSTANT ITSELF. Never a
// re-typed copy. Every value below is the same constant the handler assigns
// to the response — the compiler resolves it, so a catalog entry cannot
// describe words the server does not actually send.
//
// That also means a test of the shape `ConnectionSentences["x"] == x` compares
// a thing with itself and proves NOTHING. Do not write one. What proves the
// catalog is honest is TestConnectionSentences_CatalogCoversEverySentence in
// connection_sentence_catalog_test.go, which parses the five contract files
// and fails BY NAME on any sentence constant that is missing here and any key
// here whose constant is gone.
//
// # THE IDENTIFIER NAMING RULE — read before adding an entry
//
// The identifier is the Go constant's own name with its first letter
// lowercased, and nothing else:
//
//	condGitDefinitionOK                     -> "condGitDefinitionOK"
//	LimitReasonSecretMissingDurable         -> "limitReasonSecretMissingDurable"
//
// It is mechanical on purpose. A rule with no judgement in it is a rule
// nobody can apply differently from anybody else, so no two entries can be
// named by two different tastes, and the guard can check the mapping instead
// of trusting it. Lowercasing the first letter is what makes every identifier
// a bare JavaScript property name, so the generated TypeScript needs no
// quoting and no per-key decision either.
//
// THESE IDENTIFIERS ARE A CONTRACT. The browser will depend on them, so
// renaming one is a breaking change for every consumer — exactly like
// renaming a JSON field. Renaming the Go constant renames the identifier, so
// a Go rename is a wire change here. Change the words freely; change a name
// only deliberately.
//
// # Aliases are deliberately not entries
//
// connection_reconciliation.go aliases four of connectioncompare's sentences
// (reasonSecretMissingDurable and friends) so both endpoints answer from one
// place. Those aliases are NOT separate entries: they carry no words of their
// own, and shipping the same sentence under two identifiers would put back
// exactly the ambiguity the identifier scheme removes. The words are here
// under the connectioncompare identifier the alias points at. The catalog
// guard knows about every alias by name and fails on any new one, so this is
// a decision that has to be made again rather than a gap that grows.

import "github.com/MoranWeissman/sharko/internal/connectioncompare"

// ConnectionSentences is every server-authored sentence on the connection
// surface, keyed by its stable identifier.
//
// SOURCE OF TRUTH for cmd/gen-connection-sentences, which reads this map at
// runtime and emits ui/src/generated/connection-sentences.ts. The generator
// contains no sentence of its own; it cannot, because it never parses Go
// source — it imports this package and reads this variable.
//
// The five contract files it covers, in the order they appear below:
//
//	internal/api/connection_canonical.go        — headlines and qualifiers
//	internal/api/connection_reconciliation.go   — modes, reasons, plans, conditions
//	internal/api/connection_comparison.go       — whole-check refusals and limits
//	internal/api/connection_credential_check.go — background-check status
//	internal/connectioncompare/mode.go          — scope-limit and missing-Secret reasons
var ConnectionSentences = map[string]string{
	// ── connection_canonical.go — the display words ──────────────────────
	// The headline is the page's and the fleet row's leading word. Bare
	// "Synced" is deliberately not in the vocabulary.
	"headlineConnectionSynced":        headlineConnectionSynced,
	"headlineAddonLabelsSynced":       headlineAddonLabelsSynced,
	"headlineAddonLabelsOutOfSync":    headlineAddonLabelsOutOfSync,
	"headlineOutOfSync":               headlineOutOfSync,
	"headlineOutOfSyncApproval":       headlineOutOfSyncApproval,
	"headlineOutOfSyncCannotRestore":  headlineOutOfSyncCannotRestore,
	"headlineConnectionSecretMissing": headlineConnectionSecretMissing,
	"headlineBlocked":                 headlineBlocked,
	"headlineVerificationIncomplete":  headlineVerificationIncomplete,
	"headlineConfigurationMatchesEKS": headlineConfigurationMatchesEKS,
	"headlineUnknownCheckFailed":      headlineUnknownCheckFailed,
	"headlineNotCheckedYet":           headlineNotCheckedYet,

	"qualifierCredentialNotCompared": qualifierCredentialNotCompared,
	"qualifierSelfManaged":           qualifierSelfManaged,
	"qualifierLegacyInline":          qualifierLegacyInline,

	// ── connection_reconciliation.go — mode statements ───────────────────
	"modeStatementSharkoManaged": modeStatementSharkoManaged,
	"modeStatementSelfManaged":   modeStatementSelfManaged,
	"modeStatementLegacyInline":  modeStatementLegacyInline,
	"modeStatementForeignOwned":  modeStatementForeignOwned,

	// ── connection_reconciliation.go — sync.reason sentences ─────────────
	"reasonOutOfSyncApprovalRequired": reasonOutOfSyncApprovalRequired,
	"reasonOutOfSyncLabelsOnly":       reasonOutOfSyncLabelsOnly,
	"reasonOutOfSyncLegacyInline":     reasonOutOfSyncLegacyInline,
	"reasonOutOfSyncRepairWithheld":   reasonOutOfSyncRepairWithheld,
	"reasonVerificationIncomplete":    reasonVerificationIncomplete,
	"reasonInvariantFailClosed":       reasonInvariantFailClosed,

	// ── connection_reconciliation.go — plan sentences ────────────────────
	"planAutomaticSecretCreate":    planAutomaticSecretCreate,
	"planAutomaticLabelSync":       planAutomaticLabelSync,
	"planRequiresApprovalSentence": planRequiresApprovalSentence,

	// ── connection_reconciliation.go — condition details ─────────────────
	"condGitDefinitionOK":    condGitDefinitionOK,
	"condCredentialRefOK":    condCredentialRefOK,
	"condCredentialRefEKS":   condCredentialRefEKS,
	"condOwnershipOK":        condOwnershipOK,
	"condOwnershipGuest":     condOwnershipGuest,
	"condOwnershipNotMarked": condOwnershipNotMarked,
	"condLiveSecretFound":    condLiveSecretFound,
	"condLiveSecretMissing":  condLiveSecretMissing,
	"condComparisonFull":     condComparisonFull,
	"condComparisonDrift":    condComparisonDrift,
	"condArgoCDConnected":    condArgoCDConnected,
	"condArgoCDUnavailable":  condArgoCDUnavailable,
	"condArgoCDNotChecked":   condArgoCDNotChecked,
	"condArgoCDUnreachable":  condArgoCDUnreachable,
	"condArgoCDNoConnection": condArgoCDNoConnection,
	"condApprovalRequired":   condApprovalRequired,

	// ── connection_reconciliation.go — the short FACT sentences ──────────
	// Used when the full explanation is already on the page as sync.reason.
	"condGitDefinitionBlocked":       condGitDefinitionBlocked,
	"condLiveSecretUnread":           condLiveSecretUnread,
	"condCredentialRefUnread":        condCredentialRefUnread,
	"condCheckDidNotFinish":          condCheckDidNotFinish,
	"condCredentialRefUnknownSource": condCredentialRefUnknownSource,
	"condCredentialRefUnreadable":    condCredentialRefUnreadable,
	"condComparisonPartial":          condComparisonPartial,
	"condApprovalNoRepair":           condApprovalNoRepair,

	// ── connection_comparison.go — whole-check refusals ──────────────────
	"failNoReconciler":     failNoReconciler,
	"failNoGitConnection":  failNoGitConnection,
	"failNoHubClient":      failNoHubClient,
	"failGitRead":          failGitRead,
	"failLiveRead":         failLiveRead,
	"failBackendRead":      failBackendRead,
	"failNotManaged":       failNotManaged,
	"failCredsUnavailable": failCredsUnavailable,

	// The two failures raised INSIDE connectioncompare.Compare, plus the last
	// resort for a typed reason that lost its sentence. Their words used to be
	// typed inline in internal/connectioncompare/compare.go, a file no catalog
	// guard read, so they shipped to a person's screen from outside this
	// contract entirely.
	"failAddonLabelsUnknown": failAddonLabelsUnknown,
	"failExpectedBuild":      failExpectedBuild,
	"failCheckDidNotFinish":  failCheckDidNotFinish,

	// ── connection_comparison.go — the R3-8 repair withdrawal ────────────
	"limitReasonCommitUnknown": limitReasonCommitUnknown,

	// ── connection_repair.go — the repair refusals ───────────────────────
	// Thirteen sentences that were outside every catalog guard until R2-4.
	// Nothing referenced them, so any of them could be reworded or broken and
	// the whole Go suite stayed green.
	"repairFailNoReconciler": repairFailNoReconciler,
	"repairFailNoHubClient":  repairFailNoHubClient,
	"repairFailGitRead":      repairFailGitRead,
	"repairFailNotManaged":   repairFailNotManaged,
	"repairFailBuild":        repairFailBuild,
	"repairFailWrite":        repairFailWrite,
	// The two missing-connection sentences. They are two entries because they
	// are two facts: only the full-connection path may promise Sharko builds
	// the connection by itself. See the constants for the whole story (B3).
	"repairFailSecretGoneSharkoCreates": repairFailSecretGoneSharkoCreates,
	"repairFailSecretGoneLabelsOnly":    repairFailSecretGoneLabelsOnly,
	"repairFailRaced":                   repairFailRaced,
	"repairFailNotOwned":                repairFailNotOwned,
	"repairFailRevisionUnknown":         repairFailRevisionUnknown,
	"repairFailRevisionMissing":         repairFailRevisionMissing,
	"repairFailRevisionMoved":           repairFailRevisionMoved,

	// ── connection_repair.go — the refusal and the four outcome sentences ──
	// All five were typed inline in the handlers until R2-4, so none of them
	// was covered by any guard. Two are fmt templates whose %d is a COUNT OF
	// FIELDS — never a length and never anything derived from a value.
	"repairFailAddonLabelsUnknown":  repairFailAddonLabelsUnknown,
	"repairDoneConnectionChanged":   repairDoneConnectionChanged,
	"repairDoneConnectionUnchanged": repairDoneConnectionUnchanged,
	"repairDoneLabelsChanged":       repairDoneLabelsChanged,
	"repairDoneLabelsUnchanged":     repairDoneLabelsUnchanged,

	// ── internal/connectioncompare/compare.go — why a field is not checked ─
	// The sentences a person reads in the "not checked" list. They were
	// exported in R2-4 for one reason only: an unexported constant cannot be
	// referenced from this package, so it could not be catalogued at all.
	"reasonEKSNoStoredCredential":          connectioncompare.ReasonEKSNoStoredCredential,
	"reasonNoIndependentCopy":              connectioncompare.ReasonNoIndependentCopy,
	"reasonLabelPreservedForPreviousOwner": connectioncompare.ReasonLabelPreservedForPreviousOwner,
	"reasonLabelNotCompared":               connectioncompare.ReasonLabelNotCompared,

	// ── connection_credential_check.go — background-check status ─────────
	"checkLoopNotScheduled":      checkLoopNotScheduled,
	"checkLoopNotRunYet":         checkLoopNotRunYet,
	"checkLoopNoReconciler":      checkLoopNoReconciler,
	"checkLoopNoArgoCD":          checkLoopNoArgoCD,
	"checkLoopClusterListFailed": checkLoopClusterListFailed,

	// ── connection_credential_check.go — the drift notices ───────────────
	"credentialDriftNotice":          credentialDriftNotice,
	"credentialDriftClearedNotice":   credentialDriftClearedNotice,
	"credentialCheckRecoveredNotice": credentialCheckRecoveredNotice,

	// ── internal/connectioncompare/mode.go — scope-limit reasons ─────────
	"limitReasonSourceNotRecorded":     connectioncompare.LimitReasonSourceNotRecorded,
	"limitReasonSourceNotUnderstood":   connectioncompare.LimitReasonSourceNotUnderstood,
	"limitReasonEKSNoStoredCredential": connectioncompare.LimitReasonEKSNoStoredCredential,
	// There is deliberately no foreign-owned limit sentence. A foreign-owned
	// connection states its boundary once, through the management-mode
	// statement, and offers the take-over action — see the note above the
	// LimitReason constants in internal/connectioncompare/mode.go.
	"limitReasonSelfManaged":       connectioncompare.LimitReasonSelfManaged,
	"limitReasonAdopted":           connectioncompare.LimitReasonAdopted,
	"limitReasonInlineKubeconfig":  connectioncompare.LimitReasonInlineKubeconfig,
	"limitReasonBackendUnreadable": connectioncompare.LimitReasonBackendUnreadable,

	// ── internal/connectioncompare/mode.go — missing-Secret reasons ──────
	"limitReasonSecretMissingDurable":           connectioncompare.LimitReasonSecretMissingDurable,
	"limitReasonSecretMissingSelfManaged":       connectioncompare.LimitReasonSecretMissingSelfManaged,
	"limitReasonSecretMissingLegacyInline":      connectioncompare.LimitReasonSecretMissingLegacyInline,
	"limitReasonSecretMissingUnknownSource":     connectioncompare.LimitReasonSecretMissingUnknownSource,
	"limitReasonSecretMissingBackendUnreadable": connectioncompare.LimitReasonSecretMissingBackendUnreadable,
}
