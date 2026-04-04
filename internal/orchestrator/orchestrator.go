package orchestrator

import (
	"context"
	"io/fs"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ArgocdClient is the subset of ArgoCD operations the orchestrator needs.
type ArgocdClient interface {
	ListClusters(ctx context.Context) ([]models.ArgocdCluster, error)
	RegisterCluster(ctx context.Context, name, server string, caData []byte, token string, labels map[string]string) error
	DeleteCluster(ctx context.Context, serverURL string) error
	UpdateClusterLabels(ctx context.Context, serverURL string, labels map[string]string) error
	SyncApplication(ctx context.Context, appName string) error
	CreateProject(ctx context.Context, projectJSON []byte) error
	CreateApplication(ctx context.Context, appJSON []byte) error
	AddRepository(ctx context.Context, repoURL, username, password string) error
	GetApplication(ctx context.Context, name string) (*models.ArgocdApplication, error)
}

// Orchestrator coordinates multi-step operations across providers, ArgoCD,
// and Git to execute cluster and addon management workflows.
type Orchestrator struct {
	gitMu          *sync.Mutex // shared across all orchestrator instances — serializes Git operations
	credProvider   providers.ClusterCredentialsProvider
	argocd         ArgocdClient
	git            gitprovider.GitProvider
	gitops         GitOpsConfig
	paths          RepoPathsConfig
	templateFS     fs.FS
	secretDefs     map[string]AddonSecretDefinition // addon name → definition
	secretFetcher  SecretValueFetcher
	remoteClientFn RemoteClientFactory
	defaultAddons  map[string]bool // addon name → enabled (merged into RegisterCluster when req.Addons is empty)
	drainSleep     time.Duration   // wait after label removal in DeregisterCluster; overridable in tests
}

// SetDefaultAddons configures the default addons applied to clusters
// registered without explicit addon selections.
func (o *Orchestrator) SetDefaultAddons(defaults map[string]bool) {
	o.defaultAddons = defaults
}

// New creates an Orchestrator with the given dependencies.
// The gitMu mutex is shared across all orchestrator instances to serialize Git operations.
// Pass nil for gitMu in tests where concurrency is not being tested.
func New(
	gitMu *sync.Mutex,
	credProvider providers.ClusterCredentialsProvider,
	argocd ArgocdClient,
	git gitprovider.GitProvider,
	gitops GitOpsConfig,
	paths RepoPathsConfig,
	templateFS fs.FS,
) *Orchestrator {
	return &Orchestrator{
		gitMu:        gitMu,
		credProvider: credProvider,
		argocd:       argocd,
		git:          git,
		gitops:       gitops,
		paths:        paths,
		templateFS:   templateFS,
		drainSleep:   5 * time.Second,
	}
}
