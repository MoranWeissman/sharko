package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger"
	"golang.org/x/crypto/bcrypt"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	_ "github.com/MoranWeissman/sharko/docs/swagger" // swagger docs
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/notifications"
	"github.com/MoranWeissman/sharko/internal/observations"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/service"
)

// ArgoReconcilerCfg holds the stable parameters needed to (re)create the
// argosecrets reconciler. The K8sClient and ArgocdNamespace are set once at
// startup and never change between reinits.
type ArgoReconcilerCfg struct {
	K8sClient           kubernetes.Interface
	ArgocdNamespace     string
	Interval            time.Duration
	GitReaderFn         func() argosecrets.GitReader
	Parser              *config.Parser
	ManagedClustersPath string // path in Git repo to managed-clusters.yaml
}

// SecretReconciler is the interface the server uses to trigger and query the reconciler.
// It is implemented by internal/secrets.Reconciler but defined here to avoid an import cycle.
type SecretReconciler interface {
	Trigger()
	GetStats() interface{} // returns secrets.ReconcileStats but we keep the import-free boundary
}

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	connSvc          *service.ConnectionService
	clusterSvc       *service.ClusterService
	addonSvc         *service.AddonService
	dashboardSvc     *service.DashboardService
	observabilitySvc *service.ObservabilityService
	upgradeSvc       *service.UpgradeService
	aiClient          *ai.Client
	agentMemory       *ai.MemoryStore
	authStore         *auth.Store
	aiConfigStore     *config.AIConfigStore

	// Write API dependencies (optional — set via SetOrchestrator).
	credProvider providers.ClusterCredentialsProvider
	providerCfg  *providers.Config
	repoPaths    orchestrator.RepoPathsConfig
	gitopsCfg    orchestrator.GitOpsConfig
	gitMu        sync.Mutex // shared mutex serializing all Git operations across requests

	// Remote secret management (optional — set via SetAddonSecretDefs).
	addonSecretDefs   map[string]orchestrator.AddonSecretDefinition
	addonSecretDefsMu sync.RWMutex // protects addonSecretDefs from concurrent read/write
	secretFetcher     orchestrator.SecretValueFetcher

	// Default addons (optional — set via SetDefaultAddons).
	defaultAddons map[string]bool

	// Audit log for external-change events (always available — initialised in NewServer).
	auditLog *audit.Log

	// Notification store (always available — initialised in NewServer).
	notificationStore *notifications.Store

	// Operation store for async long-running operations (always available — initialised in NewServer).
	opsStore *operations.Store

	// Template filesystem for POST /api/v1/init (always available).
	templateFS fs.FS

	// secretReconciler reconciles addon secrets across remote clusters (optional — set via SetSecretReconciler).
	secretReconciler SecretReconciler

	// ArgoCD cluster secret management (optional — set via SetArgoSecretManager/SetArgoSecretReconciler).
	argoSecretManager    *argosecrets.Manager
	argoSecretReconciler *argosecrets.Reconciler

	// argoReconcilerConfig holds the stable parameters needed to restart the
	// argosecrets reconciler on ReinitializeFromConnection without re-creating the
	// in-cluster K8s client (which does not change between reinits).
	argoReconcilerConfig *ArgoReconcilerCfg

	// prTracker tracks PRs created by Sharko operations (optional — set via SetPRTracker).
	prTracker *prtracker.Tracker

	// obsStore persists cluster connectivity observations (optional — set via SetObservationsStore).
	obsStore *observations.Store

	// startTime records when the server was created (used for uptime reporting).
	startTime time.Time

	// version is set at startup via SetVersion and reflects the ldflags-injected build version.
	version string

	// catalog holds the curated addon catalog parsed from the embedded YAML
	// at server startup (see internal/catalog). Optional — handlers that
	// depend on it return 503 when nil.
	catalog *catalog.Catalog
}

// NewServer creates a new API server.
func NewServer(
	connSvc *service.ConnectionService,
	clusterSvc *service.ClusterService,
	addonSvc *service.AddonService,
	dashboardSvc *service.DashboardService,
	observabilitySvc *service.ObservabilityService,
	upgradeSvc *service.UpgradeService,
	aiClient *ai.Client,
) *Server {
	// Initialize agent memory — store in /tmp for containers (writable), or local dir for dev
	memoryPath := "/tmp/sharko-agent-memory.json"
	agentMemory := ai.NewMemoryStore(memoryPath)

	// Initialize auth store (auto-detects K8s vs local mode)
	authStore := auth.NewStore()

	if !authStore.HasUsers() {
		slog.Warn("WARNING: Authentication is DISABLED — all API endpoints are publicly accessible. Configure users via K8s ConfigMap or SHARKO_AUTH_USER env var.")
	}

	return &Server{
		connSvc:           connSvc,
		clusterSvc:        clusterSvc,
		addonSvc:          addonSvc,
		dashboardSvc:      dashboardSvc,
		observabilitySvc:  observabilitySvc,
		upgradeSvc:        upgradeSvc,
		aiClient:          aiClient,
		agentMemory:       agentMemory,
		authStore:         authStore,
		aiConfigStore:     nil, // set via SetAIConfigStore
		addonSecretDefs:   make(map[string]orchestrator.AddonSecretDefinition),
		auditLog:          audit.NewLog(1000),
		notificationStore: notifications.NewStore(100, notifications.DefaultNotificationsPath),
		opsStore:          operations.NewStore(),
		startTime:         time.Now(),
	}
}

// SetVersion stores the build version (injected via ldflags) for use in the health endpoint.
// Falls back to "dev" if never called or called with an empty string.
func (s *Server) SetVersion(v string) {
	s.version = v
}

// SetSecretReconciler wires in the background secret reconciler.
// Call this after NewServer, before starting the HTTP listener.
func (s *Server) SetSecretReconciler(r SecretReconciler) {
	s.secretReconciler = r
}

// SetArgoSecretManager stores the ArgoCD secrets Manager for use by downstream handlers.
func (s *Server) SetArgoSecretManager(m *argosecrets.Manager) {
	s.argoSecretManager = m
}

// ArgoSecretManager returns the ArgoCD secrets Manager (may be nil if not configured).
func (s *Server) ArgoSecretManager() *argosecrets.Manager {
	return s.argoSecretManager
}

// SetArgoSecretReconciler stores the ArgoCD secrets Reconciler.
func (s *Server) SetArgoSecretReconciler(r *argosecrets.Reconciler) {
	s.argoSecretReconciler = r
}

// ArgoSecretReconciler returns the current ArgoCD secrets Reconciler (may be nil or
// replaced by ReinitializeFromConnection). Always use this getter — never cache the
// pointer returned by this method, as it can be swapped on reinit.
func (s *Server) ArgoSecretReconciler() *argosecrets.Reconciler {
	return s.argoSecretReconciler
}

// SetArgoReconcilerConfig stores the stable parameters (k8s client, namespace, interval,
// gitReaderFn, parser) needed to restart the argosecrets reconciler on reinit.
// Called once at startup from serve.go after the in-cluster client is created.
func (s *Server) SetArgoReconcilerConfig(cfg *ArgoReconcilerCfg) {
	s.argoReconcilerConfig = cfg
}

// SetAIConfigStore sets the persistent AI config store (K8s mode only).
func (s *Server) SetAIConfigStore(store *config.AIConfigStore) {
	s.aiConfigStore = store
}

// SetTemplateFS sets the embedded template filesystem for POST /api/v1/init.
func (s *Server) SetTemplateFS(tfs fs.FS) {
	s.templateFS = tfs
}

// SetWriteAPIDeps configures the dependencies for write API endpoints.
// credProvider is the cluster credentials backend (e.g. AWS SM, K8s secrets).
// provCfg holds the provider configuration for system info endpoints.
// paths and gitops hold the repo layout and gitops commit settings.
func (s *Server) SetWriteAPIDeps(credProvider providers.ClusterCredentialsProvider, provCfg *providers.Config, paths orchestrator.RepoPathsConfig, gitops orchestrator.GitOpsConfig) {
	s.credProvider = credProvider
	s.providerCfg = provCfg
	s.repoPaths = paths
	s.gitopsCfg = gitops
}

// SetAddonSecretDefs sets the addon secret definitions (loaded from env/config).
func (s *Server) SetAddonSecretDefs(defs map[string]orchestrator.AddonSecretDefinition) {
	s.addonSecretDefs = defs
}

// SetSecretFetcher sets the secret value fetcher for remote cluster secret operations.
func (s *Server) SetSecretFetcher(fetcher orchestrator.SecretValueFetcher) {
	s.secretFetcher = fetcher
}

// SetDefaultAddons configures default addons applied to clusters registered without
// explicit addon selections.
func (s *Server) SetDefaultAddons(defaults map[string]bool) {
	s.defaultAddons = defaults
}

// SetObservationsStore wires in the cluster observations store.
// Call this after NewServer, before starting the HTTP listener.
func (s *Server) SetObservationsStore(store *observations.Store) {
	s.obsStore = store
}

// SetPRTracker wires in the PR tracker for polling and API access.
func (s *Server) SetPRTracker(tracker *prtracker.Tracker) {
	s.prTracker = tracker
}

// PRTracker returns the current PR tracker (may be nil if not configured).
func (s *Server) PRTracker() *prtracker.Tracker {
	return s.prTracker
}

// ReinitializeFromConnection reads provider config and GitOps settings from the active connection
// and rebuilds credProvider + providerCfg + gitopsCfg. Called after connection create/update/set-active
// so that write-API operations pick up the new settings immediately without a restart.
// Also called at startup so that a pod restart does not leave the provider nil.
func (s *Server) ReinitializeFromConnection() {
	slog.Info("[startup] ReinitializeFromConnection called")

	conn, err := s.connSvc.GetActiveConnection()
	if err != nil {
		slog.Warn("[startup] no active connection", "error", err)
		return
	}
	if conn == nil {
		slog.Info("[startup] no active connection configured")
		return
	}

	slog.Info("[startup] active connection found", "name", conn.Name, "has_provider", conn.Provider != nil)

	// Reinit secrets provider from connection.
	if pc := conn.Provider; pc != nil && pc.Type != "" {
		slog.Info("[startup] initializing provider", "type", pc.Type, "region", pc.Region)
		namespace := pc.Namespace
		if namespace == "" {
			namespace = os.Getenv("SHARKO_NAMESPACE")
			if namespace == "" {
				namespace = "sharko"
			}
		}

		cfg := providers.Config{
			Type:      pc.Type,
			Region:    pc.Region,
			Prefix:    pc.Prefix,
			Namespace: namespace,
			RoleARN:   pc.RoleARN,
		}

		p, err := providers.New(cfg)
		if err != nil {
			slog.Warn("[startup] failed to create provider from connection", "error", err)
		} else {
			s.credProvider = p
			s.providerCfg = &cfg
			slog.Info("[startup] provider reinitialized from connection", "type", cfg.Type, "region", cfg.Region, "prefix", cfg.Prefix)
		}
	} else {
		slog.Info("[startup] no provider config in connection")
	}

	// Reinit GitOps config from connection.
	if gitops := conn.GitOps; gitops != nil {
		if gitops.BaseBranch != "" {
			s.gitopsCfg.BaseBranch = gitops.BaseBranch
		}
		if gitops.BranchPrefix != "" {
			s.gitopsCfg.BranchPrefix = gitops.BranchPrefix
		}
		if gitops.CommitPrefix != "" {
			s.gitopsCfg.CommitPrefix = gitops.CommitPrefix
		}
		if gitops.PRAutoMerge != nil {
			s.gitopsCfg.PRAutoMerge = *gitops.PRAutoMerge
		}
		slog.Info("gitops config reinitialized from connection",
			"base_branch", s.gitopsCfg.BaseBranch,
			"branch_prefix", s.gitopsCfg.BranchPrefix,
			"pr_auto_merge", s.gitopsCfg.PRAutoMerge,
		)
	}

	// Populate RepoURL from Git connection if not already set.
	if s.gitopsCfg.RepoURL == "" && conn.Git.RepoURL != "" {
		s.gitopsCfg.RepoURL = conn.Git.RepoURL
	}

	// Restart argosecrets reconciler with the updated provider/config.
	if s.argoReconcilerConfig != nil && s.credProvider != nil {
		// Stop the existing reconciler before replacing it.
		if s.argoSecretReconciler != nil {
			s.argoSecretReconciler.Stop()
		}

		cfg := s.argoReconcilerConfig
		baseBranch := s.gitopsCfg.BaseBranch
		if baseBranch == "" {
			baseBranch = "main"
		}
		defaultRoleARN := ""
		if s.providerCfg != nil {
			defaultRoleARN = s.providerCfg.RoleARN
		}

		newManager := argosecrets.NewManager(cfg.K8sClient, cfg.ArgocdNamespace)
		newReconciler := argosecrets.NewReconciler(
			newManager,
			s.credProvider,
			cfg.GitReaderFn,
			cfg.Parser,
			baseBranch,
			defaultRoleARN,
			cfg.ManagedClustersPath,
			cfg.Interval,
		)

		auditLog := s.auditLog
		newReconciler.SetAuditFunc(func(created, updated, deleted int) {
			auditLog.Add(audit.Entry{
				Level:    "info",
				Event:    "cluster_secret_sync",
				User:     "sharko",
				Action:   "sync",
				Resource: fmt.Sprintf("ArgoCD secrets reconciled — created: %d, updated: %d, deleted: %d", created, updated, deleted),
				Source:   "reconciler",
				Result:   "success",
			})
		})

		s.argoSecretManager = newManager
		s.argoSecretReconciler = newReconciler
		newReconciler.Start(context.Background())
		slog.Info("argosecrets reconciler restarted after connection reinit")
	}
}

// NotificationStore returns the server's notification store so external
// components (e.g. the background Checker) can push notifications into it.
func (s *Server) NotificationStore() *notifications.Store {
	return s.notificationStore
}

// AuditLog returns the server's audit log so external components can record
// events (e.g. the secret reconciler after a reconcile cycle).
func (s *Server) AuditLog() *audit.Log {
	return s.auditLog
}

// SetDemoConnectionService replaces the server's connection service with one
// backed by the provided in-memory store. Used by demo mode only.
func (s *Server) SetDemoConnectionService(store config.Store) {
	s.connSvc = service.NewConnectionService(store)
}

// SetDemoGitProvider installs a fixed GitProvider on the connection service,
// bypassing real Git API calls. Used by demo mode only.
func (s *Server) SetDemoGitProvider(gp service.GitProviderOverride) {
	s.connSvc.SetGitProviderOverride(gp)
}

// AddDemoUser creates a user account in the auth store with a fixed password.
// Used by demo mode only. In local mode the auth store accepts plaintext passwords.
func (s *Server) AddDemoUser(username, password, role string) error {
	return s.authStore.AddUser(username, password, role)
}

// NewRouter builds the HTTP router with all API routes and static file serving.
// staticFS can be nil if no static files are available (e.g., dev mode).
func NewRouter(srv *Server, staticFS fs.FS) http.Handler {
	startSessionCleanup()
	mux := http.NewServeMux()

	// Swagger UI
	mux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	// Prometheus metrics (no auth — protected via ingress or separate port)
	mux.Handle("GET /metrics", promhttp.Handler())

	// Health
	mux.HandleFunc("GET /api/v1/health", srv.handleHealth)

	// Connections
	mux.HandleFunc("GET /api/v1/connections/", srv.handleListConnections)
	mux.HandleFunc("POST /api/v1/connections/", srv.handleCreateConnection)
	mux.HandleFunc("PUT /api/v1/connections/{name}", srv.handleUpdateConnection)
	mux.HandleFunc("DELETE /api/v1/connections/{name}", srv.handleDeleteConnection)
	mux.HandleFunc("POST /api/v1/connections/active", srv.handleSetActiveConnection)
	mux.HandleFunc("POST /api/v1/connections/test", srv.handleTestConnection)
	mux.HandleFunc("POST /api/v1/connections/test-credentials", srv.handleTestCredentials)
	mux.HandleFunc("GET /api/v1/connections/discover-argocd", srv.handleDiscoverArgocd)

	// Clusters — batch and adoption operations (registered before {name} wildcard routes)
	mux.HandleFunc("POST /api/v1/clusters/batch", srv.handleBatchRegisterClusters)
	mux.HandleFunc("POST /api/v1/clusters/adopt", srv.handleAdoptClusters)
	mux.HandleFunc("GET /api/v1/clusters/available", srv.handleDiscoverClusters)
	mux.HandleFunc("POST /api/v1/clusters/discover", srv.handleDiscoverEKS)

	// Clusters (read)
	mux.HandleFunc("GET /api/v1/clusters", srv.handleListClusters)
	mux.HandleFunc("GET /api/v1/clusters/{name}/values", srv.handleGetClusterValues)
	mux.HandleFunc("GET /api/v1/clusters/{name}/config-diff", srv.handleGetConfigDiff)
	mux.HandleFunc("GET /api/v1/clusters/{name}/comparison", srv.handleGetClusterComparison)
	mux.HandleFunc("GET /api/v1/clusters/{name}/history", srv.handleGetClusterHistory)
	mux.HandleFunc("GET /api/v1/clusters/{name}", srv.handleGetCluster)

	// Clusters (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/clusters", srv.handleRegisterCluster)
	mux.HandleFunc("DELETE /api/v1/clusters/{name}", srv.handleDeregisterCluster)
	mux.HandleFunc("PATCH /api/v1/clusters/{name}", srv.handleUpdateClusterAddons)
	mux.HandleFunc("POST /api/v1/clusters/{name}/refresh", srv.handleRefreshClusterCredentials)
	mux.HandleFunc("POST /api/v1/clusters/{name}/test", srv.handleTestCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/diagnose", srv.handleDiagnoseCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/unadopt", srv.handleUnadoptCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/addons/{addon}", srv.handleEnableAddon)
	mux.HandleFunc("DELETE /api/v1/clusters/{name}/addons/{addon}", srv.handleDisableAddon)

	// Init (orchestrator-backed)
	mux.HandleFunc("POST /api/v1/init", srv.handleInit)

	// Operations (async operation tracking)
	mux.HandleFunc("GET /api/v1/operations/{id}", srv.handleGetOperation)
	mux.HandleFunc("POST /api/v1/operations/{id}/heartbeat", srv.handleOperationHeartbeat)
	mux.HandleFunc("POST /api/v1/operations/{id}/cancel", srv.handleCancelOperation)

	// Addons (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/addons/upgrade-batch", srv.handleUpgradeAddonsBatch)
	mux.HandleFunc("POST /api/v1/addons/{name}/upgrade", srv.handleUpgradeAddon)
	mux.HandleFunc("POST /api/v1/addons", srv.handleAddAddon)
	mux.HandleFunc("DELETE /api/v1/addons/{name}", srv.handleRemoveAddon)
	mux.HandleFunc("PATCH /api/v1/addons/{name}", srv.handleConfigureAddon)

	// Values editor (v1.20) — Tier 2 writes + read-side schema/current-values
	mux.HandleFunc("PUT /api/v1/addons/{name}/values", srv.handleSetAddonValues)
	mux.HandleFunc("GET /api/v1/addons/{name}/values-schema", srv.handleGetAddonValuesSchema)
	mux.HandleFunc("PUT /api/v1/clusters/{cluster}/addons/{name}/values", srv.handleSetClusterAddonValues)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/addons/{name}/values", srv.handleGetClusterAddonValues)

	// Values editor extras (v1.20.1):
	//   • Pull upstream chart defaults (Tier 2)
	//   • Recent merged PRs touching a values file (read)
	mux.HandleFunc("POST /api/v1/addons/{name}/values/pull-upstream", srv.handlePullUpstreamValues)
	mux.HandleFunc("GET /api/v1/addons/{name}/values/recent-prs", srv.handleGetAddonValuesRecentPRs)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/addons/{name}/values/recent-prs", srv.handleGetClusterAddonValuesRecentPRs)

	// Addon secrets (definition CRUD)
	mux.HandleFunc("GET /api/v1/addon-secrets", srv.handleListAddonSecrets)
	mux.HandleFunc("POST /api/v1/addon-secrets", srv.handleCreateAddonSecret)
	mux.HandleFunc("DELETE /api/v1/addon-secrets/{addon}", srv.handleDeleteAddonSecret)

	// Cluster secrets (remote cluster operations)
	mux.HandleFunc("GET /api/v1/clusters/{name}/secrets", srv.handleListClusterSecrets)
	mux.HandleFunc("POST /api/v1/clusters/{name}/secrets/refresh", srv.handleRefreshClusterSecrets)

	// Secrets reconciler
	mux.HandleFunc("POST /api/v1/secrets/reconcile", srv.handleTriggerReconcile)
	mux.HandleFunc("GET /api/v1/secrets/status", srv.handleReconcileStatus)

	// Cluster status overview
	mux.HandleFunc("GET /api/v1/fleet/status", srv.handleGetFleetStatus)

	// Repo status
	mux.HandleFunc("GET /api/v1/repo/status", srv.handleRepoStatus)

	// System
	mux.HandleFunc("GET /api/v1/providers", srv.handleGetProviders)
	mux.HandleFunc("POST /api/v1/providers/test", srv.handleTestProvider)
	mux.HandleFunc("POST /api/v1/providers/test-config", srv.handleTestProviderConfig)
	mux.HandleFunc("GET /api/v1/config", srv.handleGetConfig)

	// Curated catalog (v1.21) — embedded marketplace metadata, read-only.
	// Scope: the Sharko-native curated addon list (catalog/addons.yaml)
	// distinct from /api/v1/addons/catalog which surfaces the USER's deployed
	// addons for their connected GitOps repo.
	mux.HandleFunc("GET /api/v1/catalog/addons", srv.handleListCatalogAddons)
	mux.HandleFunc("GET /api/v1/catalog/addons/{name}/versions", srv.handleListCatalogVersions)
	mux.HandleFunc("GET /api/v1/catalog/addons/{name}", srv.handleGetCatalogAddon)

	// Addons (read)
	mux.HandleFunc("GET /api/v1/addons/list", srv.handleListAddons)
	mux.HandleFunc("GET /api/v1/addons/catalog", srv.handleGetAddonCatalog)
	mux.HandleFunc("GET /api/v1/addons/version-matrix", srv.handleGetVersionMatrix)
	mux.HandleFunc("GET /api/v1/addons/{name}/values", srv.handleGetAddonValues)
	mux.HandleFunc("GET /api/v1/addons/{name}/changelog", srv.handleGetAddonChangelog)
	mux.HandleFunc("GET /api/v1/addons/{name}", srv.handleGetAddonDetail)

	// Dashboard
	mux.HandleFunc("GET /api/v1/dashboard/stats", srv.handleGetDashboardStats)
	mux.HandleFunc("GET /api/v1/dashboard/attention", srv.handleGetAttentionItems)
	mux.HandleFunc("GET /api/v1/dashboard/pull-requests", srv.handleGetPullRequests)

	// Embedded dashboards (persisted in K8s ConfigMap)
	mux.HandleFunc("GET /api/v1/embedded-dashboards", srv.handleListDashboards)
	mux.HandleFunc("POST /api/v1/embedded-dashboards", srv.handleSaveDashboards)

	// Upgrade Impact Checker
	mux.HandleFunc("GET /api/v1/upgrade/{addonName}/versions", srv.handleListUpgradeVersions)
	mux.HandleFunc("GET /api/v1/upgrade/{addonName}/recommendations", srv.handleGetRecommendations)
	mux.HandleFunc("POST /api/v1/upgrade/check", srv.handleCheckUpgrade)
	mux.HandleFunc("POST /api/v1/upgrade/ai-summary", srv.handleGetAISummary)
	mux.HandleFunc("GET /api/v1/upgrade/ai-status", srv.handleGetAIStatus)

	// AI Configuration
	mux.HandleFunc("GET /api/v1/ai/config", srv.handleGetAIConfig)
	mux.HandleFunc("POST /api/v1/ai/config", srv.handleSaveAIConfig)
	mux.HandleFunc("POST /api/v1/ai/provider", srv.handleSetAIProvider)
	mux.HandleFunc("POST /api/v1/ai/test", srv.handleTestAI)
	mux.HandleFunc("POST /api/v1/ai/test-config", srv.handleTestAIConfig)

	// Observability
	mux.HandleFunc("GET /api/v1/observability/overview", srv.handleGetObservabilityOverview)

	// AI Agent
	mux.HandleFunc("POST /api/v1/agent/chat", srv.handleAgentChat)
	mux.HandleFunc("POST /api/v1/agent/reset", srv.handleAgentReset)

	// Documentation
	mux.HandleFunc("GET /api/v1/docs/list", srv.handleDocsList)
	mux.HandleFunc("GET /api/v1/docs/{slug}", srv.handleDocsGet)

	// Notifications
	mux.HandleFunc("GET /api/v1/notifications", srv.handleListNotifications)
	mux.HandleFunc("POST /api/v1/notifications/read-all", srv.handleMarkAllNotificationsRead)

	// Pull request tracking
	mux.HandleFunc("GET /api/v1/prs", srv.handleListPRs)
	mux.HandleFunc("GET /api/v1/prs/{id}", srv.handleGetPR)
	mux.HandleFunc("POST /api/v1/prs/{id}/refresh", srv.handleRefreshPR)
	mux.HandleFunc("DELETE /api/v1/prs/{id}", srv.handleDeletePR)

	// Audit log
	mux.HandleFunc("GET /api/v1/audit", srv.handleListAuditLog)
	mux.HandleFunc("GET /api/v1/audit/stream", srv.handleAuditStream)

	// ArgoCD resource exclusions check
	mux.HandleFunc("GET /api/v1/argocd/resource-exclusions", srv.handleCheckResourceExclusions)

	// Cluster info
	mux.HandleFunc("GET /api/v1/cluster/nodes", srv.handleGetNodeInfo)

	// Webhooks (no user auth — signature verified inside the handler)
	mux.HandleFunc("POST /api/v1/webhooks/git", srv.handleGitWebhook)

	// Auth (login is rate-limited: 5 attempts per IP per minute)
	loginRL := newLoginRateLimiter(5, 1*time.Minute)
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !loginRL.Allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "too many login attempts, please try again later")
			return
		}
		srv.handleLogin(w, r)
	})
	mux.HandleFunc("POST /api/v1/auth/logout", srv.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/update-password", srv.handleUpdatePassword)
	mux.HandleFunc("POST /api/v1/auth/hash", srv.handleHashPassword)

	// API tokens (admin only)
	mux.HandleFunc("POST /api/v1/tokens", srv.handleCreateToken)
	mux.HandleFunc("GET /api/v1/tokens", srv.handleListTokens)
	mux.HandleFunc("DELETE /api/v1/tokens/{name}", srv.handleRevokeToken)

	// User management (admin only)
	mux.HandleFunc("GET /api/v1/users", srv.handleListUsers)
	mux.HandleFunc("POST /api/v1/users", srv.handleCreateUser)
	// /users/me must be registered BEFORE /users/{username} so the literal "me" path wins.
	mux.HandleFunc("GET /api/v1/users/me", srv.handleGetMe)
	mux.HandleFunc("PUT /api/v1/users/me/github-token", srv.handleSetMyGitHubToken)
	mux.HandleFunc("DELETE /api/v1/users/me/github-token", srv.handleClearMyGitHubToken)
	mux.HandleFunc("POST /api/v1/users/me/github-token/test", srv.handleTestMyGitHubToken)
	mux.HandleFunc("PUT /api/v1/users/{username}", srv.handleUpdateUser)
	mux.HandleFunc("DELETE /api/v1/users/{username}", srv.handleDeleteUser)
	mux.HandleFunc("POST /api/v1/users/{username}/reset-password", srv.handleResetPassword)

	// Static files (SPA)
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file; if not found, serve index.html for SPA routing
			path := r.URL.Path
			if path == "/" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticFS, path[1:]); err != nil {
				// File not found — serve index.html for client-side routing
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	// Wrap with middleware
	// Wrapping order (innermost → outermost): mux → maxBodySize → writeRateLimiter
	// → auditMiddleware (reads user from header set by basicAuth) → basicAuthMiddleware
	// → cors → securityHeaders → metrics → logging.
	// Execution order reverses: logging → metrics → securityHeaders → cors →
	// basicAuth → auditMiddleware → writeRateLimiter → maxBodySize → mux.
	var handler http.Handler = mux
	handler = maxBodySize(handler, 1<<20)                     // 1MB request body limit
	handler = writeRateLimiter(30, 1*time.Minute)(handler)    // 30 writes/min per IP
	handler = srv.auditMiddleware(handler)                    // emit audit entry after auth sets user
	handler = srv.basicAuthMiddleware(handler)
	handler = corsMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
	handler = metrics.Middleware(handler)                      // Prometheus request metrics
	handler = loggingMiddleware(handler)

	return handler
}

// maxBodySize limits request body size to prevent OOM from large payloads.
func maxBodySize(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// --- Rate limiter (shared) ---

// rateLimiter is a sliding-window, per-key rate limiter.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

// Allow checks whether the given key (IP) is within the rate limit.
// It cleans up expired entries on each call.
func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Evict stale entries across all keys
	for k, times := range rl.attempts {
		filtered := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			delete(rl.attempts, k)
		} else {
			rl.attempts[k] = filtered
		}
	}

	if len(rl.attempts[key]) >= rl.limit {
		return false
	}
	rl.attempts[key] = append(rl.attempts[key], now)
	return true
}

// loginRateLimiter is an alias kept for readability at the call site.
type loginRateLimiter = rateLimiter

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	return newRateLimiter(limit, window)
}

// writeRateLimiter returns a middleware that rate-limits POST/PUT/PATCH/DELETE requests
// per client IP. GET and OPTIONS requests pass through without consuming quota.
func writeRateLimiter(limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := newRateLimiter(limit, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				// Skip the login endpoint — it has its own stricter limiter
				if r.URL.Path == "/api/v1/auth/login" {
					next.ServeHTTP(w, r)
					return
				}
				if !rl.Allow(clientIP(r)) {
					writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client IP, preferring X-Forwarded-For (behind ALB/proxy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may contain multiple IPs; the first is the real client
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Fall back to RemoteAddr (strip port)
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// --- Session token auth ---
//
// Security model: sessions use random tokens passed via the Authorization header
// (Bearer <token>), NOT cookies. This means:
//   - CSRF is inherently mitigated: cross-origin requests cannot set custom headers
//     under the browser's CORS policy, so no CSRF middleware is needed.
//   - HttpOnly/Secure/SameSite cookie attributes do not apply (no cookies used).
//   - Token confidentiality relies on HTTPS in transit and secure client storage
//     (the UI stores the token in sessionStorage).
//   - Sessions expire after 24h; a background goroutine cleans expired entries.

type sessionInfo struct {
	Username string
	Expiry   time.Time
}

var (
	activeSessions   = make(map[string]*sessionInfo) // token -> session
	sessionsMu       sync.RWMutex
	sessionLifetime  = 24 * time.Hour
	sessionCleanOnce sync.Once
)

func startSessionCleanup() {
	sessionCleanOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				sessionsMu.Lock()
				now := time.Now()
				for token, sess := range activeSessions {
					if now.After(sess.Expiry) {
						delete(activeSessions, token)
					}
				}
				sessionsMu.Unlock()
			}
		}()
	})
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func isValidSession(token string) bool {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := activeSessions[token]
	return ok && time.Now().Before(sess.Expiry)
}

func getSessionUser(token string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := activeSessions[token]
	if !ok {
		return ""
	}
	return sess.Username
}

// handleLogin godoc
//
// @Summary Login
// @Description Validates credentials and returns a session token for use in subsequent requests
// @Tags auth
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Login credentials with username and password"
// @Success 200 {object} map[string]interface{} "Session token, username, and role"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Failure 429 {object} map[string]interface{} "Too many login attempts"
// @Router /auth/login [post]
// handleLogin validates credentials and returns a session token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// If no auth configured, allow any login
	if !s.authStore.HasUsers() {
		token := generateToken()
		sessionsMu.Lock()
		activeSessions[token] = &sessionInfo{Username: "anonymous", Expiry: time.Now().Add(sessionLifetime)}
		sessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
		return
	}

	if !s.authStore.ValidateCredentials(req.Username, req.Password) {
		s.auditLog.Add(audit.Entry{
			Level:    "warn",
			Event:    "login_failed",
			User:     req.Username,
			Action:   "login",
			Resource: "session",
			Source:   "api",
			Result:   "failure",
		})
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token := generateToken()
	sessionsMu.Lock()
	activeSessions[token] = &sessionInfo{Username: req.Username, Expiry: time.Now().Add(sessionLifetime)}
	sessionsMu.Unlock()

	user := s.authStore.GetUser(req.Username)
	role := "admin"
	if user != nil {
		role = user.Role
	}

	s.auditLog.Add(audit.Entry{
		Level:    "info",
		Event:    "login",
		User:     req.Username,
		Action:   "login",
		Resource: "session",
		Source:   "api",
		Result:   "success",
	})
	slog.Info("user logged in", "username", req.Username, "role", role)
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "username": req.Username, "role": role})
}

// handleLogout godoc
//
// @Summary Logout
// @Description Invalidates the current session token
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Logged out"
// @Failure 401 {object} map[string]interface{} "No valid session"
// @Router /auth/logout [post]
// handleLogout invalidates the caller's session token.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" || token == authHeader {
		writeError(w, http.StatusUnauthorized, "no session token provided")
		return
	}

	username := getSessionUser(token)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}

	sessionsMu.Lock()
	delete(activeSessions, token)
	sessionsMu.Unlock()

	s.auditLog.Add(audit.Entry{
		Level:    "info",
		Event:    "logout",
		User:     username,
		Action:   "logout",
		Resource: "session",
		Source:   "api",
		Result:   "success",
	})

	slog.Info("user logged out", "username", username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// basicAuthMiddleware enforces token-based auth on all API routes.
// Accepts: Authorization: Bearer <token>
// Skips: health checks, login endpoint, and static files.
func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	// If no users configured, skip auth entirely
	if !s.authStore.HasUsers() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip any client-supplied role header to prevent spoofing.
		// Only the middleware sets this header (for API key auth).
		r.Header.Del("X-Sharko-Role")

		path := r.URL.Path

		// Skip auth for: health, login, git webhooks (signature-verified), static files
		if path == "/api/v1/health" || path == "/api/v1/auth/login" || path == "/api/v1/webhooks/git" || !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Check Bearer token
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if isValidSession(token) {
				username := getSessionUser(token)
				r.Header.Set("X-Sharko-User", username)
				// Look up user role from the store so authz middleware can enforce RBAC
				if user := s.authStore.GetUser(username); user != nil {
					r.Header.Set("X-Sharko-Role", user.Role)
				}
				next.ServeHTTP(w, r)
				return
			}

			// Check if Bearer token is an API key
			if strings.HasPrefix(token, "sharko_") {
				username, role, ok := s.authStore.ValidateToken(token)
				if ok {
					r.Header.Set("X-Sharko-User", username)
					r.Header.Set("X-Sharko-Role", role)
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

// handleUpdatePassword godoc
//
// @Summary Update password
// @Description Changes the current user's password after verifying the existing password
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Current and new password"
// @Success 200 {object} map[string]interface{} "Password updated"
// @Failure 400 {object} map[string]interface{} "Bad request or weak password"
// @Failure 401 {object} map[string]interface{} "Current password incorrect"
// @Router /auth/update-password [post]
// handleUpdatePassword allows changing the password. Verifies current password first.
func (s *Server) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	if !s.authStore.HasUsers() {
		writeError(w, http.StatusBadRequest, "no password configured")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.NewPassword == "" || len(req.NewPassword) < 12 {
		writeError(w, http.StatusBadRequest, "new password must be at least 12 characters")
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	if err := s.authStore.UpdatePassword(username, req.CurrentPassword, req.NewPassword); err != nil {
		if strings.Contains(err.Error(), "incorrect") {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if strings.Contains(err.Error(), "at least") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "password_changed",
		Resource: "user:" + username,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

// handleHashPassword godoc
//
// @Summary Hash password
// @Description Generates a bcrypt hash from a plaintext password. Only available when auth is disabled.
// @Tags auth
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Password to hash"
// @Success 200 {object} map[string]interface{} "Bcrypt hash"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 403 {object} map[string]interface{} "Forbidden when auth is enabled"
// @Router /auth/hash [post]
// handleHashPassword generates a bcrypt hash from a plaintext password.
// Only available when auth is disabled (no users configured) for initial setup.
func (s *Server) handleHashPassword(w http.ResponseWriter, r *http.Request) {
	if s.authStore.HasUsers() {
		writeError(w, http.StatusForbidden, "hash endpoint is only available when auth is disabled")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate hash")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"hash": string(hash)})
}

// securityHeadersMiddleware sets security-related HTTP response headers on every response.
// This includes Content-Security-Policy, X-Content-Type-Options, X-Frame-Options,
// Referrer-Policy, and Strict-Transport-Security (HTTPS only).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"img-src 'self' data:; " +
		"font-src 'self' https://fonts.gstatic.com https://fonts.googleapis.com; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// HSTS only when the connection is (or was proxied as) HTTPS
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers.
func corsMiddleware(next http.Handler) http.Handler {
	corsOrigin := os.Getenv("SHARKO_CORS_ORIGIN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS origin
		origin := r.Header.Get("Origin")
		if corsOrigin == "*" {
			// Dev mode: allow all origins
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if corsOrigin != "" {
			// Explicit origin configured
			if origin == corsOrigin {
				w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
				w.Header().Set("Vary", "Origin")
			}
		} else {
			// Default: same-origin only — reflect Origin if it matches Host
			if origin != "" {
				host := r.Host
				// Check if origin matches the host (same-origin)
				if strings.Contains(origin, "://"+host) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Sharko-Connection")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware logs each request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for health checks (too noisy from K8s probes)
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sr, r)
		slog.Info("request completed", "method", r.Method, "path", r.URL.Path, "status", sr.statusCode, "duration", time.Since(start))
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("error encoding response", "error", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
// v1.39.3 route fix
