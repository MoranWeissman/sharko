package config

import (
	"context"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/models"
)

// DefaultManagedClustersPath is the conventional repo path of the
// managed-clusters document, used when a caller passes an empty path.
const DefaultManagedClustersPath = "configuration/managed-clusters.yaml"

// ManagedClustersReader is the minimal read-only Git surface the credential
// lookup-key resolver needs. Both gitprovider.GitProvider and the various
// GitReader test fakes satisfy it.
type ManagedClustersReader interface {
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
}

// ResolveCredentialLookupKey is THE shared resolver for the
// raw-cluster-name credential-fetch bug class (V2-cleanup-55.1): given a
// cluster name, it returns the key that must be passed to
// ClusterCredentialsProvider.GetCredentials — the secretPath stored on the
// cluster's managed-clusters.yaml record when set, else the plain name.
//
// It reads managedClustersPath at branch via git and delegates to
// ResolveCredentialLookupKeyFromData. Every failure path (nil reader, file
// missing/unreadable, parse error, cluster not in the file, no secretPath
// stored) falls back to the plain name, which is byte-identical to the
// pre-resolver behavior.
//
// Empty managedClustersPath defaults to DefaultManagedClustersPath; empty
// branch defaults to "main" (the same default the service layer uses).
func ResolveCredentialLookupKey(ctx context.Context, git ManagedClustersReader, managedClustersPath, branch, name string) string {
	key, _ := ResolveCredentialRouting(ctx, git, managedClustersPath, branch, name)
	return key
}

// ResolveCredentialRouting is the V2-cleanup-60.4 extension of
// ResolveCredentialLookupKey: alongside the lookup key it returns the
// cluster's stored credsSource so fetch sites can route per cluster (an
// inline-kubeconfig-registered cluster has NO backend secret — its
// credentials live only in the ArgoCD cluster Secret — so it must be read
// via the ArgoCD provider regardless of the configured backend type).
//
// credsSource is "" (unknown) on every failure path AND for records written
// before the field existed; callers treat unknown as "backend first, then
// ArgoCD-read fallback" (see providers.ClusterCredsRouter).
func ResolveCredentialRouting(ctx context.Context, git ManagedClustersReader, managedClustersPath, branch, name string) (lookupKey, credsSource string) {
	if git == nil || name == "" {
		return name, ""
	}
	if managedClustersPath == "" {
		managedClustersPath = DefaultManagedClustersPath
	}
	if branch == "" {
		branch = "main"
	}
	data, err := git.GetFileContent(ctx, managedClustersPath, branch)
	if err != nil || data == nil {
		// No readable record — fall back to the plain name.
		return name, ""
	}
	return ResolveCredentialRoutingFromData(data, name)
}

// ResolveCredentialLookupKeyFromData resolves the credential lookup key for
// name from already-fetched managed-clusters.yaml bytes. Callers that have
// the document in hand (e.g. RemoveCluster, which must resolve BEFORE it
// deletes the cluster's entry) use this variant so the resolution cannot
// race the removal. Parse failures and unknown clusters fall back to the
// plain name.
func ResolveCredentialLookupKeyFromData(data []byte, name string) string {
	key, _ := ResolveCredentialRoutingFromData(data, name)
	return key
}

// ResolveCredentialRoutingFromData resolves (lookupKey, credsSource) for
// name from already-fetched managed-clusters.yaml bytes — the routing twin
// of ResolveCredentialLookupKeyFromData (V2-cleanup-60.4). Parse failures
// and unknown clusters fall back to (name, "").
func ResolveCredentialRoutingFromData(data []byte, name string) (lookupKey, credsSource string) {
	if len(data) == 0 || name == "" {
		return name, ""
	}
	clusters, err := NewParser().ParseClusterAddons(data)
	if err != nil {
		return name, ""
	}
	key, source := models.CredentialRoutingFor(clusters, name)
	if key != name {
		slog.Info("[credlookup] using stored secretPath override for credential fetch",
			"cluster", name, "lookupKey", key)
	}
	return key, source
}
