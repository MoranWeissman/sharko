package config

import (
	"context"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/models"
)

// DefaultManagedClustersPath is the conventional repo path of the
// managed-clusters document, used when a caller passes an empty path.
const DefaultManagedClustersPath = "configuration/managed-clusters.yaml"

// V4ManagedClustersPath is where the list of clusters Sharko manages lives
// in a v4 repo: a root file called "managed-clusters.yaml". Same
// ManagedClusters kind and shape this file already parses; only the
// location changed.
//
// Declared here rather than imported from internal/orchestrator because
// orchestrator imports config, not the other way round — the same lockstep
// duplication internal/clusterreconciler already carries for the same
// reason. Keep the literal identical to
// orchestrator.V4ManagedClustersPath.
const V4ManagedClustersPath = "managed-clusters.yaml"

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
	key, _, _ := ResolveCredentialRouting(ctx, git, managedClustersPath, branch, name)
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
//
// roleARN (V2-cleanup-62.2) is the cluster's stored per-cluster IAM role
// for EKS token minting; "" on every failure path and for records that
// predate the field — the mint then falls back to the SM-secret roleArn /
// connection-level default exactly as before.
func ResolveCredentialRouting(ctx context.Context, git ManagedClustersReader, managedClustersPath, branch, name string) (lookupKey, credsSource, roleARN string) {
	if git == nil || name == "" {
		return name, "", ""
	}
	if managedClustersPath == "" {
		managedClustersPath = DefaultManagedClustersPath
	}
	if branch == "" {
		branch = "main"
	}
	data, err := git.GetFileContent(ctx, managedClustersPath, branch)
	if err != nil || data == nil {
		// The configured (v3) path did not resolve. Before giving up and
		// falling back to the plain cluster name, try the v4 location —
		// managed-clusters.yaml holds the same ManagedClusters document on
		// a v4 repo. Without this, a v4-registered cluster with a custom
		// secretPath, credsSource, or roleARN silently loses all three:
		// Sharko would fetch credentials by the raw cluster name against
		// the wrong backend, which is precisely the V2-cleanup-55.1 bug
		// class this resolver exists to prevent — reintroduced by the
		// change of file location.
		//
		// Same v3-then-v4 shape ClusterService.readManagedClustersData and
		// clusterreconciler.pollOnce already use. A repo is one format or
		// the other, so this costs one extra read only on the v3-absent
		// path.
		v4Data, v4Err := git.GetFileContent(ctx, V4ManagedClustersPath, branch)
		if v4Err != nil || v4Data == nil {
			return name, "", ""
		}
		data = v4Data
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
	key, _, _ := ResolveCredentialRoutingFromData(data, name)
	return key
}

// ResolveCredentialRoutingFromData resolves (lookupKey, credsSource,
// roleARN) for name from already-fetched managed-clusters.yaml bytes — the
// routing twin of ResolveCredentialLookupKeyFromData (V2-cleanup-60.4;
// roleARN added by V2-cleanup-62.2). Parse failures and unknown clusters
// fall back to (name, "", "").
func ResolveCredentialRoutingFromData(data []byte, name string) (lookupKey, credsSource, roleARN string) {
	if len(data) == 0 || name == "" {
		return name, "", ""
	}
	clusters, err := NewParser().ParseClusterAddons(data)
	if err != nil {
		return name, "", ""
	}
	key, source, role := models.CredentialRoutingFor(clusters, name)
	if key != name {
		slog.Info("[credlookup] using stored secretPath override for credential fetch",
			"cluster", name, "lookupKey", key)
	}
	return key, source, role
}
