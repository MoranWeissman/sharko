package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/readcache"
)

// clustersListCacheKey is the readcache key for ListClusters (perf M1).
const clustersListCacheKey = "clusters:list"

// isGitFileNotFound reports whether err signals "file does not exist" from a
// gitprovider.GitProvider.GetFileContent call.
//
// Detection is type-based — every provider implementation wraps actual
// missing-file conditions with gitprovider.ErrFileNotFound (see
// internal/gitprovider/provider.go). The previous substring-matching
// implementation silently masked legitimate auth/branch/perm errors that
// happened to contain the words "not found" or "404" as "missing file
// → empty list" — examples that would have tripped a substring-matcher:
//
//   - "GitHub repository not found — check the URL and credentials"
//   - "branch 'main' not found"
//   - "deployment 'foo' not found"
//   - "got 4040 bytes" (the "404" substring case)
//
// fs.ErrNotExist is also accepted for callers that go through stdlib paths
// (e.g. local filesystem in tests).
//
// A nil err returns false. This helper is intentionally lenient so a missing
// managed-clusters.yaml in a brand-new install is treated as an empty
// list rather than a 500.
func isGitFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gitprovider.ErrFileNotFound) || errors.Is(err, fs.ErrNotExist)
}

// ClusterService handles cluster-related operations.
type ClusterService struct {
	parser              *config.Parser
	managedClustersPath string // path in Git repo to managed-clusters.yaml

	// baseBranchFn is the per-instance test seam for the configured GitOps
	// base branch, mirroring AddonService.baseBranchFn (Wave 2 ride-along
	// w2-q6 item 1) so every read in this file follows the connection's
	// configured branch instead of a hardcoded "main". nil (tests, e2e
	// harness) falls back to "main" via the branch() helper below.
	baseBranchFn func() string

	// cache is the per-instance TTL cache for ListClusters (perf M1). See
	// DashboardService.cache's doc for the shared-vs-private-per-test
	// discipline; SetCache mirrors DashboardService.SetCache exactly.
	cache *readcache.Cache
}

// SetCache replaces this service's read cache with a shared instance (see
// internal/readcache and DashboardService.SetCache).
func (s *ClusterService) SetCache(c *readcache.Cache) {
	s.cache = c
}

// SetBaseBranchFn wires in a live accessor for the configured GitOps base
// branch (e.g. api.Server.GitopsBaseBranch), matching
// AddonService.SetBaseBranchFn.
func (s *ClusterService) SetBaseBranchFn(fn func() string) {
	s.baseBranchFn = fn
}

// branch returns the configured GitOps base branch, defaulting to "main"
// when no seam is wired (tests, e2e harness) or it resolves to "".
func (s *ClusterService) branch() string {
	if s.baseBranchFn == nil {
		return "main"
	}
	if b := s.baseBranchFn(); b != "" {
		return b
	}
	return "main"
}

// NewClusterService creates a new ClusterService.
// managedClustersPath is the Git repo path to the managed clusters YAML
// (e.g. "configuration/managed-clusters.yaml"). An empty string defaults to
// "configuration/managed-clusters.yaml".
func NewClusterService(managedClustersPath string) *ClusterService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &ClusterService{
		parser:              config.NewParser(),
		managedClustersPath: managedClustersPath,
		cache:               readcache.New(readcache.DefaultTTL),
	}
}

// readManagedClustersData reads the cluster registry file, trying the
// configured (v3) path first and falling back to the fixed v4 path
// (orchestrator.V4ManagedClustersPath, "managed-clusters.yaml" — design doc
// §2.4: same kind/shape as the v3 file, only the location changed) when
// the v3 path is genuinely absent. A connected repo is one format or the
// other, never both, so this costs one extra read only on the (cheap,
// not-found) v3-absent path.
//
// Returns (nil, nil) when NEITHER path resolves — callers keep their
// existing "treat as empty" fallback (clusterData = []byte("clusters:
// []")) unchanged. Returns a non-nil error only for a genuine read
// failure (auth, branch, transport) on either path.
func (s *ClusterService) readManagedClustersData(ctx context.Context, gp gitprovider.GitProvider) ([]byte, error) {
	data, err := gp.GetFileContent(ctx, s.managedClustersPath, s.branch())
	if err == nil {
		return data, nil
	}
	if !isGitFileNotFound(err) {
		return nil, err
	}
	v4Data, v4Err := gp.GetFileContent(ctx, orchestrator.V4ManagedClustersPath, s.branch())
	if v4Err == nil {
		return v4Data, nil
	}
	if !isGitFileNotFound(v4Err) {
		return nil, v4Err
	}
	return nil, nil
}

// ListClusters returns all clusters with health stats from Git + ArgoCD.
// Cached for readcache.DefaultTTL (perf M1) — see internal/readcache's
// package doc for the invalidation contract. Fleet-wide, not per-user:
// every authenticated caller sees the same cluster registry and health
// stats, so a single cache entry (not keyed by role/user) is safe.
//
// Callers (handleListClusters) mutate the returned *models.ClustersResponse
// in place afterward (connectivity enrichment, filter/sort/paginate) —
// readcache.GetOrCompute hands back a fresh JSON-decoded copy on every
// call specifically so that in-place mutation can never corrupt what a
// later cache hit returns to a different request.
func (s *ClusterService) ListClusters(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClustersResponse, error) {
	return readcache.GetOrCompute(s.cache, clustersListCacheKey, func() (*models.ClustersResponse, error) {
		return s.listClustersUncached(ctx, gp, ac)
	})
}

// listClustersUncached is ListClusters' actual computation (perf M1 wraps
// it with a cache; S1 walk-day-5 added the v4 label synthesis below).
func (s *ClusterService) listClustersUncached(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClustersResponse, error) {
	log := logging.LoggerFromContext(ctx)
	// Fetch Git config — v3 managed-clusters.yaml, or its v4 equivalent
	// managed-clusters.yaml (v4 Wave 1 Story 4.4).
	clusterData, err := s.readManagedClustersData(ctx, gp)
	if err != nil {
		return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
	}
	if clusterData == nil {
		clusterData = []byte("clusters: []")
	}

	clusters, err := s.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return nil, err
	}

	// v4 repos keep per-cluster addon enablement in cluster-addons/<name>.yaml,
	// never in managed-clusters.yaml labels — a v4 cluster's Labels map is
	// always empty at this point. Synthesize the same `<addon>: enabled`
	// label shape a v3 file carries so every label-reading consumer
	// downstream (this service's own GetEnabledAddons, and the UI's addon
	// count in ClustersOverview.tsx) works unchanged for either repo layout
	// (S1 walk-day-5 root cause).
	if isV4Repo(ctx, gp, s.branch()) {
		clusterAddons, caErr := listClusterAddonsSpecs(ctx, gp, s.branch())
		if caErr != nil {
			return nil, fmt.Errorf("reading cluster-addons/*.yaml: %w", caErr)
		}
		for i := range clusters {
			if spec, ok := clusterAddons[clusters[i].Name]; ok {
				clusters[i].Labels = v4ClusterLabels(spec)
			}
		}
	}

	// Fetch ArgoCD clusters for health stats
	argocdClusters, err := ac.ListClusters(ctx)
	if err != nil {
		log.Warn("could not fetch argocd clusters", "error", err)
		// Continue without ArgoCD data. PendingRegistrations defaults to
		// a non-nil empty slice — never let a nil array reach the FE.
		return &models.ClustersResponse{
			Clusters:             clusters,
			HealthStats:          s.computeHealthStats(clusters, nil),
			PendingRegistrations: []models.PendingRegistration{},
			OrphanRegistrations:  []models.OrphanRegistration{},
		}, nil
	}

	// Build ArgoCD cluster lookup
	argocdMap := make(map[string]models.ArgocdCluster)
	for _, ac := range argocdClusters {
		argocdMap[ac.Name] = ac
	}

	// Enrich clusters with ArgoCD status; mark as managed (in managed-clusters.yaml)
	for i := range clusters {
		clusters[i].Managed = true
		if ac, ok := argocdMap[clusters[i].Name]; ok {
			clusters[i].ConnectionStatus = ac.ConnectionState
			clusters[i].ServerVersion = ac.ServerVersion
			clusters[i].ServerURL = ac.Server
			delete(argocdMap, clusters[i].Name)
		} else {
			clusters[i].ConnectionStatus = "missing"
		}
	}

	// Add clusters that exist in ArgoCD but not in Git
	notInGitClusters := make([]models.Cluster, 0)
	for name, ac := range argocdMap {
		// Skip the in-cluster entry
		if name == "in-cluster" || strings.HasPrefix(ac.Server, "https://kubernetes.default") {
			continue
		}
		notInGitClusters = append(notInGitClusters, models.Cluster{
			Name:             name,
			Labels:           map[string]string{},
			ConnectionStatus: "not_in_git",
			ServerVersion:    ac.ServerVersion,
			ServerURL:        ac.Server,
		})
	}

	allClusters := append(clusters, notInGitClusters...)

	// PendingRegistrations is populated by the API handler from the Git
	// provider's open-PR list. OrphanRegistrations is populated by the
	// API handler from the ArgoCD cluster list. The service contract
	// guarantees both are non-nil empty slices here — handler may
	// overwrite with the resolved lists. Defaulting at the
	// service layer keeps every code path that returns ClustersResponse
	// honest about the no-nil contract.
	return &models.ClustersResponse{
		Clusters:             allClusters,
		HealthStats:          s.computeHealthStats(clusters, notInGitClusters),
		PendingRegistrations: []models.PendingRegistration{},
		OrphanRegistrations:  []models.OrphanRegistration{},
	}, nil
}

// GetClusterDetail returns detail for a single cluster.
func (s *ClusterService) GetClusterDetail(ctx context.Context, clusterName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClusterDetailResponse, error) {
	log := logging.LoggerFromContext(ctx)
	clusterData, err := s.readManagedClustersData(ctx, gp)
	if err != nil {
		return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
	}
	if clusterData == nil {
		clusterData = []byte("clusters: []")
	}

	isV4 := isV4Repo(ctx, gp, s.branch())
	var repoCfg *config.RepoConfig
	if isV4 {
		repoCfg, err = buildV4RepoConfig(ctx, gp, s.branch(), s.parser, nil, clusterData)
		if err != nil {
			return nil, err
		}
	} else {
		catalogData, catErr := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
		if catErr != nil {
			if isGitFileNotFound(catErr) {
				log.Warn("addons-catalog.yaml not found — treating as an empty catalog", "path", "configuration/addons-catalog.yaml", "branch", s.branch())
				catalogData = []byte("applicationsets: []")
			} else {
				return nil, fmt.Errorf("reading addons-catalog.yaml: %w", catErr)
			}
		}

		repoCfg, err = s.parser.ParseAll(clusterData, catalogData)
		if err != nil {
			return nil, err
		}
	}

	// Find the target cluster
	var cluster *models.Cluster
	for i := range repoCfg.Clusters {
		if repoCfg.Clusters[i].Name == clusterName {
			cluster = &repoCfg.Clusters[i]
			break
		}
	}
	if cluster == nil {
		return nil, nil
	}

	// v4 repos keep per-cluster addon enablement in
	// cluster-addons/<name>.yaml, never in managed-clusters.yaml labels —
	// synthesize the same label shape GetEnabledAddons already reads so it
	// works unchanged for either repo layout (S1 walk-day-5 root cause).
	if isV4 {
		v4Spec, found, v4Err := readV4ClusterAddons(ctx, gp, s.branch(), clusterName)
		if v4Err != nil {
			return nil, fmt.Errorf("reading cluster-addons for %s: %w", clusterName, v4Err)
		}
		if found {
			cluster.Labels = v4ClusterLabels(v4Spec)
		}
	}

	// Get enabled addons for this cluster
	addons := s.parser.GetEnabledAddons(*cluster, repoCfg.Addons)

	// Enrich with ArgoCD status
	argocdSvc := argocd.NewService(ac)
	apps, err := argocdSvc.GetClusterApplications(ctx, clusterName)
	if err != nil {
		log.Warn("could not fetch argocd apps for cluster", "cluster", clusterName, "error", err)
	} else {
		appMap := make(map[string]models.ArgocdApplication)
		for _, app := range apps {
			appMap[app.Name] = app
		}

		for i := range addons {
			appName := addons[i].AddonName + "-" + clusterName
			if app, ok := appMap[appName]; ok {
				addons[i].ArgocdSyncStatus = app.SyncStatus
				addons[i].ArgocdHealthStatus = app.HealthStatus
				addons[i].ArgocdVersion = app.SourceTargetRevision
			} else if app, ok := appMap[addons[i].AddonName]; ok {
				addons[i].ArgocdSyncStatus = app.SyncStatus
				addons[i].ArgocdHealthStatus = app.HealthStatus
				addons[i].ArgocdVersion = app.SourceTargetRevision
			}
		}
	}

	// Get connection state
	connState, _ := argocdSvc.GetClusterConnectionState(ctx, clusterName)
	cluster.ConnectionStatus = connState

	// Surface the ArgoCD-registered API-server URL so the UI's
	// ClusterTypeBadge can classify EKS/AKS/GKE/kind/minikube instead of
	// always falling back to "Self-hosted" (V2-cleanup-74.1). Left unset
	// for the hub-local in-cluster entry, mirroring ListClusters.
	if argocdClusters, err := ac.ListClusters(ctx); err != nil {
		log.Warn("could not fetch argocd clusters for server url", "cluster", clusterName, "error", err)
	} else {
		for _, argoCluster := range argocdClusters {
			if argoCluster.Name != clusterName {
				continue
			}
			if argoCluster.Name == "in-cluster" || strings.HasPrefix(argoCluster.Server, "https://kubernetes.default") {
				break
			}
			cluster.ServerURL = argoCluster.Server
			break
		}
	}

	return &models.ClusterDetailResponse{
		Cluster: *cluster,
		Addons:  addons,
	}, nil
}

// GetClusterComparison returns Git vs ArgoCD comparison for a cluster.
func (s *ClusterService) GetClusterComparison(ctx context.Context, clusterName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClusterComparisonResponse, error) {
	log := logging.LoggerFromContext(ctx)
	clusterData, err := s.readManagedClustersData(ctx, gp)
	if err != nil {
		return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
	}
	if clusterData == nil {
		clusterData = []byte("clusters: []")
	}

	isV4 := isV4Repo(ctx, gp, s.branch())
	var repoCfg *config.RepoConfig
	if isV4 {
		repoCfg, err = buildV4RepoConfig(ctx, gp, s.branch(), s.parser, nil, clusterData)
		if err != nil {
			return nil, err
		}
	} else {
		catalogData, catErr := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
		if catErr != nil {
			if isGitFileNotFound(catErr) {
				log.Warn("addons-catalog.yaml not found — treating as an empty catalog", "path", "configuration/addons-catalog.yaml", "branch", s.branch())
				catalogData = []byte("applicationsets: []")
			} else {
				return nil, fmt.Errorf("reading addons-catalog.yaml: %w", catErr)
			}
		}

		repoCfg, err = s.parser.ParseAll(clusterData, catalogData)
		if err != nil {
			return nil, err
		}
	}

	// Find cluster
	var cluster *models.Cluster
	for i := range repoCfg.Clusters {
		if repoCfg.Clusters[i].Name == clusterName {
			cluster = &repoCfg.Clusters[i]
			break
		}
	}
	if cluster == nil {
		return nil, nil
	}

	// v4 repos keep per-cluster addon enablement in
	// cluster-addons/<name>.yaml, never in managed-clusters.yaml labels —
	// synthesize the same label shape GetEnabledAddons already reads so it
	// works unchanged for either repo layout (S1 walk-day-5 root cause).
	if isV4 {
		v4Spec, found, v4Err := readV4ClusterAddons(ctx, gp, s.branch(), clusterName)
		if v4Err != nil {
			return nil, fmt.Errorf("reading cluster-addons for %s: %w", clusterName, v4Err)
		}
		if found {
			cluster.Labels = v4ClusterLabels(v4Spec)
		}
	}

	// Surface the ArgoCD-registered API-server URL so the UI's
	// ClusterTypeBadge can classify EKS/AKS/GKE/kind/minikube instead of
	// always falling back to "Unknown" (V2-cleanup-80.1). Left unset for
	// the hub-local in-cluster entry, mirroring ListClusters/GetClusterDetail.
	if argocdClusters, err := ac.ListClusters(ctx); err != nil {
		log.Warn("could not fetch argocd clusters for server url", "cluster", clusterName, "error", err)
	} else {
		for _, argoCluster := range argocdClusters {
			if argoCluster.Name != clusterName {
				continue
			}
			if argoCluster.Name == "in-cluster" || strings.HasPrefix(argoCluster.Server, "https://kubernetes.default") {
				break
			}
			cluster.ServerURL = argoCluster.Server
			break
		}
	}

	gitAddons := s.parser.GetEnabledAddons(*cluster, repoCfg.Addons)

	// Fetch ArgoCD apps
	argocdSvc := argocd.NewService(ac)
	argocdApps, err := argocdSvc.GetClusterApplications(ctx, clusterName)
	if err != nil {
		log.Warn("could not fetch argocd apps", "error", err)
		argocdApps = nil
	}

	argocdAppMap := make(map[string]models.ArgocdApplication)
	for _, app := range argocdApps {
		argocdAppMap[app.Name] = app
	}

	// Build comparisons
	comparisons := make([]models.AddonComparisonStatus, 0)
	trackedArgocdApps := make(map[string]bool)

	totalHealthy, totalIssues, totalMissing := 0, 0, 0

	for _, addon := range gitAddons {
		// GetEnabledAddons now only returns enabled addons, so no need to check addon.Enabled
		comp := models.AddonComparisonStatus{
			AddonName:     addon.AddonName,
			GitConfigured: true,
			GitChart:      addon.Chart,
			// A chart repository URL is a repository URL: it is routinely
			// written with the credential in the userinfo section, whoever
			// wrote it. Same helper as the ArgoCD-sourced one (B7).
			GitRepoURL:         credsafe.SafeRepoURL(addon.RepoURL),
			GitVersion:         addon.CurrentVersion,
			GitNamespace:       addon.Namespace,
			GitEnabled:         true,
			EnvironmentVersion: addon.EnvironmentVersion,
			CustomVersion:      addon.CustomVersion,
			HasVersionOverride: addon.HasVersionOverride,
			Issues:             []string{},
		}

		// Try to find matching ArgoCD app
		appName := addon.AddonName + "-" + clusterName
		app, found := argocdAppMap[appName]
		if !found {
			app, found = argocdAppMap[addon.AddonName]
			if found {
				appName = addon.AddonName
			}
		}

		if found {
			trackedArgocdApps[appName] = true
			comp.ArgocdDeployed = true
			comp.ArgocdApplicationName = app.Name
			// Every ArgoCD-sourced value below goes through internal/credsafe
			// on its way onto the response (B7, B8). See safeAddonFailure.
			comp.ArgocdSyncStatus = credsafe.SafeSyncStatus(app.SyncStatus)
			comp.ArgocdHealthStatus = credsafe.SafeHealthStatus(app.HealthStatus)
			comp.ArgocdDeployedVersion = app.SourceTargetRevision
			comp.ArgocdNamespace = app.DestinationNamespace
			comp.ArgocdSourceRepoURL = credsafe.SafeRepoURL(app.SourceRepoURL)
			comp.ArgocdDestinationServer = credsafe.SafeRepoURL(app.DestinationServer)
			comp.ArgocdOperationState = credsafe.SafeOperationPhase(app.OperationState)

			var failing bool
			comp.Status, failing = classifyAddonApp(app)
			if failing {
				// Both fields carry Sharko's own sentence plus the facts
				// credsafe is willing to vouch for. They used to carry
				// ArgoCD's operationState.message — the short first line in
				// issues[] and the full 4000 characters here — and that
				// message quotes the repository ArgoCD was syncing from,
				// token and all, on an ordinary 200 response.
				comp.Issues = append(comp.Issues, credsafe.ArgocdSyncFailureShort)
				comp.ArgocdOperationMessage = safeAddonFailure(app)
			}
			switch comp.Status {
			case "healthy":
				totalHealthy++
			case "deploying":
				// Active rollout, no error — informational, not an issue.
				// Must stay in sync with the UI with_issues filter in
				// ui/src/views/ClusterDetail.tsx which also excludes "deploying".
			default:
				totalIssues++
			}
		} else {
			comp.Status = "missing_in_argocd"
			totalMissing++
		}

		comparisons = append(comparisons, comp)
	}

	// Find Sharko's own system apps that aren't catalog addons but should be
	// visible. Currently only the connectivity-check app: it IS GitOps-managed
	// (its ApplicationSet lives in the repo) but it is not a catalog addon.
	// Surface it with status "sharko_system" so operators understand what it is.
	//
	// Foreign apps (ArgoCD applications the user deployed themselves, unrelated
	// to Sharko's addon model) are intentionally SKIPPED — they are not addons
	// and must not appear in the addon list (V3-TX-B).
	connectivityCheckAppName := "connectivity-check-" + clusterName
	for appName, app := range argocdAppMap {
		if trackedArgocdApps[appName] {
			continue
		}
		// Skip known infrastructure apps that aren't addons
		if isInfrastructureApp(appName) {
			continue
		}
		// Sharko's own connectivity-check app: visible but distinct.
		if appName == connectivityCheckAppName {
			comparisons = append(comparisons, models.AddonComparisonStatus{
				AddonName:               appName,
				ArgocdDeployed:          true,
				ArgocdApplicationName:   app.Name,
				ArgocdSyncStatus:        credsafe.SafeSyncStatus(app.SyncStatus),
				ArgocdHealthStatus:      credsafe.SafeHealthStatus(app.HealthStatus),
				ArgocdDeployedVersion:   app.SourceTargetRevision,
				ArgocdNamespace:         app.DestinationNamespace,
				ArgocdSourceRepoURL:     credsafe.SafeRepoURL(app.SourceRepoURL),
				ArgocdDestinationServer: credsafe.SafeRepoURL(app.DestinationServer),
				Status:                  "sharko_system",
				Issues:                  []string{},
			})
			continue
		}
		// Foreign apps intentionally skipped — not addons (V3-TX-B).
	}
	totalUntracked := 0

	// ArgoCD summary stats
	argocdHealthy, argocdSynced, argocdDegraded, argocdOutOfSync := 0, 0, 0, 0
	for _, app := range argocdApps {
		if app.HealthStatus == "Healthy" {
			argocdHealthy++
		}
		if app.HealthStatus == "Degraded" {
			argocdDegraded++
		}
		if app.SyncStatus == "Synced" {
			argocdSynced++
		}
		if app.SyncStatus == "OutOfSync" {
			argocdOutOfSync++
		}
	}

	// connMessage is ArgoCD's own connectionMessage for this cluster: whatever
	// the Kubernetes client, the cloud provider's IAM layer or the transport
	// said, quoted in full. It never reaches the response — Sharko says its own
	// sentence instead, and the full text stays in the server-side log where
	// only an operator with log access can read it (B8).
	connStatus, _, connErr := argocdSvc.GetClusterConnectionInfo(ctx, clusterName)
	if connErr != nil {
		log.Warn("could not fetch argocd connection info", "cluster", clusterName, "error", connErr)
		if connStatus == "" {
			connStatus = "Unknown"
		}
	}
	connStatus = credsafe.SafeConnectionState(connStatus)
	connMessage := ""
	if connStatus == "Failed" || connStatus == credsafe.Unrecognised {
		connMessage = credsafe.ArgocdClusterConnectionFailureMessage
	}
	cluster.ConnectionStatus = connStatus

	return &models.ClusterComparisonResponse{
		Cluster:                     *cluster,
		GitTotalAddons:              len(gitAddons),
		GitEnabledAddons:            len(gitAddons),
		GitDisabledAddons:           0,
		ArgocdTotalApplications:     len(argocdApps),
		ArgocdHealthyApplications:   argocdHealthy,
		ArgocdSyncedApplications:    argocdSynced,
		ArgocdDegradedApplications:  argocdDegraded,
		ArgocdOutOfSyncApplications: argocdOutOfSync,
		AddonComparisons:            comparisons,
		TotalHealthy:                totalHealthy,
		TotalWithIssues:             totalIssues,
		TotalMissingInArgocd:        totalMissing,
		TotalUntrackedInArgocd:      totalUntracked,
		TotalDisabledInGit:          0,
		ClusterConnectionState:      connStatus,
		ArgocdConnectionStatus:      connStatus,
		ArgocdConnectionMessage:     connMessage,
	}, nil
}

// GetConfigDiff returns the diff between a cluster's addon values and global defaults.
func (s *ClusterService) GetConfigDiff(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ConfigDiffResponse, error) {
	if isV4Repo(ctx, gp, s.branch()) {
		return s.getConfigDiffV4(ctx, clusterName, gp)
	}
	log := logging.LoggerFromContext(ctx)
	// Fetch cluster values file
	clusterValuesPath := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	clusterValuesData, err := gp.GetFileContent(ctx, clusterValuesPath, s.branch())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster values for %s: %w", clusterName, err)
	}

	// Parse cluster values YAML
	var clusterConfig map[string]interface{}
	if err := yaml.Unmarshal(clusterValuesData, &clusterConfig); err != nil {
		return nil, fmt.Errorf("failed to parse cluster values: %w", err)
	}

	resp := &models.ConfigDiffResponse{
		ClusterName: clusterName,
		AddonDiffs:  []models.ConfigDiffEntry{},
	}

	// Extract clusterGlobalValues if present
	if globalVals, ok := clusterConfig["clusterGlobalValues"]; ok {
		if m, ok := globalVals.(map[string]interface{}); ok {
			resp.GlobalValues = m
		}
	}

	// Iterate over addon sections (everything except clusterGlobalValues)
	addonNames := make([]string, 0, len(clusterConfig))
	for key := range clusterConfig {
		if key == "clusterGlobalValues" {
			continue
		}
		addonNames = append(addonNames, key)
	}
	sort.Strings(addonNames)

	for _, addonName := range addonNames {
		addonSection := clusterConfig[addonName]

		// Marshal cluster addon values to YAML
		clusterYAML, err := yaml.Marshal(addonSection)
		if err != nil {
			log.Warn("could not marshal cluster values for addon", "addon", addonName, "error", err)
			continue
		}

		// Fetch global defaults for this addon
		globalPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
		globalData, err := gp.GetFileContent(ctx, globalPath, s.branch())
		globalYAML := ""
		if err != nil {
			log.Info("no global defaults found for addon", "addon", addonName, "error", err)
		} else {
			globalYAML = string(globalData)
		}

		clusterYAMLStr := string(clusterYAML)
		hasOverrides := strings.TrimSpace(clusterYAMLStr) != strings.TrimSpace(globalYAML)

		resp.AddonDiffs = append(resp.AddonDiffs, models.ConfigDiffEntry{
			AddonName:     addonName,
			HasOverrides:  hasOverrides,
			GlobalValues:  globalYAML,
			ClusterValues: clusterYAMLStr,
		})
	}

	return resp, nil
}

// getConfigDiffV4 is GetConfigDiff's v4 branch (v4 closing wave, final fix
// row). v3 keeps every addon's cluster override in one combined file
// (configuration/addons-clusters-values/<cluster>.yaml, one top-level key
// per addon plus a clusterGlobalValues scratch block); v4 gives each addon
// its own file at values/clusters/<cluster>/<addon>.yaml, with fleet-wide
// defaults at values/global/<addon>.yaml (design doc §2.2) instead of
// configuration/addons-global-values/<addon>.yaml. This mirrors GetClusterDetail
// and GetClusterComparison's existing isV4Repo dispatch in this same file.
//
// v3's clusterGlobalValues block is deliberately not carried into the v4
// response: migration_v3v4.go's splitV3ClusterValuesFile drops it during
// the v3-to-v4 migration with the note "nothing ever read it (it was a
// scratch area for YAML shortcuts)" — so there is nothing to surface as
// resp.GlobalValues on a v4 repo, and it stays unset (the field carries
// `omitempty`).
//
// A missing values/clusters/<cluster>/ directory means the cluster has no
// overrides for any addon yet — the ordinary state for a freshly-enabled
// cluster — and yields an empty AddonDiffs list, not an error, matching the
// "missing means empty" rule the v4 format uses everywhere else (design
// doc D16).
func (s *ClusterService) getConfigDiffV4(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ConfigDiffResponse, error) {
	log := logging.LoggerFromContext(ctx)
	resp := &models.ConfigDiffResponse{
		ClusterName: clusterName,
		AddonDiffs:  []models.ConfigDiffEntry{},
	}

	clusterDir := fmt.Sprintf("%s/%s", orchestrator.V4ClusterValuesDir, clusterName)
	entries, err := gp.ListDirectory(ctx, clusterDir, s.branch())
	if err != nil {
		if !isGitFileNotFound(err) {
			return nil, fmt.Errorf("listing %s: %w", clusterDir, err)
		}
		entries = nil
	}

	addonNames := make([]string, 0, len(entries))
	for _, name := range entries {
		if strings.HasSuffix(name, ".yaml") {
			addonNames = append(addonNames, strings.TrimSuffix(name, ".yaml"))
		}
	}
	sort.Strings(addonNames)

	for _, addonName := range addonNames {
		clusterValuesPath := fmt.Sprintf("%s/%s.yaml", clusterDir, addonName)
		clusterData, cErr := gp.GetFileContent(ctx, clusterValuesPath, s.branch())
		if cErr != nil {
			log.Warn("could not read cluster values for addon", "addon", addonName, "cluster", clusterName, "error", cErr)
			continue
		}

		globalPath := fmt.Sprintf("%s/%s.yaml", orchestrator.V4GlobalValuesDir, addonName)
		globalData, gErr := gp.GetFileContent(ctx, globalPath, s.branch())
		globalYAML := ""
		if gErr != nil {
			log.Info("no global defaults found for addon", "addon", addonName, "error", gErr)
		} else {
			globalYAML = string(globalData)
		}

		clusterYAMLStr := string(clusterData)
		hasOverrides := strings.TrimSpace(clusterYAMLStr) != strings.TrimSpace(globalYAML)

		resp.AddonDiffs = append(resp.AddonDiffs, models.ConfigDiffEntry{
			AddonName:     addonName,
			HasOverrides:  hasOverrides,
			GlobalValues:  globalYAML,
			ClusterValues: clusterYAMLStr,
		})
	}

	return resp, nil
}

func (s *ClusterService) computeHealthStats(gitClusters []models.Cluster, notInGit []models.Cluster) *models.ClusterHealthStats {
	stats := &models.ClusterHealthStats{
		TotalInGit: len(gitClusters),
		NotInGit:   len(notInGit),
	}

	for _, c := range gitClusters {
		switch c.ConnectionStatus {
		case "Successful", "connected":
			stats.Connected++
		case "Failed", "failed":
			stats.Failed++
		case "missing":
			stats.MissingFromArgoCD++
		}
	}

	return stats
}

// infrastructureAppPrefixes are ArgoCD app name prefixes for infrastructure
// components that are not managed via the addons catalog. These are excluded
// from the "untracked in ArgoCD" list in the comparison view.
var infrastructureAppPrefixes = []string{
	"karpenter-nodepool",
	"bootstrap-",
	"eso-",
	"cluster-addons",
	"clusters",
	"external-secrets-operator",
	"eso-remote-prerequisites",
	"github-repo-credentials",
}

func isInfrastructureApp(appName string) bool {
	lower := strings.ToLower(appName)
	for _, prefix := range infrastructureAppPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// GetClusterValues returns the raw cluster-specific values YAML.
func (s *ClusterService) GetClusterValues(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ClusterValuesResponse, error) {
	if isV4Repo(ctx, gp, s.branch()) {
		return s.getClusterValuesV4(ctx, clusterName, gp)
	}
	path := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	data, err := gp.GetFileContent(ctx, path, s.branch())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster values for %s: %w", clusterName, err)
	}

	return &models.ClusterValuesResponse{
		ClusterName: clusterName,
		ValuesYAML:  string(data),
	}, nil
}

// getClusterValuesV4 is GetClusterValues' v4 branch (v4 closing wave, final
// fix row). v3 keeps one combined file per cluster
// (configuration/addons-clusters-values/<cluster>.yaml) with one top-level
// key per addon; v4 splits that into one file per addon at
// values/clusters/<cluster>/<addon>.yaml (design doc §2.2). This combines
// every addon file back into the same shape — one top-level key per addon —
// so ValuesYAML keeps returning the same kind of document for either repo
// layout, and callers of this response never need to know which layout the
// repo uses.
//
// A missing values/clusters/<cluster>/ directory (cluster has no overrides
// yet) yields an empty ValuesYAML string, matching v3's own "no cluster
// file yet" behaviour for a brand-new cluster written straight to Git by
// hand outside Sharko.
func (s *ClusterService) getClusterValuesV4(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ClusterValuesResponse, error) {
	log := logging.LoggerFromContext(ctx)
	clusterDir := fmt.Sprintf("%s/%s", orchestrator.V4ClusterValuesDir, clusterName)
	entries, err := gp.ListDirectory(ctx, clusterDir, s.branch())
	if err != nil {
		if !isGitFileNotFound(err) {
			return nil, fmt.Errorf("listing %s: %w", clusterDir, err)
		}
		entries = nil
	}

	var yamlNames []string
	for _, name := range entries {
		if strings.HasSuffix(name, ".yaml") {
			yamlNames = append(yamlNames, name)
		}
	}
	sort.Strings(yamlNames)

	combined := make(map[string]interface{}, len(yamlNames))
	for _, name := range yamlNames {
		addonName := strings.TrimSuffix(name, ".yaml")
		data, rErr := gp.GetFileContent(ctx, fmt.Sprintf("%s/%s", clusterDir, name), s.branch())
		if rErr != nil {
			log.Warn("could not read cluster values file for addon", "addon", addonName, "cluster", clusterName, "error", rErr)
			continue
		}
		var val interface{}
		if uErr := yaml.Unmarshal(data, &val); uErr != nil {
			log.Warn("could not parse cluster values file for addon", "addon", addonName, "cluster", clusterName, "error", uErr)
			continue
		}
		combined[addonName] = val
	}

	valuesYAML := ""
	if len(combined) > 0 {
		marshalled, mErr := yaml.Marshal(combined)
		if mErr != nil {
			return nil, fmt.Errorf("marshalling combined cluster values for %s: %w", clusterName, mErr)
		}
		valuesYAML = string(marshalled)
	}

	return &models.ClusterValuesResponse{
		ClusterName: clusterName,
		ValuesYAML:  valuesYAML,
	}, nil
}

// GetClusterAddonValues extracts the YAML for one addon's section in a
// cluster's overrides file. CurrentOverrides is the empty string when the
// cluster file does not exist yet, or when the file exists but does not
// contain a section for this addon.
//
// Schema lookup mirrors AddonService.GetAddonValuesAndSchema — best-effort
// read of `configuration/addons-global-values/<addon>.schema.json`.
func (s *ClusterService) GetClusterAddonValues(ctx context.Context, clusterName, addonName string, gp gitprovider.GitProvider) (*models.ClusterAddonValuesResponse, error) {
	log := logging.LoggerFromContext(ctx)
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	resp := &models.ClusterAddonValuesResponse{
		ClusterName: clusterName,
		AddonName:   addonName,
	}

	clusterPath := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	if data, err := gp.GetFileContent(ctx, clusterPath, s.branch()); err == nil && len(data) > 0 {
		root := map[string]interface{}{}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			log.Warn("could not parse cluster overrides file", "cluster", clusterName, "error", uerr)
		} else if section, ok := root[addonName]; ok {
			if marshalled, merr := yaml.Marshal(section); merr == nil {
				resp.CurrentOverrides = string(marshalled)
			}
		}
	}

	schemaPath := fmt.Sprintf("configuration/addons-global-values/%s.schema.json", addonName)
	if schemaData, err := gp.GetFileContent(ctx, schemaPath, s.branch()); err == nil && len(schemaData) > 0 {
		var schema map[string]interface{}
		if jerr := json.Unmarshal(schemaData, &schema); jerr != nil {
			log.Warn("ignoring unparseable values schema", "addon", addonName, "error", jerr)
		} else {
			resp.Schema = schema
		}
	}

	return resp, nil
}

// classifyAddonApp derives the addon comparison status string from a live
// ArgoCD application, and says whether the operation is failing. Priority
// order:
//
//  1. sync_failing — op phase Failed|Error, OR phase Running AND
//     (any SyncFailed task OR message contains "completed unsuccessfully").
//  2. deploying — op phase Running OR health Progressing (no error signal).
//  3. Existing health-based mapping (healthy / unhealthy / unknown_health /
//     unknown_state).
//
// The function is the single source of truth for both the cluster comparison
// endpoint and the addon catalog endpoint so they stay in sync.
//
// # Why the second return value is a bool and not a message any more (B8)
//
// It used to hand back the first line of ArgoCD's operationState.message, and
// the caller put that straight into issues[] on a 200 response. ArgoCD's
// message quotes whatever it was syncing from, which includes the repository
// address with its access token inside it, so the message could not travel.
// Reading the message to CLASSIFY is fine and stays — that reading happens
// server-side and only a true/false comes back out. Nothing of the text
// escapes the function.
func classifyAddonApp(app models.ArgocdApplication) (status string, syncFailing bool) {
	phase := app.OperationPhase
	health := app.HealthStatus
	opMsg := app.OperationMessage

	// 1. Detect a permanently-failing operation.
	opFailed := phase == "Failed" || phase == "Error"
	opRunningWithFailure := phase == "Running" &&
		(app.HasSyncFailedResource || strings.Contains(opMsg, "completed unsuccessfully"))
	if opFailed || opRunningWithFailure {
		return "sync_failing", true
	}

	// 2. Active rollout — no error signal yet.
	if phase == "Running" || health == "Progressing" {
		return "deploying", false
	}

	// 3. Existing health mapping.
	return classifyHealth(health, ""), false
}

// safeAddonFailure is what argocd_operation_message carries now: Sharko's own
// fixed sentence plus the facts internal/credsafe is willing to vouch for.
//
// The whole of the ArgoCD text is left behind on purpose. It has no grammar —
// it is Helm's words, or the Kubernetes API server's, or a Git transport's,
// quoted verbatim — so there is no part of it Sharko can point at and say
// "this part is not the credential". Scanning it for things that look like
// secrets would be the text-matching approach this codebase bans, and it fails
// on the first format nobody predicted.
func safeAddonFailure(app models.ArgocdApplication) string {
	return credsafe.SafeOperationDetail(credsafe.ArgocdSyncFailureMessage, credsafe.OperationFacts{
		Phase:        app.OperationPhase,
		SyncStatus:   app.SyncStatus,
		HealthStatus: app.HealthStatus,
		RepoURL:      app.SourceRepoURL,
	})
}

func classifyHealth(healthStatus, _ string) string {
	switch healthStatus {
	case "Healthy":
		return "healthy"
	case "Progressing":
		return "progressing"
	case "Degraded":
		return "unhealthy"
	case "Unknown", "":
		return "unknown_health"
	default:
		return "unknown_state"
	}
}
