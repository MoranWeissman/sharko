package api

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
)

// argocdClusterLister is the narrow read-only slice of *argocd.Client that
// the orphan resolver needs. Defining the interface here lets the resolver
// be tested with a tiny fake without spinning up an httptest server (mirrors
// the V124-22 / V125-1.5 dignified-degrade testability pattern).
type argocdClusterLister interface {
	ListClusters(ctx context.Context) ([]models.ArgocdCluster, error)
}

// resolveOrphanRegistrations computes the set of ArgoCD cluster Secrets that
// are orphaned — i.e. they exist in ArgoCD but have NO corresponding entry
// in managed-clusters.yaml AND no open registration PR matching the cluster
// name.
//
// V125-1-7 / BUG-058 — when a manual-mode registration PR is closed without
// merging, the ArgoCD cluster Secret created pre-merge by the
// internal/orchestrator/cluster.go:408 fall-through (nil argoSecretManager
// + manual mode → direct ArgoCD API RegisterCluster call) is left behind in
// the live argocd ns. V125-1-5's pending-PR filter masked this while the PR
// was open; once closed, the Secret reappeared as a `not_in_git` cluster
// with no UI recovery path. This resolver surfaces those orphans into a
// dedicated lifecycle state so the new DELETE
// /api/v1/clusters/{name}/orphan endpoint can clean them up.
//
// Algorithm:
//
//  1. List ArgoCD clusters (caller-provided lister).
//  2. Build sets of git-managed cluster names + pending-registration names.
//  3. For each ArgoCD cluster: skip the in-cluster entry (`name == "in-cluster"`
//     or server starts with `https://kubernetes.default`); skip names that
//     are in the git-managed set OR in the pending set; everything else is
//     an orphan.
//  4. Return the slice with LastSeenAt = the call-time timestamp (the ArgoCD
//     cluster Secret API exposes no stable creation timestamp, so this is a
//     degraded approximation — see OrphanRegistration doc on
//     internal/models/cluster.go).
//
// Defensive degrade (V124-22 / V125-1.5 pattern): a provider error or nil
// lister returns an EMPTY slice + a log warning rather than failing the
// entire /clusters endpoint. A missing Orphan Registrations section on the
// next refresh is acceptable; a 500 that takes down the whole clusters page
// is not.
//
// The return type is always a non-nil slice. Callers do NOT need to nil-
// check. V125-1.4 lesson: never let a nil array reach the FE.
func resolveOrphanRegistrations(
	ctx context.Context,
	lister argocdClusterLister,
	gitClusters []models.Cluster,
	pendingNames map[string]struct{},
) []models.OrphanRegistration {
	out := []models.OrphanRegistration{}

	if lister == nil {
		return out
	}

	argoClusters, err := lister.ListClusters(ctx)
	if err != nil {
		slog.Warn("list_argocd_clusters_for_orphan_registrations: degrading to empty",
			"err", err.Error())
		return out
	}

	// Set of cluster names that ARE in managed-clusters.yaml. These are
	// legitimately managed and must never appear as orphans.
	managedNames := make(map[string]struct{}, len(gitClusters))
	for _, c := range gitClusters {
		managedNames[c.Name] = struct{}{}
	}

	// LastSeenAt is the response time of THIS resolver call. The ArgoCD
	// cluster Secret API exposes no stable creation timestamp, so we tell
	// the user "as of this refresh, this orphan exists" rather than "this
	// orphan has existed since X". Documented contract — do not change to
	// per-cluster timestamps without source of truth.
	now := time.Now().UTC().Format(time.RFC3339)

	for _, ac := range argoClusters {
		// Skip the in-cluster entry (Sharko's own host cluster).
		if ac.Name == "in-cluster" || strings.HasPrefix(ac.Server, "https://kubernetes.default") {
			continue
		}
		// Managed: legitimately in git, not orphan.
		if _, hit := managedNames[ac.Name]; hit {
			continue
		}
		// Pending: has an open register PR, not orphan (yet).
		if pendingNames != nil {
			if _, hit := pendingNames[ac.Name]; hit {
				continue
			}
		}
		out = append(out, models.OrphanRegistration{
			ClusterName: ac.Name,
			ServerURL:   ac.Server,
			LastSeenAt:  now,
		})
	}
	return out
}
