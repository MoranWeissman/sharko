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
// credentials). Registration succeeds with zero credentials (see
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
