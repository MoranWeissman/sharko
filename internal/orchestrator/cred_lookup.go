package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// credentialLookupKey resolves the key to pass to
// credProvider.GetCredentials for the named cluster: the secretPath stored
// on the cluster's managed-clusters.yaml record when set, else the plain
// cluster name (V2-cleanup-55.1 — the raw-name credential-fetch bug class).
//
// Reads managed-clusters.yaml from the base branch via o.git; every failure
// path falls back to the plain name, byte-identical to the old behavior.
func (o *Orchestrator) credentialLookupKey(ctx context.Context, name string) string {
	if o.git == nil {
		return name
	}
	return config.ResolveCredentialLookupKey(ctx, o.git, o.paths.ManagedClusters, o.gitops.BaseBranch, name)
}

// fetchClusterCredentials fetches the named cluster's credentials routed by
// the cluster's own stored creds source (V2-cleanup-60.4 / review H4):
//
//   - credsSource inline-kubeconfig → the ArgoCD cluster-Secret reader,
//     REGARDLESS of the configured backend type (an inline-registered
//     cluster has no backend secret — its credentials live only in the
//     ArgoCD Secret written at registration).
//   - backend sources (secret-kubeconfig / eks-token) → o.credProvider with
//     the resolved lookup key, exactly as before.
//   - unknown (record predates the credsSource field) → backend first, then
//     ArgoCD-read fallback; both failing returns the original backend error.
//
// Callers that previously wrote
// o.credProvider.GetCredentials(o.credentialLookupKey(ctx, name)) go
// through this instead. remove.go intentionally does NOT (it resolves from
// pre-deletion bytes and is owned by the removal-safety story) — it can
// adopt the exported config.ResolveCredentialRoutingFromData +
// providers.ClusterCredsRouter pair as a follow-up.
func (o *Orchestrator) fetchClusterCredentials(ctx context.Context, name string) (*providers.Kubeconfig, error) {
	lookupKey := name
	credsSource := ""
	roleARN := ""
	if o.git != nil {
		lookupKey, credsSource, roleARN = config.ResolveCredentialRouting(ctx, o.git, o.paths.ManagedClusters, o.gitops.BaseBranch, name)
	}
	if o.credsRouter == nil {
		// Legacy-constructed orchestrator (tests building the struct
		// directly): behave exactly as before the router existed.
		if o.credProvider == nil {
			return nil, fmt.Errorf("no credentials provider configured")
		}
		return providers.GetCredentialsWithOptionalRole(o.credProvider, lookupKey, roleARN)
	}
	return o.credsRouter.Fetch(name, lookupKey, credsSource, roleARN)
}

// clusterHasResolvableCredentials reports whether Sharko can currently
// fetch spoke-cluster credentials for the named cluster (V2-cleanup-88.3 —
// lazy credentials). This is a REAL fetch attempt via fetchClusterCredentials
// — the enforcement-moment counterpart of the cheap, config-presence-only
// models.Cluster.CredentialsResolvable used by the read-only
// addon_secrets_ready API field. A "not resolvable" from the cheap check
// always means this fails too; this one can occasionally say "no" when the
// cheap check said "yes" (e.g. a stored secret was deleted out-of-band
// after registration) — that asymmetry is intentional, this is the strict
// gate and the API field is only a fast hint.
func (o *Orchestrator) clusterHasResolvableCredentials(ctx context.Context, name string) bool {
	creds, err := o.fetchClusterCredentials(ctx, name)
	return err == nil && creds != nil
}
