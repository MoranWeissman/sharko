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
	// but the sign-in token is minted fresh on every fetch, so the credential
	// blob itself can never be compared. Scope is limited for that one
	// reason; a repair is still offered, because rewriting the connection from
	// the backend is exactly the fix.
	//
	// The name says token and not exec on purpose. The writer for this source
	// supplies a minted bearer token, so BuildClusterSecret emits the
	// bearerToken shape — not an execProviderConfig. Calling this mode "exec"
	// would describe a shape Sharko does not write here.
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
	LimitReasonSourceNotUnderstood = "Sharko does not recognise what this cluster's record says about where its credentials are kept, so it will not assume anything. It checks the labels and the plain connection facts only. Check the cluster's entry in git, or update Sharko if the entry was written by a newer version."

	// LimitReasonEKSTokenChangesEveryTime is the EKS answer, and it is exact
	// wording the product owner signed off character for character. It names
	// what WAS checked instead of claiming "everything else" — the old sentence
	// said everything else was checked, which was not true, because a
	// deliberately empty annotation set and the guest-scope rules mean "the
	// rest" is a specific list and not all of it. It is pinned by
	// TestClassify_EKSTokenLimitReasonIsExact so nobody paraphrases it later.
	LimitReasonEKSTokenChangesEveryTime = "Sharko checked the Secret identity, type, server, and owned labels. It did not compare `data.config`, because the EKS sign-in token changes every time it is created."
)

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
		return Policy{
			Mode:        ModeForeignOwned,
			Scope:       ScopeOwnershipConflict,
			RepairScope: RepairScopeNone,
			LimitReason: "This cluster's ArgoCD connection is marked as owned by another tool, so Sharko does not compare it or change it. Take the connection over in Sharko first if you want Sharko to manage it.",
		}
	}

	// 2a. The record says a person manages this connection.
	if models.IsUserManagedConnection(in.ConnectionManagedBy) {
		return Policy{
			Mode:        ModeSelfManaged,
			Scope:       ScopeAddonLabelsOnly,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: "You maintain this cluster's ArgoCD connection yourself, so Sharko only checks the addon labels on it and never its connection details.",
		}
	}

	// 2b. The live Secret was already there and Sharko adopted it.
	if in.LiveSecretFound && in.LiveAdopted {
		return Policy{
			Mode:        ModeAdopted,
			Scope:       ScopeAddonLabelsOnly,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: "Sharko adopted this cluster's existing ArgoCD connection rather than creating it, so Sharko only checks the addon labels on it and never its connection details.",
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
			LimitReason: "This cluster was registered with a pasted kubeconfig, and those credentials are only stored in the connection itself. Sharko has no second copy to check the connection details against, so it checks the labels and the plain connection facts only.",
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
			LimitReason: "This cluster's credentials are kept in a secrets backend, but Sharko cannot read them from there right now, so it cannot check the connection details against them.",
		}
	}

	if in.CredsSource == models.CredsSourceEKSToken {
		// The one place scope and repair deliberately disagree.
		//
		// The backend holds EKS metadata, not a fixed credential: every fetch
		// mints a brand-new short-lived STS bearer token (see the token mint
		// in internal/providers/aws_sm.go's buildFromStructured). So a rebuilt
		// credential blob differs from the live one on every single check,
		// with nothing having drifted — which means Sharko cannot honestly
		// compare that blob, and cannot claim this connection is fully in
		// sync.
		//
		// A repair is still worth offering, and is still full: rewriting the
		// connection from the backend produces a correct connection whatever
		// state it was in. "I cannot prove this is right, but I can make it
		// right" is a useful thing to be able to say.
		return Policy{
			Mode:        ModeEKSToken,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeFullConnection,
			LimitReason: LimitReasonEKSTokenChangesEveryTime,
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
