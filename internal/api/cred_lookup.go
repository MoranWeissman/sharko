package api

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// credentialLookupKey resolves the key to pass to
// s.credProvider().GetCredentials for the named cluster: the secretPath
// stored on the cluster's managed-clusters.yaml record when set, else the
// plain cluster name (V2-cleanup-55.1 — the raw-name credential-fetch bug
// class; the live repro was Diagnose fetching AWS SM secret "moran" while
// the stored record said secret_path=sharko-smoke-target-1-kubeconfig).
//
// Reads managed-clusters.yaml via the active Git connection; every failure
// path (no active connection, file missing, parse error, cluster unknown)
// falls back to the plain name, byte-identical to the old behavior.
func (s *Server) credentialLookupKey(ctx context.Context, name string) string {
	key, _, _ := s.credentialRouting(ctx, name)
	return key
}

// credentialRouting resolves (lookupKey, credsSource, roleARN) for the named
// cluster from its managed-clusters.yaml record (V2-cleanup-60.4; roleARN
// added by V2-cleanup-62.2). credsSource and roleARN are "" on every failure
// path and for records that predate the fields.
func (s *Server) credentialRouting(ctx context.Context, name string) (lookupKey, credsSource, roleARN string) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil || gp == nil {
		return name, "", ""
	}
	return config.ResolveCredentialRouting(ctx, gp, s.repoPaths.ManagedClusters, s.gitopsConfig().BaseBranch, name)
}

// fetchClusterCredentials fetches the named cluster's credentials routed by
// the cluster's own stored creds source (V2-cleanup-60.4 / review H4):
// inline-kubeconfig-registered clusters are read from the ArgoCD cluster
// Secret REGARDLESS of the configured backend type (they have no backend
// secret — the pre-60.4 behavior answered "secret not found" for them
// whenever an aws-sm / k8s-secrets connection was configured); backend
// sources keep the exact pre-existing backend route; unknown/legacy records
// try the backend first and heal via the ArgoCD reader.
//
// Callers must keep their existing `s.credProvider() == nil` early-return:
// this helper preserves the "test feature unavailable" surface when no
// provider is published at all.
func (s *Server) fetchClusterCredentials(ctx context.Context, name string) (*providers.Kubeconfig, error) {
	lookupKey, credsSource, roleARN := s.credentialRouting(ctx, name)
	if router := s.credsRouter(); router != nil {
		return router.Fetch(name, lookupKey, credsSource, roleARN)
	}
	// No provider set was ever published (nil-provider installs keep their
	// handler-level early-returns; this is a defensive fallback only).
	if cp := s.credProvider(); cp != nil {
		return providers.GetCredentialsWithOptionalRole(cp, lookupKey, roleARN)
	}
	return nil, fmt.Errorf("no credentials provider configured")
}
