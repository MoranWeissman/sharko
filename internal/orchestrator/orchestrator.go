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

// PRTracker is the subset of prtracker.Tracker needed by the orchestrator.
// Defined locally (not in internal/prtracker) to avoid importing prtracker
// from orchestrator — the API layer wires the concrete implementation in
// via SetPRTracker. Following the same adapter pattern used for
// ArgoSecretManager.
type PRTracker interface {
	TrackPR(ctx context.Context, pr TrackedPR) error
}

// TrackedPR is the orchestrator-side mirror of prtracker.PRInfo. We
// declare it locally so the orchestrator stays free of an import on
// internal/prtracker. The API-side adapter converts this to PRInfo.
type TrackedPR struct {
	PRID       int
	PRUrl      string
	PRBranch   string
	PRTitle    string
	PRBase     string
	Cluster    string
	Addon      string
	Operation  string
	User       string
	Source     string
	CreatedAt  time.Time
	LastStatus string
}

// PRMetadata describes the PR-level metadata needed to track a PR
// once commitChanges creates it. Filled in by the orchestrator
// operation (RegisterCluster, AddAddon, etc.) so a single TrackPR
// call is made centrally rather than scattered across handlers.
type PRMetadata struct {
	OperationCode string // canonical enum — see prtracker.Op* constants
	Cluster       string
	Addon         string
	Title         string // human-readable PR title (defaults to commitMsg when empty)
	User          string // author/actor (set by handler-derived ctx; "system" when unknown)
	Source        string // ui, cli, api — defaults to "api" in handlers
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
	prTracker         PRTracker         // optional — tracks every commitChanges-created PR (V125-1-6)
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

// SetPRTracker wires in the PR tracker so every PR created via
// commitChangesWithMeta is automatically registered for status polling
// and dashboard surfacing. Pass nil (or simply skip the call) to disable
// orchestrator-side tracking — handlers may still call the tracker
// directly for non-orchestrator paths (init.go's CreateInitPR, the AI
// assistant write tools, the secret_path PATCH branch in
// clusters_write.go).
func (o *Orchestrator) SetPRTracker(t PRTracker) {
	o.prTracker = t
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
