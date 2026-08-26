package models

// Credential lookup-key resolution (V2-cleanup-55.1).
//
// A cluster's credentials live in the secrets backend under EITHER the
// cluster name (the default) OR an explicit secretPath stored on the
// cluster's managed-clusters.yaml record. Every call to
// ClusterCredentialsProvider.GetCredentials MUST pass the resolved key —
// passing the raw cluster name when a secretPath override is stored makes
// the provider look up a secret that does not exist (the live bug: cluster
// "moran" stored secret_path=sharko-smoke-target-1-kubeconfig, and the
// Diagnose endpoint tried to fetch AWS SM secret "moran").
//
// These helpers are the single source of truth for that resolution. Callers
// that already hold a parsed cluster record use the CredentialLookupKey
// methods; callers that only hold a cluster name resolve through
// CredentialLookupKeyFor (or the git-reading wrapper in internal/config).

// Canonical creds-source labels (V2-cleanup-60.4). These mirror the
// orchestrator's CredsSource constants (internal/orchestrator/types.go) but
// live here so the lower layers (config, providers) can route on them
// without importing the orchestrator. The registration writer stamps the
// effective source onto the cluster's managed-clusters.yaml record as
// credsSource; records written before the field existed carry "" (unknown).
const (
	// CredsSourceInlineKubeconfig — the cluster was registered with a pasted
	// kubeconfig. Its credentials live ONLY in the ArgoCD cluster Secret;
	// no secrets backend holds anything for it. Credential fetches MUST go
	// through the ArgoCD reader regardless of the configured backend type.
	CredsSourceInlineKubeconfig = "inline-kubeconfig"
	// CredsSourceSecretKubeconfig — a kubeconfig stored in the secrets
	// backend. Fetches go through the configured backend provider.
	CredsSourceSecretKubeconfig = "secret-kubeconfig"
	// CredsSourceEKSToken — structured EKS JSON in the secrets backend that
	// mints a short-lived STS token. Same backend route as secret-kubeconfig.
	CredsSourceEKSToken = "eks-token"
)

// CredentialLookupKey returns the key to pass to
// ClusterCredentialsProvider.GetCredentials for this cluster: the stored
// SecretPath override when set, else the cluster name.
func (c Cluster) CredentialLookupKey() string {
	if c.SecretPath != "" {
		return c.SecretPath
	}
	return c.Name
}

// CredentialLookupKey is the ManagedClusterEntry (enveloped
// managed-clusters.yaml record) twin of Cluster.CredentialLookupKey.
func (e ManagedClusterEntry) CredentialLookupKey() string {
	if e.SecretPath != "" {
		return e.SecretPath
	}
	return e.Name
}

// CredentialLookupKeyFor returns the credential lookup key for the named
// cluster given the parsed managed-clusters records. When the cluster has a
// stored SecretPath it wins; when the cluster is found without one — or is
// not found at all — the plain name is returned, which is byte-identical to
// the pre-resolver behavior.
func CredentialLookupKeyFor(clusters []Cluster, name string) string {
	key, _, _ := CredentialRoutingFor(clusters, name)
	return key
}

// CredentialRoutingFor is the V2-cleanup-60.4 extension of
// CredentialLookupKeyFor: alongside the lookup key it returns the cluster's
// stored credsSource so credential-fetch sites can route per cluster —
// inline-kubeconfig-registered clusters read via the ArgoCD provider
// regardless of the configured backend, backend-registered clusters keep
// their backend route. credsSource is "" when the cluster is not found or
// its record predates the field (unknown — callers fall back to the
// backend-first-then-ArgoCD-read heuristic).
//
// roleARN (V2-cleanup-62.2) is the cluster's stored per-cluster IAM role
// for EKS token minting; "" when the cluster is not found, the record
// predates the field, or the cluster uses the connection-level default.
func CredentialRoutingFor(clusters []Cluster, name string) (lookupKey, credsSource, roleARN string) {
	for _, c := range clusters {
		if c.Name == name {
			return c.CredentialLookupKey(), c.CredsSource, c.RoleARN
		}
	}
	return name, "", ""
}

// CredentialsResolvable reports whether Sharko has a plausible, resolvable
// path to this cluster's own credentials — a CHEAP presence-of-config check
// over the stored record, NOT a live probe (V2-cleanup-88.3 — lazy
// credentials).
//
// # Not the same question as ExpectedCredentialsRebuildableWithoutLiveSecret
//
// These two predicates look interchangeable and are NOT. Read both before
// touching either, and do not "simplify" them into one function —
// TestCredentialPredicates_DisagreeOnInlineSharkoManaged in
// credlookup_test.go fails on purpose if you do.
//
//   - This one asks: can Sharko GET credentials for this cluster at all, by
//     any route, including reading them back out of the ArgoCD cluster
//     Secret it wrote at registration? An inline-kubeconfig cluster answers
//     TRUE here.
//   - The other one asks: can Sharko rebuild what the ArgoCD cluster Secret
//     SHOULD contain without reading that Secret? An inline-kubeconfig
//     cluster answers FALSE there, because the pasted credentials were never
//     stored anywhere else — the live Secret is their only home.
//
// The connection-Secret comparison must use the other one. Using this one
// would have it rebuild the expected Secret out of the live Secret, compare
// the Secret with itself, always match, and report a cluster as in sync no
// matter how badly its connection had drifted.
//
// Registration succeeds with zero credentials (see
// RegisterCluster); this predicate is what the read-only
// Cluster.DerivedHealthStatus-style `addon_secrets_ready` API field keys
// off, so the UI can show "this cluster needs credentials before you can
// enable a secret-bearing addon" without an extra round trip. The
// orchestrator's EnableAddon pre-flight gate performs the real, strict
// version of this check (an actual credential fetch attempt) — a "false"
// here always predicts a gate rejection; a "true" here is a hint, not a
// guarantee (e.g. a stored secret deleted out-of-band after registration
// would still read "true" here but fail the real gate).
//
// backendConfigured reports whether a secrets-provider backend is wired up
// at the connection level (orchestrator's o.credProvider != nil / the API's
// s.credProvider() != nil) — a backend creds source can only ever resolve
// when a backend actually exists to ask.
//
//   - inline-kubeconfig + Sharko-managed connection → true: Sharko wrote
//     the ArgoCD cluster Secret from the pasted credentials at registration
//     and can read it back.
//   - inline-kubeconfig + self-managed ("user") connection → false: Sharko
//     NEVER writes the ArgoCD cluster Secret for a self-managed connection
//     (V2-cleanup-57.2), so there is nothing to read back even though the
//     stored source is "inline".
//   - secret-kubeconfig / eks-token → true only when a backend is
//     configured.
//   - "" (record predates the credsSource field, or credentials were never
//     supplied at a lazy-credentials registration) → true only when a
//     backend is configured — the same backend-first fallback every other
//     "unknown source" reader in this package uses.
func (c Cluster) CredentialsResolvable(backendConfigured bool) bool {
	return credentialsResolvable(c.CredsSource, c.ConnectionManagedBy, backendConfigured)
}

// CredentialsResolvable is the ManagedClusterEntry twin of
// Cluster.CredentialsResolvable.
func (e ManagedClusterEntry) CredentialsResolvable(backendConfigured bool) bool {
	return credentialsResolvable(e.CredsSource, e.ConnectionManagedBy, backendConfigured)
}

func credentialsResolvable(credsSource, connectionManagedBy string, backendConfigured bool) bool {
	if credsSource == CredsSourceInlineKubeconfig {
		return !IsUserManagedConnection(connectionManagedBy)
	}
	// secret-kubeconfig / eks-token / "" (unknown, pre-field record) all
	// route through the backend provider — resolvable only when one exists.
	return backendConfigured
}

// ExpectedCredentialsRebuildableWithoutLiveSecret reports whether Sharko can
// work out what this cluster's ArgoCD connection Secret SHOULD contain
// WITHOUT reading that Secret.
//
// # Why this is a separate predicate from CredentialsResolvable
//
// CredentialsResolvable (above) answers "can Sharko get credentials for this
// cluster at all". One of the routes it counts is reading them back out of
// the live ArgoCD cluster Secret — which is fine for its callers (an
// enable-addon pre-flight only needs credentials that work, it does not care
// where they came from) and is exactly wrong for a comparison.
//
// A comparison that built its expected Secret from the live Secret would be
// comparing the Secret with itself. It would match every time, on every
// cluster, including one whose connection had been hand-edited or clobbered
// by another tool — and it would tell the user that cluster is in sync. So
// this predicate exists, narrower on purpose, and the comparison uses it.
//
// TRUE only for the two sources whose credential material lives somewhere
// Sharko can read independently — the secrets backend — AND only when that
// backend can actually be read independently (see backendCanProvideStoredFacts
// below):
//
//   - secret-kubeconfig: a kubeconfig stored in the backend. Fetch it again,
//     rebuild, compare. The live Secret contributes nothing to the expected
//     side.
//   - eks-token: structured EKS metadata stored in the backend. Same, with
//     one honest limit — see the note on the token below.
//
// FALSE for everything else, and each "no" is a different "no":
//
//   - inline-kubeconfig: the pasted kubeconfig was written into the ArgoCD
//     Secret at registration and kept nowhere else. There is no second copy
//     to rebuild from. (True regardless of who manages the connection —
//     Sharko-managed inline is the case CredentialsResolvable says TRUE for
//     and this says FALSE for, which is the whole reason both exist.)
//   - "" (the record predates the credsSource field — every install upgraded
//     from an older Sharko has these): Sharko does not know where the
//     credentials came from. Guessing "probably the backend" would produce
//     an expected Secret built on an assumption and report drift, or worse
//     agreement, that is not real. Never guess, never backfill the field
//     automatically, and never fall back to reading the live Secret.
//   - a self-managed connection (connectionManagedBy = user): Sharko never
//     writes this Secret's credential material at all, so it has no expected
//     value to compare against — the Git-defined connection says nothing
//     about what it should be.
//
// backendCanProvideStoredFacts is NARROWER than CredentialsResolvable's
// backendConfigured, and the difference is the whole point of the parameter's
// name.
//
// "A backend is configured" is not enough here. A configured backend that is
// itself the ArgoCD cluster-Secret reader reads the live Secret — the very thing
// this predicate exists to stay away from. A configured backend with no
// read-only stored-facts capability cannot be read at all without falling back
// to a credential fetch, which for an EKS cluster mints a real sign-in token. In
// both of those cases there is no independent copy after all, so this parameter
// must be FALSE for them even though a provider is wired up.
//
// The answer is not computed here and must not be. It comes from the one place
// that knows what the backend actually is:
// providers.ClusterCredsRouter.CanReadStoredFactsIndependentOfArgoCDSecret,
// which is the same code the read itself goes through. Passing anything else —
// in particular a bare "is a provider configured" boolean — makes this predicate
// say yes to a read that will then be refused, and the caller ends up claiming
// it checked the credential half of a connection it never read.
//
// # The honest limit on eks-token
//
// TRUE here means "the non-credential parts of the expected Secret can be
// rebuilt independently" — the server address, the CA bundle, the labels, the
// annotations Sharko owns. It does NOT mean every byte of data.config can be
// compared. For an eks-token cluster the write path calls GetCredentials, which
// mints a fresh short-lived STS bearer token (see internal/providers/aws_sm.go's
// buildFromStructured). The read-only comparison check never calls GetCredentials
// and creates no tokens at all. A check reports the token as a field it did not
// compare rather than as drift — see the connection-mode policy in
// internal/connectioncompare.
func (c Cluster) ExpectedCredentialsRebuildableWithoutLiveSecret(backendCanProvideStoredFacts bool) bool {
	return expectedCredentialsRebuildableWithoutLiveSecret(c.CredsSource, c.ConnectionManagedBy, backendCanProvideStoredFacts)
}

// ExpectedCredentialsRebuildableWithoutLiveSecret is the ManagedClusterEntry
// twin of Cluster.ExpectedCredentialsRebuildableWithoutLiveSecret.
func (e ManagedClusterEntry) ExpectedCredentialsRebuildableWithoutLiveSecret(backendCanProvideStoredFacts bool) bool {
	return expectedCredentialsRebuildableWithoutLiveSecret(e.CredsSource, e.ConnectionManagedBy, backendCanProvideStoredFacts)
}

func expectedCredentialsRebuildableWithoutLiveSecret(credsSource, connectionManagedBy string, backendCanProvideStoredFacts bool) bool {
	// Sharko never writes a self-managed connection's credential material,
	// so it holds no expectation about it, whatever the recorded source says.
	if IsUserManagedConnection(connectionManagedBy) {
		return false
	}
	switch credsSource {
	case CredsSourceSecretKubeconfig, CredsSourceEKSToken:
		return backendCanProvideStoredFacts
	default:
		// inline-kubeconfig (only copy is the live Secret) and "" (source
		// unknown — never guessed) both land here.
		return false
	}
}
