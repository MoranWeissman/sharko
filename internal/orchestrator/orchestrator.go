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

// ArgoSecretManager is the interface the orchestrator uses to create or update
// ArgoCD cluster secrets. It is defined here (not in internal/argosecrets) to
// avoid an import cycle: orchestrator must not import argosecrets.
// The adapter in internal/api bridges the two packages.
type ArgoSecretManager interface {
	Ensure(ctx context.Context, spec ArgoSecretSpec) error
	SetAnnotation(ctx context.Context, name, key, value string) error
	GetAnnotation(ctx context.Context, name, key string) (string, error)
	GetManagedByLabel(ctx context.Context, name string) (string, error)
	Unadopt(ctx context.Context, name string) error
}

// ArgoSecretSpec mirrors argosecrets.ClusterSecretSpec but is defined locally
// to keep the orchestrator free from argosecrets imports.
type ArgoSecretSpec struct {
	Name        string
	Server      string
	Region      string
	RoleARN     string
	Labels      map[string]string
	Annotations map[string]string
}

// Orchestrator coordinates multi-step operations across providers, ArgoCD,
// and Git to execute cluster and addon management workflows.
type Orchestrator struct {
	gitMu             *sync.Mutex // shared across all orchestrator instances — serializes Git operations
	credProvider      providers.ClusterCredentialsProvider
	argocd            ArgocdClient
	git               gitprovider.GitProvider
	gitops            GitOpsConfig
	paths             RepoPathsConfig
	templateFS        fs.FS
	secretDefs        map[string]AddonSecretDefinition // addon name → definition
	secretFetcher     SecretValueFetcher
	remoteClientFn    RemoteClientFactory
	defaultAddons     map[string]bool   // addon name → enabled (merged into RegisterCluster when req.Addons is empty)
	drainSleep        time.Duration     // wait after label removal in DeregisterCluster; overridable in tests
	argoSecretManager ArgoSecretManager // optional — creates ArgoCD cluster secrets
	defaultRoleARN    string            // connection-level default RoleARN for ArgoCD secret creation
}

// SetDefaultAddons configures the default addons applied to clusters
// registered without explicit addon selections.
func (o *Orchestrator) SetDefaultAddons(defaults map[string]bool) {
	o.defaultAddons = defaults
}

// SetArgoSecretManager wires in the ArgoCD cluster secret manager and the
// default IAM role ARN used when building ArgoSecretSpec during RegisterCluster.
// Both are always set together because they are only meaningful as a pair.
// Passing nil for m disables ArgoCD secret creation (backward-compatible default).
func (o *Orchestrator) SetArgoSecretManager(m ArgoSecretManager, defaultRoleARN string) {
	o.argoSecretManager = m
	o.defaultRoleARN = defaultRoleARN
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
