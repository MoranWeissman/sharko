package orchestrator

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/config"
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
