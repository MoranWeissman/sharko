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
	for _, c := range clusters {
		if c.Name == name {
			return c.CredentialLookupKey()
		}
	}
	return name
}
