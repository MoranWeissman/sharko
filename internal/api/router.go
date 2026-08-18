package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"
	"golang.org/x/crypto/bcrypt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	_ "github.com/MoranWeissman/sharko/docs/swagger" // swagger docs
	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
	"github.com/MoranWeissman/sharko/internal/capabilities"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/changelog"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/notifications"
	"github.com/MoranWeissman/sharko/internal/observations"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/readcache"
	"github.com/MoranWeissman/sharko/internal/secrets"
	"github.com/MoranWeissman/sharko/internal/service"
	"github.com/MoranWeissman/sharko/internal/settings"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// providerSet is the immutable snapshot published through
// Server.providerState (review M1 — provider hot-reload was a data race:
// ReinitializeFromConnection plain-assigned three fields while handlers
// read them). A new set is built and atomically stored on every publish;
// readers get a consistent trio + router or all-nil.
type providerSet struct {
	credProvider   providers.ClusterCredentialsProvider
	addonSecretCfg *providers.AddonSecretProviderConfig
	clusterTestCfg *providers.ClusterTestProviderConfig
	// credsRouter routes per-cluster credential fetches by the cluster's
	// stored creds source (V2-cleanup-60.4) — rebuilt on every publish so
	// its cached ArgoCD reader tracks the active configuration.
	credsRouter *providers.ClusterCredsRouter
}

// credProvider returns the currently-published cluster-credentials
// provider (nil when none is configured). Race-safe (M1).
func (s *Server) credProvider() providers.ClusterCredentialsProvider {
	if ps := s.providerState.Load(); ps != nil {
		return ps.credProvider
	}
	return nil
}

// addonSecretCfg returns the currently-published addon-secret provider
// config (nil when none is configured). Race-safe (M1).
func (s *Server) addonSecretCfg() *providers.AddonSecretProviderConfig {
	if ps := s.providerState.Load(); ps != nil {
		return ps.addonSecretCfg
	}
	return nil
}

// clusterTestCfg returns the currently-published cluster-test provider
// config (nil when none is configured). Race-safe (M1).
func (s *Server) clusterTestCfg() *providers.ClusterTestProviderConfig {
	if ps := s.providerState.Load(); ps != nil {
		return ps.clusterTestCfg
	}
	return nil
}

// credsRouter returns the currently-published per-cluster credential-fetch
// router (nil when no provider set was ever published). Race-safe (M1).
func (s *Server) credsRouter() *providers.ClusterCredsRouter {
	if ps := s.providerState.Load(); ps != nil {
		return ps.credsRouter
	}
	return nil
}

// publishProviders atomically publishes a new provider snapshot. The ONLY
// writer path for the provider trio (SetWriteAPIDeps at boot,
// ReinitializeFromConnection on hot-reload, tests via their seams).
func (s *Server) publishProviders(
	credProvider providers.ClusterCredentialsProvider,
	addonSecretCfg *providers.AddonSecretProviderConfig,
	clusterTestCfg *providers.ClusterTestProviderConfig,
) {
	base := providers.ClusterTestProviderConfig{}
	if clusterTestCfg != nil {
		base = *clusterTestCfg
	}
	s.providerState.Store(&providerSet{
		credProvider:   credProvider,
		addonSecretCfg: addonSecretCfg,
		clusterTestCfg: clusterTestCfg,
		credsRouter:    providers.NewClusterCredsRouter(credProvider, base),
	})
}

// gitopsConfig returns the currently-published GitOps config BY VALUE (a
// consistent immutable snapshot). Returns zero value when none has been
// published yet. Race-safe (GF2).
func (s *Server) gitopsConfig() orchestrator.GitOpsConfig {
	if p := s.gitopsState.Load(); p != nil {
		return *p
	}
	return orchestrator.GitOpsConfig{}
}

// GitopsBaseBranch is the exported form of gitopsConfig().BaseBranch — the
// single seam packages outside internal/api (e.g. internal/service's
// AddonService via SetBaseBranchFn, internal/notifications' ServiceProvider)
// use to read the configured base branch instead of hardcoding "main". Live
// (reads the current published snapshot, not a value captured at wiring
// time), so it stays correct across ReinitializeFromConnection hot-reloads.
func (s *Server) GitopsBaseBranch() string {
	return s.gitopsConfig().BaseBranch
}

// ClusterCredentialsProvider is the exported form of credProvider() — the
// single seam packages outside internal/api use to read the
// CURRENTLY-published cluster-credentials provider. Live (reads the current
// published snapshot, not a value captured at wiring time), so a caller that
// resolves through it on every use sees the same provider generation the
// check path reads, across ReinitializeFromConnection hot-reloads (R2-1:
// cmd/sharko/serve.go wires the cluster reconciler's Deps.Vault as this
// method, so the background write and the repair stop holding the boot
// generation). Returns nil when no backend is configured right now — callers
// must fail closed, never fall back to an earlier value.
func (s *Server) ClusterCredentialsProvider() providers.ClusterCredentialsProvider {
	return s.credProvider()
}

// publishGitopsCfg atomically publishes a new GitOps config snapshot. The
// ONLY writer path for gitops config (SetWriteAPIDeps at boot,
// ReinitializeFromConnection on hot-reload, tests via their seams). GF2.
func (s *Server) publishGitopsCfg(cfg orchestrator.GitOpsConfig) {
	s.gitopsState.Store(&cfg)
}

// SecretReconciler is the interface the server uses to trigger and query the reconciler.
// It is implemented by internal/secrets.Reconciler but defined here to avoid an import cycle.
type SecretReconciler interface {
	Trigger()
	// GetStats returns secrets.ReconcileStats directly — the compiler enforces
	// this boundary now, so every SecretReconciler (the real one and the demo
	// stand-in alike) must hand back the concrete type; there is no longer a
	// type-assertion step on the caller's side that could silently fail.
	GetStats() secrets.ReconcileStats
	// LastRunTime, LastError, and Interval (System-page managed-secrets
	// summary) are primitive-typed on purpose — same import-free-boundary
	// reasoning as GetStats, but callers that only need these facts don't
	// have to type-assert an interface{} to get them.
	//
	// LastError (P1-B B2) is ALREADY the mapped, safe-to-show sentence
	// (secrets.Reconciler.LastError applies secrets.FailureSentence before
	// returning) — never the raw error text.
	LastRunTime() time.Time
	LastError() string
	// LastErrorCluster names the cluster LastError's sentence is ABOUT — the
	// first failing cluster+addon pair's cluster name from the most recent
	// reconcile run (P1-B B2, mirrors clusterreconciler.Reconciler.
	// LastError's cluster — #716). Empty when LastError() is empty, or when
	// the failure was plan-level (reading the catalog/managed-clusters file
	// itself failed, before any per-item work started) — no cluster to name.
	LastErrorCluster() string
	// LastErrorAt is the timestamp of the reconcile run LastError was
	// recorded during — an error with no "since when" isn't actionable.
	// Zero exactly when LastError() is empty.
	LastErrorAt() time.Time
	Interval() time.Duration
	// LastItemChecked reports the last-checked timestamp for one
	// addon-values secret's cluster+addon pair — primitive-typed, same
	// import-free-boundary reasoning as the three methods above (System
	// page's addon_values_secrets rows). ok is false when this pair has
	// never been reconciled on this server instance.
	LastItemChecked(cluster, addon string) (lastChecked time.Time, ok bool)
	// LastItemOutcome reports the last-recorded outcome for one addon-values
	// secret's cluster+addon pair as a plain string — one of "created",
	// "updated", "unchanged", "skipped", "error", "out_of_sync", "missing"
	// (see secrets.ItemOutcome). Primitive-typed, same import-free-boundary
	// reasoning as LastItemChecked. ok is false when this pair has never
	// been checked, synced, or reconciled on this server instance.
	LastItemOutcome(cluster, addon string) (outcome string, ok bool)
	// LastItemError reports the RAW reconcile-failure text recorded for one
	// addon-values secret's cluster+addon pair (S8) — ok is false when the
	// pair has never been checked or its last outcome carried no error.
	// Primitive-typed, same import-free-boundary reasoning as
	// LastItemOutcome. UNLIKE CheckOne/SyncOne's error below, this text is
	// NOT safe to show a caller verbatim — a misbehaving secrets-provider
	// SDK could echo a fragment of a value into its own error text (see
	// secrets.Reconciler.LastItemError's doc comment). Callers must map it
	// through addonValuesSecretCheckFailureSentence first.
	LastItemError(cluster, addon string) (errMsg string, ok bool)
	// LastItemConsecutiveFailures (P2-D D3) reports how many passes in a row
	// this addon-values secret's check or write attempt itself failed —
	// primitive-typed, same import-free-boundary reasoning as
	// LastItemOutcome. ok is false when this pair has never been checked or
	// written on this server instance.
	LastItemConsecutiveFailures(cluster, addon string) (count int, ok bool)
	// KnownItemCount reports how many cluster+addon pairs this engine holds
	// a record for right now — the real blast radius of one CheckAll
	// (P3-F1), so the audit entry a "Refresh all" writes can state how much
	// it actually covered. 0 means "no pass has run on this server instance
	// yet", NOT "there is nothing to check": a caller must treat 0 as "we
	// cannot say" and fall back to wording with no number in it.
	KnownItemCount() int
	// CheckOne re-checks a single addon-values secret against its source
	// right now, WITHOUT writing anything (S4's "Refresh" row action).
	// Returns the outcome as a plain string (see secrets.ItemOutcome); a
	// non-nil error means the check itself could not run (no Git
	// connection, no credentials, cluster unreachable, incomplete catalog
	// definition, or the pair does not resolve to any known secret).
	//
	// H1 (code review): UNLIKE this interface's older doc comment used to
	// claim, this error text is NOT safe to show a caller verbatim — the
	// underlying checkWork call wraps credentials/connect/secrets-provider
	// errors the same way the periodic pass's reconcileSecret does (see
	// LastItemError's own doc comment above), and a misbehaving provider SDK
	// could in principle echo a fragment of a value into its own error text.
	// Callers must map it through addonValuesSecretCheckFailureSentence
	// (internal/api/system_managed_secrets.go) before returning it in a
	// response body — see handleRefreshAddonValuesSecret.
	CheckOne(ctx context.Context, clusterName, addonName string) (outcome string, err error)
	// SyncOne re-pushes a single addon-values secret (S4's "Sync" row
	// action) — the single-item counterpart to Trigger()'s fleet-wide pass.
	// Returns the outcome as a plain string (see secrets.ItemOutcome); a
	// non-nil error means the push itself failed or could not run.
	//
	// H1 (code review): same caveat as CheckOne above — this error text is
	// NOT safe to show a caller verbatim. Callers must map it through
	// addonValuesSecretSyncFailureSentence (internal/api/system_managed_secrets.go)
	// first — see handleSyncAddonValuesSecret.
	SyncOne(ctx context.Context, clusterName, addonName string) (outcome string, err error)
	// SyncCluster delivers every addon-values secret Git defines for ONE
	// cluster right now (task #152, story 152.A) — the git-backed engine
	// behind POST /clusters/{name}/secrets/refresh. What to deliver comes
	// exclusively from the Git catalog via the reconciler's own plan
	// (planPushes → reconcileSecret, the same pipeline the periodic timer
	// runs) — never from a request body, never from any in-memory
	// definition. addonName narrows the push to one addon when non-empty;
	// an addon Git does not define for this cluster refuses the whole
	// call. refreshed and failed carry destination Secret names only —
	// never a value, never a backend path. Error text follows the same
	// safety split as SyncOne: the fixed refusal sentences
	// (secrets.ErrClusterNotInGit / ErrAddonNotInGit / ErrNoGitConnection
	// wording) are safe verbatim; anything else must be mapped to a canned
	// sentence first — see clusterSecretsRefreshRefusal
	// (internal/api/cluster_secrets.go).
	SyncCluster(ctx context.Context, clusterName, addonName string) (refreshed []string, failed []string, err error)
	// CheckAll re-checks EVERY addon-values secret against its source right
	// now, WITHOUT writing anything (P1-A A3 — what the page's "Refresh
	// all" drives on this engine). The fleet-wide counterpart of CheckOne,
	// and deliberately NOT Trigger(): Trigger() runs the periodic pass,
	// which creates and rotates secrets, and a button labelled "check" must
	// never do that. A non-nil error means nothing could be checked at all
	// (no Git connection, catalog unreadable); a single unreachable cluster
	// is isolated inside the pass and does not surface here.
	CheckAll(ctx context.Context) error
	// OrphanedSecrets (leftover-secrets S1) returns a snapshot of every
	// leftover values secret this engine's own passes have found: a live
	// secret that carries BOTH Sharko's managed-by label AND the
	// sharko.dev/addon provenance annotation, that no current desired-state
	// plan claims any more (see secrets.Reconciler.scanOrphans for the full
	// safety rule). Pure in-memory read — GET /system/managed-secrets never
	// triggers a K8s call to build this. Sorted deterministically (cluster,
	// then namespace, then name).
	OrphanedSecrets() []models.OrphanedSecret
	// DeleteOrphanedSecret deletes one leftover secret found by
	// OrphanedSecrets — the operator-gated, human-confirmed action behind
	// DELETE /clusters/{name}/orphaned-secrets/{namespace}/{secret}. Never
	// trusts the scan record as proof on its own: re-verifies the record is
	// still known, that a FRESH plan still does not claim it, and that the
	// live secret still passes both the provenance-annotation check and the
	// managed-by label gate before deleting anything. A non-nil error means
	// nothing was deleted; the error text is safe to show the caller
	// verbatim (see secrets.ErrOrphanUnknown / ErrOrphanReclaimed /
	// ErrOrphanRefused for the specific refusal reasons the handler maps to
	// status codes).
	DeleteOrphanedSecret(ctx context.Context, cluster, namespace, name string) error
	// IsEnabled reports whether the addon-values engine is currently
	// switched on (M6, code review) — checked synchronously by
	// handleCheckSecrets BEFORE it 202s and writes an audit entry, so a
	// disabled engine gets a 409 with no audit entry claiming a check
	// happened, instead of the 202/audit landing before the background
	// goroutine discovers the engine is off and only logs it.
	IsEnabled(ctx context.Context) bool
}

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	connSvc          *service.ConnectionService
	clusterSvc       *service.ClusterService
	addonSvc         *service.AddonService
	dashboardSvc     *service.DashboardService
	observabilitySvc *service.ObservabilityService
	upgradeSvc       *service.UpgradeService
	aiClient         *ai.Client
	agentMemory      *ai.MemoryStore
	authStore        *auth.Store
	aiConfigStore    *config.AIConfigStore

	// Write API dependencies (optional — set via SetWriteAPIDeps).
	//
	// Two typed configs cover the provider surface. Handlers that need
	// addon-secret fields (RoleARN, Region, Prefix) read from
	// addonSecretCfg(); the /providers + /providers/test endpoints display
	// either addonSecretCfg() or clusterTestCfg() per request semantics.
	//
	// providerState is the RACE-SAFE publication point for the provider
	// trio + the per-cluster creds router (V2-cleanup-60.4 / review M1):
	// ReinitializeFromConnection hot-swaps providers while request handlers
	// read them concurrently, so ALL reads go through the credProvider() /
	// addonSecretCfg() / clusterTestCfg() / credsRouter() accessors and all
	// writes through publishProviders(). Never add a direct field back.
	providerState atomic.Pointer[providerSet]
	repoPaths     orchestrator.RepoPathsConfig
	// gitopsState is the RACE-SAFE publication point for the GitOps config
	// (GF2): the V3 C1 always-on 60s settings-reclaim goroutine calls
	// ReinitializeFromConnection, which hot-swaps gitops config fields while
	// request handlers read them concurrently. ALL reads go through
	// gitopsConfig() accessor, all writes through publishGitopsCfg().
	// Never add a direct field back.
	gitopsState atomic.Pointer[orchestrator.GitOpsConfig]
	gitMu       sync.Mutex // shared mutex serializing all Git operations across requests

	// Demo-only addon-secret definitions (set via SetDemoAddonSecretDefs —
	// demo mode and tests only, never a production boot; the Git catalog is
	// the only production source of addon-secret definitions).
	addonSecretDefs   map[string]orchestrator.AddonSecretDefinition
	addonSecretDefsMu sync.RWMutex // protects addonSecretDefs from concurrent read/write
	secretFetcher     orchestrator.SecretValueFetcher

	// Default addons (optional — set via SetDefaultAddons).
	defaultAddons map[string]bool

	// Audit log for external-change events (always available — initialised in NewServer).
	auditLog *audit.Log

	// connCredChecks holds each managed cluster's last read-only
	// credential-check outcome (W3-3 — see connection_credential_check.go).
	// Written by the background check loop AND by the manual
	// connection-comparison endpoint, read by the fleet rows. Always
	// available — initialised in NewServer; its getter is nil-safe anyway.
	connCredChecks *connectionCredentialCheckStore

	// readCache is the shared TTL cache backing the six hot fleet-wide
	// reads (perf M1 — see internal/readcache's package doc): dashboard
	// stats, clusters list, fleet status, catalog list, version matrix,
	// observability overview. Always available — initialised in
	// NewServer, which also wires the SAME instance into clusterSvc,
	// addonSvc, dashboardSvc, and observabilitySvc via their SetCache
	// setters, so InvalidateReadCache() (called from auditMiddleware on
	// every successful mutating request, and explicitly from the PR
	// tracker's merge callback in cmd/sharko/serve.go) clears every
	// service's cache in one call.
	readCache *readcache.Cache

	// Notification store (always available — initialised in NewServer as
	// in-memory only; upgraded to ConfigMap-backed persistence via
	// SetNotificationCMStore once the in-cluster k8s client is ready).
	notificationStore *notifications.Store

	// Change-log store: durable, capped (100/cluster) record of completed
	// cluster changes, populated from the PR tracker's merge/close
	// transition (V2-cleanup-84.1). Always available — initialised in
	// NewServer as in-memory only; upgraded to ConfigMap-backed
	// persistence via SetChangeLogCMStore once the in-cluster k8s client
	// is ready. Mirrors notificationStore's lifecycle exactly.
	changeLogStore *changelog.Store

	// Operation store for async long-running operations (always available — initialised in NewServer).
	opsStore *operations.Store

	// Template filesystem for POST /api/v1/init (always available).
	templateFS fs.FS

	// secretReconciler reconciles addon secrets across remote clusters (optional — set via SetSecretReconciler).
	secretReconciler SecretReconciler

	// demoRemoteClusterClientFn replaces the "fetch credentials, build a
	// throwaway client" step of the live-Secret read
	// (internal/api/secret_resource.go) for demo mode ONLY — set via
	// SetDemoRemoteClusterClient, nil everywhere else. Demo mode has no
	// real clusters, and its fake kubeconfigs point at addresses nothing
	// answers, so the real path would just sit there until the client's
	// 30-second timeout. Deliberately scoped to that one read handler: it
	// is not a general remote-client override, and no write path consults
	// it.
	demoRemoteClusterClientFn func(ctx context.Context, cluster string) (kubernetes.Interface, error)

	// ArgoCD cluster secret manager (optional — set via SetArgoSecretManager).
	// The Manager is a pure writer for the kubeconfig direct-write path
	// (adopt, remove, providers, API handlers). The legacy Reconciler loop
	// was retired in Operator Phase 0; internal/clusterreconciler is the
	// canonical reconciler for managed-clusters.yaml.
	argoSecretManager *argosecrets.Manager

	// prTracker tracks PRs created by Sharko operations (optional — set via SetPRTracker).
	prTracker *prtracker.Tracker

	// reconcilerTrigger nudges the cluster Secret reconciler after the
	// orchestrator commits a managed-clusters.yaml change. Optional —
	// set via SetReconcilerTrigger. Each per-request orchestrator
	// inherits this trigger via attachPRTracker. nil disables — the
	// reconciler still converges on its periodic safety-net tick.
	reconcilerTrigger func()

	// reconcilerCheckTrigger asks the cluster Secret reconciler for a
	// READ-ONLY check pass — read git, read the live secrets, compare,
	// write down what was found, change nothing (P1-A A2). This is what
	// POST /clusters/{name}/reconcile fires, so the page's Refresh button
	// and its help text finally mean the same thing. Optional — set via
	// SetReconcilerCheckTrigger; nil means no reconciler is running on this
	// server and the handler answers 503.
	//
	// Deliberately separate from reconcilerTrigger above: that one is the
	// WRITE nudge the orchestrator fires after a merge, and it must keep
	// writing.
	reconcilerCheckTrigger func()

	// clusterRecon is the canonical cluster-secret reconciler (optional —
	// set via SetClusterReconciler). Used read-only by the cluster read
	// model to project each cluster's last reconcile outcome
	// (V2-cleanup-89.4) and by handleReconcileCluster to confirm a
	// reconciler is actually running before triggering it. nil in
	// deployment modes where the reconciler isn't wired (out-of-cluster, no
	// credentials provider) — callers must nil-check.
	clusterRecon *clusterreconciler.Reconciler

	// obsStore persists cluster connectivity observations (optional — set via SetObservationsStore).
	obsStore *observations.Store

	// appSetReader is the READ-ONLY view of the ArgoCD ApplicationSets in
	// the argocd namespace, used by the brownfield-takeover checks (v4
	// Wave 2, Epic 6) to answer "would anything delete running workloads
	// if this cluster's labels changed?". Optional — nil out-of-cluster,
	// or when the install has not been given read access to
	// ApplicationSets. Every caller nil-checks and reports "I could not
	// check" rather than "all clear".
	appSetReader appsets.Reader

	// appSetManager is the same view PLUS the narrow write surface the
	// v3 → v4 runtime handoff needs (internal/appsets/handoff.go): make an
	// old ApplicationSet safe to strand, take the delete-everything marker
	// off the Applications it generated, and later retire it. Optional in
	// exactly the same way appSetReader is — but its absence has teeth:
	// the migration REFUSES on any repo with clusters registered rather
	// than open a pull request that would uninstall the fleet.
	appSetManager appsets.Manager

	// settingsStore persists server-wide settings such as probe_mode
	// (optional — set via SetSettingsStore once the in-cluster K8s client
	// is ready; nil in out-of-cluster / dev mode, in which case probe_mode
	// reads as its default "check-app" and writes are rejected with 503).
	settingsStore *settings.Store

	// eventRecorder emits Kubernetes events for Sharko operational failures
	// and successes (V3 E1). Optional — set via SetEventRecorder once the
	// in-cluster K8s client is ready; nil in out-of-cluster / dev mode,
	// in which case all Event() calls are no-ops.
	eventRecorder *events.EventRecorder

	// awsDetector / hubPlatformDetector back GET /system/capabilities
	// (V2-cleanup-88.1). Both lazily built on first use (see
	// getAWSDetector / getHubPlatformDetector in capabilities.go) and
	// internally cache their own result — see internal/capabilities.
	awsDetectorOnce         sync.Once
	awsDetector             *capabilities.AWSDetector
	hubPlatformDetectorOnce sync.Once
	hubPlatformDetector     *capabilities.HubPlatformDetector

	// doctorAssumeRoleFn backs the connection doctor's "cross-account role
	// assumable" check (V2-cleanup-88.4). Lazily built (see
	// getDoctorAssumeRoleFn in clusters_doctor.go) — same rationale as
	// awsDetector/hubPlatformDetector: Server literals built directly by
	// table-driven tests still work, and tests in this package may pre-set
	// the field directly to inject a fake AssumeRole attempt.
	doctorAssumeRoleOnce sync.Once
	doctorAssumeRoleFn   func(ctx context.Context, roleARN, region string) error

	// doctorK8sClientFn backs the connection doctor's "cluster accepts the
	// token" check (V2-cleanup-88.4) — the same
	// remoteclient.NewClientFromKubeconfig call the Test handler makes.
	// Lazily built and test-injectable for the exact same reason as
	// doctorAssumeRoleFn: it lets tests substitute a fake kubernetes.Interface
	// (e.g. client-go's fake clientset) to exercise verify.Stage1's pass path
	// deterministically, with no real network I/O.
	doctorK8sClientOnce sync.Once
	doctorK8sClientFn   func(kubeconfig []byte) (kubernetes.Interface, error)

	// doctorAddonSecretProviderFn backs the connection doctor's
	// "addon-secret paths readable" check (V2-cleanup-88.4) — the same
	// providers.NewAddonSecretProvider factory every addon-secret consumer
	// in this codebase is built from. Lazily built and test-injectable for
	// the same reason as the other doctor seams: it lets tests substitute a
	// fake providers.SecretProvider without depending on ambient (and
	// CI-environment-dependent) cluster/cloud credentials.
	doctorAddonSecretProviderOnce sync.Once
	doctorAddonSecretProviderFn   func(cfg providers.AddonSecretProviderConfig) (providers.SecretProvider, error)

	// authDisabledForTests reproduces the historical "zero users = open"
	// router behavior for this package's unit tests ONLY (they drive
	// handlers through the full router without logging in). Unexported on
	// purpose: the only writers live in _test.go files, so no production
	// build can ever set it. Even when set, adding a user re-arms auth —
	// basicAuthMiddleware checks HasUsers() on every request.
	authDisabledForTests bool

	// startTime records when the server was created (used for uptime reporting).
	startTime time.Time

	// version is set at startup via SetVersion and reflects the ldflags-injected build version.
	version string

	// catalog holds the curated addon catalog parsed from the embedded YAML
	// at server startup (see internal/catalog). Optional — handlers that
	// depend on it return 503 when nil.
	catalog *catalog.Catalog

	// catalogSources holds the parsed SHARKO_CATALOG_URLS config. Empty
	// Sources → embedded-only mode. The fetcher reads this via
	// CatalogSources().
	catalogSources *config.CatalogSourcesConfig

	// sourcesFetcher periodically pulls third-party catalog URLs. Nil
	// when no catalog sources are configured (embedded-only mode). The
	// merge layer reads snapshots from this via SourcesFetcher().
	sourcesFetcher *sources.Fetcher

	// freshness is the v4 wave 1 Story 3.4 background scheduler that keeps
	// a durable "last checked" snapshot of chart versions (per curated
	// addon) and the engine pin check, independent of who's browsing. Nil
	// is tolerated everywhere it's read — handlers fall back to their
	// pre-Story-3.4 on-demand behavior when it's unset (tests, or a build
	// that never wires one).
	freshness *catalog.FreshnessScheduler
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

	// Bootstrap admin credential handling:
	//
	//   1. Operator-supplied path — if SHARKO_BOOTSTRAP_ADMIN_PASSWORD is
	//      set (via Helm `bootstrapAdmin.password` or `existingSecret`),
	//      seed admin.password from it. The plaintext is NEVER logged.
	//   2. Auto-generated path — if no operator value was supplied, the
	//      Helm chart wrote `admin.initialPassword` to the Sharko Secret
	//      on first install. Log it once in a clearly-marked block so
	//      operators can grep `kubectl logs` instead of needing
	//      out-of-band knowledge of `sharko reset-admin`.
	//
	// Order matters: seed first so MaybeLogBootstrapCredential does not
	// log a stale auto-generated value when the operator has supplied one.
	if err := authStore.SeedBootstrapAdminFromEnv(); err != nil {
		slog.Warn("could not apply operator-supplied bootstrap admin password", "error", err)
	}
	authStore.MaybeLogBootstrapCredential()

	if !authStore.HasUsers() {
		// Auth is never open anymore: with zero users every request is
		// refused (fail closed). Before the router starts serving,
		// cmd/sharko/serve.go either seeds demo users (demo mode) or
		// calls EnsureInitialAdmin, which creates an admin with a
		// random password. This is just a startup breadcrumb.
		slog.Info("no users configured yet — an initial admin will be created before the server starts serving (demo mode seeds its own users)")
	}

	// perf M1: one shared read cache, threaded into every service that
	// reads or invalidates it. See the readCache field doc.
	readCache := readcache.New(readcache.DefaultTTL)
	clusterSvc.SetCache(readCache)
	addonSvc.SetCache(readCache)
	dashboardSvc.SetCache(readCache)
	observabilitySvc.SetCache(readCache)

	// One audit log, shared by the request middleware and the
	// credential-check store's transition-only entries (W3-3).
	auditLog := audit.NewLog(1000)

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
		auditLog:          auditLog,
		connCredChecks:    newConnectionCredentialCheckStore(auditLog.Add),
		notificationStore: notifications.NewStore(100, nil),
		changeLogStore:    changelog.NewStore(changelog.DefaultMaxPerCluster, nil),
		opsStore:          operations.NewStore(),
		startTime:         time.Now(),
		readCache:         readCache,
	}
}

// InvalidateReadCache drops every entry in the shared read cache (perf
// M1). Called from auditMiddleware after any successful mutating request,
// and explicitly from the PR tracker's merge callback in
// cmd/sharko/serve.go (a background-goroutine trigger that never goes
// through the HTTP middleware). Safe to call liberally — a cache miss just
// means the next read recomputes instead of hitting a stale-forever entry.
func (s *Server) InvalidateReadCache() {
	s.readCache.InvalidateAll()
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

// SetDemoRemoteClusterClient replaces the credentials-fetch + client-build
// step of the live-Secret read (internal/api/secret_resource.go) for demo
// mode. Demo mode has no real clusters; without this the read would dial a
// fake address and block until the client's 30-second timeout. Nothing
// outside internal/demo calls this, and no write path consults the field.
func (s *Server) SetDemoRemoteClusterClient(fn func(ctx context.Context, cluster string) (kubernetes.Interface, error)) {
	s.demoRemoteClusterClientFn = fn
}

// SetArgoSecretManager stores the ArgoCD secrets Manager for use by downstream handlers.
func (s *Server) SetArgoSecretManager(m *argosecrets.Manager) {
	s.argoSecretManager = m
}

// ArgoSecretManager returns the ArgoCD secrets Manager (may be nil if not configured).
func (s *Server) ArgoSecretManager() *argosecrets.Manager {
	return s.argoSecretManager
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
//
// credProvider is the cluster-test backend (the ClusterCredentialsProvider used
// to verify connectivity to managed clusters — argocd-only).
//
// addonSecretCfg + clusterTestCfg are the two typed config blocks for
// the provider surface. Either or both may be nil if no provider was
// successfully constructed at startup. Handlers that need RoleARN /
// Region / Prefix (default IAM role, AWS region, secret-name prefix)
// read from addonSecretCfg with a nil-guard.
//
// paths and gitops hold the repo layout and gitops commit settings.
func (s *Server) SetWriteAPIDeps(
	credProvider providers.ClusterCredentialsProvider,
	addonSecretCfg *providers.AddonSecretProviderConfig,
	clusterTestCfg *providers.ClusterTestProviderConfig,
	paths orchestrator.RepoPathsConfig,
	gitops orchestrator.GitOpsConfig,
) {
	s.publishProviders(credProvider, addonSecretCfg, clusterTestCfg)
	s.repoPaths = paths
	s.publishGitopsCfg(gitops)
}

// SetDemoAddonSecretDefs seeds addon secret definitions for DEMO MODE AND
// TESTS ONLY (task #152, story 152.A). There is no HTTP door into this map
// any more — the POST/DELETE /addon-secrets endpoints are gone — and no
// production code path calls this: a real boot's addon-secret definitions
// live exclusively in the Git catalog, read by internal/secrets.Reconciler.
// The map only decorates demo-mode read surfaces (the Managed Secrets
// page's rows, the blanked resource view). Never wire this into
// cmd/sharko/serve.go.
func (s *Server) SetDemoAddonSecretDefs(defs map[string]orchestrator.AddonSecretDefinition) {
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

// SetSettingsStore wires in the server-wide settings store (probe_mode,
// V2-cleanup-85.4). Call this after NewServer, before starting the HTTP
// listener.
func (s *Server) SetSettingsStore(store *settings.Store) {
	s.settingsStore = store
}

// SetEventRecorder wires in the Kubernetes event recorder for operational
// events (V3 E1). Call this after NewServer, before starting the HTTP listener.
// Optional — when nil (out-of-cluster / dev mode), all Event() calls are no-ops.
func (s *Server) SetEventRecorder(recorder *events.EventRecorder) {
	s.eventRecorder = recorder
}

// EventRecorder returns the current event recorder (may be nil if not in-cluster).
func (s *Server) EventRecorder() *events.EventRecorder {
	return s.eventRecorder
}

// emitWarning records one k8s Warning event, nil-safe (V3 E1). reason must be
// a stable events.Reason* constant; message must be plain-English with NO
// secret material (no tokens, kubeconfigs, credentials, secret values, or AWS
// account ids). Safe to call when the recorder is nil (out-of-cluster / dev
// mode) — *events.EventRecorder.Event is itself nil-receiver-safe.
func (s *Server) emitWarning(reason, message string) {
	s.eventRecorder.Event(reason, message, events.EventTypeWarning)
}

// SetPRTracker wires in the PR tracker for polling and API access.
func (s *Server) SetPRTracker(tracker *prtracker.Tracker) {
	s.prTracker = tracker
}

// PRTracker returns the current PR tracker (may be nil if not configured).
func (s *Server) PRTracker() *prtracker.Tracker {
	return s.prTracker
}

// SetReconcilerTrigger wires in the cluster Secret reconciler's Trigger()
// nudge so every per-request orchestrator built by attachPRTracker can
// request an immediate post-PR reconcile. Optional — when nil the
// reconciler still converges on its periodic safety-net tick (30s).
//
// Call this once at startup AFTER constructing the reconciler, BEFORE
// the HTTP listener starts. Idempotent — passing nil clears the hook.
func (s *Server) SetReconcilerTrigger(fn func()) {
	s.reconcilerTrigger = fn
}

// ReconcilerTrigger returns the currently-wired reconciler trigger (may be
// nil if no reconciler is configured). Exposed for tests + harness wiring
// so the e2e suite can assert the trigger seam is present without going
// through a real orchestrator request.
func (s *Server) ReconcilerTrigger() func() {
	return s.reconcilerTrigger
}

// SetReconcilerCheckTrigger wires in the cluster Secret reconciler's
// TriggerCheck() nudge — the READ-ONLY pass POST /clusters/{name}/reconcile
// fires (P1-A A2). Call once at startup alongside SetReconcilerTrigger.
// Optional — when nil that endpoint answers 503, because there is no
// reconciler on this server to ask.
func (s *Server) SetReconcilerCheckTrigger(fn func()) {
	s.reconcilerCheckTrigger = fn
}

// ReconcilerCheckTrigger returns the currently-wired check trigger (may be
// nil). Exposed for tests + harness wiring, same as ReconcilerTrigger.
func (s *Server) ReconcilerCheckTrigger() func() {
	return s.reconcilerCheckTrigger
}

// SetClusterReconciler wires in the canonical cluster-secret reconciler
// (V2-cleanup-89.4) so the cluster read model can project each cluster's
// last reconcile outcome and handleReconcileCluster can tell a real
// reconciler apart from "not running in this deployment mode". Call once
// at startup alongside SetReconcilerTrigger. Optional — nil is a valid,
// commonly-hit state (out-of-cluster / no credentials provider) and every
// reader of s.clusterRecon must nil-check.
func (s *Server) SetClusterReconciler(r *clusterreconciler.Reconciler) {
	s.clusterRecon = r
}

// ClusterReconciler returns the currently-wired cluster-secret reconciler
// (may be nil). Exposed for tests + harness wiring.
func (s *Server) ClusterReconciler() *clusterreconciler.Reconciler {
	return s.clusterRecon
}

// SetApplicationSetReader wires the read-only ApplicationSet view the
// brownfield-takeover checks use (v4 Wave 2, Epic 6). Call once at startup
// when running in-cluster. Optional: leaving it nil is a valid state and
// every caller reports "Sharko could not check" instead of claiming the
// ApplicationSets are fine.
func (s *Server) SetApplicationSetReader(r appsets.Reader) {
	s.appSetReader = r
	// The dynamic-client implementation covers the handoff writes too. A
	// caller that hands us a read-only fake simply leaves the manager nil,
	// which is a valid state — the migration then refuses on a live fleet
	// instead of silently skipping the runtime handoff.
	if m, ok := r.(appsets.Manager); ok {
		s.appSetManager = m
	}
}

// SetApplicationSetManager wires the ApplicationSet read+write surface the
// v3 → v4 runtime handoff needs. Normally set for free by
// SetApplicationSetReader; this exists so a test can supply a writer
// without pretending to be the whole reader wiring.
func (s *Server) SetApplicationSetManager(m appsets.Manager) {
	s.appSetManager = m
	if m != nil && s.appSetReader == nil {
		s.appSetReader = m
	}
}

// ReinitializeFromConnection reads provider config and GitOps settings from the active connection
// and rebuilds credProvider + providerCfg + gitopsCfg. Called after connection create/update/set-active
// so that write-API operations pick up the new settings immediately without a restart.
// Also called at startup so that a pod restart does not leave the provider nil.
//
// HOT-RELOAD CONSTRAINT (V2-cleanup-53.1): this is the seam that keeps the
// registration/cluster-test credProvider tracking the ACTIVE connection
// without a pod restart. Register/test handlers build a per-request
// orchestrator from s.credProvider(), so swapping it here is sufficient — but
// the fan-through below MUST route every supported provider type (via the
// shared providers.ClusterTestConfigFromConnection mapper). Dropping a type
// here silently reverts registrations to the in-cluster ArgoCD auto-default
// until the next restart — exactly the live bug this story fixed.
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

	// Reinit cluster-test provider from connection.
	//
	// We ALWAYS call the cluster-test factory when there is an active
	// connection so the auto-default path (in-cluster + empty type →
	// ArgoCDProvider) can fire. NewClusterTestProvider itself returns
	// the "no provider configured" error out-of-cluster — that path
	// leaves credProvider unchanged (logged at info) and the existing
	// no_secrets_backend surface still applies in the Test handler.
	//
	// The connection-level Provider block fans out into TWO typed
	// configs here: AddonSecretProviderConfig
	// (Type/Region/Prefix/Namespace/RoleARN for addon-secret backends)
	// and ClusterTestProviderConfig (Type/ArgoCDNamespace for
	// cluster-test). Both are stashed on the Server so /providers and
	// the orchestrator handlers can read the right slice.
	pc := conn.Provider
	addonPc := conn.AddonSecretProvider
	{
		namespace := os.Getenv("SHARKO_NAMESPACE")
		if namespace == "" {
			namespace = "sharko"
		}

		// Unpack the two provider blocks into raw fields for the shared resolvers.
		// V3-P1.1: addon-secret backend resolution goes through
		// AddonSecretConfigFromConnection so boot and hot-reload can never
		// drift (mirrors the V2-cleanup-53.1 cluster-test mapper discipline).
		var ccType, ccRegion, ccPrefix, ccNamespace, ccRoleARN string
		if pc != nil {
			ccType, ccRegion, ccPrefix, ccNamespace, ccRoleARN = pc.Type, pc.Region, pc.Prefix, pc.Namespace, pc.RoleARN
		}
		var asType, asRegion, asPrefix, asNamespace, asRoleARN string
		if addonPc != nil {
			asType, asRegion, asPrefix, asNamespace, asRoleARN = addonPc.Type, addonPc.Region, addonPc.Prefix, addonPc.Namespace, addonPc.RoleARN
		}

		addonCfg := providers.AddonSecretConfigFromConnection(
			ccType, ccRegion, ccPrefix, ccNamespace, ccRoleARN,
			asType, asRegion, asPrefix, asNamespace, asRoleARN,
			namespace,
		)

		// Cluster-test fan-through goes through the SINGLE shared
		// mapper (providers.ClusterTestConfigFromConnection) — the
		// same one serve.go uses at boot, so boot and hot-reload
		// wiring can never drift (V2-cleanup-53.1). aws-sm and
		// k8s-secrets now fan through to their restored cluster-creds
		// arms; gcp-sm/azure-kv and unknown types still fall to the
		// auto-default. The V125-1-10.8 guard is preserved inside the
		// mapper: pc.Namespace (addon-secrets-shaped) is NEVER copied
		// into ArgoCDNamespace — empty ArgoCDNamespace lets the
		// canonical resolveArgoCDNamespaceTyped precedence take over:
		//   1. cfg.ArgoCDNamespace (empty here, by design)
		//   2. SHARKO_ARGOCD_NAMESPACE env (deprecated compat alias)
		//   3. "argocd" hardcoded default
		// For k8s-secrets, namespace (SHARKO_NAMESPACE default,
		// overridden by pc.Namespace) flows into the DISTINCT
		// Namespace field, matching the addon-side convention.
		testCfg := providers.ClusterTestProviderConfig{}
		if pc != nil {
			testCfg = providers.ClusterTestConfigFromConnection(
				ccType, ccRegion, ccPrefix, ccNamespace, ccRoleARN)
			if ccType != "" {
				slog.Info("[startup] initializing provider", "type", ccType, "region", ccRegion)
			} else {
				slog.Info("[startup] no explicit provider type — cluster-test will auto-default")
			}
		} else {
			slog.Info("[startup] no provider config in connection — cluster-test will auto-default")
		}

		p, err := providers.NewClusterTestProvider(testCfg)
		if err != nil {
			slog.Info("[startup] no credentials provider configured", "reason", err)
		} else {
			slog.Info("[startup] credentials provider constructed", "type", testCfg.Type)
		}

		// Race-safe publish (M1): handlers read the provider trio
		// concurrently with this hot-reload; the atomic snapshot swap
		// replaces the old plain field assignments.
		//
		// V3-P1.1: publish the configs (addonSecretCfg, clusterTestCfg)
		// even when the cluster-creds provider failed to construct
		// (p == nil), since addon-secret backend is independent of
		// cluster-creds backend. The credProvider slot can be nil
		// (handlers already surface structured 503 for nil credProvider).
		s.publishProviders(p, &addonCfg, &testCfg)
		slog.Info("[startup] provider reinitialized from connection", "addon_type", addonCfg.Type, "addon_region", addonCfg.Region, "addon_prefix", addonCfg.Prefix)
	}

	// Reinit GitOps config from connection (GF2 race-safe: load, mutate local copy, publish once).
	cfg := s.gitopsConfig()
	if gitops := conn.GitOps; gitops != nil {
		if gitops.BaseBranch != "" {
			cfg.BaseBranch = gitops.BaseBranch
		}
		if gitops.BranchPrefix != "" {
			cfg.BranchPrefix = gitops.BranchPrefix
		}
		if gitops.CommitPrefix != "" {
			cfg.CommitPrefix = gitops.CommitPrefix
		}
		if gitops.PRAutoMerge != nil {
			cfg.PRAutoMerge = *gitops.PRAutoMerge
		}
		slog.Info("gitops config reinitialized from connection",
			"base_branch", cfg.BaseBranch,
			"branch_prefix", cfg.BranchPrefix,
			"pr_auto_merge", cfg.PRAutoMerge,
		)
	}

	// Populate RepoURL from Git connection if not already set.
	if cfg.RepoURL == "" && conn.Git.RepoURL != "" {
		cfg.RepoURL = conn.Git.RepoURL
	}
	s.publishGitopsCfg(cfg)

	// Reinit default addons from git (V3-P2.1a hot-reload).
	// Reuses the same read+fallback logic boot and GET handler use.
	addons, err := s.ReadDefaultAddons(context.Background())
	if err != nil {
		slog.Info("failed to read default addons during hot-reload", "error", err)
	} else if len(addons) > 0 {
		defaults := make(map[string]bool, len(addons))
		for _, name := range addons {
			defaults[name] = true
		}
		s.SetDefaultAddons(defaults)
		slog.Info("default addons reinitialized", "count", len(addons))
	}

}

// NotificationStore returns the server's notification store so external
// components (e.g. the background Checker) can push notifications into it.
func (s *Server) NotificationStore() *notifications.Store {
	return s.notificationStore
}

// SetNotificationCMStore upgrades the notification store from in-memory-only
// to ConfigMap-backed persistence. Call this once at startup, after the
// in-cluster k8s client used for the PR tracker's cmstore is available (see
// cmd/sharko/serve.go) — the notification store itself is always constructed
// eagerly in NewServer, before that client exists. No-op if cmStore is nil
// (e.g. out-of-cluster/local dev, where the store stays in-memory only).
func (s *Server) SetNotificationCMStore(ctx context.Context, cmStore *cmstore.Store) error {
	return s.notificationStore.AttachCMStore(ctx, cmStore)
}

// ChangeLogStore returns the server's change-log store so external
// components (the PR tracker's completion hook, wired in
// cmd/sharko/serve.go) can record completed changes into it.
func (s *Server) ChangeLogStore() *changelog.Store {
	return s.changeLogStore
}

// SetChangeLogCMStore upgrades the change-log store from in-memory-only to
// ConfigMap-backed persistence. Call this once at startup, after the
// in-cluster k8s client used for the PR tracker's cmstore is available
// (see cmd/sharko/serve.go) — the change-log store itself is always
// constructed eagerly in NewServer, before that client exists. No-op if
// cmStore is nil (e.g. out-of-cluster/local dev, where the store stays
// in-memory only). Mirrors SetNotificationCMStore.
func (s *Server) SetChangeLogCMStore(ctx context.Context, cmStore *cmstore.Store) error {
	return s.changeLogStore.AttachCMStore(ctx, cmStore)
}

// AuditLog returns the server's audit log so external components can record
// events (e.g. the secret reconciler after a reconcile cycle).
func (s *Server) AuditLog() *audit.Log {
	return s.auditLog
}

// ConnectionService returns the server's current connection service. Exists
// so callers that must resolve the connection service LATE — after demo mode
// may have swapped it via SetDemoConnectionService — can read the live value
// instead of capturing a pointer up front (S3 demo wiring fix: notification
// checker + connection poller in cmd/sharko/serve.go now read through this
// getter instead of closing over the pre-demo local variable).
func (s *Server) ConnectionService() *service.ConnectionService {
	return s.connSvc
}

// SetDemoConnectionService replaces the server's connection service with one
// backed by the provided in-memory store. Used by demo mode only.
//
// Also repoints dashboardSvc at the new connection service: DashboardService
// bakes connSvc in at construction (cmd/sharko/serve.go, well before demo
// setup runs), so without this the dashboard's connection-totals stat kept
// reading the real store even in demo mode (S3 demo wiring gap).
func (s *Server) SetDemoConnectionService(store config.Store) {
	s.connSvc = service.NewConnectionService(store)
	if s.dashboardSvc != nil {
		s.dashboardSvc.SetConnectionService(s.connSvc)
	}
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

// EnsureInitialAdmin makes sure at least one user account exists before the
// router starts serving. When the auth store has zero users (no chart-seeded
// accounts, no SHARKO_BOOTSTRAP_ADMIN_PASSWORD, no SHARKO_AUTH_USER), it
// generates an admin with a random password and stores it where the operator
// can read it — the sharko-initial-admin-secret Secret in-cluster, or a
// 0600 local file outside a cluster. Existing users are never touched.
//
// cmd/sharko/serve.go calls this on the non-demo serve path, after demo
// seeding would have happened and before api.NewRouter. Auth is enforced
// either way: a start with zero users no longer runs open (see
// basicAuthMiddleware), so a failure here should abort startup — a server
// nobody can log in to helps no one.
func (s *Server) EnsureInitialAdmin(ctx context.Context) error {
	_, err := s.authStore.EnsureInitialAdmin(ctx)
	return err
}

// LoadPersistedAPITokens loads API tokens persisted across restarts (the
// sharko-api-tokens Secret in-cluster, a 0600 local file outside a cluster)
// and turns on write-through persistence for create/renew/revoke.
//
// cmd/sharko/serve.go calls this on the non-demo serve path, right after
// EnsureInitialAdmin. A failure here should abort startup: starting with an
// empty token set and then writing through would clobber the durable copy
// and silently lose every existing machine token — the restart-recovery bug
// (NFR12) persistence exists to fix. Demo mode skips this and keeps tokens
// in-memory only.
func (s *Server) LoadPersistedAPITokens(ctx context.Context) error {
	return s.authStore.InitTokenPersistence(ctx)
}

// SetAWSDetector overrides the AWS identity detector GET
// /system/capabilities serves, bypassing the lazy real-detector build in
// getAWSDetector (capabilities.go) entirely. Exists for demo/test
// injection: demo mode calls this with capabilities.NewStaticAWSDetector so
// the demo instance never runs a real sts:GetCallerIdentity against
// whatever AWS identity happens to be ambient on the host it's running on.
// Call this after NewServer, before the first request reaches the
// capabilities endpoint.
func (s *Server) SetAWSDetector(d *capabilities.AWSDetector) {
	s.awsDetector = d
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

	// Prometheus metrics (no auth — protected via ingress or separate port).
	// Composes the default registry (legacy promauto metrics) with the
	// V2-3 SLO-surface custom registry — see internal/api/metrics.go.
	mux.Handle("GET /metrics", metricsHandler())

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

	// Default addons (GitOps-native)
	mux.HandleFunc("GET /api/v1/default-addons", srv.handleGetDefaultAddons)
	mux.HandleFunc("PUT /api/v1/default-addons", srv.handlePutDefaultAddons)

	// Clusters — batch and adoption operations (registered before {name} wildcard routes)
	mux.HandleFunc("POST /api/v1/clusters/batch", srv.handleBatchRegisterClusters)
	mux.HandleFunc("POST /api/v1/clusters/adopt", srv.handleAdoptClusters)
	mux.HandleFunc("GET /api/v1/clusters/available", srv.handleDiscoverClusters)

	// Clusters (read)
	mux.HandleFunc("GET /api/v1/clusters", srv.handleListClusters)
	mux.HandleFunc("GET /api/v1/clusters/{name}/values", srv.handleGetClusterValues)
	mux.HandleFunc("GET /api/v1/clusters/{name}/config-diff", srv.handleGetConfigDiff)
	mux.HandleFunc("GET /api/v1/clusters/{name}/comparison", srv.handleGetClusterComparison)
	// "Does this cluster's ArgoCD connection look the way Sharko means it
	// to look?" — read-only, writes nothing, and a separate route from
	// /comparison above on purpose: that one returns an untyped map, and
	// widening it would put new keys in front of its existing callers with
	// nothing type-checking them. See connection_comparison.go's header for
	// why the request carries a cluster name and nothing else.
	mux.HandleFunc("GET /api/v1/clusters/{name}/connection-comparison", srv.handleGetConnectionComparison)
	// The reconciliation contract for a connection: desired vs applied
	// revision, honest sync + verification scope, ArgoCD health, conditions,
	// grouped drift and the plan. Same read-only core as /connection-comparison
	// (which stays byte-for-byte unchanged for its current clients); see
	// connection_reconciliation.go's header.
	mux.HandleFunc("GET /api/v1/clusters/{name}/connection-reconciliation", srv.handleGetConnectionReconciliation)
	mux.HandleFunc("GET /api/v1/clusters/{name}/history", srv.handleGetClusterHistory)
	mux.HandleFunc("GET /api/v1/clusters/{name}/changes", srv.handleGetClusterChanges)
	mux.HandleFunc("GET /api/v1/clusters/{name}", srv.handleGetCluster)

	// Clusters (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/clusters", srv.handleRegisterCluster)
	mux.HandleFunc("DELETE /api/v1/clusters/{name}", srv.handleDeregisterCluster)
	mux.HandleFunc("PATCH /api/v1/clusters/{name}", srv.handleUpdateClusterAddons)
	mux.HandleFunc("POST /api/v1/clusters/{name}/refresh", srv.handleRefreshClusterCredentials)
	mux.HandleFunc("POST /api/v1/clusters/{name}/reconcile", srv.handleReconcileCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/resync", srv.handleResyncCluster)
	// "Make this cluster's ArgoCD connection match the Git-defined connection."
	// A separate route from /resync above on purpose: that one re-applies
	// addon labels and is unchanged by this step, while this one can
	// rewrite the whole connection and is admin-gated for that reason.
	// See connection_repair.go's header.
	mux.HandleFunc("POST /api/v1/clusters/{name}/connection-repair", srv.handleRepairConnection)
	mux.HandleFunc("POST /api/v1/clusters/{name}/test", srv.handleTestCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/diagnose", srv.handleDiagnoseCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/doctor", srv.handleDoctorCluster)
	mux.HandleFunc("POST /api/v1/clusters/{name}/unadopt", srv.handleUnadoptCluster)

	// Brownfield takeover (v4 Wave 2, Epic 6). The two GETs write nothing
	// and are safe to re-run; the two POSTs both refuse without an
	// explicit confirmation in the body.
	mux.HandleFunc("GET /api/v1/clusters/{name}/takeover/preflight", srv.handleClusterTakeoverPreflight)
	mux.HandleFunc("POST /api/v1/clusters/{name}/takeover", srv.handleClusterTakeover)
	mux.HandleFunc("POST /api/v1/clusters/{name}/takeover/legacy-labels/drop", srv.handleClusterDropLegacyLabels)
	mux.HandleFunc("GET /api/v1/clusters/{name}/unregister/consequences", srv.handleClusterUnregisterConsequences)
	mux.HandleFunc("POST /api/v1/clusters/{name}/addons/{addon}", srv.handleEnableAddon)
	mux.HandleFunc("DELETE /api/v1/clusters/{name}/addons/{addon}", srv.handleDisableAddon)
	mux.HandleFunc("POST /api/v1/clusters/{name}/addons/{addon}/restart-sync", srv.handleRestartAddonSync)
	// Single-item addon-values-secret row actions (S4 — Managed Secrets
	// page). Refresh re-checks against the vault without writing; Sync
	// re-pushes. Both are scoped to exactly this one secret, never a
	// fleet-wide pass — see internal/secrets.Reconciler.CheckOne/SyncOne.
	mux.HandleFunc("POST /api/v1/clusters/{name}/addons/{addon}/secret/refresh", srv.handleRefreshAddonValuesSecret)
	mux.HandleFunc("POST /api/v1/clusters/{name}/addons/{addon}/secret/sync", srv.handleSyncAddonValuesSecret)
	// "Show me the actual Secret on the cluster" — read-only, one live
	// round trip, ON CLICK ONLY. Every value is blanked server-side before
	// the response body is built; see internal/api/secret_resource.go's own
	// header for the cost rule and the blanking point. These must never be
	// called from a list render, a timer, or a fan-out loop.
	mux.HandleFunc("GET /api/v1/clusters/{name}/secret/resource", srv.handleGetConnectionSecretResource)
	mux.HandleFunc("GET /api/v1/clusters/{name}/addons/{addon}/secret/resource", srv.handleGetAddonValuesSecretResource)
	// v4 Wave 1 Story 4.3 — the sharpened enable/disable pipeline for v4
	// (cluster-addons/*.yaml) repos. Distinct routes from the pair above so a v3
	// repo's behavior never changes underfoot (addon_ops_v4.go).
	mux.HandleFunc("POST /api/v1/v4/clusters/{name}/addons/{addon}", srv.handleEnableAddonV4)
	mux.HandleFunc("DELETE /api/v1/v4/clusters/{name}/addons/{addon}", srv.handleDisableAddonV4)
	// Orphan-cluster Secret cleanup. Refuses to delete a cluster that's
	// actually managed (in git) or pending (open register PR) — see
	// clusters_orphan_delete.go for the safety gates.
	mux.HandleFunc("DELETE /api/v1/clusters/{name}/orphan", srv.handleDeleteOrphanCluster)

	// Init (orchestrator-backed)
	mux.HandleFunc("POST /api/v1/init", srv.handleInit)
	// Read-only repo-state probe consumed by the first-run wizard's Step 4
	// before it offers to initialize — empty / initialized / partial.
	mux.HandleFunc("GET /api/v1/init/status", srv.handleInitStatus)

	// Operations (async operation tracking)
	mux.HandleFunc("GET /api/v1/operations/{id}", srv.handleGetOperation)
	mux.HandleFunc("POST /api/v1/operations/{id}/heartbeat", srv.handleOperationHeartbeat)
	mux.HandleFunc("POST /api/v1/operations/{id}/cancel", srv.handleCancelOperation)

	// Addons (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/addons/upgrade-batch", srv.handleUpgradeAddonsBatch)
	mux.HandleFunc("POST /api/v1/addons/{name}/upgrade", srv.handleUpgradeAddon)
	mux.HandleFunc("POST /api/v1/addons/{name}/upgrade-clusters", srv.handleUpgradeAddonClustersV4)
	mux.HandleFunc("POST /api/v1/addons", srv.handleAddAddon)
	mux.HandleFunc("DELETE /api/v1/addons/{name}", srv.handleRemoveAddon)
	mux.HandleFunc("PATCH /api/v1/addons/{name}", srv.handleConfigureAddon)

	// Engine pin (v4 Wave 1 Story 2.5) — check + upgrade-PR for
	// sharko-engine.yaml. Read-only check is Viewer+; opening the
	// upgrade PR is Operator+, same tier as the addon-write endpoints above.
	mux.HandleFunc("GET /api/v1/engine/pin", srv.handleCheckEnginePin)
	mux.HandleFunc("POST /api/v1/engine/pin/upgrade", srv.handleUpgradeEnginePin)

	// Values editor (v1.20) — Tier 2 writes + read-side schema/current-values
	mux.HandleFunc("PUT /api/v1/addons/{name}/values", srv.handleSetAddonValues)
	mux.HandleFunc("GET /api/v1/addons/{name}/values-schema", srv.handleGetAddonValuesSchema)
	mux.HandleFunc("PUT /api/v1/clusters/{cluster}/addons/{name}/values", srv.handleSetClusterAddonValues)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/addons/{name}/values", srv.handleGetClusterAddonValues)

	// Values editor extras:
	//   • Recent merged PRs touching a values file (read)
	//
	// The `POST .../values/pull-upstream` endpoint was removed; its
	// functionality moved to a `refresh_from_upstream: true` flag on the
	// existing `PUT /api/v1/addons/{name}/values` handler.
	mux.HandleFunc("GET /api/v1/addons/{name}/values/recent-prs", srv.handleGetAddonValuesRecentPRs)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/addons/{name}/values/recent-prs", srv.handleGetClusterAddonValuesRecentPRs)

	// Diff-and-merge preview. Tier 1 read — returns a candidate body
	// that the user submits via the existing PUT values endpoint. POST
	// is used for forward-compat (the body will eventually carry a
	// "preview against version X" parameter).
	mux.HandleFunc("POST /api/v1/addons/{name}/values/preview-merge", srv.handlePreviewMergeAddonValues)

	// Manual AI annotate + per-addon opt-out toggle.
	mux.HandleFunc("POST /api/v1/addons/{name}/values/annotate", srv.handleAnnotateAddonValues)
	mux.HandleFunc("PUT /api/v1/addons/{name}/values/ai-opt-out", srv.handleSetAddonAIOptOut)

	// Legacy `<addon>:` wrap migration. One PR per call, covering every
	// wrapped global values file in the repo. Pass `?addon=<name>` to
	// migrate a single file.
	mux.HandleFunc("POST /api/v1/addons/unwrap-globals", srv.handleUnwrapGlobalValues)

	// The /addon-secrets definition-CRUD endpoints are GONE (task #152,
	// story 152.A). They let an API caller drop a whole secret definition
	// — backend path, destination namespace, Secret name, key list — into
	// an in-memory map that the refresh endpoint then delivered, bypassing
	// Git entirely. The Git catalog is the ONLY source of addon-secret
	// definitions now; the refresh endpoint below runs the reconciler's
	// git-backed plan. Do not add these routes back.

	// Cluster secrets (remote cluster operations)
	mux.HandleFunc("GET /api/v1/clusters/{name}/secrets", srv.handleListClusterSecrets)
	mux.HandleFunc("POST /api/v1/clusters/{name}/secrets/refresh", srv.handleRefreshClusterSecrets)

	// Leftover addon-values secrets (leftover-secrets S1) — a values secret
	// the catalog no longer claims, found by the addon-values engine's own
	// scan passes (never at list time), operator-gated delete only.
	mux.HandleFunc("DELETE /api/v1/clusters/{name}/orphaned-secrets/{namespace}/{secret}", srv.handleDeleteOrphanedSecret)

	// Secrets reconciler
	mux.HandleFunc("POST /api/v1/secrets/reconcile", srv.handleTriggerReconcile)
	mux.HandleFunc("POST /api/v1/secrets/check", srv.handleCheckSecrets)
	mux.HandleFunc("GET /api/v1/secrets/status", srv.handleReconcileStatus)

	// Cluster status overview
	mux.HandleFunc("GET /api/v1/fleet/status", srv.handleGetFleetStatus)

	// Repo status
	mux.HandleFunc("GET /api/v1/repo/status", srv.handleRepoStatus)

	// v3 -> v4 migration (v4 Wave 2, Stories 5.1 + 5.2). Status is a
	// read-only probe (Viewer+); preview and migrate are admin — one call
	// rewrites the whole repository.
	mux.HandleFunc("GET /api/v1/migration/status", srv.handleMigrationStatus)
	mux.HandleFunc("POST /api/v1/migration/preview", srv.handleMigrationPreview)
	mux.HandleFunc("POST /api/v1/migration/migrate", srv.handleMigrateRepo)
	mux.HandleFunc("POST /api/v1/migration/complete", srv.handleMigrationComplete)

	// System
	mux.HandleFunc("GET /api/v1/providers", srv.handleGetProviders)
	mux.HandleFunc("POST /api/v1/providers/test", srv.handleTestProvider)
	mux.HandleFunc("POST /api/v1/providers/test-config", srv.handleTestProviderConfig)
	mux.HandleFunc("GET /api/v1/config", srv.handleGetConfig)
	mux.HandleFunc("GET /api/v1/settings/probe-mode", srv.handleGetProbeMode)
	mux.HandleFunc("PUT /api/v1/settings/probe-mode", srv.handleSetProbeMode)
	mux.HandleFunc("GET /api/v1/settings/allow-inline-credentials", srv.handleGetAllowInlineCredentials)
	mux.HandleFunc("PUT /api/v1/settings/allow-inline-credentials", srv.handleSetAllowInlineCredentials)
	mux.HandleFunc("GET /api/v1/settings/managed-cluster-self-heal", srv.handleGetManagedClusterSelfHeal)
	mux.HandleFunc("PUT /api/v1/settings/managed-cluster-self-heal", srv.handleSetManagedClusterSelfHeal)
	mux.HandleFunc("GET /api/v1/settings/addon-values-engine-enabled", srv.handleGetAddonValuesEngineEnabled)
	mux.HandleFunc("PUT /api/v1/settings/addon-values-engine-enabled", srv.handleSetAddonValuesEngineEnabled)
	// Capability auto-detection (V2-cleanup-88.1) — read-tier, any
	// authenticated user (the register-cluster screen needs it before the
	// user has picked a role).
	mux.HandleFunc("GET /api/v1/system/capabilities", srv.handleGetSystemCapabilities)
	// Managed-secrets visibility for the System page — Viewer+ gated on
	// "managed-secrets.list" (P3-E; previously the same no-explicit-gate
	// convention as /providers, /config, and /secrets/status still below).
	mux.HandleFunc("GET /api/v1/system/managed-secrets", srv.handleGetManagedSecrets)

	// MARKETPLACE — what you could run. The curated list Sharko ships (plus
	// any third-party feeds an operator configured), read-only, discovery
	// only. Nothing here is running anywhere and nothing here is approved:
	// approving is the pull request POST /api/v1/catalog/addons opens.
	mux.HandleFunc("GET /api/v1/marketplace/addons", srv.handleListCatalogAddons)
	// The feeds behind the Marketplace list (embedded + third-party URLs
	// from SHARKO_CATALOG_URLS) with per-source fetch status. Read-only.
	mux.HandleFunc("GET /api/v1/marketplace/sources", srv.handleListCatalogSources)
	// Force-refresh every configured third-party feed synchronously.
	// Tier 2 (admin). Audit-logged.
	mux.HandleFunc("POST /api/v1/marketplace/sources/refresh", srv.handleRefreshCatalogSources)
	mux.HandleFunc("GET /api/v1/marketplace/addons/{name}/versions", srv.handleListCatalogVersions)
	// README proxy for the in-page Marketplace detail view. Resolves
	// curated addon → ArtifactHub package, then returns the README
	// markdown.
	mux.HandleFunc("GET /api/v1/marketplace/addons/{name}/readme", srv.handleGetCatalogReadme)
	// Tool README (distinct from Helm chart README). Resolved server-side
	// so the browser doesn't need GitHub API access.
	mux.HandleFunc("GET /api/v1/marketplace/addons/{name}/project-readme", srv.handleGetCuratedProjectReadme)
	mux.HandleFunc("GET /api/v1/marketplace/addons/{name}", srv.handleGetCatalogAddon)

	// CATALOG — what your org allows. Read straight from catalog.yaml in
	// the connected git repo; the shipped curated list is NOT mixed in.
	// POST is the approval: it writes full, self-contained entries and
	// opens a pull request, optionally enabling them on a cluster in the
	// same one.
	mux.HandleFunc("GET /api/v1/catalog/addons", srv.handleListOrgCatalog)
	mux.HandleFunc("POST /api/v1/catalog/addons", srv.handleAddToCatalog)
	mux.HandleFunc("GET /api/v1/catalog/addons/{name}", srv.handleGetOrgCatalogAddon)
	// PATCH is a merge-semantics edit of one existing entry; DELETE removes
	// one entry (refusing while it's still enabled on any cluster). Both
	// open a pull request, same as POST above.
	mux.HandleFunc("PATCH /api/v1/catalog/addons/{name}", srv.handleEditOrgCatalogAddon)
	mux.HandleFunc("DELETE /api/v1/catalog/addons/{name}", srv.handleDeleteOrgCatalogAddon)

	// Version-freshness summary + an out-of-cycle refresh trigger for the
	// background scheduler (internal/catalog.FreshnessScheduler). It watches
	// BOTH lists: the Marketplace's curated entries and your own approved
	// ones. Distinct from /marketplace/sources/refresh above: that one
	// re-pulls third-party FEEDS; this one re-checks chart VERSIONS + the
	// engine pin.
	mux.HandleFunc("GET /api/v1/catalog/freshness", srv.handleGetCatalogFreshness)
	mux.HandleFunc("POST /api/v1/catalog/freshness/refresh", srv.handleRefreshCatalogFreshness)

	// ArtifactHub proxy + reprobe — more of the discovery window, so they
	// sit under /marketplace with the rest of it. Server-side proxy so the
	// browser doesn't call ArtifactHub directly (CORS + shared cache +
	// rate-limit).
	mux.HandleFunc("GET /api/v1/marketplace/search", srv.handleSearchCatalog)
	mux.HandleFunc("GET /api/v1/marketplace/remote/{repo}/{name}/project-readme", srv.handleGetRemoteProjectReadme)
	mux.HandleFunc("GET /api/v1/marketplace/remote/{repo}/{name}", srv.handleGetRemotePackage)
	mux.HandleFunc("POST /api/v1/marketplace/reprobe", srv.handleReprobeArtifactHub)

	// Chart-lookup helpers behind the "add your own chart" form. These
	// belong to the Catalog side: they exist so somebody can describe a
	// chart the Marketplace has never heard of and add it anyway.
	//
	// Paste Helm URL validator — confirms an arbitrary repo+chart is
	// reachable and parseable, returns versions for the Configure modal.
	mux.HandleFunc("GET /api/v1/catalog/validate", srv.handleValidateCatalogChart)
	// Lists chart names available in an arbitrary Helm repository so the
	// form can show a chart-name dropdown after the operator validates the
	// repo URL.
	mux.HandleFunc("GET /api/v1/catalog/repo-charts", srv.handleListRepoCharts)

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
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", srv.handleMarkNotificationRead)

	// Pull request tracking
	mux.HandleFunc("GET /api/v1/prs", srv.handleListPRs)
	// /prs/merged must be registered BEFORE /prs/{id} so the literal "merged"
	// path wins over the {id} wildcard.
	mux.HandleFunc("GET /api/v1/prs/merged", srv.handleListMergedPRs)
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
	mux.HandleFunc("GET /api/v1/cluster/home", srv.handleGetHomeCluster)

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

	// Stale dead-route stub.
	//
	// `/api/v1/login` was never registered, but unauthenticated POSTs to it
	// were absorbed by basicAuthMiddleware and returned 401 — making the
	// path look like a real auth-protected endpoint. Returning an explicit
	// 404 with a hint pointing to /api/v1/auth/login (the actual route)
	// eliminates the false positive. basicAuthMiddleware skips auth for
	// this path so the 404 actually reaches the client.
	mux.HandleFunc("POST /api/v1/login", srv.handleStaleLoginRoute)
	mux.HandleFunc("POST /api/v1/auth/logout", srv.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/update-password", srv.handleUpdatePassword)
	mux.HandleFunc("POST /api/v1/auth/hash", srv.handleHashPassword)

	// API tokens (admin only)
	mux.HandleFunc("POST /api/v1/tokens", srv.handleCreateToken)
	mux.HandleFunc("GET /api/v1/tokens", srv.handleListTokens)
	mux.HandleFunc("POST /api/v1/tokens/{name}/renew", srv.handleRenewToken)
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

	// Catch-all for unknown /api/v1/* paths. Without this, the SPA
	// catch-all below would serve index.html for unmatched API paths,
	// silently masking removed or mistyped endpoints as 200 OK HTML.
	//
	// Registering a literal `/api/v1/` prefix BEFORE the SPA catch-all
	// (Go 1.22+ ServeMux longest-match semantics) ensures every unmatched
	// API path returns a structured 404 JSON. Real API routes are
	// registered above with method+path patterns that win by specificity.
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":  "API endpoint not found",
			"code":   "endpoint_not_found",
			"path":   r.URL.Path,
			"method": r.Method,
			"hint":   "see /swagger/index.html for the supported API surface",
		})
	})

	// Static files (SPA)
	//
	// Cache honesty (sprint-backlog-burndown-r1 Lane B): after a live image
	// roll, an already-open tab may still hold the OLD index.html, which
	// references hashed chunk files the NEW image no longer ships. If we
	// let the shell get cached and the server quietly answers a missing
	// chunk with index.html (the old SPA-fallback-for-everything behavior),
	// the browser gets HTML where it expected JS/CSS — a MIME-type error
	// that leaves the tab dead until a hard refresh. Three rules fix that:
	//   1. index.html always carries Cache-Control: no-cache, so the
	//      browser revalidates the shell on every load instead of trusting
	//      a stale copy.
	//   2. /assets/* files carry a content hash in their name (Vite's
	//      build output) — a HIT is safe to cache forever (immutable).
	//   3. /assets/* MISSES return an honest 404, never the index.html
	//      fallback. The SPA fallback stays for route paths like /clusters.
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			reqPath := r.URL.Path

			if strings.HasPrefix(reqPath, "/assets/") {
				if _, err := fs.Stat(staticFS, strings.TrimPrefix(reqPath, "/")); err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				fileServer.ServeHTTP(w, r)
				return
			}

			// Everything else: serve the matching static file if it exists
			// (e.g. favicon, robots.txt), otherwise fall back to
			// index.html for client-side routing (e.g. /clusters,
			// /observability). Any path that resolves to the shell —
			// "/", "/index.html", or the SPA fallback — gets no-cache.
			servingIndex := false
			switch {
			case reqPath == "/", reqPath == "/index.html":
				servingIndex = true
			default:
				if _, err := fs.Stat(staticFS, reqPath[1:]); err != nil {
					// File not found — serve index.html for client-side routing
					r.URL.Path = "/"
					servingIndex = true
				}
			}
			if servingIndex {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	// Wrap with middleware
	// Wrapping order (innermost → outermost): mux → maxBodySize → writeRateLimiter
	// → auditMiddleware (reads user from header set by basicAuth) → basicAuthMiddleware
	// → cors → securityHeaders → metrics → logging → requestID.
	// Execution order reverses: requestID → logging → metrics → securityHeaders →
	// cors → basicAuth → auditMiddleware → writeRateLimiter → maxBodySize → mux.
	//
	// requestID is outermost so the request_id stamped on the context is
	// visible to every downstream layer (logging, metrics, audit, handlers,
	// orchestrator, gitops, ...). V2-2.2.
	var handler http.Handler = mux
	handler = maxBodySize(handler, 1<<20)                  // 1MB request body limit
	handler = writeRateLimiter(30, 1*time.Minute)(handler) // 30 writes/min per IP
	handler = srv.auditMiddleware(handler)                 // emit audit entry after auth sets user
	handler = srv.basicAuthMiddleware(handler)
	handler = corsMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
	handler = metrics.Middleware(handler) // Prometheus request metrics
	handler = loggingMiddleware(handler)
	handler = requestIDMiddleware(handler) // attach correlation ID at the boundary

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
//   - Sessions expire after DefaultSessionLifetime; a background goroutine
//     cleans expired entries and every request re-checks the expiry, so an
//     expired session is refused with a 401 even before the sweep runs.
//     Once expired there is no refresh — the user logs in again.

type sessionInfo struct {
	Username string
	Expiry   time.Time
}

// DefaultSessionLifetime is how long a human login stays valid before the
// user has to sign in again. There is no refresh token and no remember-me:
// when the window is up, the session is gone. Documented in
// docs/site/api/overview.md and docs/site/operator/security.md.
const DefaultSessionLifetime = 24 * time.Hour

var (
	activeSessions   = make(map[string]*sessionInfo) // token -> session
	sessionsMu       sync.RWMutex
	sessionLifetime  = DefaultSessionLifetime
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

// getSessionUser returns the username behind a session token, or "" if the
// token is unknown OR its lifetime has run out. The expiry check matters here
// as well as in isValidSession: the hourly sweep is a cleanup, not the gate.
func getSessionUser(token string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := activeSessions[token]
	if !ok || !time.Now().Before(sess.Expiry) {
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

	// No users configured is NOT a free pass anymore: the old branch that
	// minted an "anonymous" session for any credentials is gone (v4
	// fail-open removal). With zero users, ValidateCredentials below
	// refuses everything, so the caller gets the same 401 as any bad
	// credential.
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

// handleStaleLoginRoute serves the dead `/api/v1/login` path with an
// explicit 404 + hint pointing at the real `/api/v1/auth/login` endpoint.
//
// Nothing in the codebase, scripts, CLI, UI, or docs uses
// `/api/v1/login`. The path was never registered, but unauthenticated
// POSTs to it were absorbed by basicAuthMiddleware and returned 401,
// which looked like a real auth-protected endpoint. Returning 404 with
// a clear hint disambiguates "wrong path" from "wrong creds". The path
// is added to the basicAuthMiddleware skip list so this handler — not
// the 401 response — is what the client sees.
func (s *Server) handleStaleLoginRoute(w http.ResponseWriter, r *http.Request) {
	slog.Warn("client hit dead /api/v1/login route — real endpoint is /api/v1/auth/login",
		"path", r.URL.Path,
		"client_ip", clientIP(r),
	)
	writeError(w, http.StatusNotFound, "endpoint not found — did you mean POST /api/v1/auth/login?")
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
//
// Fail CLOSED: the old "no users configured = every endpoint open" behavior
// is gone. The HasUsers() check now runs per request (not once at router
// construction), and zero users means 401 — never open. In practice this
// branch is unreachable: cmd/sharko/serve.go guarantees users exist before
// the router is built (demo seeding or EnsureInitialAdmin), but if the
// store ever ends up empty at request time the server refuses, it does not
// open up.
func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TEST-ONLY seam: reproduces the historical "no users = open"
		// wiring (the middleware used to return the mux unwrapped, so
		// even the role-header strip did not run) for this package's
		// unit tests, which exercise handlers through the full router
		// without a login dance and stamp X-Sharko-User/-Role headers
		// themselves. The field is unexported and its only writers live
		// in _test.go files — no production code path can set it. The
		// moment a test adds a user, auth is enforced again (HasUsers()
		// is checked live per request).
		if s.authDisabledForTests && !s.authStore.HasUsers() {
			next.ServeHTTP(w, r)
			return
		}

		// Strip any client-supplied role header to prevent spoofing.
		// Only the middleware sets this header (for API key auth).
		r.Header.Del("X-Sharko-Role")

		path := r.URL.Path

		// Skip auth for: health, login, git webhooks (signature-verified), static files.
		// /api/v1/login is the dead-route stub — it must reach
		// handleStaleLoginRoute so we return a clean 404 instead of
		// swallowing the request as a 401 here.
		if path == "/api/v1/health" || path == "/api/v1/auth/login" || path == "/api/v1/login" || path == "/api/v1/webhooks/git" || !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Fail CLOSED: zero users at request time = refuse, never open.
		// This branch is practically unreachable (serve.go guarantees
		// users exist before the router is built), but if the store ever
		// ends up empty the server refuses — it does not open up.
		if !s.authStore.HasUsers() {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// refusal carries a specific, plain-English reason when the caller
		// presented a real token that is no longer usable (expired). It stays
		// empty for anything else so unknown tokens get a flat 401.
		refusal := ""

		// Check Bearer token
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			ok, reason := s.tryAuthenticateToken(r, token)
			if ok {
				next.ServeHTTP(w, r)
				return
			}
			refusal = reason
		}

		// EventSource (used by the audit Live Tail SSE stream in the UI)
		// cannot set an Authorization header, so it passes the session
		// token as a ?token= query param instead. This fallback is scoped
		// tightly to GET /api/v1/audit/stream ONLY — every other route
		// still requires a real Bearer header. Do not widen this check
		// (V2-cleanup-85.2).
		if isAuditStreamRequest(r) && !strings.HasPrefix(authHeader, "Bearer ") {
			if token := r.URL.Query().Get("token"); token != "" {
				ok, reason := s.tryAuthenticateToken(r, token)
				if ok {
					next.ServeHTTP(w, r)
					return
				}
				refusal = reason
			}
		}

		if refusal != "" {
			writeError(w, http.StatusUnauthorized, refusal)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

// isAuditStreamRequest reports whether r targets the audit Live Tail SSE
// endpoint — the ONLY route where basicAuthMiddleware accepts a ?token=
// query-param fallback (see tryAuthenticateToken).
func isAuditStreamRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/v1/audit/stream"
}

// tryAuthenticateToken validates token as either a session token or a
// sharko_-prefixed API key — the SAME validation the Authorization: Bearer
// path uses — and, on success, stamps X-Sharko-User / X-Sharko-Role on r.
//
// Returns (true, "") when authentication succeeded. On failure the second
// value is a plain-English reason to show the caller, but ONLY when the
// caller demonstrably holds the credential in question (an API token that
// matched a stored hash but has expired). For an unknown or revoked token it
// is empty, so the caller gets a flat "unauthorized" and learns nothing about
// which token names exist.
func (s *Server) tryAuthenticateToken(r *http.Request, token string) (bool, string) {
	if token == "" {
		return false, ""
	}
	if isValidSession(token) {
		username := getSessionUser(token)
		r.Header.Set("X-Sharko-User", username)
		// Look up user role from the store so authz middleware can enforce RBAC
		if user := s.authStore.GetUser(username); user != nil {
			r.Header.Set("X-Sharko-Role", user.Role)
		}
		return true, ""
	}

	// Check if the token is an API key
	if strings.HasPrefix(token, "sharko_") {
		username, role, err := s.authStore.AuthenticateToken(token)
		if err == nil {
			r.Header.Set("X-Sharko-User", username)
			r.Header.Set("X-Sharko-Role", role)
			return true, ""
		}
		return false, tokenRefusalMessage(err)
	}
	return false, ""
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
// @Description Generates a bcrypt hash from a plaintext password, for operators who manage user accounts by hand in the users ConfigMap and Secret. Requires authentication.
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Password to hash"
// @Success 200 {object} map[string]interface{} "Bcrypt hash"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Not authenticated"
// @Router /auth/hash [post]
// handleHashPassword generates a bcrypt hash from a plaintext password.
//
// History: this endpoint used to be gated on "no users configured" — the
// pre-auth setup window. That window no longer exists (Sharko now generates
// an initial admin instead of ever running without users), so the old gate
// would have made the endpoint permanently dead. It is now a plain
// authenticated utility: basicAuthMiddleware requires a valid session or
// API key before the request reaches here, the handler is a pure function
// over its input, and it reads no stored data — so it adds no
// unauthenticated surface.
func (s *Server) handleHashPassword(w http.ResponseWriter, r *http.Request) {
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
		// https: (not http:) so external badge/logo images in a rendered
		// chart README (ui/src/components/RichMarkdown.tsx's sanitize
		// schema deliberately allows http/https <img> src) actually load
		// instead of being emitted then silently blocked by the browser
		// (v4-walkfix W1 item 4).
		"img-src 'self' data: https:; " +
		"font-src 'self' https://fonts.gstatic.com https://fonts.googleapis.com; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	// swaggerCSP is identical to csp except it allows inline scripts. The bundled
	// swagger-ui-dist page bootstraps via an inline <script>, which the strict
	// script-src 'self' policy blocks (blank page). This relaxed policy is sent
	// ONLY for /swagger/-prefixed paths; every other route keeps the strict csp.
	const swaggerCSP = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"img-src 'self' data:; " +
		"font-src 'self' https://fonts.gstatic.com https://fonts.googleapis.com; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/swagger/") {
			w.Header().Set("Content-Security-Policy", swaggerCSP)
		} else {
			w.Header().Set("Content-Security-Policy", csp)
		}
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
//
// The wrapper must transparently expose the optional interfaces that the
// underlying writer implements (Flusher, Hijacker, CloseNotifier).
// Otherwise handlers that rely on a type assertion — most importantly
// Server-Sent Events handlers like /api/v1/audit/stream which do
// `w.(http.Flusher)` — will see the assertion fail and fall back to a
// 500 "streaming not supported" response. WebSocket upgrade paths rely
// on http.Hijacker the same way.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the wrapped writer when it implements http.Flusher.
// Required for Server-Sent Events / streaming responses (e.g. /audit/stream).
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped writer when it implements http.Hijacker.
// Required for WebSocket upgrades and any handler that needs to take over
// the underlying TCP connection.
func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := sr.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported")
}

// CloseNotify forwards to the wrapped writer when it implements
// http.CloseNotifier. The interface is deprecated in favour of
// Request.Context().Done(), but some libraries still rely on the
// type-assertion shape.
//
//nolint:staticcheck // CloseNotifier is deprecated but downstream code still uses it
func (sr *statusRecorder) CloseNotify() <-chan bool {
	if cn, ok := sr.ResponseWriter.(http.CloseNotifier); ok {
		return cn.CloseNotify()
	}
	closed := make(chan bool, 1)
	return closed
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
		slog.Info("request completed",
			"request_id", logging.RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sr.statusCode,
			"duration", time.Since(start))
	})
}

// requestIDMiddleware honours an inbound X-Request-ID header when present
// (so a caller — UI, CLI, upstream service — can trace its own request
// through Sharko's logs) and otherwise generates a fresh `req-<hex>` ID.
// The ID is attached to the request context via logging.WithRequestID so
// every downstream layer (logging, audit, handlers, orchestrator, gitops)
// can stamp it on its slog output.
//
// The chosen ID is also echoed back as X-Request-ID on the response, which
// makes operator-driven correlation ("here's the curl output, find the
// log line") trivial. V2-2.2.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = logging.NewRequestID()
		}
		// Cap inbound header length to a sensible bound so a malicious or
		// buggy client can't propagate an arbitrarily large ID through the
		// log pipeline. 128 bytes leaves room for upstream tracing systems
		// (e.g. trace-id + span-id concatenation) while preventing abuse.
		if len(id) > 128 {
			id = id[:128]
		}
		ctx := logging.WithRequestID(r.Context(), id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
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

// errorResponse is the one shape every error leaves the server in (error
// review package 2): a plain headline that's always populated — every
// existing consumer that reads only `.error` keeps working unchanged — plus
// three additive fields that are left out of the JSON entirely (not sent as
// "") when there's nothing to say. API stability: additive-only, nothing
// renamed or removed from what shipped before this.
type errorResponse struct {
	Error string `json:"error"`
	Cause string `json:"cause,omitempty"`
	Hint  string `json:"hint,omitempty"`
	Code  string `json:"code,omitempty"`
}

// writeError writes a JSON error response with just a headline — the ~271
// existing call sites that only have a message string (no error value to
// classify) keep this exact call and keep getting exactly {"error": "..."}.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeErrorWithCause writes the full shaped envelope for a call site that
// has both a plain headline and the error that produced it — e.g. the
// connection test/save boundary (see connection_messages.go). headline is
// the caller's own plain sentence (unchanged, still the `.error` field);
// shapeError fills in cause/hint/code around it. This is additive — new
// call sites can adopt it incrementally without any of the existing
// writeError(w, status, msg) sites having to change.
func writeErrorWithCause(w http.ResponseWriter, status int, headline string, err error) {
	h, cause, hint, code := shapeError(err, headline)
	writeJSON(w, status, errorResponse{Error: h, Cause: cause, Hint: hint, Code: code})
}

// shapeError builds the additive fields of the error envelope from err: a
// technical-detail cause, an actionable hint, and a machine code. headline
// passes straight through unchanged — the caller already knows the plain
// sentence for what it was doing; this only adds the supporting fields.
//
// cause is populated ONLY for four families whose error carries safe,
// well-understood text — never from a generic "innermost error in the
// chain" walk. That was the first design tried here, and it does NOT hold
// up: while Go's structured wrapped types (net.OpError, url.Error,
// syscall.Errno, …) strip identifying context on Unwrap, plenty of leaf
// error types do not — a gopkg.in/yaml.v3 TypeError's Error() embeds the
// actual offending line of the file being parsed, verbatim, and that
// surfaced through this exact code path (values-file preview) in testing.
// A single "walk to the bottom" rule can't tell those two cases apart, so
// rather than risk it case by case, cause is opt-in per family:
//
//   - ArgoCD credential sentinels (argocd.ErrTokenInvalid /
//     ErrPermissionDenied) — fixed sentence, and verify.Hint's generic
//     auth/RBAC wording is written for cluster credentials, not Sharko's
//     own ArgoCD connection, so these also get a hint naming Settings →
//     Connections directly.
//   - Git-provider sentinels (gitprovider.ErrFileNotFound /
//     ErrPullRequestNotFound) — fixed sentence.
//   - Kubernetes API errors (apierrors.StatusError) — Status().Message is a
//     controlled, structured surface (e.g. `namespaces "x" not found`), not
//     free-form file/user content, but ONLY for a bounded allowlist of
//     Reason kinds (NotFound, Forbidden, Unauthorized, AlreadyExists,
//     Conflict, Timeout). Review findings r1, M3: Invalid (NewInvalid,
//     admission-webhook denials) and any message that mentions "admission
//     webhook" can embed arbitrary third-party text — a denying webhook or a
//     rejected field value gets echoed into Message verbatim by client-go.
//     Those get a safe summary naming only the (bounded) Reason instead of
//     the message text; so does every Reason not on the allowlist, since the
//     allowlist is opt-in, not opt-out.
//   - Sharko input-validation errors (service.ErrValidation) — already a
//     clean, Sharko-authored sentence (see internal/service/connection.go's
//     validationError), so it's used as-is.
//
// Everything else — including every plain upstream network/timeout/DNS
// failure classified below — gets cause = "" (omitted). This keeps
// writeServerError/writeUpstreamError's existing no-leak guarantee intact
// without needing to audit every error type that might ever reach them.
//
// hint/code come from internal/verify's fixed classification table
// (ClassifyError/Hint) and never echo err's own text, so they're always
// safe to expose regardless of what err says. code is left empty when
// classification lands on ERR_UNKNOWN — an unclassified code isn't
// actionable, so there's nothing worth putting in the field.
func shapeError(err error, headline string) (respHeadline, cause, hint, code string) {
	if err == nil {
		return headline, "", "", ""
	}
	cause, hint, code = classifyBoundaryError(err)
	return headline, cause, hint, code
}

// classifyBoundaryError is shapeError's cause/hint/code logic, split out so
// it can be unit-tested directly against the four bespoke families plus the
// generic (cause-less) fallback.
func classifyBoundaryError(err error) (cause, hint, code string) {
	switch {
	case errors.Is(err, argocd.ErrTokenInvalid):
		return argocd.ErrTokenInvalid.Error(),
			"check the ArgoCD token in Settings → Connections and replace it if it's expired.",
			string(verify.ERR_AUTH)
	case errors.Is(err, argocd.ErrPermissionDenied):
		return argocd.ErrPermissionDenied.Error(),
			"grant the token the required permission in ArgoCD, or replace it in Settings → Connections.",
			string(verify.ERR_RBAC)
	case errors.Is(err, gitprovider.ErrFileNotFound):
		return "the file was not found in the Git repository", "", ""
	case errors.Is(err, gitprovider.ErrPullRequestNotFound):
		return "the pull request was not found in the Git repository", "", ""
	case errors.Is(err, service.ErrValidation):
		return err.Error(), "", ""
	}

	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		cause = safeStatusErrorCause(statusErr)
	}

	if ec := verify.ClassifyError(err); ec != verify.ERR_UNKNOWN {
		code = string(ec)
		hint = verify.Hint(ec)
	}
	return cause, hint, code
}

// statusErrorBoundedReasons is the allowlist of apierrors.StatusError Reason
// kinds whose Message is safe to echo verbatim into the error envelope's
// `cause` field. These are controlled, structured surfaces (e.g. `namespaces
// "x" not found`) — never free-form file/user content (review findings r1,
// M3).
var statusErrorBoundedReasons = map[metav1.StatusReason]bool{
	metav1.StatusReasonNotFound:      true,
	metav1.StatusReasonForbidden:     true,
	metav1.StatusReasonUnauthorized:  true,
	metav1.StatusReasonAlreadyExists: true,
	metav1.StatusReasonConflict:      true,
	metav1.StatusReasonTimeout:       true,
}

// safeStatusErrorCause returns a cause string for a Kubernetes StatusError
// that is safe to expose to the caller. Review findings r1, M3: the naive
// "always copy Status().Message" approach leaked arbitrary third-party text
// on every 5xx — an admission-webhook denial or a NewInvalid error can embed
// the rejected field value, or an operator-authored denial message,
// verbatim into Message. Only the bounded Reason kinds in
// statusErrorBoundedReasons get their Message echoed; everything else
// (Invalid, any message mentioning "admission webhook" regardless of
// Reason, and any Reason not on the allowlist) gets a safe summary naming
// only the Reason.
func safeStatusErrorCause(statusErr *apierrors.StatusError) string {
	status := statusErr.Status()
	if statusErrorBoundedReasons[status.Reason] && !strings.Contains(status.Message, "admission webhook") {
		return status.Message
	}
	return fmt.Sprintf("kubernetes rejected the request (reason: %s)", status.Reason)
}

// writeServerError writes a sanitized 5xx response while logging the full
// error server-side. The user-visible `error` field deliberately stays
// http.StatusText(status) rather than echoing err — that field's job is to
// stay consistent with the HTTP status line, not to explain the failure.
// The additive `cause` field (see shapeError) only ever surfaces for the
// four bespoke, pre-written error families shapeError knows about — never a
// generic "whatever err says," which is exactly what keeps this sanitized:
// err itself is never echoed into the response body, only logged.
//
// status MUST be a 5xx HTTP status (e.g. http.StatusInternalServerError,
// http.StatusServiceUnavailable, http.StatusBadGateway).
//
// op should be a short, snake_case identifier for the failing operation
// (e.g. "list_clusters") so logs are grep-friendly. Use writeError for any
// 4xx response — those messages are user-actionable and safe to surface.
func writeServerError(w http.ResponseWriter, status int, op string, err error) {
	slog.Error("server error", "op", op, "status", status, "error", err)
	_, cause, hint, code := shapeError(err, http.StatusText(status))
	body := map[string]string{
		"error": http.StatusText(status),
		"op":    op,
	}
	if cause != "" {
		body["cause"] = cause
	}
	if hint != "" {
		body["hint"] = hint
	}
	if code != "" {
		body["code"] = code
	}
	writeJSON(w, status, body)
}

// classifyUpstreamError maps a Go error returned from an upstream service
// (Git provider, ArgoCD, AWS, K8s API server, …) onto an appropriate HTTP
// status code so that operators and clients can distinguish "the upstream
// is unreachable" (502) from "the upstream timed out" (504), "the upstream
// rate-limited us" (429), and "something went wrong on our end" (500).
//
// Branches:
//   - errors.Is(err, argocd.ErrTokenInvalid)             → 502 Bad Gateway
//   - errors.Is(err, syscall.ECONNREFUSED)               → 502 Bad Gateway
//   - errors.As to *net.DNSError                         → 502 Bad Gateway
//   - errors.As to *url.Error with Timeout()             → 504 Gateway Timeout
//   - case-insensitive substring match for "rate limit"
//     or "too many requests" or "429"                    → 429 Too Many Requests
//   - default                                            → 500 Internal Server Error
//
// The string match is intentionally broad because Git providers
// (GitHub/Azure DevOps) and Helm registries surface rate-limit conditions
// through different concrete types — sometimes a wrapped *url.Error
// carrying a JSON body, sometimes a synthesized error built from the
// response body. Matching on the canonical phrasing keeps the classifier
// useful without needing per-provider error types.
func classifyUpstreamError(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}

	// 502 — ArgoCD rejected Sharko's own credentials outright. This is an
	// upstream-refused-us problem (like a refused connection), not an
	// internal one — a bare 500 here used to hide "the token is dead" behind
	// a generic server-error response (error review package 1).
	if errors.Is(err, argocd.ErrTokenInvalid) {
		return http.StatusBadGateway
	}

	// 502 — connection refused (the remote port wasn't accepting).
	if errors.Is(err, syscall.ECONNREFUSED) {
		return http.StatusBadGateway
	}

	// 502 — DNS resolution failed (the remote hostname doesn't resolve).
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return http.StatusBadGateway
	}

	// 504 — request timed out somewhere in the URL stack. We check this
	// before the rate-limit string match because *url.Error wraps a
	// concrete cause and the Timeout() helper is more precise than any
	// substring search.
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return http.StatusGatewayTimeout
	}

	// 429 — upstream rate limit. The phrasing is normalised to lower case
	// so we match irrespective of how the upstream surfaced it.
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "rate limit") ||
		strings.Contains(low, "too many requests") ||
		strings.Contains(low, "429") {
		return http.StatusTooManyRequests
	}

	// Default — opaque internal failure.
	return http.StatusInternalServerError
}

// writeUpstreamError classifies the error first, then funnels through
// writeServerError so the response body stays sanitized (no leak of
// upstream paths/messages) and the structured log preserves the full
// error for debugging.
//
// Use this at any handler call site where the error originates from a
// remote service (Git provider, ArgoCD, AWS API, K8s API server). For
// genuinely internal failures (config parse, in-memory store, etc.) keep
// using writeServerError directly with an explicit 500 — those are not
// upstream-classifiable and pretending otherwise would mislead operators.
func writeUpstreamError(w http.ResponseWriter, op string, err error) {
	writeServerError(w, classifyUpstreamError(err), op, err)
}

// writeMissingProviderError is the canonical response for write/discover
// endpoints whose backing credentials provider is not configured at runtime.
//
// 503 Service Unavailable is the correct status: the endpoint exists, the
// resource (cluster CRUD) is temporarily unavailable because a precondition
// is unmet, and the operator can fix it themselves. The response body
// surfaces a structured `hint` field pointing at the standard
// configuration flows so the UI / CLI can render an actionable message
// without parsing English text.
//
// Used by every handler that calls `s.credProvider() == nil` early-return.
func writeMissingProviderError(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "credentials provider is not configured",
		"code":  "provider_not_configured",
		"hint":  "configure a secrets provider via Settings → Connections (UI), or POST /api/v1/connections/ with provider config (API)",
	})
}

// v1.39.3 route fix
