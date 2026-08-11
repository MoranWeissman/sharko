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
	// secrets backend (credsSource secret-kubeconfig), a backend is wired up,
	// the connection is Sharko's to manage, and Sharko owns the live Secret.
	// Sharko can re-fetch the stored kubeconfig and rebuild the whole expected
	// Secret without reading the live one, so everything is checkable.
	ModeBackendStoredCredentials Mode = "backend_stored_credentials"

	// ModeEKSExec — structured EKS metadata in the secrets backend
	// (credsSource eks-token), same conditions. The metadata is re-readable,
	// but the bearer token is minted fresh on every fetch, so the credential
	// blob itself can never be compared. Scope is limited for that one
	// reason; a repair is still offered, because rewriting the connection from
	// the backend is exactly the fix.
	ModeEKSExec Mode = "eks_exec"

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

	// ModeUnknownSource — credsSource is empty. The record predates the
	// field, which every install upgraded from an older Sharko has. Sharko
	// does not know where these credentials came from, and it never guesses,
	// never backfills the field, and never reads the live Secret to find out.
	ModeUnknownSource Mode = "unknown_source"
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

	// BackendConfigured reports whether a secrets-provider backend is wired
	// up at the connection level. A backend-stored source can only be
	// re-read when there is a backend to ask.
	BackendConfigured bool

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
//  3. Inline kubeconfig and an unknown source are both "no independent copy
//     of the credentials" — limited.
//  4. Only a backend-stored source with a backend actually wired up, on a
//     connection Sharko owns, reaches the checkable end of the table.
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

	// 3a. Pasted kubeconfig — the live Secret is the only copy.
	if in.CredsSource == models.CredsSourceInlineKubeconfig {
		return Policy{
			Mode:        ModeInlineKubeconfig,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: "This cluster was registered with a pasted kubeconfig, and those credentials are only stored in the connection itself. Sharko has no second copy to check the connection details against, so it checks the labels and the plain connection facts only.",
		}
	}

	// 3b. The record predates the credsSource field. Never guessed, never
	// backfilled, never resolved by reading the live Secret.
	if in.CredsSource == "" {
		return Policy{
			Mode:        ModeUnknownSource,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: "This cluster's record does not say where its credentials are kept — it was registered by an older version of Sharko. Sharko will not guess, so it checks the labels and the plain connection facts only.",
		}
	}

	// 4. A backend-stored source. Checkable only when there is a backend to
	// re-read from; without one Sharko has no independent copy after all.
	if !in.BackendConfigured {
		return Policy{
			Mode:        backendMode(in.CredsSource),
			Scope:       ScopeLimited,
			RepairScope: RepairScopeAddonLabelsOnly,
			LimitReason: "This cluster's credentials are kept in a secrets backend, but no secrets backend is connected right now, so Sharko cannot check the connection details against it.",
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
			Mode:        ModeEKSExec,
			Scope:       ScopeLimited,
			RepairScope: RepairScopeFullConnection,
			LimitReason: "This cluster signs in with a short-lived token that is issued fresh every time, so the stored sign-in details are never the same twice and Sharko cannot compare them. Everything else about the connection is checked.",
		}
	}

	return Policy{
		Mode:        ModeBackendStoredCredentials,
		Scope:       ScopeFull,
		RepairScope: RepairScopeFullConnection,
	}
}

// backendMode maps a backend-stored creds source to its mode. Only reached
// with one of the two backend sources; anything else has already been
// classified above.
func backendMode(credsSource string) Mode {
	if credsSource == models.CredsSourceEKSToken {
		return ModeEKSExec
	}
	return ModeBackendStoredCredentials
}
