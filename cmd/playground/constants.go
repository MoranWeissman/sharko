package main

import "fmt"

// Cluster names and constants shared across all playground operations.
// These are the load-bearing names that all four stories use — DO NOT
// change them without coordinating across the epic.
const (
	// ClusterHub is the hub kind cluster name (runs ArgoCD + Sharko + GitFake).
	ClusterHub = "sharko-play-hub"
	// ClusterSpokePrefix is the prefix for spoke cluster names (N spokes =
	// sharko-play-spoke-1..N).
	ClusterSpokePrefix = "sharko-play-spoke-"

	// ContextHub is the kubectl context name for the hub cluster.
	ContextHub = "kind-" + ClusterHub

	// Release is the Helm release name for Sharko on the hub.
	Release = "sharko"
	// Namespace is the K8s namespace where Sharko is installed.
	Namespace = "sharko"
	// ArgoCDNamespace is the K8s namespace where ArgoCD is installed.
	ArgoCDNamespace = "argocd"

	// GitFakeRepoName is the repo path-segment name for the in-cluster GitFake.
	GitFakeRepoName = "sharko-playground"
	// GitFakeSeedBranch is the default branch in the GitFake repo.
	GitFakeSeedBranch = "main"

	// GiteaRepoName is the repo name for the Gitea repository (used when PLAYGROUND_GIT_BACKEND=gitea).
	GiteaRepoName = "sharko-playground"
	// GiteaAdminUser is the admin username created in Gitea.
	GiteaAdminUser = "sharko"
	// GiteaAdminPassword is the admin password (local dev only — NOT for production).
	GiteaAdminPassword = "sharko-play"
	// GiteaAdminEmail is the email for the Gitea admin user.
	GiteaAdminEmail = "admin@gitea.example.com"
	// GiteaSeedBranch is the default branch in the Gitea repo.
	GiteaSeedBranch = "main"

	// ServiceAccountName is the SA name created on each spoke for cluster-admin
	// kubeconfig generation.
	ServiceAccountName = "sharko-spoke-admin"

	// SpokeTokenDuration is how long the ServiceAccount bearer token baked into
	// each spoke's registration kubeconfig (and thus into the ArgoCD cluster
	// Secret) stays valid.
	//
	// Why 30 days and not something short: the playground is a throwaway LOCAL
	// dev environment, so a long-lived token is the right call here — this is
	// NOT a pattern to copy for a real fleet, which needs proper credential
	// management (short-lived tokens + rotation). kind's kube-apiserver has no
	// max-token-expiration set, so it honors whatever duration we ask for.
	//
	// What breaks if this is too short: once the token expires, ArgoCD's
	// spoke cluster test-connection starts returning 401 ("the server has
	// asked for the client to provide credentials") and ArgoCD loses access
	// to the spoke while still showing a stale green status. This was found
	// live during a walkthrough when the old 1h duration expired mid-session
	// and silently broke the whole fleet's auth.
	SpokeTokenDuration = "720h"

	// DefaultSpokes is the number of spoke clusters to create when
	// PLAYGROUND_SPOKES is not set.
	DefaultSpokes = 2

	// GiteaConnectionName is the connection name registered when using the Gitea backend.
	GiteaConnectionName = "gitea-playground"
)

// SpokeDisplayNames returns the display names (as registered in ArgoCD /
// managed-clusters.yaml) for N spokes. The first two are spoke-eu and
// spoke-us; subsequent spokes are spoke-3, spoke-4, etc.
func SpokeDisplayNames(n int) []string {
	if n == 0 {
		return nil
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		switch i {
		case 0:
			names[i] = "spoke-eu"
		case 1:
			names[i] = "spoke-us"
		default:
			names[i] = fmt.Sprintf("spoke-%d", i+1)
		}
	}
	return names
}

// SpokeClusterName returns the kind cluster name for spoke index i (0-based).
func SpokeClusterName(i int) string {
	return fmt.Sprintf("%s%d", ClusterSpokePrefix, i+1)
}

// SpokeContext returns the kubectl context name for spoke index i (0-based).
func SpokeContext(i int) string {
	return "kind-" + SpokeClusterName(i)
}
