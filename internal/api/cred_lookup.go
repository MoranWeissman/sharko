package api

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/config"
)

// credentialLookupKey resolves the key to pass to
// s.credProvider.GetCredentials for the named cluster: the secretPath
// stored on the cluster's managed-clusters.yaml record when set, else the
// plain cluster name (V2-cleanup-55.1 — the raw-name credential-fetch bug
// class; the live repro was Diagnose fetching AWS SM secret "moran" while
// the stored record said secret_path=sharko-smoke-target-1-kubeconfig).
//
// Reads managed-clusters.yaml via the active Git connection; every failure
// path (no active connection, file missing, parse error, cluster unknown)
// falls back to the plain name, byte-identical to the old behavior.
func (s *Server) credentialLookupKey(ctx context.Context, name string) string {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil || gp == nil {
		return name
	}
	return config.ResolveCredentialLookupKey(ctx, gp, s.repoPaths.ManagedClusters, s.gitopsCfg.BaseBranch, name)
}
