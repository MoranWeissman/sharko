package api

// connection_reconciliation.go — GET /clusters/{name}/connection-reconciliation.
//
// The reconciliation contract for one cluster connection: which definition is
// live, whether the resulting connection is healthy, why convergence is
// blocked, and what Sharko will do next. It is the API half of the
// connection page's redesign (epic-connection-reconciliation-view, Story 1).
//
// READ-ONLY AND ZERO-MINT, INHERITED. This handler runs the existing shared
// read-only core compareClusterConnection ONCE — the same core the comparison
// endpoint and the background credential-check loop already run — and joins
// facts that already exist elsewhere: ArgoCD's own connection health, the
// live Secret's provenance annotations, and the server's self-heal setting.
// It adds no new read of any secrets backend, no Kubernetes write, no git
// write, and it is structurally unable to mint a credential: the only
// credential-adjacent call in the whole path is the comparison core's
// StoredFactsIndependentOfArgoCDSecret read, which has no route to
// GetCredentials (see connection_comparison.go).
//
// THE SYNCHRONIZATION INVARIANT (product correction 1, 2026-08-18):
// sync.state == "synced" is produced ONLY when verification_scope == "full" —
// meaning every field Sharko OWNS on this connection was successfully
// compared and matched. A qualifier does not repair a false headline. The
// invariant is enforced HERE, in the builder — the UI is never responsible
// for correcting an invalid combination — and a defensive guard at the end of
// the builder fails closed to "unknown" if a future edit ever breaks it.
// TestConnectionReconciliation_SyncedRequiresFullScope_Invariant drives the
// builder across every mode × situation and fails on any violation.
//
// NEITHER SIDE OF A SENSITIVE FIELD EVER LEAVES. Sensitive drift entries
// carry path + status + sensitive:true and NOTHING else — the wire type for
// the credential_material group has no expected/live fields at all, so a leak
// through it is a compile error, not a code-review catch.
//
// ROLE GATE. secret.resource.read (operator or higher) — the same action the
// comparison endpoint and the live connection-Secret read use.

import (
	"context"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// connectionHealthTimeout caps the ArgoCD health join — one ArgoCD cluster
// list on a click, on top of the comparison's own 20-second budget.
const connectionHealthTimeout = 10 * time.Second

// The four management modes (page-state matrix v3). Derived from the
// comparison's ownership modes — see managementModeFor.
const (
	managementModeSharkoManaged = "sharko_managed"
	managementModeSelfManaged   = "self_managed"
	managementModeLegacyInline  = "legacy_inline"
	managementModeForeignOwned  = "foreign_owned"
)

// managed_scope — what Sharko OWNS on this connection (product correction 4).
const (
	managedScopeFullConnection = "full_connection"
	managedScopeAddonLabels    = "addon_labels"
	managedScopeNone           = "none"
)

// The four durable sync states (product ruling 4 — no durable "reconciling";
// Checking…/Applying…/Repairing… are request-local UI states and deliberately
// NOT in this API).
const (
	syncStateSynced    = "synced"
	syncStateOutOfSync = "out_of_sync"
	syncStateBlocked   = "blocked"
	syncStateUnknown   = "unknown"
)

// verification_scope — how much OF WHAT SHARKO OWNS was successfully compared
// (product correction 1). full is required for synced; partial means at least
// one owned field could not be compared (EKS credential content, an inline
// credential with no independent copy); none means nothing was compared.
//
// THREE VALUES, and that is the whole enum (ruling d, 2026-08-19). There used
// to be a fourth, labels_only, which no code path could ever produce — Go
// does not complain about an unused package-level constant, so it sat on the
// wire contract and in the generated OpenAPI spec as a value no client would
// ever receive. Speculative wire values are not published. The closed set is
// pinned by the invariant sweep, which asserts it across every combination
// the builder can be handed.
const (
	verificationScopeFull    = "full"
	verificationScopePartial = "partial"
	verificationScopeNone    = "none"
)

// ArgoCD connection health — independent of sync in every row of the matrix.
//
// FOUR VALUES SINCE B13. not_checked means "ArgoCD has not probed this
// connection", which is a statement about a connection that EXISTS. On a
// self-managed cluster whose Secret has not been created, there is no
// connection for ArgoCD to have an opinion about, and saying "ArgoCD has not
// checked it yet" invites a person to wait for a probe that has nothing to
// probe. unknown says the honest thing: Sharko has no health answer here.
// connectionHealthWordFor is the ONE place it is produced, so the fleet row
// and the connection page cannot differ.
const (
	healthStateConnected   = "connected"
	healthStateUnavailable = "unavailable"
	healthStateNotChecked  = "not_checked"
	healthStateUnknown     = "unknown"
)

// plan.action — the one explicit action this state permits.
const (
	planActionNone              = "none"
	planActionRepairConnection  = "repair_connection"
	planActionSyncAddonLabels   = "sync_addon_labels"
	planActionTakeOver          = "take_over"
	planActionMigrateCredential = "migrate_credentials"
)

// Mode statements — the product owner's exact approved sentences (ruling 8).
// Pinned character-for-character by
// TestConnectionReconciliation_ModeStatementsExact; do not paraphrase.
const (
	modeStatementSharkoManaged = "Git defines the connection. Sharko resolves its credential references and maintains the resulting ArgoCD Secret."
	modeStatementSelfManaged   = "Connection data is managed outside Sharko. Sharko reconciles only its addon-label keys from Git."
	modeStatementLegacyInline  = "This connection's credential exists only in the live Secret and cannot be restored from Git."
	// modeStatementForeignOwned is the ONLY place the page states the
	// foreign-ownership boundary. There is no ownership condition row beside
	// it and no limit sentence behind it — the take-over action states the
	// decision, and a third sentence would add neither a fact nor a choice.
	//
	// "MARKED AS MANAGED" IS A CORRECTNESS WORD, not a softer one. Sharko
	// observes an ownership marker on a Secret; it does not independently
	// verify that the other tool really manages the connection. Saying "another
	// tool owns this" claimed knowledge Sharko does not have. Do not "improve"
	// this sentence back into a claim.
	//
	// It used to be byte-identical to a literal inside connectioncompare.Compare,
	// and the page's de-duplication silently depended on those two staying
	// equal. Both copies are gone; nothing anywhere compares this sentence's
	// text to decide what renders.
	modeStatementForeignOwned = "This connection is marked as managed by another tool. Sharko will not change it."
)

// Fixed reason sentences. Every one is a literal here — never provider,
// git or Kubernetes error text.
const (
	// reasonOutOfSyncApprovalRequired points the admin at the repair door.
	// It may ONLY be used when plan.action is repair_connection — pointing
	// at a door that is not offered is the exact lie the withheld-door
	// sentences below exist to prevent. outOfSyncApprovalReason enforces
	// the pairing; the invariant sweep pins it.
	reasonOutOfSyncApprovalRequired = "The live connection no longer matches what Git defines. Sharko will not change connection details or credential material by itself — an admin reviews and applies the change through the repair action."
	reasonOutOfSyncLabelsOnly       = "Some addon labels on this connection do not match what Git declares."
	// reasonOutOfSyncLegacyInline is the legacy-inline drift explanation:
	// the repair door does not exist for this mode (no durable credential
	// reference — ruling 1), so the sentence points at migration and never
	// at the repair action.
	reasonOutOfSyncLegacyInline = "The live connection no longer matches what Git defines. Its credential exists only in the live Secret, so Sharko cannot rebuild the connection from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it."
	// reasonOutOfSyncRepairWithheld is the fallback when drift needs an
	// admin but no repair is offered and the comparison did not carry its
	// own withheld-door sentence. It should rarely be seen — the
	// commit-unknown, unknown-source and backend-unreadable cases all carry
	// the comparison's own sentence, which wins.
	reasonOutOfSyncRepairWithheld = "The live connection no longer matches what Git defines, and Sharko cannot offer a repair for this connection right now."
	// reasonVerificationIncomplete is the fallback for an unknown state
	// whose comparison carried no limit sentence (e.g. a provider hot-swap
	// between the policy decision and the read left a full-scope comparison
	// with unchecked fields). An unknown state never ships with an empty
	// reason.
	reasonVerificationIncomplete = "Sharko could not compare part of what it owns on this connection, so it cannot call the connection synced."
	// The four missing-Secret sentences live in internal/connectioncompare
	// now and are ALIASED here, not copied. Both endpoints answer "there is
	// no connection Secret" from the same function
	// (connectioncompare.SecretMissingReason), so the comparison endpoint and
	// this page cannot tell different stories about the same connection —
	// which is exactly what happened before: the comparison endpoint promised
	// creation for every missing Secret while this endpoint overrode it per
	// mode.
	reasonSecretMissingDurable       = connectioncompare.LimitReasonSecretMissingDurable
	reasonSecretMissingLegacyInline  = connectioncompare.LimitReasonSecretMissingLegacyInline
	reasonSecretMissingSelfManaged   = connectioncompare.LimitReasonSecretMissingSelfManaged
	reasonSecretMissingUnknownSource = connectioncompare.LimitReasonSecretMissingUnknownSource
	// reasonInvariantFailClosed is the defensive guard's sentence. It should
	// never be seen: producing it means the builder computed a synced state
	// at a non-full verification scope, which is a bug by definition. The
	// guard fails closed to unknown rather than shipping a false headline.
	reasonInvariantFailClosed = "Sharko could not verify enough of this connection to call it synced."
)

// Fixed plan sentences. plan.automatic only ever carries a sentence for
// something that genuinely happens by itself — the work the cluster
// reconciler does on its own schedule.
const (
	// The two automatic sentences are deliberately the SAME SHAPE — they sit
	// in the same field of the same block and a reader compares them:
	// "Sharko automatically <does the thing>."
	//
	// That shape is the product owner's correction of 2026-08-20. The earlier
	// wording ended with a conversational tail that told the reader nobody had
	// to ask for the change; it was rejected, and the tail is now retired in
	// the banned-wording sweep so it cannot come back. The approved register is
	// professional and short: state the action, name Git as the source of
	// truth, stop.
	//
	// They name no machinery. The reconciler is Sharko's own plumbing and
	// means nothing to the person reading the page (product ruling,
	// 2026-08-19: information text speaks in the user's terms; mechanisms
	// belong in a tooltip at most). They also promise no clock: the interval
	// is operator-configurable, so any "within N minutes" would be a lie on an
	// installation that sets it longer.
	//
	// "automatically" is scoped to THIS change on purpose. The out-of-sync
	// branch can set plan.automatic for the addon labels while
	// plan.requires_approval still asks an admin to approve a connection
	// change — a broader "you do not need to do anything" would read as
	// covering both and contradict the sentence beside it.
	//
	// Pinned character for character by
	// TestConnectionReconciliation_NewSentencesExact.
	planAutomaticSecretCreate    = "Sharko automatically creates this connection Secret from Git and the configured credentials source."
	planAutomaticLabelSync       = "Sharko automatically reapplies the addon labels defined in Git."
	planRequiresApprovalSentence = "An admin reviews and applies this change through the guarded repair action. Sharko never changes connection details or credential material by itself."
)

// Fixed condition sentences (product ruling 5 — current facts, not an
// invented step-by-step workflow).
const (
	condGitDefinitionOK  = "The connection definition was read from Git."
	condCredentialRefOK  = "The credential reference resolves from the configured credentials source."
	condCredentialRefEKS = "The credentials source stores EKS cluster details, not a reusable sign-in credential, so credential content is not compared."
	condOwnershipOK      = "Sharko owns this connection Secret."
	condOwnershipGuest   = "This connection is maintained outside Sharko. Sharko manages only its addon-label keys."
	// condOwnershipNotMarked is the ownership row for a connection Git lists
	// as Sharko's whose live Secret does not say so.
	//
	// The row used to be condOwnershipOK on every one of these pages, because
	// it was written from the management MODE and never from the live Secret.
	// The mode is a statement about the Git record; the marker is a statement
	// about the object in the cluster, and when they disagree the object wins
	// — it is the thing every writer checks before it touches anything.
	//
	// It states the fact and the consequence, and promises nothing: Sharko
	// genuinely will not rewrite this Secret while it is unmarked, and saying
	// so is the whole point of the row.
	condOwnershipNotMarked = "This connection Secret does not carry Sharko's ownership marker, so Sharko will not change it."
	condLiveSecretFound    = "The live connection Secret was found."
	condLiveSecretMissing  = "This cluster has no live connection Secret."
	condComparisonFull     = "Every field Sharko owns was compared."
	// condComparisonDrift is the product owner's exact ruled sentence
	// (ruling b, 2026-08-19). The wording it replaced is banned everywhere
	// and is deliberately NOT spelled out here — this is a production file,
	// and the whole-tree sweep must be able to cover it. Git defines the
	// connection, so a difference is a difference from Git.
	//
	// Pinned character-for-character by
	// TestConnectionReconciliation_RuledConditionSentenceExact.
	condComparisonDrift   = "At least one compared field differs from the Git-defined connection."
	condArgoCDConnected   = "ArgoCD reports this connection as working."
	condArgoCDUnavailable = "ArgoCD cannot currently use this connection."
	condArgoCDNotChecked  = "ArgoCD has not checked this connection. ArgoCD only probes a cluster once an application is scheduled on it."
	// condArgoCDUnreachable is the F8 split: when Sharko could not ask
	// ArgoCD at all, the honest sentence says so plainly — it never borrows
	// the no-application explanation above, which is only true when ArgoCD
	// itself answered and simply had no probe to report.
	condArgoCDUnreachable = "Sharko could not ask ArgoCD about this connection right now."
	// condArgoCDNoConnection is the health sentence for a connection that is
	// not there. There is no probe to wait for, so the not-checked sentence
	// would be an invitation to wait for nothing.
	//
	// THREE SENTENCES, THREE DIFFERENT SITUATIONS, AND NONE OF THEM IS A
	// SUBSTITUTE FOR ANOTHER. ArgoCD answered and has simply never probed →
	// condArgoCDNotChecked. Sharko could not ask ArgoCD at all →
	// condArgoCDUnreachable. There is no connection Secret, so there is
	// nothing for ArgoCD to probe → condArgoCDNoConnection. Merging any two
	// of them puts a sentence on the page that is not true of the situation
	// the person is actually looking at.
	condArgoCDNoConnection = "There is no connection Secret for ArgoCD to use, so there is no health to report."
	condApprovalRequired   = "Applying this change needs an admin's approval through the repair action."
)

// The short FACT sentences, used when the full explanation is already on the
// page as sync.reason (B13 item 7).
//
// WHY THEY EXIST. A condition's job is to state a fact; sync.reason's job is
// to explain the state. They were the same string in six places, and both
// render expanded, so a clean EKS connection said "credential content not
// compared" four times over and a failed check repeated its whole
// explanation twice. Nothing tested it, because nothing had a reason to look
// at two fields at once.
//
// saidOnce below is the rule: a condition never repeats sync.reason
// verbatim. It falls back to one of these, which say the same thing in the
// register a condition should be in — short, factual, no next step. The
// actionable sentence is said once, where a person reads it.
// TestConnectionReconciliation_NoConditionRepeatsTheReason sweeps the
// builder and pins it.
const (
	condGitDefinitionBlocked       = "Sharko could not read this connection's definition from Git."
	condLiveSecretUnread           = "Sharko could not read this cluster's live connection Secret."
	condCredentialRefUnread        = "Sharko could not read this connection's configured credentials source."
	condCheckDidNotFinish          = "The check did not finish."
	condCredentialRefUnknownSource = "Sharko does not know where this connection's credentials are kept."
	condCredentialRefUnreadable    = "Sharko cannot read this connection's credentials source right now."
	condComparisonPartial          = "Not every field Sharko owns on this connection was compared."
	condApprovalNoRepair           = "This change needs an admin's approval, and Sharko is not offering a repair action for this connection."
)

// saidOnce keeps a condition from repeating the page's reason sentence
// word for word. It returns detail, unless detail is empty or is exactly
// what sync.reason already says — then it returns this condition's own
// short fact.
//
// # IT IS A KNOWN DEFECT, AND IT IS BEING RETIRED
//
// Comparing two human sentences to decide what renders is exactly what the
// product owner ruled out: presentation structure must follow typed facts,
// never equality between human sentences. The foreign-owned ownership row was
// the case that exposed it — its de-duplication depended on two hand-written
// literals in two packages being byte-identical, and one character of drift in
// either would have silently doubled a sentence on the page with no test able
// to notice. That row is gone and both literals with it.
//
// FIVE of the nine call sites have been retired. Four of them — the
// check-failed branches below — could be removed with provably no change to
// what anybody reads: on a check_failed answer buildReconciliationSync sets
// sync.Reason = v.FailureReason, so the two arguments were the SAME VALUE by
// construction and this function always returned the fact. They now name the
// fact directly.
//
// FOUR REMAIN, and they genuinely vary — driving the builder across the whole
// status × scope × mode × drift cross product shows each of them rendering
// both the fallback fact AND a pass-through sentence, depending on state:
//
//   - the unknown-source credential_reference row
//   - the unreadable-backend credential_reference row
//   - the comparison row on the limited path
//   - the approval row when the repair door is withheld
//
// Converting those four changes what a reader sees, so they are NOT changed
// here on somebody's judgement — they are reported for a product ruling.
// TestConnectionReconciliation_NoTextEqualityDeduplication pins that list by
// name, so a tenth call site cannot appear quietly and the foreign-owned row
// cannot come back.
func saidOnce(detail, syncReason, fact string) string {
	if detail == "" || detail == syncReason {
		return fact
	}
	return detail
}

// Condition IDs — the fixed vocabulary from the approved contract.
const (
	conditionGitDefinition       = "git_definition"
	conditionCredentialReference = "credential_reference"
	conditionOwnership           = "ownership"
	conditionLiveSecret          = "live_secret"
	conditionComparison          = "comparison"
	conditionArgoCDConnection    = "argocd_connection"
	conditionApproval            = "approval"
)

// Condition statuses.
const (
	conditionStatusOK        = "ok"
	conditionStatusAttention = "attention"
	conditionStatusBlocked   = "blocked"
)

// connectionReconciliationDefinition is the desired-state identity: which git
// file and revision define this connection, and which revision is applied.
type connectionReconciliationDefinition struct {
	// File is the git file the definition was read from.
	File string `json:"file,omitempty"`
	// Branch is the configured git branch.
	Branch string `json:"branch,omitempty"`
	// DesiredRevision is the branch head commit every file in this check was
	// read at. Empty when the git provider cannot name one — an empty value
	// says "Sharko does not know", never a guessed or stale commit.
	DesiredRevision string `json:"desired_revision,omitempty"`
	// AppliedRevision is the commit the last successful write of this Secret
	// was built from — the live Secret's sharko.dev/revision provenance
	// annotation, the durable copy. Empty when the Secret is missing or was
	// never stamped.
	AppliedRevision string `json:"applied_revision,omitempty"`
	// CredentialSourceType is a KIND (secret-kubeconfig, eks-token,
	// inline-kubeconfig), never a path into a store and never a value.
	CredentialSourceType string `json:"credential_source_type,omitempty"`
}

// connectionReconciliationSync is the reconciliation summary.
type connectionReconciliationSync struct {
	// State is synced, out_of_sync, blocked or unknown. synced is produced
	// ONLY when VerificationScope is full — enforced in the builder.
	State string `json:"state"`
	// VerificationScope is how much of what Sharko OWNS was successfully
	// compared: full, partial or none.
	VerificationScope string `json:"verification_scope"`
	// ApprovalRequired is true exactly when the drift touches connection
	// configuration or credential material — the halves Sharko never changes
	// by itself.
	ApprovalRequired bool `json:"approval_required"`
	// Reason is a fixed sentence explaining a state that is not clean.
	Reason string `json:"reason,omitempty"`
	// CheckedAt is when this check ran, RFC3339. ABSENT when no check has
	// run — never Go's zero time (the W3-6 rule).
	CheckedAt string `json:"checked_at,omitempty"`
	// LastSuccessfulApplication is the live Secret's sharko.dev/written-at
	// provenance annotation. ABSENT when unknown.
	LastSuccessfulApplication string `json:"last_successful_application,omitempty"`
	// Headline is the display word for this state, named by scope — the
	// SERVER's answer, so the fleet list and this page can never phrase the
	// same connection differently (B5). The browser renders it verbatim and
	// derives nothing. See connection_canonical.go.
	Headline string `json:"headline"`
	// Qualifier is the sentence beside the headline when the verification is
	// narrower than the full connection, or when the connection's data is
	// managed outside Sharko. Absent when the state needs none.
	Qualifier string `json:"qualifier,omitempty"`
}

// connectionReconciliationHealth is ArgoCD's own connection health —
// independent of sync in every state. Showing connected next to an
// out-of-sync or unverifiable git state is correct, not a contradiction.
type connectionReconciliationHealth struct {
	// State is connected, unavailable, not_checked or unknown. "unknown" is
	// the B13 answer for a self-managed connection whose Secret does not
	// exist: there is no connection for ArgoCD to have an opinion about, so
	// "not checked yet" would invite a person to wait for a probe of
	// nothing. It never overwrites a real probe result.
	State string `json:"state"`
	// Message is ArgoCD's own failure text — the same text the cluster pages
	// already show today. Never a Go error and never provider SDK text from
	// Sharko's side.
	Message string `json:"message,omitempty"`
}

// connectionReconciliationCondition is one current fact.
type connectionReconciliationCondition struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// connectionReconciliationDriftEntry is one non-sensitive field that did not
// match.
type connectionReconciliationDriftEntry struct {
	Path     string  `json:"path"`
	Status   string  `json:"status"`
	Expected *string `json:"expected,omitempty"`
	Live     *string `json:"live,omitempty"`
}

// connectionReconciliationSensitiveDriftEntry is one sensitive field that did
// not match. It has NO expected and NO live field AT ALL — the omission is a
// property of the type, so no future code path can fill them in.
type connectionReconciliationSensitiveDriftEntry struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Sensitive bool   `json:"sensitive"`
}

// connectionReconciliationDrift groups the differences by managed domain.
type connectionReconciliationDrift struct {
	ConnectionConfiguration []connectionReconciliationDriftEntry          `json:"connection_configuration"`
	CredentialMaterial      []connectionReconciliationSensitiveDriftEntry `json:"credential_material"`
	AddonLabels             []connectionReconciliationDriftEntry          `json:"addon_labels"`
	NotChecked              []connectionComparisonNotChecked              `json:"not_checked"`
}

// connectionReconciliationPlan is what Sharko will do next — and what it will
// not do without approval.
type connectionReconciliationPlan struct {
	// Automatic is a fixed sentence for something that genuinely happens by
	// itself, without anyone asking. Absent when nothing will.
	Automatic string `json:"automatic,omitempty"`
	// RequiresApproval is a fixed sentence naming the approval-gated half.
	// Absent when nothing needs approval.
	RequiresApproval string `json:"requires_approval,omitempty"`
	// Action is the one explicit action this state permits: none,
	// repair_connection, sync_addon_labels, take_over or migrate_credentials.
	Action string `json:"action"`
	// ActionScopes is exactly the non-sensitive field scopes the action would
	// change. Empty when the action changes nothing.
	ActionScopes []string `json:"action_scopes"`
	// ReviewedCommit is the commit a repair must echo back (the existing
	// R3-4 rule) — set only when Action is repair_connection.
	ReviewedCommit string `json:"reviewed_commit,omitempty"`
}

// connectionReconciliationView is the response body. Nothing in it can carry
// credential material: the value-bearing fields are the cluster name, label
// values, the API server address, data key names, commit SHAs, timestamps,
// ArgoCD's own connection message, and the fixed sentences in this file.
type connectionReconciliationView struct {
	Cluster string `json:"cluster"`
	// ManagementMode is sharko_managed, self_managed, legacy_inline or
	// foreign_owned.
	ManagementMode string `json:"management_mode"`
	// ManagedScope is what Sharko OWNS on this connection: full_connection,
	// addon_labels, or none. For legacy_inline it is addon_labels — the exact
	// limited scope the code can actually rebuild without the live Secret.
	ManagedScope string `json:"managed_scope"`
	// ModeStatement is the product-approved sentence for the mode.
	ModeStatement string `json:"mode_statement"`

	Definition connectionReconciliationDefinition  `json:"definition"`
	Sync       connectionReconciliationSync        `json:"sync"`
	Health     connectionReconciliationHealth      `json:"health"`
	Conditions []connectionReconciliationCondition `json:"conditions"`
	Drift      connectionReconciliationDrift       `json:"drift"`
	Plan       connectionReconciliationPlan        `json:"plan"`

	// ValuesNeverReturned is always true — the promise, visible on the wire.
	ValuesNeverReturned bool `json:"values_never_returned"`
}

// handleGetConnectionReconciliation godoc
//
// @Summary The reconciliation view of a cluster's ArgoCD connection
// @Description Read-only. Answers, for one cluster connection: which Git definition is desired and which is applied, whether the live connection matches it and how much of that Sharko could honestly verify, whether ArgoCD can use the connection, why convergence is blocked, what happens automatically, and which single explicit action is permitted. Runs the same shared read-only comparison the connection-comparison endpoint runs — writes nothing, creates no sign-in tokens on any path — and joins facts Sharko already holds: ArgoCD's own connection health, the live Secret's provenance annotations, and the server's self-heal policy. sync.state is "synced" only when verification_scope is "full" — a connection whose credential content could not be compared is never reported as synced. Sensitive drift entries carry the field path, a status word and sensitive true, and nothing else: no value, no length, no hash, no fragment, on either side.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} connectionReconciliationView "The reconciliation view"
// @Failure 400 {object} map[string]interface{} "Cluster name is required"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "This cluster is not in the Git-managed cluster list"
// @Failure 503 {object} map[string]interface{} "Sharko is missing something it needs to run the check"
// @Router /clusters/{name}/connection-reconciliation [get]
func (s *Server) handleGetConnectionReconciliation(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "secret.resource.read") {
		return
	}

	cluster := r.PathValue("name")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	view, refusal := s.compareClusterConnection(r.Context(), cluster)
	if refusal != nil {
		writeError(w, refusal.httpStatus, refusal.sentence)
		return
	}

	// Same store the comparison endpoint and the background loop feed, so
	// the fleet page never lags a check a person just ran here either.
	s.connCredChecks.record(cluster, view)

	healthState, healthMessage, argoUnreachable := s.argoConnectionHealth(r.Context(), cluster)
	selfHealOn := s.settingsStore.GetManagedSecretsSettings(r.Context()).SelfHealEnabled

	out := buildConnectionReconciliationView(connectionReconciliationFacts{
		view:            view,
		healthState:     healthState,
		healthMessage:   healthMessage,
		argoUnreachable: argoUnreachable,
		selfHealOn:      selfHealOn,
	})

	s.auditSecretResourceRead(r, "cluster:"+cluster,
		"viewed the connection reconciliation view", auditResultFor(connectioncompare.Status(view.Status)))
	writeJSON(w, http.StatusOK, out)
}

// argoConnectionHealth asks ArgoCD for its own connection state for this
// cluster and maps it onto the three health words. Best effort: when ArgoCD
// cannot be asked, the honest answer is not_checked — never a guess, and
// never a Go error's text (only ArgoCD's own connection message, which the
// cluster pages already show today, is passed through — and only for a
// failed connection).
//
// The third return (F8) separates the two very different reasons for
// not_checked: unreachable=true means Sharko could not ask ArgoCD at all;
// unreachable=false with not_checked means ArgoCD answered and has simply
// never probed this cluster (it never probes a cluster with no application
// scheduled on it). The two get different condition sentences — the no-app
// explanation would be a guess dressed up as a fact when ArgoCD was down.
func (s *Server) argoConnectionHealth(parent context.Context, cluster string) (state, message string, unreachable bool) {
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		return healthStateNotChecked, "", true
	}
	ctx, cancel := context.WithTimeout(parent, connectionHealthTimeout)
	defer cancel()
	status, msg, err := argocd.NewService(ac).GetClusterConnectionInfo(ctx, cluster)
	if err != nil {
		return healthStateNotChecked, "", true
	}
	// argoHealthWordFor is the SHARED mapping (connection_canonical.go) — the
	// fleet row maps ArgoCD's same connection-state string through the same
	// function, so the two surfaces cannot disagree about one cluster's
	// health. Only a failed connection carries ArgoCD's own message.
	//
	// Reaching this line means ArgoCD answered, so unreachable is false even
	// when the word is not_checked (F8: ArgoCD answered and simply has never
	// probed this cluster).
	state = argoHealthWordFor(status)
	if state == healthStateUnavailable {
		return state, msg, false
	}
	return state, "", false
}

// connectionReconciliationFacts is everything the builder is allowed to look
// at. Value-only, no clients — the builder is a pure function, so the
// invariant tests can drive it across every mode × situation.
type connectionReconciliationFacts struct {
	view          connectionComparisonView
	healthState   string
	healthMessage string
	// argoUnreachable is true when Sharko could not ask ArgoCD at all (F8).
	// Only meaningful next to healthState not_checked; it picks the honest
	// condition sentence — "could not ask" vs "never probed".
	argoUnreachable bool
	// selfHealOn is the live managed_cluster_self_heal setting — whether
	// slash-free label drift on a v3 Sharko-managed cluster converges by
	// itself.
	selfHealOn bool
}

// buildConnectionReconciliationView maps one finished comparison plus the
// joined facts onto the reconciliation contract. Pure; the page-state matrix
// v3 rows 1–16 are all produced here (row 17 is a request-local UI state and
// deliberately not in the API).
func buildConnectionReconciliationView(f connectionReconciliationFacts) connectionReconciliationView {
	v := f.view
	// ONE derivation, shared with the fleet row (B5). Everything about mode,
	// scope, sync state and the display words comes from the canonical core;
	// this endpoint only adds the facts the fleet row does not carry (the
	// git/provenance identity, the conditions, ArgoCD health, and the plan).
	canon := connectionCanonicalStateFor(v)
	mode := canon.ManagementMode
	drift := groupConnectionDrift(v)

	// The health word goes through the SAME correction the fleet row uses, so
	// neither surface can be the one that still says "ArgoCD has not checked
	// this connection" about a connection that does not exist. ArgoCD's own
	// failure message only rides with an actual ArgoCD failure — an unknown
	// health has no message, because there is nothing that reported one.
	healthState := connectionHealthWordFor(f.healthState, canon)
	healthMessage := f.healthMessage
	if healthState != healthStateUnavailable {
		healthMessage = ""
	}

	out := connectionReconciliationView{
		Cluster:        v.Cluster,
		ManagementMode: mode,
		ManagedScope:   canon.ManagedScope,
		ModeStatement:  modeStatementFor(mode),
		Definition: connectionReconciliationDefinition{
			File:                 v.ComparedPath,
			Branch:               v.Branch,
			DesiredRevision:      v.ComparedCommit,
			AppliedRevision:      v.liveAppliedRevision,
			CredentialSourceType: v.CredentialSourceType,
		},
		Health:              connectionReconciliationHealth{State: healthState, Message: healthMessage},
		Drift:               drift,
		ValuesNeverReturned: true,
	}

	// The plan is the one part of the response the canonical core cannot
	// compute: it is the only place the self-heal setting is allowed to
	// matter, and it only ever chooses between plan.Automatic's sentence and
	// the sync_addon_labels door. It can neither produce nor withdraw
	// repair_connection — repairConnectionOffered is that whole rule — which
	// is exactly why the canonical sync block above needs neither the
	// setting nor this plan.
	out.Plan = buildReconciliationPlan(v, mode, drift, f.selfHealOn)
	out.Sync = connectionReconciliationSync{
		State:                     canon.SyncState,
		VerificationScope:         canon.VerificationScope,
		ApprovalRequired:          canon.ApprovalRequired,
		Reason:                    canon.Reason,
		CheckedAt:                 canon.CheckedAt,
		LastSuccessfulApplication: v.liveWrittenAt,
		Headline:                  canon.Headline,
		Qualifier:                 canon.Qualifier,
	}
	// BOTH SIGNALS, AND THE CORRECTED WORD. healthState is the word the page
	// renders (connectionHealthWordFor above), so the condition can never
	// disagree with the health block beside it — that correction is the only
	// producer of unknown, and unknown has its own sentence. argoUnreachable
	// is the second, independent signal: it splits the two facts that still
	// hide behind not_checked. Passing only one of them would collapse three
	// different situations into two sentences.
	out.Conditions = buildReconciliationConditions(v, mode, out.Sync, out.Plan, healthState, f.argoUnreachable)
	return out
}

// managementModeFor maps the comparison's ownership mode onto the four
// management modes (the contract's normative mapping rule).
func managementModeFor(v connectionComparisonView) string {
	switch connectioncompare.Mode(v.OwnershipMode) {
	case connectioncompare.ModeSelfManaged, connectioncompare.ModeAdopted:
		return managementModeSelfManaged
	case connectioncompare.ModeInlineKubeconfig:
		return managementModeLegacyInline
	case connectioncompare.ModeForeignOwned:
		return managementModeForeignOwned
	case connectioncompare.ModeBackendStoredCredentials, connectioncompare.ModeEKSToken,
		connectioncompare.ModeUnknownSource:
		return managementModeSharkoManaged
	}
	// A check that failed before classification carries no ownership mode.
	// The recorded credential source still names the mode when it is known;
	// otherwise Sharko-managed is the honest default for a git-listed
	// cluster — it is what an empty connectionManagedBy means at
	// registration.
	if v.CredentialSourceType == models.CredsSourceInlineKubeconfig {
		return managementModeLegacyInline
	}
	return managementModeSharkoManaged
}

// managedScopeFor names what Sharko owns per mode (product correction 4).
// legacy_inline gets addon_labels: the connection is nominally Sharko's, but
// addon labels are the exact scope the code can actually rebuild without the
// live Secret — the repair policy for inline is addon-labels-only for the
// same reason.
func managedScopeFor(mode string) string {
	switch mode {
	case managementModeSelfManaged, managementModeLegacyInline:
		return managedScopeAddonLabels
	case managementModeForeignOwned:
		return managedScopeNone
	default:
		return managedScopeFullConnection
	}
}

// modeStatementFor returns the approved sentence for the mode.
func modeStatementFor(mode string) string {
	switch mode {
	case managementModeSelfManaged:
		return modeStatementSelfManaged
	case managementModeLegacyInline:
		return modeStatementLegacyInline
	case managementModeForeignOwned:
		return modeStatementForeignOwned
	default:
		return modeStatementSharkoManaged
	}
}

// isAddonLabelDrift answers "is this difference about one of Sharko's own
// addon-enablement labels?" — the question groupConnectionDrift needs and the
// question the whole addon_labels bucket means.
//
// IT IS NOT "DOES THE PATH START WITH metadata.labels[". That is what this
// used to be, and it was wrong, because the comparison also compares three
// labels that are NOT addon labels and produce the identical path shape:
// app.kubernetes.io/managed-by, argocd.argoproj.io/secret-type and
// sharko.dev/connectivity-check (internal/connectioncompare/fields.go —
// ownedLabelKey, systemLabelsOwnedAt, connectivityCheckOwnedAt). Sorting
// those into the addon-labels bucket cleared approval_required, withheld the
// full-connection repair that is the only thing that restores them, and
// explained a connection ArgoCD no longer recognises as "some addon labels do
// not match".
//
// argosecrets.IsAddonLabelKey is the ONE predicate that decides this
// everywhere else — the reconciler's drift computation, the self-heal write
// primitive and connectioncompare's own ownedLabelKey all ask it. This asks
// the same one rather than approximating it with a prefix.
func isAddonLabelDrift(path string) bool {
	key, ok := connectioncompare.LabelKeyFromFieldPath(path)
	if !ok {
		return false
	}
	return argosecrets.IsAddonLabelKey(key)
}

// groupConnectionDrift sorts the comparison's differences into the three
// managed domains. Any sensitive difference routes to credential_material
// regardless of its path — the sensitive flag wins, so a future sensitive
// field can never land in a group whose wire type carries values.
func groupConnectionDrift(v connectionComparisonView) connectionReconciliationDrift {
	d := connectionReconciliationDrift{
		ConnectionConfiguration: []connectionReconciliationDriftEntry{},
		CredentialMaterial:      []connectionReconciliationSensitiveDriftEntry{},
		AddonLabels:             []connectionReconciliationDriftEntry{},
		NotChecked:              []connectionComparisonNotChecked{},
	}
	for _, diff := range v.Differences {
		switch {
		case diff.Sensitive:
			d.CredentialMaterial = append(d.CredentialMaterial, connectionReconciliationSensitiveDriftEntry{
				Path:      diff.Path,
				Status:    diff.Status,
				Sensitive: true,
			})
		case isAddonLabelDrift(diff.Path):
			d.AddonLabels = append(d.AddonLabels, connectionReconciliationDriftEntry{
				Path: diff.Path, Status: diff.Status, Expected: diff.Expected, Live: diff.Live,
			})
		default:
			d.ConnectionConfiguration = append(d.ConnectionConfiguration, connectionReconciliationDriftEntry{
				Path: diff.Path, Status: diff.Status, Expected: diff.Expected, Live: diff.Live,
			})
		}
	}
	d.NotChecked = append(d.NotChecked, v.NotChecked...)
	return d
}

// outOfSyncApprovalReason picks the honest sentence for approval-gated
// drift. The generic "…through the repair action" sentence may ONLY appear
// when repair_connection IS the offered action. When the door is withheld,
// the body explains why it is absent instead of pointing at it — the
// contract's own "the plan explaining what is withheld" rule:
//
//   - legacy_inline: the mode-appropriate migration sentence, which never
//     mentions the repair action (the door does not exist for this mode).
//   - the comparison's own limit sentence when it carries one — verbatim.
//     That is the R3-8 commit-unknown withdrawal sentence ("Sharko cannot
//     tell which commit your Git branch is on…"), the unknown-source
//     "source not recorded / not understood" sentences, or the
//     backend-unreadable sentence.
//   - otherwise a fixed fallback that names the absence plainly.
func outOfSyncApprovalReason(v connectionComparisonView, mode string, repairOffered bool) string {
	if repairOffered {
		return reasonOutOfSyncApprovalRequired
	}
	if mode == managementModeLegacyInline {
		return reasonOutOfSyncLegacyInline
	}
	if v.LimitReason != "" {
		return v.LimitReason
	}
	return reasonOutOfSyncRepairWithheld
}

// buildReconciliationSync derives the sync block. The mapping is the
// page-state matrix v3/v4, row by row.
//
// repairOffered is an input because the approval sentences depend on whether
// the repair door is actually offered — a generic "…through the repair
// action" sentence must never sit next to a withheld door. It is a bool and
// not the whole plan on purpose: the plan also depends on the self-heal
// setting, and this block must provably not (see connection_canonical.go).
// repairConnectionOffered is the single answer both callers use.
func buildReconciliationSync(v connectionComparisonView, mode string, drift connectionReconciliationDrift, repairOffered bool) connectionReconciliationSync {
	sync := connectionReconciliationSync{
		// CheckedAt copies the comparison's own timestamp — a string, never a
		// Go time, so a zero time cannot be fabricated (the W3-6 rule).
		CheckedAt:                 v.CheckedAt,
		LastSuccessfulApplication: v.liveWrittenAt,
	}
	approval := len(drift.ConnectionConfiguration) > 0 || len(drift.CredentialMaterial) > 0
	sync.ApprovalRequired = approval

	switch connectioncompare.Status(v.Status) {
	case connectioncompare.StatusCheckFailed:
		// Rows 8 and 16 — a check that could not finish is unknown, never
		// out_of_sync and never synced.
		sync.State = syncStateUnknown
		sync.VerificationScope = verificationScopeNone
		sync.Reason = v.FailureReason

	case connectioncompare.StatusOwnershipConflict:
		// Row 9 — blocked is deterministic: foreign ownership only.
		sync.State = syncStateBlocked
		sync.VerificationScope = verificationScopeNone
		sync.Reason = v.LimitReason

	case connectioncompare.StatusMissing:
		// Rows 10, 11 and 11a — no Secret is out_of_sync with the definition,
		// and the reason must be honest about whether Sharko can restore it.
		//
		// ONE ANSWER, SHARED WITH THE COMPARISON ENDPOINT. This used to be a
		// switch over the four management modes, whose default promised
		// creation — so an unknown-source connection was told Sharko would
		// build it a Secret while the plan below correctly refused to promise
		// any such thing.
		//
		// It then called SecretMissingReason with a Mode rebuilt from the
		// view's mode string, and that was still wrong: the mode alone does
		// not say whether Sharko can actually rebuild the Secret. Classify
		// hands out the same two backend modes whether the secrets backend
		// can be read or not, so a cluster whose provider was unreachable was
		// told to wait for a reconciler pass that cannot run — right below a
		// limit sentence saying Sharko cannot read those credentials. It now
		// asks with the whole classified Policy, carried on the view.
		sync.State = syncStateOutOfSync
		sync.VerificationScope = verificationScopeNone
		sync.Reason = connectioncompare.SecretMissingReason(v.policy)

	case connectioncompare.StatusOutOfSync:
		// Rows 3–7 and 14 — a reported difference is out_of_sync at every
		// scope.
		sync.State = syncStateOutOfSync
		sync.VerificationScope = verificationScopeForComparison(v)
		if approval {
			sync.Reason = outOfSyncApprovalReason(v, mode, repairOffered)
		} else {
			sync.Reason = reasonOutOfSyncLabelsOnly
		}

	case connectioncompare.StatusSynced:
		// Row 1 — the comparison itself only produces synced at full scope
		// with nothing unchecked, so this is the one bare-synced path. A
		// reported difference beats the word: if the input ever carried both
		// (which Compare never does), the difference wins, fail-closed. And
		// the scope comes from the same honesty mapping as everywhere else,
		// so an unchecked field can never ride under a "full" claim — the
		// invariant guard then downgrades the word too.
		if len(v.Differences) > 0 {
			sync.State = syncStateOutOfSync
			sync.VerificationScope = verificationScopeForComparison(v)
			if approval {
				sync.Reason = outOfSyncApprovalReason(v, mode, repairOffered)
			} else {
				sync.Reason = reasonOutOfSyncLabelsOnly
			}
			break
		}
		sync.State = syncStateSynced
		sync.VerificationScope = verificationScopeForComparison(v)

	case connectioncompare.StatusLimited:
		// Everything the comparison could check matched, but the scope was
		// narrower than the whole connection. Same fail-closed rule as
		// above: a difference in the input means out_of_sync, whatever the
		// status word said.
		if len(v.Differences) > 0 {
			sync.State = syncStateOutOfSync
			sync.VerificationScope = verificationScopeForComparison(v)
			if approval {
				sync.Reason = outOfSyncApprovalReason(v, mode, repairOffered)
			} else {
				sync.Reason = reasonOutOfSyncLabelsOnly
			}
			break
		}
		if mode == managementModeSelfManaged && len(v.NotChecked) == 0 {
			// Row 13 — Sharko owns ONLY the addon labels here, and every one
			// of them was compared and matched: that is full verification of
			// the owned scope, and honestly synced. managed_scope names the
			// scope so the page can say "Addon labels synced", never bare
			// "Synced".
			sync.State = syncStateSynced
			sync.VerificationScope = verificationScopeForComparison(v)
		} else if mode == managementModeSelfManaged {
			// Row 13, but one of those addon labels was not compared — B15.
			// This branch used to say synced and lean on the fail-closed
			// invariant further down to take the word back. That worked, but
			// it logged the downgrade as a bug in Sharko, and this is not one:
			// a label named on the hands-off list that Git also declares is a
			// real disagreement an operator can settle. So the honest answer
			// is written here, and the invariant below stays the last resort
			// it was meant to be.
			sync.State = syncStateUnknown
			sync.VerificationScope = verificationScopeForComparison(v)
			sync.Reason = reasonVerificationIncomplete
		} else {
			// Rows 2 and 12 — part of what Sharko owns was not compared
			// (EKS credential content, an inline credential, an unreadable
			// backend, an unrecorded source). "I found no problem in the part
			// I looked at" is not "this is right": the state is unknown, with
			// the comparison's own limit sentence as the reason — and never
			// an empty reason: a full-scope policy whose read then came back
			// narrower (provider hot-swap between the policy decision and
			// the read) carries no limit sentence, so a fixed fallback
			// covers it.
			sync.State = syncStateUnknown
			sync.VerificationScope = verificationScopeForComparison(v)
			sync.Reason = v.LimitReason
			if sync.Reason == "" {
				sync.Reason = reasonVerificationIncomplete
			}
		}

	default:
		// Row 15 and fail-closed: no comparison outcome at all. Unknown,
		// nothing verified. CheckedAt stays whatever the caller had — absent
		// when never run.
		sync.State = syncStateUnknown
		sync.VerificationScope = verificationScopeNone
		sync.Reason = v.FailureReason
	}
	return sync
}

// verificationScopeForComparison maps the comparison's scope onto the
// verification_scope enum for a comparison that actually ran.
//
// The NotChecked list beats the scope word: a comparison that reported ANY
// deliberately-unchecked field did not verify everything Sharko owns,
// whatever its declared scope says. That combination is real — a provider
// hot-swap between the policy decision and the stored-facts read leaves a
// full-scope comparison with the data fields unchecked — and "full" would
// be a false claim there. The invariant sweep pins scope-vs-NotChecked
// honesty across every combination.
func verificationScopeForComparison(v connectionComparisonView) string {
	switch connectioncompare.Scope(v.Scope) {
	case connectioncompare.ScopeFull:
		if len(v.NotChecked) > 0 {
			return verificationScopePartial
		}
		return verificationScopeFull
	case connectioncompare.ScopeAddonLabelsOnly:
		// Everything Sharko owns at this scope (the addon labels) was
		// compared — full, of the owned scope managed_scope names. Unless
		// something was reported unchecked, which demotes it the same way.
		if len(v.NotChecked) > 0 {
			return verificationScopePartial
		}
		return verificationScopeFull
	case connectioncompare.ScopeLimited:
		// The identity, type and owned labels were genuinely compared; at
		// least one owned field (credential content, or the data fields with
		// no independent copy) was not — partial, never full.
		return verificationScopePartial
	default:
		return verificationScopeNone
	}
}

// buildReconciliationPlan derives the plan block: what happens by itself,
// what needs approval, and the one explicit action this state permits.
func buildReconciliationPlan(v connectionComparisonView, mode string, drift connectionReconciliationDrift, selfHealOn bool) connectionReconciliationPlan {
	p := connectionReconciliationPlan{Action: planActionNone, ActionScopes: []string{}}
	status := connectioncompare.Status(v.Status)
	hasConfigDrift := len(drift.ConnectionConfiguration) > 0
	hasCredDrift := len(drift.CredentialMaterial) > 0
	hasLabelDrift := len(drift.AddonLabels) > 0

	// labelsSelfHeal mirrors the reconciler's real behaviour (the fleet
	// rows' connectionSelfHeals rule): a v4 repo converges addon labels
	// every tick; a self-managed connection has its labels re-applied every
	// tick unconditionally; a v3 Sharko-managed cluster converges other
	// slash-free labels only when the self-heal setting is on.
	labelsSelfHeal := mode == managementModeSelfManaged ||
		v.ComparedPath == clusterreconciler.V4ManagedClustersPath ||
		selfHealOn

	// …AND NONE OF IT HAPPENS ON A SECRET SHARKO'S OWN LISTING CANNOT SEE.
	//
	// Every automatic label path above runs from the periodic pass, and that
	// pass reaches a cluster's Secret through listManagedSecrets, which
	// selects on app.kubernetes.io/managed-by=sharko. A Secret without that
	// marker is not in the map, so the cluster is treated as one to create,
	// and the create path re-reads it, finds it unmarked and leaves it alone
	// as Adopt territory — every tick, forever. Promising "Sharko
	// automatically reapplies the addon labels defined in Git." there is a
	// promise nothing can keep.
	//
	// Self-managed connections are excluded from the gate on purpose: they
	// are DELIBERATELY unmarked (SyncLabelsOnly strips the marker so the
	// orphan sweeps leave the person's connection alone) and their labels are
	// written by a different primitive that never asks about ownership. Their
	// promise is kept.
	if mode != managementModeSelfManaged && v.liveOwnershipMarkerRefusesWrite() {
		labelsSelfHeal = false
	}

	switch mode {
	case managementModeForeignOwned:
		// Row 9 — the takeover door is the only path that changes an owner.
		p.Action = planActionTakeOver
		return p

	case managementModeSelfManaged:
		// Rows 13 and 14 — never a connection repair.
		//
		// RULING (a), 2026-08-19: matrix row 14 was WRONG and the running
		// reconciler is authoritative. syncSelfManaged calls
		// argosecrets.Manager.SyncLabelsOnly on EVERY tick for EVERY
		// self-managed entry, unconditionally and with no setting gate
		// (internal/clusterreconciler/reconciler.go — the reconcile pass and
		// the resync path both do it), which is also why the fleet row
		// already reports self_heals: true for these connections. So label
		// drift here converges by itself: the plan says so, and Sharko does
		// NOT offer a manual button for work the reconciler performs on its
		// own. approval_required stays false — nothing here touches
		// connection configuration or credential material.
		if hasLabelDrift {
			p.Automatic = planAutomaticLabelSync
		}
		return p

	case managementModeLegacyInline:
		// Rows 11 and 12 — NEVER repair_connection: there is no durable
		// credential reference to rebuild from (product ruling 1). The
		// documented migration path is the way forward. Pure label drift
		// still gets the label-sync door, which is within the rebuildable
		// scope.
		if status == connectioncompare.StatusOutOfSync && hasLabelDrift && !hasConfigDrift && !hasCredDrift {
			p.Action = planActionSyncAddonLabels
			p.ActionScopes = []string{"metadata.labels"}
		} else {
			p.Action = planActionMigrateCredential
		}
		if hasConfigDrift || hasCredDrift {
			// Never the generic repair-door sentence: the repair action does
			// not exist for this mode, so the sentence points at migration.
			p.RequiresApproval = reasonOutOfSyncLegacyInline
		}
		return p
	}

	// Sharko-managed (including an unknown recorded source, whose blocked
	// credential_reference condition carries the difference).
	switch status {
	case connectioncompare.StatusMissing:
		// Row 10 — creation genuinely happens by itself, but only when
		// Sharko really can rebuild this connection.
		//
		// THIS WAS THE SAME FALSE PROMISE AS sync.reason, one block over.
		// It used to test the RECORDED credentials source, which only says
		// what the cluster's git entry claims — not whether Sharko can read
		// that source. A cluster recorded as secret-kubeconfig whose provider
		// is unconfigured or unreachable got "Sharko will create this
		// connection Secret … on its own" when no such creation could ever
		// happen. The classified Policy is the one thing that knows, and both
		// promises on this page now come from it.
		if v.policy.RepairScope == connectioncompare.RepairScopeFullConnection {
			p.Automatic = planAutomaticSecretCreate
		}

	case connectioncompare.StatusOutOfSync:
		if hasConfigDrift || hasCredDrift {
			// Rows 3, 6, 7 — the approval-gated half. The repair offer
			// follows the comparison's own policy: it is already withdrawn
			// when the compared commit is unknown (R3-8) or the mode does not
			// allow a full rewrite. The generic repair-door sentence rides
			// ONLY with an actually-offered repair; a withheld door carries
			// the explanation of the absence instead (the comparison's own
			// sentence when it has one — the R3-8 withdrawal, the
			// unknown-source sentences — else the fixed fallback).
			if repairConnectionOffered(v, mode, drift) {
				p.Action = planActionRepairConnection
				p.ActionScopes = []string{"metadata.labels", "data.name", "data.server", "data.config"}
				p.ReviewedCommit = v.ComparedCommit
				p.RequiresApproval = planRequiresApprovalSentence
			} else {
				p.RequiresApproval = outOfSyncApprovalReason(v, mode, false)
			}
			if hasLabelDrift && labelsSelfHeal {
				p.Automatic = planAutomaticLabelSync
			}
		} else if hasLabelDrift {
			// Rows 4 and 5 — labels-only drift: converges by itself where the
			// reconciler really does that, otherwise the label-sync door.
			if labelsSelfHeal {
				p.Automatic = planAutomaticLabelSync
			} else {
				p.Action = planActionSyncAddonLabels
				p.ActionScopes = []string{"metadata.labels"}
			}
		}

	case connectioncompare.StatusLimited:
		// Row 2 — EKS clean: the repair stays offered by policy ("I cannot
		// prove this is right, but I can make it right"; one token, on the
		// write only). Only a full-connection repair the comparison itself
		// offers qualifies.
		if repairConnectionOffered(v, mode, drift) {
			p.Action = planActionRepairConnection
			p.ActionScopes = []string{"metadata.labels", "data.name", "data.server", "data.config"}
			p.ReviewedCommit = v.ComparedCommit
		}

		// StatusSynced (row 1), StatusCheckFailed (rows 8/16) and "no outcome"
		// (row 15): action none, nothing automatic to promise.
	}
	return p
}

// liveSecretCondition states the plainest fact about a connection: whether
// its Secret is there. ONE function, so every condition list answers it the
// same way — the self-managed list did not answer it at all, which is why a
// self-managed connection with no Secret showed the missing Secret nowhere
// on its page.
func liveSecretCondition(v connectionComparisonView) connectionReconciliationCondition {
	if connectioncompare.Status(v.Status) == connectioncompare.StatusMissing {
		return connectionReconciliationCondition{
			ID: conditionLiveSecret, Status: conditionStatusBlocked, Detail: condLiveSecretMissing,
		}
	}
	return connectionReconciliationCondition{
		ID: conditionLiveSecret, Status: conditionStatusOK, Detail: condLiveSecretFound,
	}
}

// buildReconciliationConditions itemises the current facts (product ruling
// 5). Routine success renders compactly on the page; the API always names
// the facts it has. The plan is an input so the approval condition can only
// name the repair door when the plan actually offers it.
func buildReconciliationConditions(v connectionComparisonView, mode string, sync connectionReconciliationSync, plan connectionReconciliationPlan, healthState string, argoUnreachable bool) []connectionReconciliationCondition {
	argoCond := connectionReconciliationCondition{ID: conditionArgoCDConnection}
	switch healthState {
	case healthStateConnected:
		argoCond.Status = conditionStatusOK
		argoCond.Detail = condArgoCDConnected
	case healthStateUnavailable:
		argoCond.Status = conditionStatusAttention
		argoCond.Detail = condArgoCDUnavailable
	case healthStateUnknown:
		argoCond.Status = conditionStatusAttention
		argoCond.Detail = condArgoCDNoConnection
	default:
		argoCond.Status = conditionStatusAttention
		// F8: two different facts hide behind not_checked. When Sharko
		// could not ask ArgoCD, say that — the no-application explanation
		// is reserved for the case where ArgoCD answered and genuinely has
		// never probed this cluster.
		if argoUnreachable {
			argoCond.Detail = condArgoCDUnreachable
		} else {
			argoCond.Detail = condArgoCDNotChecked
		}
	}

	// Self-managed pages answer ownership and ArgoCD health only (product
	// ruling 7): Sharko does not compare, repair or claim ownership of
	// connection data it does not manage.
	//
	// PLUS, SINCE B13, WHETHER THE SECRET IS THERE. That is not connection
	// data Sharko manages — it is the plainest fact about the connection, and
	// leaving it out is why a missing self-managed Secret was not shown as a
	// fact anywhere on the page. It is stated in both directions, so the list
	// does not appear and disappear depending on the answer.
	if mode == managementModeSelfManaged {
		return []connectionReconciliationCondition{
			{ID: conditionOwnership, Status: conditionStatusOK, Detail: condOwnershipGuest},
			liveSecretCondition(v),
			argoCond,
		}
	}

	// A check that could not finish: name the step that failed, by matching
	// the comparison's own fixed sentences — they are constants in this
	// package, so this is identity on literals, not parsing.
	//
	// THIS NOW RUNS BEFORE THE FOREIGN-OWNED BRANCH, and the order is the fix.
	// A foreign-owned connection whose credentials backend could not be read
	// reaches Compare's FAILURE exit, which fires before its ownership exit —
	// so the answer is check_failed with a foreign mode. The foreign branch
	// used to swallow that: the page reported the ownership boundary and never
	// mentioned the backend read that actually failed, which is the one fact
	// the reader needed. Nothing else moves, because every other mode was
	// already reaching this switch.
	//
	// The four details below name their fact DIRECTLY. They used to go through
	// saidOnce, which was dead logic on this path: buildReconciliationSync sets
	// sync.Reason = v.FailureReason for a check_failed answer, so the "is the
	// detail the same as the reason" question was always yes and the fallback
	// always won. Same words on the page, one less sentence comparison
	// deciding what renders.
	if connectioncompare.Status(v.Status) == connectioncompare.StatusCheckFailed {
		// KEYED ON THE TYPED REASON, NOT ON THE SENTENCE.
		//
		// This switch used to have full paragraphs for case values — the very
		// words on the page. So rewording one sentence by a comma would have
		// dropped this whole answer into default and named the wrong failed
		// step, silently. The product owner's ruling is the reason it does not
		// any more: "presentation structure must follow typed facts, never
		// equality between human sentences."
		//
		// v.failure is the fact; v.FailureReason is the words, derived from it
		// in exactly one place (finishView). The sentences below are now free
		// to be rewritten without touching this file.
		switch v.failure {
		case connectioncompare.CheckFailureGitRead, connectioncompare.CheckFailureNoGitConnection:
			return []connectionReconciliationCondition{
				{ID: conditionGitDefinition, Status: conditionStatusBlocked, Detail: condGitDefinitionBlocked},
				argoCond,
			}
		case connectioncompare.CheckFailureLiveRead, connectioncompare.CheckFailureNoHubClient:
			return []connectionReconciliationCondition{
				{ID: conditionGitDefinition, Status: conditionStatusOK, Detail: condGitDefinitionOK},
				{ID: conditionLiveSecret, Status: conditionStatusBlocked, Detail: condLiveSecretUnread},
				argoCond,
			}
		case connectioncompare.CheckFailureBackendRead, connectioncompare.CheckFailureCredentialsUnavailable:
			return []connectionReconciliationCondition{
				{ID: conditionGitDefinition, Status: conditionStatusOK, Detail: condGitDefinitionOK},
				{ID: conditionCredentialReference, Status: conditionStatusBlocked, Detail: condCredentialRefUnread},
				argoCond,
			}
		default:
			return []connectionReconciliationCondition{
				{ID: conditionComparison, Status: conditionStatusBlocked, Detail: condCheckDidNotFinish},
				argoCond,
			}
		}
	}

	// Foreign-owned: NO OWNERSHIP CONDITION ROW AT ALL.
	//
	// The page already has two right places for this fact, and both are
	// already filled: mode_statement explains the boundary, and plan.action
	// offers the take-over. A third sentence added no new fact and no new
	// decision — it just said the same thing again in a different register.
	//
	// The component is OMITTED, not blanked. Returning a condition with an
	// empty detail would paint an empty row rather than remove one, so there
	// is no condition to return, not a condition with no words.
	if mode == managementModeForeignOwned {
		return []connectionReconciliationCondition{argoCond}
	}

	conds := []connectionReconciliationCondition{
		{ID: conditionGitDefinition, Status: conditionStatusOK, Detail: condGitDefinitionOK},
	}

	// Credential reference — per mode.
	switch connectioncompare.Mode(v.OwnershipMode) {
	case connectioncompare.ModeInlineKubeconfig:
		conds = append(conds, connectionReconciliationCondition{
			ID: conditionCredentialReference, Status: conditionStatusBlocked, Detail: modeStatementLegacyInline,
		})
	case connectioncompare.ModeUnknownSource:
		conds = append(conds, connectionReconciliationCondition{
			ID: conditionCredentialReference, Status: conditionStatusBlocked,
			Detail: saidOnce(v.LimitReason, sync.Reason, condCredentialRefUnknownSource),
		})
	case connectioncompare.ModeEKSToken:
		conds = append(conds, connectionReconciliationCondition{
			ID: conditionCredentialReference, Status: conditionStatusAttention, Detail: condCredentialRefEKS,
		})
	case connectioncompare.ModeBackendStoredCredentials:
		if connectioncompare.Scope(v.Scope) == connectioncompare.ScopeFull {
			conds = append(conds, connectionReconciliationCondition{
				ID: conditionCredentialReference, Status: conditionStatusOK, Detail: condCredentialRefOK,
			})
		} else {
			// The backend cannot be read independently right now — the
			// comparison's own limit sentence says so, unless the page is
			// already saying exactly that as the reason.
			conds = append(conds, connectionReconciliationCondition{
				ID: conditionCredentialReference, Status: conditionStatusAttention,
				Detail: saidOnce(v.LimitReason, sync.Reason, condCredentialRefUnreadable),
			})
		}
	}

	// Ownership — from the LIVE MARKER, not from the mode.
	//
	// Reaching here means the connection is not self-managed and not
	// foreign-owned, so the mode says Sharko manages it. That is a fact about
	// the Git record. Whether the live Secret carries Sharko's ownership
	// marker is a separate fact, and it is the one every writer keys on: a
	// Secret without the marker is invisible to the reconciler's listing and
	// refused by both repair paths. Stating "Sharko owns this connection
	// Secret." as a PASSED check about such a Secret was the page asserting
	// something false, right where a reader looks for reassurance.
	ownershipCond := connectionReconciliationCondition{
		ID: conditionOwnership, Status: conditionStatusOK, Detail: condOwnershipOK,
	}
	if v.liveOwnershipMarkerRefusesWrite() {
		ownershipCond.Status = conditionStatusBlocked
		ownershipCond.Detail = condOwnershipNotMarked
	}
	conds = append(conds, ownershipCond)

	conds = append(conds, liveSecretCondition(v))

	// A Secret that is not there was never compared, so there is no
	// comparison fact to state about it.
	if connectioncompare.Status(v.Status) != connectioncompare.StatusMissing {
		switch connectioncompare.Status(v.Status) {
		case connectioncompare.StatusSynced:
			conds = append(conds, connectionReconciliationCondition{
				ID: conditionComparison, Status: conditionStatusOK, Detail: condComparisonFull,
			})
		case connectioncompare.StatusOutOfSync:
			conds = append(conds, connectionReconciliationCondition{
				ID: conditionComparison, Status: conditionStatusAttention, Detail: condComparisonDrift,
			})
		case connectioncompare.StatusLimited:
			// THE ITEM 7 CASE. sync.reason already carries the comparison's
			// own limit sentence on this path, and both fields render
			// expanded, so a clean EKS connection said it twice word for
			// word — inside a page that says a short form of it in the
			// headline as well.
			conds = append(conds, connectionReconciliationCondition{
				ID: conditionComparison, Status: conditionStatusAttention,
				Detail: saidOnce(v.LimitReason, sync.Reason, condComparisonPartial),
			})
		}
	}

	conds = append(conds, argoCond)

	if sync.ApprovalRequired {
		// The condition names the repair door only when the plan actually
		// offers it; a withheld door carries the explanation of its absence.
		detail := condApprovalRequired
		if plan.Action != planActionRepairConnection {
			// A withheld door explains its absence — but sync.reason is
			// computed from the same function, so on this path the two were
			// the identical sentence.
			detail = saidOnce(outOfSyncApprovalReason(v, mode, false), sync.Reason, condApprovalNoRepair)
		}
		conds = append(conds, connectionReconciliationCondition{
			ID: conditionApproval, Status: conditionStatusBlocked, Detail: detail,
		})
	}
	return conds
}
