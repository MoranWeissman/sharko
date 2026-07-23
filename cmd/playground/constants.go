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
