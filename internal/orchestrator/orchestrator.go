package orchestrator

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ArgocdClient is the subset of ArgoCD operations the orchestrator needs.
type ArgocdClient interface {
	RegisterCluster(ctx context.Context, name, server string, caData []byte, token string, labels map[string]string) error
	DeleteCluster(ctx context.Context, serverURL string) error
	UpdateClusterLabels(ctx context.Context, serverURL string, labels map[string]string) error
	SyncApplication(ctx context.Context, appName string) error
}

// Orchestrator coordinates multi-step operations across providers, ArgoCD,
// and Git to execute cluster and addon management workflows.
type Orchestrator struct {
	credProvider providers.ClusterCredentialsProvider
	argocd       ArgocdClient
	git          gitprovider.GitProvider
	gitops       GitOpsConfig
	paths        RepoPathsConfig
}

// New creates an Orchestrator with the given dependencies.
func New(
	credProvider providers.ClusterCredentialsProvider,
	argocd ArgocdClient,
	git gitprovider.GitProvider,
	gitops GitOpsConfig,
	paths RepoPathsConfig,
) *Orchestrator {
	return &Orchestrator{
		credProvider: credProvider,
		argocd:       argocd,
		git:          git,
		gitops:       gitops,
		paths:        paths,
	}
}
