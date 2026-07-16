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
	// ListApplications returns all applications visible to the token. Used by
	// the bootstrap-app probe (V2-cleanup-11.2): ArgoCD answers GET on a
	// non-existent app with 403 (not 404) for apiKey tokens, so the probe
	// LISTs and filters by name instead of GET-by-name — a missing app is then
	// an empty filter result (offer init), not a phantom permission error.
	ListApplications(ctx context.Context) ([]models.ArgocdApplication, error)
}

// ArgoSecretManager is the interface the orchestrator uses for ArgoCD
// cluster-secret metadata operations that are NOT covered by the cluster
// reconciler — i.e. adopt / unadopt label + annotation manipulation. It is
// defined here (not in internal/argosecrets) to avoid an import cycle:
// orchestrator must not import argosecrets. The adapter in internal/api
// bridges the two packages.
//
// The orchestrator does NOT create or update cluster Secrets pre-merge —
// the reconciler owns Secret lifecycle. Adopt / unadopt remain
// orchestrator concerns because they mutate ArgoCD-side metadata
// (managed-by label + adopted annotation) on a per-operation basis that
// the periodic reconciler does not own.
type ArgoSecretManager interface {
	SetAnnotation(ctx context.Context, name, key, value string) error
	GetAnnotation(ctx context.Context, name, key string) (string, error)
	GetManagedByLabel(ctx context.Context, name string) (string, error)
	Unadopt(ctx context.Context, name string) error
	// Ensure creates or updates the ArgoCD cluster Secret for spec. Used by
	// the kubeconfig registration path to write the Secret directly from the
	// pasted credentials (V2-cleanup-8.2) — those credentials never reach the
	// secrets backend the reconciler reads from, so the reconciler can never
	// create the Secret for them. Returns (changed, error).
	Ensure(ctx context.Context, spec ArgoSecretSpec) (bool, error)
}

// ArgoSecretSpec mirrors argosecrets.ClusterSecretSpec but is defined locally
// to keep the orchestrator free from argosecrets imports. The api-layer
// adapter (api/argo_adapter.go) converts it to argosecrets.ClusterSecretSpec.
type ArgoSecretSpec struct {
	Name    string
	Server  string
	Region  string
	RoleARN string
	// CAData is the base64-encoded PEM CA bundle (same encoding the
	// reconciler uses). Written into tlsClientConfig.caData.
	CAData string
	// Token is a static bearer token. When set, the Secret is written in
	// ArgoCD's bearerToken shape (the kubeconfig registration path).
	Token string
	// CertData / KeyData are the base64-encoded PEM client certificate pair
	// (same encoding as CAData). When BOTH are set, the Secret is written in
	// ArgoCD's plain-TLS shape — cert pair takes precedence over Token
	// (V2-cleanup-56.1, kind / kubeadm / on-prem kubeconfigs).
	CertData    string
	KeyData     string
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

// EventEmitter is the subset of events.EventRecorder the orchestrator needs
// to emit a Kubernetes Warning event on a genuine operational failure (V3 E1).
// Defined locally (not importing internal/events) to keep the orchestrator
// decoupled, mirroring the PRTracker / ArgoSecretManager adapter pattern.
// The API layer wires the concrete recorder in via SetEventEmitter; nil is a
// silent no-op (out-of-cluster / dev mode), so every call site is nil-safe.
type EventEmitter interface {
	// Emit records one event. eventType is "Normal" or "Warning"; reason is a
	// stable UpperCamelCase constant; message is plain-English (NEVER any
	// secret material).
	Emit(reason, message, eventType string)
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

	// AutoMergeOverride lets a single operation override the connection-
	// level PRAutoMerge default for THIS PR only. nil means "fall back
	// to o.gitops.PRAutoMerge"; non-nil wins. Resolved inside
	// commitChangesWithMeta via resolveAutoMerge — never mutate
	// o.gitops.PRAutoMerge (shared state across concurrent requests).
	AutoMergeOverride *bool
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
	argoSecretManager ArgoSecretManager // optional — adopt/unadopt ArgoCD cluster-secret metadata only (reconciler owns Secret lifecycle)
	defaultRoleARN    string            // connection-level default RoleARN, held for SetArgoSecretManager back-compat
	prTracker         PRTracker         // optional — tracks every commitChanges-created PR
	triggerFn         func()            // optional — invoked after Sharko writes managed-clusters.yaml to nudge the reconciler; nil disables
	eventEmitter      EventEmitter      // optional — emits k8s Warning events on genuine failures (V3 E1); nil is a silent no-op

	// credsRouter routes per-cluster credential fetches by the cluster's
	// stored creds source (V2-cleanup-60.4): inline-registered clusters are
	// read via the ArgoCD provider regardless of the configured backend.
	// New() defaults it from credProvider; the API layer overrides it via
	// SetCredsRouter so api-side and orchestrator-side fetches share one
	// router (and its cached ArgoCD reader).
	credsRouter *providers.ClusterCredsRouter

	// allowInlineCredentialsFn, when non-nil, is consulted by RegisterCluster
	// to decide whether an inline-kubeconfig registration that actually
	// supplied kubeconfig bytes may proceed (V2-cleanup-89.6's
	// allow_inline_credentials server setting). nil means "no settings store
	// wired" — every registration is allowed, matching today's behavior and
	// the setting's own default (true). Wired from
	// settings.Store.IsInlineCredentialsAllowed in the API layer, which
	// already swallows read errors and defaults to true — RegisterCluster
	// never blocks on a settings-store outage.
	allowInlineCredentialsFn func(ctx context.Context) bool
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

// SetAllowInlineCredentialsFn wires in the server-wide allow_inline_credentials
// policy check (V2-cleanup-89.6). Pass nil (or skip the call) to disable the
// gate entirely — RegisterCluster then allows every inline-kubeconfig
// registration, matching pre-89.6 behavior. Production wires this to
// settings.Store.IsInlineCredentialsAllowed, which is itself nil-safe and
// defaults to true.
func (o *Orchestrator) SetAllowInlineCredentialsFn(fn func(ctx context.Context) bool) {
	o.allowInlineCredentialsFn = fn
}

// SetReconcilerTrigger wires in a nudge function that is invoked after the
// orchestrator commits a managed-clusters.yaml change (register / refresh).
// Production wires this to reconciler.Trigger so post-merge Secret
// convergence happens immediately rather than waiting for the periodic 30s
// safety-net tick. Passing nil (or skipping the call) disables the nudge —
// the reconciler still converges on its periodic tick.
//
// The function MUST be cheap and non-blocking; reconciler.Trigger is a
// channel send into a buffered drain. The orchestrator calls it
// fire-and-forget — any panic in the trigger fn is the caller's bug.
func (o *Orchestrator) SetReconcilerTrigger(fn func()) {
	o.triggerFn = fn
}

// fireReconcilerTrigger is the internal nudge helper. Safe to call when
// triggerFn is nil (no-op). Centralised so future audit logging or
// metrics around the trigger have one site to instrument.
func (o *Orchestrator) fireReconcilerTrigger() {
	if o.triggerFn != nil {
		o.triggerFn()
	}
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

// SetEventEmitter wires in the Kubernetes event emitter (V3 E1) so the
// orchestrator can surface a Warning event when a genuine operational failure
// occurs (e.g. a PR fails to open). Pass nil (or skip the call) to disable
// event emission entirely — every emit site is nil-safe via emitWarning.
func (o *Orchestrator) SetEventEmitter(e EventEmitter) {
	o.eventEmitter = e
}

// emitWarning records one Warning event, nil-safe. Reason must be a stable
// UpperCamelCase constant; message must be plain-English with NO secret
// material (no tokens, kubeconfigs, credentials, secret values, or account
// ids). Safe to call when eventEmitter is nil (out-of-cluster / dev mode).
func (o *Orchestrator) emitWarning(reason, message string) {
	if o.eventEmitter != nil {
		o.eventEmitter.Emit(reason, message, "Warning")
	}
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
		credsRouter:  providers.NewClusterCredsRouter(credProvider, providers.ClusterTestProviderConfig{}),
	}
}

// SetCredsRouter overrides the per-cluster credential-fetch router
// (V2-cleanup-60.4). The API layer passes its server-lifetime router here
// (via attachPRTracker) so every per-request orchestrator shares the same
// cached ArgoCD reader and the same test seam. Passing nil is a no-op —
// the New() default stays in place.
func (o *Orchestrator) SetCredsRouter(r *providers.ClusterCredsRouter) {
	if r != nil {
		o.credsRouter = r
	}
}
