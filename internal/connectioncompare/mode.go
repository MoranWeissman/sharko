// Package connectioncompare answers one question, read-only: does a
// cluster's ArgoCD connection Secret look the way Sharko means it to look,
// and how much of that can Sharko honestly check?
//
// Nothing in this package writes. There is no Kubernetes write, no Git write,
// no repair. Step 3 of the connection-Secret feature adds repair and reads
// this package's policy to decide what it may offer; step 4 adds the page.
// Both of them ask HERE — the mode policy below is the only place the
// "what may Sharko say, and what may Sharko offer to do" question is
// answered, so the answer cannot drift between the check and the fix.
//
// Two hard rules the whole package is shaped around:
//
//  1. The expected side is NEVER built from the live Secret. Building it from
//     the live Secret would compare the Secret with itself, match every time,
//     and report a badly drifted connection as in sync. Where Sharko has no
//     independent copy of the credential material, the honest answer is a
//     narrower scope — not a guess and not a self-comparison.
//  2. Sensitive material is compared in memory and neither side is ever
//     returned. See fields.go.
package connectioncompare

import (
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Mode is which of the seven kinds of connection this is. Exactly one applies
// to any cluster at any moment — Classify picks it, and the order it checks in
// is the policy (see Classify).
type Mode string

const (
	// ModeBackendStoredCredentials — the cluster's kubeconfig lives in the
	// secrets backend (credsSource secret-kubeconfig), that backend can be read
	// independently of the live Secret, the connection is Sharko's to manage,
	// and Sharko owns the live Secret.
	// Sharko can re-fetch the stored kubeconfig and rebuild the whole expected
	// Secret without reading the live one, so everything is checkable.
	ModeBackendStoredCredentials Mode = "backend_stored_credentials"

	// ModeEKSToken — structured EKS metadata in the secrets backend
	// (credsSource eks-token), same conditions. The metadata is re-readable,
	// but it is metadata: the configured credentials source stores what is
	// needed to CREATE a sign-in token, not a token. So there is no credential
	// on the expected side and nothing to compare the live blob against, and
	// the scope is limited for that one reason. A repair is still offered,
	// because rewriting the connection from the backend is exactly the fix —
	// and a WRITE does create a token, once, at the moment it writes.
	//
	// A check creates nothing. The read-only path goes through
	// providers.StoredConnectionFacts, which stops at the stored payload and
	// never reaches the token mint.
	//
	// The name says token and not exec on purpose. The writer for this source
	// supplies a bearer token it creates at write time, so BuildClusterSecret
	// emits the bearerToken shape — not an execProviderConfig. Calling this
	// mode "exec" would describe a shape Sharko does not write here.
	ModeEKSToken Mode = "eks_token"

	// ModeInlineKubeconfig — the cluster was registered with a pasted
	// kubeconfig (credsSource inline-kubeconfig). Those credentials were
	// written into the ArgoCD Secret and kept nowhere else, so there is no
	// independent copy to rebuild an expected Secret from. Labels are still
	// checkable, because they come from Git.
	ModeInlineKubeconfig Mode = "inline_kubeconfig"

	// ModeSelfManaged — the record says connectionManagedBy: user. The person
	// creates and maintains this Secret; Sharko only ever syncs addon labels
	// onto it and never touches its credential material. So Sharko holds no
	// expectation about anything except those labels.
	ModeSelfManaged Mode = "self_managed"

	// ModeAdopted — the live Secret carries an adopted marker: it existed in
	// the user's ArgoCD before Sharko, and Sharko came in as a guest. Same
	// stance as self-managed: addon labels only.
	ModeAdopted Mode = "adopted"

	// ModeForeignOwned — the live Secret's ownership label names some other
	// tool. Sharko compares nothing and offers nothing. This is the existing
	// ownership gate (the one Manager.Ensure refuses on), not a new one.
	ModeForeignOwned Mode = "owned_by_another_tool"

	// ModeUnknownSource — Sharko does not know where this cluster's
	// credentials are kept. Two different situations land here, and they get
	// different sentences (see Classify):
	//
	//  1. The recorded source is empty. The record predates the field, which
	//     every install upgraded from an older Sharko has.
	//  2. The recorded source is a value that is not on the supported list —
	//     a typo, a hand-edit, or something a future version writes that this
	//     version has never heard of.
	//
	// Either way Sharko never guesses, never backfills the field, and never
	// reads the live Secret to find out.
	ModeUnknownSource Mode = "unknown_source"
)

// supportedCredsSources is the ALLOWLIST of recorded credential sources this
// version of Sharko understands. Nothing else gets a scope wider than limited,
// and nothing else is ever offered a full repair.
//
// This is an allowlist and not a "handle the ones I know, fall through for the
// rest" chain on purpose. A fall-through gives the widest, most trusting
// treatment to exactly the values nobody planned for: a typo in a hand-edited
// file, a value written by a newer Sharko this binary has never seen, or
// anything an attacker managed to get into the git record. The safe default for
// a value Sharko does not understand is to admit it does not understand it.
//
// TestClassify_EveryModelsCredsSourceConstantIsOnTheAllowlist fails loudly if
// a new constant is added to internal/models without being added here.
var supportedCredsSources = map[string]bool{
	models.CredsSourceInlineKubeconfig: true,
	models.CredsSourceSecretKubeconfig: true,
	models.CredsSourceEKSToken:         true,
}

// The two sentences for the unknown-source mode. They are separate because the
// two situations are separate: one is an old record that never had the
// information, the other is information Sharko cannot make sense of. Telling a
// person "your record is old" when the real problem is a typo would send them
// looking in the wrong place.
const (
	// LimitReasonSourceNotRecorded — the record has no credential source at
	// all, because it was written before Sharko had the field.
	LimitReasonSourceNotRecorded = "This cluster's record does not say where its credentials are kept — it was registered by an older version of Sharko. Sharko will not guess, so it checks the labels and the plain connection facts only."

	// LimitReasonSourceNotUnderstood — the record has a credential source and
	// this version of Sharko does not know what it means.
	LimitReasonSourceNotUnderstood = "Sharko does not recognise what this cluster's record says about where its credentials are kept, so it will not assume anything. It checks the labels and the plain connection facts only. Check the cluster's entry in Git, or update Sharko if the entry was written by a newer version."

	// LimitReasonEKSNoStoredCredential is the EKS answer, and it is exact
	// wording the product owner signed off character for character. It names
	// what WAS checked instead of claiming "everything else" — the old sentence
	// said everything else was checked, which was not true, because a
	// deliberately empty annotation set and the guest-scope rules mean "the
	// rest" is a specific list and not all of it.
	//
	// The reason half used to say the sign-in token changes every time it is
	// created. That reading suggested the check creates a token, which it does
	// not: the read-only path stops at the stored payload and creates nothing.
	// The real limit is that there is no stored credential to compare against at
	// all, and that is what the sentence says now (product owner's wording,
	// 2026-08-13). It is pinned by TestClassify_EKSTokenLimitReasonIsExact so
	// nobody paraphrases it later.
	LimitReasonEKSNoStoredCredential = "Sharko checked the Secret identity, type, server, and owned labels. The backend stores EKS cluster details, not a reusable sign-in credential. Sharko therefore has no stored credential to compare with `data.config`."
)

// The five sentences for "there is no connection Secret", one per situation.
//
// THERE USED TO BE ONE, AND IT PROMISED SOMETHING SHARKO CANNOT DO. Compare
// answered every missing Secret with "This cluster has no connection Secret
// yet. The reconciler will create it on its next pass." — unconditionally,
// whatever mode the connection was in. That is true for a cluster whose
// credentials live in a backend Sharko can read. It is FALSE for a
// self-managed connection, where the person maintains the Secret and Sharko
// deliberately never creates it; false for a legacy pasted-credential
// connection, whose credential existed only inside the Secret that is now
// gone; and false for a connection whose credentials source Sharko cannot
// read, where the create attempt cannot get off the ground. The reconciliation endpoint
// overrode the sentence per mode, so the connection page read correctly —
// but the older comparison endpoint shipped the raw sentence, and a person
// reading it waited for a Secret that was never coming.
//
// SecretMissingReason below is the one answer both endpoints now use, so
// they cannot tell different stories about the same connection. Each
// sentence is pinned by exact text in
// TestCompare_MissingSecretSentenceIsModeAware.
const (
	// LimitReasonSecretMissingDurable is the ONLY one that promises creation,
	// and it is only ever produced for a mode whose credentials really can be
	// rebuilt from the configured source.
	//
	// The second half matches the plan sentences on the connection page word
	// for word in SHAPE — "Sharko automatically <does the thing>." On a
	// missing Secret the page carries this sentence as sync.reason AND the
	// plan sentence right beside it, so the two have to read like one voice.
	// That shape is the product owner's correction of 2026-08-20: the earlier
	// wording ended with a conversational tail about nobody having to ask,
	// which was rejected and is now retired in the banned-wording sweep. It
	// names no machinery and promises no clock (product ruling, 2026-08-19:
	// information text speaks in the user's terms, and the interval is
	// operator-settable so any "within N minutes" would be a lie somewhere).
	// Pinned character for character by
	// TestCompare_MissingSecretSentenceIsModeAware and by
	// TestConnectionReconciliation_NewSentencesExact on the API side.
	LimitReasonSecretMissingDurable = "This cluster has no connection Secret right now. Sharko automatically creates it from Git and the configured credentials source."

	// LimitReasonSecretMissingSelfManaged — the person maintains this
	// connection. Sharko does not create it, and says so.
	LimitReasonSecretMissingSelfManaged = "You maintain this cluster's connection Secret yourself and it has not been created yet. Sharko does not create it."

	// LimitReasonSecretMissingLegacyInline — the credential existed only
	// inside the Secret that is gone, so there is nothing to rebuild from.
	LimitReasonSecretMissingLegacyInline = "This cluster's connection Secret is gone, and its credential existed only in that Secret — Sharko cannot restore it from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it."

	// LimitReasonSecretMissingUnknownSource — the cluster's record does not
	// name a credentials source Sharko understands.
	//
	// THIS SENTENCE USED TO REFUSE, AND THE REFUSAL WAS NOT TRUE (B3a). It
	// said Sharko had no credentials source it understands for this cluster
	// and would therefore build nothing. Sharko's create path never looks at
	// the recorded source at all: the periodic pass partitions on
	// connectionManagedBy alone (internal/clusterreconciler/reconciler.go,
	// reconcileDiff), and createOne resolves credentials through
	// ConnectionCredentialSpecForWrite, which uses the cluster's lookup key
	// and its role and nothing else (internal/clusterreconciler/
	// repair_credentials.go). The identifier CredsSource does not appear in a
	// single production file in that package. So a cluster whose record has
	// no source — which is EVERY cluster registered before the field existed,
	// on every install upgraded from an older Sharko — was told Sharko would
	// stand still, while Sharko was in fact about to build the connection out
	// of whatever the credentials backend holds under that cluster's key.
	//
	// The honest answer is neither a promise nor a refusal: Sharko does try,
	// and the try may find nothing, and what the person can do about it is
	// record a source. The retired refusal is in the tree-wide banned-wording
	// sweep so it cannot come back.
	LimitReasonSecretMissingUnknownSource = "This cluster has no connection Secret right now. Sharko still tries to build it from Git and the credentials backend, but this cluster's record does not name a credentials source Sharko understands, so the attempt may not find any credentials to use. Record a supported credentials source for this cluster."

	// LimitReasonSecretMissingBackendUnreadable — the record names a
	// credentials source Sharko understands, but Sharko cannot read that
	// backend at the moment, so the reconciler pass that would create the
	// Secret cannot happen either. Telling this person to wait for the next
	// pass would send them away to wait for something that never comes.
	LimitReasonSecretMissingBackendUnreadable = "This cluster has no connection Secret right now. Its credentials are kept in a secrets backend, and Sharko cannot read that backend at the moment, so it cannot create the Secret. Fix the secrets backend connection first, then check again."
)

// The five scope-limit sentences Classify itself produces.
//
// THEY USED TO BE TYPED INLINE, inside the Policy literals in Classify. That
// put five shipped, user-facing sentences somewhere no catalog and no guard
// could see them: the message catalog collects CONSTANTS, so a sentence that
// is not a constant is silently outside it, and the catalog would have looked
// complete while missing five of the sentences a person actually reads. Two of
// them were already hand-copied into test fixtures, and one into a browser
// test — the exact drift this whole contract exists to make impossible.
//
// The values are byte-for-byte what Classify returned before. Nothing about
// the classification changed; only where the words live. (One constant has
// since been removed outright — see the note directly below.)
//
// A FOREIGN-OWNED CONNECTION HAS NO LIMIT SENTENCE, and that is deliberate.
// There used to be one here. It said another tool owns the connection and told
// the reader to take it over first — and every surface that shows a
// foreign-owned connection already says both of those things, once each, in
// the place that belongs to them: the management-mode statement states the
// boundary, and plan.action offers the take-over. A third sentence added no
// new fact and no new decision, so it is gone rather than reworded.
//
// It also had to go for a second reason. Compare threw this sentence away and
// substituted a hand-written literal of its own, which happened to be
// byte-identical to the mode statement — and that accidental equality was the
// only thing making the page de-duplicate correctly. Presentation structure
// must follow typed facts, never equality between human sentences, so both
// literals were removed instead of being kept in step by hand. The typed facts
// (Mode, Scope and the take-over action) carry the whole answer now.
const (
	// LimitReasonSelfManaged — the record says a person maintains this
	// connection.
	LimitReasonSelfManaged = "You maintain this cluster's ArgoCD connection yourself, so Sharko only checks the addon labels on it and never its connection details."

	// LimitReasonAdopted — the live Secret was already there and Sharko came
	// in as a guest.
	LimitReasonAdopted = "Sharko adopted this cluster's existing ArgoCD connection rather than creating it, so Sharko only checks the addon labels on it and never its connection details."

	// LimitReasonInlineKubeconfig — the pasted credential is only in the live
	// Secret, so there is no independent copy to compare against.
	LimitReasonInlineKubeconfig = "This cluster was registered with a pasted kubeconfig, and those credentials are only stored in the connection itself. Sharko has no second copy to check the connection details against, so it checks the labels and the plain connection facts only."

	// LimitReasonBackendUnreadable — the source is one Sharko understands, but
	// the backend cannot be read right now. Distinct from
	// LimitReasonSecretMissingBackendUnreadable, which answers the different
	// question "why is there no Secret".
	LimitReasonBackendUnreadable = "This cluster's credentials are kept in a secrets backend, but Sharko cannot read them from there right now, so it cannot check the connection details against them."
)

// SecretMissingReason is the honest sentence for a connection whose live
// Secret does not exist.
//
// IT TAKES THE WHOLE POLICY, NOT THE MODE, AND THAT MATTERS. An earlier
// version keyed on the mode alone and argued that was safe because "Classify
// only reaches ModeBackendStoredCredentials or ModeEKSToken through the
// supportedCredsSources allowlist". The allowlist is necessary and it is NOT
// sufficient: Classify returns those same two modes from a second place, the
// !BackendCanProvideStoredFacts branch, which means Sharko cannot read the
// credentials source right now. One connection then carried two sentences that
// contradicted each other at the same moment — a limit reason saying Sharko
// cannot read the credentials, and a missing-Secret sentence promising it
// would build the Secret from them the next time the cluster reconciler runs.
// A cluster whose provider is unconfigured or unreachable was told to wait for
// a pass that cannot run, and the page never named the thing to fix.
//
// So the promise is gated on RepairScopeFullConnection, which is the policy's
// own statement that Sharko may rewrite this whole connection from the backend
// plus git. Classify produces it on exactly the two paths where the backend
// really can be read; the unreadable-backend branch drops to
// RepairScopeAddonLabelsOnly, and every other mode never had it. A promise is
// opt-in, and it is now opt-in on the capability rather than on the label.
//
// Taking Policy rather than two arguments is deliberate: Classify is the only
// thing that builds a Policy, so a caller cannot pair a mode with a
// capability those two never actually came with.
// TestClassifyThenSecretMissingReason_ComposedOverRealInputs drives the join
// itself, which is where this defect lived.
//
// # Only one mode is a real refusal, and ModeAdopted was never it (B3b)
//
// ModeAdopted used to share the self-managed answer, which tells the reader
// "Sharko does not create it". Adopted and self-managed do look alike while a
// Secret exists — Sharko is a guest on both, and touches only the addon labels
// — but they part company the moment the Secret is gone. Self-managed is
// recorded in Git as connectionManagedBy: user, and that recorded value is the
// ONE thing the create path partitions on, so Sharko really does stand still
// (internal/clusterreconciler/reconciler.go, reconcileDiff). Adopted is not
// recorded in Git at all: it is an annotation on the live Secret. With the
// Secret gone the annotation is gone with it, so the cluster is an ordinary
// Sharko-managed entry that lands in the create set like any other.
//
// Classify cannot in fact hand this function ModeAdopted or ModeForeignOwned,
// because both of those need in.LiveSecretFound and this function is only ever
// asked about a connection that has no live Secret. That made the mapping
// unreachable rather than shipped — but it was a live trap: it read as the
// intended answer, a test pinned it as correct, and one edit to Classify would
// have shipped it. The pairing is gone, and
// TestSecretMissingReason_JoinIsExhaustiveBothWays holds both halves: which
// modes actually arrive here, and that the two live-Secret-only modes fall to
// the sentence below, which neither promises creation nor refuses it.
func SecretMissingReason(p Policy) string {
	switch p.Mode {
	case ModeSelfManaged:
		return LimitReasonSecretMissingSelfManaged
	case ModeInlineKubeconfig:
		return LimitReasonSecretMissingLegacyInline
	case ModeBackendStoredCredentials, ModeEKSToken:
		if p.RepairScope == RepairScopeFullConnection {
			return LimitReasonSecretMissingDurable
		}
		return LimitReasonSecretMissingBackendUnreadable
	}
	return LimitReasonSecretMissingUnknownSource
}

// Scope is how much of the connection Sharko can honestly check for a mode.
type Scope string

const (
	// ScopeFull — every field Sharko owns can be checked, credential blob
	// included.
	ScopeFull Scope = "full"

	// ScopeLimited — the labels and the plain connection facts can be
	// checked, but at least one thing Sharko owns cannot be. A cluster in
	// this scope can never be reported fully in sync, because "in sync" would
	// be a claim about something Sharko did not check.
	ScopeLimited Scope = "limited"

	// ScopeAddonLabelsOnly — Sharko is a guest on this connection. Only the
	// addon-enablement labels are Sharko's, and only they are checked.
	ScopeAddonLabelsOnly Scope = "addon_labels_only"

	// ScopeOwnershipConflict — nothing is checked, because the connection
	// belongs to another tool.
	ScopeOwnershipConflict Scope = "ownership_conflict"
)

// RepairScope is what a repair would be allowed to touch, if the user asked
// for one. Step 2 never repairs anything — this field exists so step 3 reads
// its limits from the same policy the check used, instead of re-deciding them.
type RepairScope string

const (
	// RepairScopeNone — no repair may ever be offered.
	RepairScopeNone RepairScope = "none"

	// RepairScopeAddonLabelsOnly — a repair may re-apply the addon labels Git
	// declares, and nothing else. This is what POST /clusters/{name}/resync
	// already does today.
	RepairScopeAddonLabelsOnly RepairScope = "addon_labels_only"

	// RepairScopeFullConnection — a repair may rewrite the whole connection
	// from the secrets backend plus Git.
	RepairScopeFullConnection RepairScope = "full_connection"
)

// Policy is the complete answer for one connection: which kind it is, how
// much of it Sharko may check, and what a repair would be allowed to touch.
type Policy struct {
	Mode        Mode
	Scope       Scope
	RepairScope RepairScope

	// LimitReason is a fixed, safe sentence explaining a scope narrower than
	// full, in words a person can act on. Empty for ScopeFull. It carries no
	// credential material, no path into a secrets store, and no provider
	// error text — every value it can hold is a literal in this file.
	LimitReason string
}

// FullyCheckable reports whether this mode allows a fully-in-sync answer at
// all. Anything but ScopeFull cannot, because reporting "in sync" would claim
// something Sharko did not check.
func (p Policy) FullyCheckable() bool { return p.Scope == ScopeFull }

// RepairOffered reports whether any repair may be offered for this mode.
func (p Policy) RepairOffered() bool { return p.RepairScope != RepairScopeNone }

// ClassifyInput is everything Classify is allowed to look at. Deliberately
// small and value-only: there is no field here that a credential value, a
// hash of one, or a secrets-store path could arrive through, so there is no
// code path in the classifier that could leak one.
type ClassifyInput struct {
	// CredsSource is the cluster's recorded credsSource from
	// managed-clusters.yaml. Empty means the record predates the field.
	CredsSource string

	// ConnectionManagedBy is the cluster's recorded connectionManagedBy.
	ConnectionManagedBy string

	// BackendCanProvideStoredFacts reports whether the configured secrets
	// backend can tell Sharko what a cluster's connection should look like
	// WITHOUT reading the live ArgoCD Secret.
	//
	// It is NOT "a secrets provider is configured". That was the earlier,
	// wider question and it was wrong here: an ArgoCD fallback provider is
	// configured, and reading it reads the live Secret; a backend with no
	// read-only stored-facts capability is configured, and it cannot be read
	// at all without a credential fetch that mints an EKS token. Either of
	// those given a true here produced a policy that said full scope and
	// offered a full repair for a read that was then refused — the answer
	// came back limited while the policy still said full.
	//
	// The caller must pass
	// providers.ClusterCredsRouter.CanReadStoredFactsIndependentOfArgoCDSecret,
	// which is the same code the read itself goes through, and must pass the
	// SAME value to models.ExpectedCredentialsRebuildableWithoutLiveSecret.
	// When it is false the scope stays limited, no full repair is offered, and
	// synced is never reported.
	BackendCanProvideStoredFacts bool

	// LiveSecretFound reports whether an ArgoCD cluster Secret exists for
	// this cluster right now. When false the live-only signals below are
	// meaningless and are ignored.
	LiveSecretFound bool

	// LiveManagedBy is the live Secret's app.kubernetes.io/managed-by value
	// ("" when unset). A value that is neither empty nor Sharko's own is what
	// makes this an ownership conflict.
	LiveManagedBy string

	// LiveAdopted is argosecrets.IsAdopted over the live Secret's
	// annotations — true under any of the three historical key spellings.
	LiveAdopted bool
}

// Classify returns the one policy that applies. The ORDER of the checks below
// IS the policy, most restrictive first, so a connection can never be handed
// a wider scope by a later, looser rule:
//
//  1. Another tool's ownership marker beats everything. Sharko does not
//     inspect, compare, or offer to change a connection somebody else owns.
//  2. A self-managed record and an adopted Secret are both "Sharko is a
//     guest here" — addon labels only.
//  3. A recorded source that is not on the supported list — including no
//     recorded source at all — is "Sharko does not know where the credentials
//     came from" — limited. This is checked BEFORE any source-specific rule,
//     so an unrecognised value can never fall through into a wider scope.
//  4. Inline kubeconfig is "no independent copy of the credentials" —
//     limited.
//  5. Only a backend-stored source whose backend can be read INDEPENDENTLY of
//     the live Secret, on a connection Sharko owns, reaches the checkable end
//     of the table.
//
// It never returns a zero Policy: every path assigns a mode, a scope and a
// repair scope.
func Classify(in ClassifyInput) Policy {
	// 1. Another tool owns the live connection.
	//
	// This is the same gate Manager.Ensure refuses writes on — an ownership
	// label that names anyone but Sharko. It is checked FIRST and it is not
	// weakened to make the comparison richer: an ownership conflict is the
	// answer even when the cluster's own record would otherwise allow a
	// wider scope. A person needs to be told who holds the connection, not
	// given a diff of a connection Sharko has no business converging.
	if in.LiveSecretFound && in.LiveManagedBy != "" && in.LiveManagedBy != argosecrets.ManagedByValue {
		// No LimitReason: the mode and the scope ARE the answer, and every
		// surface states the ownership boundary once from those typed facts.
		// See the note above the LimitReason constants.
		return Policy{
			Mode:        ModeForeignOwned,
			Scope:       ScopeOwnershipConflict,
			RepairScope: RepairScopeNone,
		}
	}

	// 2a. The record says a person manages this connection.
	if models.IsUserManagedConnection(in.ConnectionManagedBy) {
		return Policy{
			Mode:        ModeSelfManaged,
			Scope:       ScopeAddonLabelsOnly,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: LimitReasonSelfManaged,
		}
	}

	// 2b. The live Secret was already there and Sharko adopted it.
	if in.LiveSecretFound && in.LiveAdopted {
		return Policy{
			Mode:        ModeAdopted,
			Scope:       ScopeAddonLabelsOnly,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: LimitReasonAdopted,
		}
	}

	// 3. The recorded source is not one this version of Sharko understands.
	//
	// This gate is BEFORE every source-specific rule below, and it is the
	// reason there is no fall-through at the bottom of this function. An empty
	// value and an unrecognised value both land here — same scope, same repair
	// limit — but they are genuinely different situations for the person
	// reading the answer, so they get different sentences. Neither one is ever
	// guessed at and neither one is ever backfilled.
	if !supportedCredsSources[in.CredsSource] {
		reason := LimitReasonSourceNotUnderstood
		if in.CredsSource == "" {
			reason = LimitReasonSourceNotRecorded
		}
		return Policy{
			Mode:        ModeUnknownSource,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: reason,
		}
	}

	// 4. Pasted kubeconfig — the live Secret is the only copy.
	if in.CredsSource == models.CredsSourceInlineKubeconfig {
		return Policy{
			Mode:        ModeInlineKubeconfig,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: LimitReasonInlineKubeconfig,
		}
	}

	// 5. A backend-stored source. Checkable only when the backend can be read
	// independently of the live Secret; when it cannot, Sharko has no
	// independent copy after all — whether that is because nothing is
	// connected, because what is connected reads the live Secret itself, or
	// because it cannot be read without minting a credential.
	//
	// Only secret-kubeconfig and eks-token can reach this point: the allowlist
	// gate above already turned everything else away, so backendMode below is
	// only ever asked about one of those two.
	if !in.BackendCanProvideStoredFacts {
		return Policy{
			Mode:        backendMode(in.CredsSource),
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: LimitReasonBackendUnreadable,
		}
	}

	if in.CredsSource == models.CredsSourceEKSToken {
		// The one place scope and repair deliberately disagree.
		//
		// The configured credentials source holds EKS metadata, not a
		// credential: it stores what is needed to CREATE a short-lived STS
		// bearer token (the token creation lives in
		// internal/providers/aws_sm.go's buildFromStructured, on the WRITE
		// path). So there is no credential on the expected side at all —
		// nothing to compare the live blob against, rather than two values
		// that happen never to agree. Sharko cannot honestly compare that
		// blob, and cannot claim this connection is fully in sync.
		//
		// The check itself creates nothing. It reads through
		// providers.StoredConnectionFacts, which stops at the stored payload.
		//
		// A repair is still worth offering, and is still full: rewriting the
		// connection from the backend produces a correct connection whatever
		// state it was in. "I cannot prove this is right, but I can make it
		// right" is a useful thing to be able to say.
		return Policy{
			Mode:        ModeEKSToken,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeFullConnection,
			LimitReason: LimitReasonEKSNoStoredCredential,
		}
	}

	if in.CredsSource == models.CredsSourceSecretKubeconfig {
		return Policy{
			Mode:        ModeBackendStoredCredentials,
			Scope:       ScopeFull,
			RepairScope: RepairScopeFullConnection,
		}
	}

	// Nothing reaches here today: the allowlist gate above only lets three
	// values through, and all three are handled. It exists so that adding a
	// fourth value to the allowlist and forgetting to handle it here lands on
	// the narrow answer instead of the trusting one. The widest scope is never
	// something a value gets by falling off the end of a function.
	return Policy{
		Mode:        ModeUnknownSource,
		Scope:       ScopeLimited,
		RepairScope: RepairScopeAddonLabelsOnly,
		LimitReason: LimitReasonSourceNotUnderstood,
	}
}

// backendMode maps a backend-stored creds source to its mode.
//
// It is only ever called from the "no backend is connected" branch above, which
// sits AFTER the supportedCredsSources gate — so credsSource here really is one
// of secret-kubeconfig or eks-token, and this claim is enforced by the gate
// rather than merely hoped for.
func backendMode(credsSource string) Mode {
	if credsSource == models.CredsSourceEKSToken {
		return ModeEKSToken
	}
	return ModeBackendStoredCredentials
}
