package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/readcache"
)

// dashboardStatsCacheKey is the readcache key for GetStats (perf M1).
const dashboardStatsCacheKey = "dashboard:stats"

// gitDerivedDashboardStats is the git-sourced half of GetStats — cluster
// names, valid "<addon>-<cluster>" ArgoCD Application names, and addon
// counts — computed by EITHER the v3 branch (managed-clusters.yaml +
// addons-catalog.yaml labels) or the v4 branch (managed-clusters.yaml +
// cluster-addons/*.yaml + the approved list in catalog.yaml), so the downstream
// ArgoCD-side stats code (cluster connectivity, application sync/health,
// bootstrap app) runs identically regardless of repo format (Wave 2
// ride-along w2-q6 item 4).
type gitDerivedDashboardStats struct {
	clusterNames       []string
	validAddonApps     map[string]bool
	totalAvailable     int
	enabledDeployments int
	// enabledCountByCluster is the enabled-addon count per cluster name —
	// needed to tell an "untested" cluster (Unknown/"" ArgoCD state, zero
	// enabled addons — nothing for ArgoCD to probe, ever) apart from a
	// "pending" one (same ArgoCD state, but addons ARE enabled, so a probe
	// result is genuinely on the way). Dashboard UX review 2026-08-01,
	// blocker B1.
	enabledCountByCluster map[string]int
}

// DashboardService handles dashboard-related operations.
type DashboardService struct {
	parser              *config.Parser
	connSvc             *ConnectionService
	managedClustersPath string // path in Git repo to managed-clusters.yaml

	// curated is the Marketplace's curated addon list, wired via
	// SetCuratedCatalog. gitStatsV4 hands it to catalog.BuildCatalogView so
	// an approved addon the Marketplace happens to know by name picks up a
	// description and a docs link. It NEVER adds anything to the count:
	// TotalAvailable is the size of the org's approved list, and a fresh v4
	// repo with an empty catalog.yaml correctly shows zero — that is the
	// whole point of the approved-list model. nil is safe: every approved
	// addon then reads as catalog.OriginInternal.
	curated *catalog.Catalog

	// baseBranchFn is the per-instance test seam for the configured GitOps
	// base branch, mirroring AddonService.baseBranchFn (Wave 2 ride-along
	// w2-q6 item 1). nil (tests, e2e harness) falls back to "main" via the
	// branch() helper below.
	baseBranchFn func() string

	// cache is the per-instance TTL cache for GetStats (perf M1).
	// NewDashboardService gives every instance its own private cache by
	// default; SetCache overrides it with a shared instance so production
	// code can invalidate every cached endpoint from one call (api.Server
	// wires ONE *readcache.Cache into every service that needs it via
	// NewServer). Tests that never call SetCache keep their own unshared
	// cache and can't leak state into other tests — see internal/readcache's
	// package doc for why this is per-instance rather than a package-level
	// global.
	cache *readcache.Cache
}

// SetCache replaces this service's read cache with a shared instance (see
// internal/readcache and the AddonService/ClusterService/ObservabilityService
// analogues).
func (s *DashboardService) SetCache(c *readcache.Cache) {
	s.cache = c
}

// SetCuratedCatalog wires in the Marketplace's curated list so gitStatsV4
// can fill in the display fields for an approved addon it recognises, the
// same way AddonService.SetCuratedCatalog does for the version matrix and
// the catalog views. Pass nil (or skip the call) to leave every approved
// addon reading as catalog.OriginInternal.
func (s *DashboardService) SetCuratedCatalog(c *catalog.Catalog) {
	s.curated = c
}

// SetBaseBranchFn wires in a live accessor for the configured GitOps base
// branch (e.g. api.Server.GitopsBaseBranch), matching
// AddonService.SetBaseBranchFn.
func (s *DashboardService) SetBaseBranchFn(fn func() string) {
	s.baseBranchFn = fn
}

// branch returns the configured GitOps base branch, defaulting to "main"
// when no seam is wired (tests, e2e harness) or it resolves to "".
func (s *DashboardService) branch() string {
	if s.baseBranchFn == nil {
		return "main"
	}
	if b := s.baseBranchFn(); b != "" {
		return b
	}
	return "main"
}

// NewDashboardService creates a new DashboardService.
// managedClustersPath is the Git repo path to managed-clusters.yaml.
// An empty string defaults to "configuration/managed-clusters.yaml".
func NewDashboardService(connSvc *ConnectionService, managedClustersPath string) *DashboardService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &DashboardService{
		parser:              config.NewParser(),
		connSvc:             connSvc,
		managedClustersPath: managedClustersPath,
		cache:               readcache.New(readcache.DefaultTTL),
	}
}

// gitStatsV3 computes gitDerivedDashboardStats from the v3 file shape
// (managed-clusters.yaml labels + addons-catalog.yaml) — the ORIGINAL
// GetStats body, unchanged behavior on a v3 repo.
func (s *DashboardService) gitStatsV3(ctx context.Context, gp gitprovider.GitProvider) (gitDerivedDashboardStats, error) {
	// Missing file (fresh-install gitops repo) degrades to empty stats
	// rather than propagating a 500 with the raw filesystem error
	// string. Same isGitFileNotFound (errors.Is) pattern as
	// ClusterService.ListClusters.
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return gitDerivedDashboardStats{}, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return gitDerivedDashboardStats{}, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return gitDerivedDashboardStats{}, err
	}

	out := gitDerivedDashboardStats{
		validAddonApps:        make(map[string]bool),
		totalAvailable:        len(repoCfg.Addons),
		enabledCountByCluster: make(map[string]int),
	}
	for _, c := range repoCfg.Clusters {
		out.clusterNames = append(out.clusterNames, c.Name)
	}
	for _, cluster := range repoCfg.Clusters {
		for _, addon := range repoCfg.Addons {
			if cluster.Labels[addon.Name] == "enabled" {
				out.validAddonApps[addon.Name+"-"+cluster.Name] = true
				out.enabledDeployments++
				out.enabledCountByCluster[cluster.Name]++
			}
		}
	}
	return out, nil
}

// gitStatsV4 computes gitDerivedDashboardStats from the v4 file shape
// (managed-clusters.yaml for the cluster registry, cluster-addons/*.yaml for
// per-cluster addon enablement, catalog.yaml for how many addons the org
// approved) — the v4-aware counterpart the review flagged as missing (Wave
// 2 ride-along w2-q6 item 4: "dashboard git-side stats v3-only"). Mirrors
// AddonService.getVersionMatrixV4's "missing means empty" tolerance for
// every file: a fresh v4 repo with no clusters and nothing approved yet
// degrades to zeroed stats rather than a 500.
func (s *DashboardService) gitStatsV4(ctx context.Context, gp gitprovider.GitProvider) (gitDerivedDashboardStats, error) {
	out := gitDerivedDashboardStats{
		validAddonApps:        make(map[string]bool),
		enabledCountByCluster: make(map[string]int),
	}

	connData, err := gp.GetFileContent(ctx, orchestrator.V4ManagedClustersPath, s.branch())
	if err != nil {
		if !isGitFileNotFound(err) {
			return gitDerivedDashboardStats{}, fmt.Errorf("reading %s: %w", orchestrator.V4ManagedClustersPath, err)
		}
	} else if len(connData) > 0 {
		spec, perr := models.LoadManagedClusters(connData)
		if perr != nil {
			return gitDerivedDashboardStats{}, fmt.Errorf("parsing %s: %w", orchestrator.V4ManagedClustersPath, perr)
		}
		for _, c := range spec.Clusters {
			out.clusterNames = append(out.clusterNames, c.Name)
		}
	}

	// TotalAvailable is how many addons the org APPROVED — the number of
	// entries in catalog.yaml. It is not the size of the list Sharko
	// ships. A fresh repo shows 0 on purpose: nothing is available to your
	// clusters until somebody adds it, and a tile claiming 45 available
	// addons on day zero is exactly the lie this rebuild removes.
	approvedData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.branch())
	var approved config.AddonCatalogSpec
	if err != nil {
		if !isGitFileNotFound(err) {
			return gitDerivedDashboardStats{}, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
	} else if len(approvedData) > 0 {
		approved, err = config.LoadAddonCatalog(approvedData)
		if err != nil {
			return gitDerivedDashboardStats{}, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
		}
	}
	out.totalAvailable = len(catalog.BuildCatalogView(s.curated, approved))

	// listClusterAddonsSpecs (addon.go, same package) lists cluster-addons/*.yaml
	// and is already "missing dir means empty" tolerant.
	clusterAddons, err := listClusterAddonsSpecs(ctx, gp, s.branch())
	if err != nil {
		return gitDerivedDashboardStats{}, fmt.Errorf("reading cluster-addons/*.yaml: %w", err)
	}
	for clusterName, spec := range clusterAddons {
		for addonName, addon := range spec.Addons {
			if addon.Enabled {
				out.validAddonApps[addonName+"-"+clusterName] = true
				out.enabledDeployments++
				out.enabledCountByCluster[clusterName]++
			}
		}
	}

	return out, nil
}

// GetStats returns aggregated dashboard statistics. Cached for
// readcache.DefaultTTL (perf M1) — see internal/readcache's package doc
// for the invalidation contract. Fleet-wide, not per-user: every
// authenticated caller sees the same connection/cluster/addon counts, so a
// single cache entry (not keyed by role/user) is safe.
func (s *DashboardService) GetStats(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.DashboardStatisticsResponse, error) {
	return readcache.GetOrCompute(s.cache, dashboardStatsCacheKey, func() (*models.DashboardStatisticsResponse, error) {
		return s.getStatsUncached(ctx, gp, ac)
	})
}

// getStatsUncached is GetStats' actual computation, unchanged in output
// from before perf M1/M2. The only behavior change is perf M2: the four
// independent upstream reads below (gitStats, the ArgoCD cluster list, the
// ArgoCD application list, and the bootstrap-app probe) used to run one
// after another; none of them consume another's result, so they now run
// concurrently and are combined once every goroutine has returned. Each
// keeps its EXACT original error handling (gitStats failure still fails
// the whole call; the two ArgoCD list failures still degrade to a
// log.Warn + zeroed stats; the bootstrap probe still tries every name in
// orchestrator.BootstrapAppNames() and keeps the last error).
func (s *DashboardService) getStatsUncached(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.DashboardStatisticsResponse, error) {
	log := logging.LoggerFromContext(ctx)
	// Connection stats — local config-store read, not worth fanning out.
	connList, err := s.connSvc.List()
	if err != nil {
		return nil, err
	}
	connStats := models.DashboardConnectionStats{
		Total:  len(connList.Connections),
		Active: connList.ActiveConnection,
	}

	var (
		gitStats       gitDerivedDashboardStats
		gitStatsErr    error
		argocdClusters []models.ArgocdCluster
		clustersErr    error
		apps           []models.ArgocdApplication
		appsErr        error
		bootstrapApp   *models.ArgocdApplication
		bootstrapErr   error
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		// v4-repo detection: identical probe to AddonService.GetVersionMatrix
		// (orchestrator.EnginePinPath resolving to non-empty content on the
		// base branch) — "no pin found" is the ordinary "not a v4 repo yet"
		// case, never a hard failure.
		if pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, s.branch()); pinErr == nil && len(pinContent) > 0 {
			gitStats, gitStatsErr = s.gitStatsV4(ctx, gp)
		} else {
			gitStats, gitStatsErr = s.gitStatsV3(ctx, gp)
		}
	}()

	go func() {
		defer wg.Done()
		argocdClusters, clustersErr = ac.ListClusters(ctx)
	}()

	go func() {
		defer wg.Done()
		apps, appsErr = ac.ListApplications(ctx)
	}()

	go func() {
		defer wg.Done()
		// v4 Wave 1 Story 4.2: the canonical bootstrap app is the engine
		// pin (orchestrator.BootstrapRootAppName = "sharko-engine"), not
		// the old v3 "cluster-addons-bootstrap" AppSet-fanout root. Both
		// names are tried, current first: a cluster bootstrapped before
		// the v4 rename still runs "cluster-addons-bootstrap", and
		// reading only the new name would show its dashboard tile as
		// Unknown forever.
		for _, name := range orchestrator.BootstrapAppNames() {
			app, err := ac.GetApplication(ctx, name)
			if err == nil && app != nil {
				bootstrapApp = app
				bootstrapErr = nil
				break
			}
			if err != nil {
				bootstrapErr = err
			}
		}
	}()

	wg.Wait()

	if gitStatsErr != nil {
		return nil, gitStatsErr
	}

	// Cluster stats from ArgoCD — five-state breakdown (connected/pending/
	// untested/missing/failed), the SAME vocabulary as
	// ui/src/lib/clusterStatus.ts's classifyClusterConnection(). This is the
	// single place the "Successful"/"Connected" string mapping lives
	// server-side (dashboard UX review 2026-08-01, blocker B1 + the
	// "server vs clusterStatus.ts:127" alignment note): the server now
	// accepts both spellings just like the UI classifier does, instead of
	// only "Successful".
	clusterStats := models.DashboardClusterStats{
		Total: len(gitStats.clusterNames),
	}
	if clustersErr == nil {
		// ArgoCD's cluster-list API only returns an entry for a cluster
		// that HAS a cluster secret registered — a Git-registered cluster
		// with no ArgoCD secret at all produces no entry, mirroring
		// ClusterService.ListClusters's "missing" bucket (internal/service/
		// cluster.go).
		argocdMap := make(map[string]models.ArgocdCluster, len(argocdClusters))
		for _, c := range argocdClusters {
			argocdMap[c.Name] = c
		}
		for _, name := range gitStats.clusterNames {
			entry, ok := argocdMap[name]
			if !ok {
				clusterStats.Missing++
				continue
			}
			switch entry.ConnectionState {
			case "Successful", "Connected":
				clusterStats.Connected++
			case "", "Unknown":
				if gitStats.enabledCountByCluster[name] == 0 {
					// Nothing enabled on this cluster — ArgoCD has
					// nothing to probe and never will until an addon
					// is turned on. Neutral "untested", not "pending".
					clusterStats.Untested++
				} else {
					clusterStats.Pending++
				}
			default:
				clusterStats.Failed++
			}
		}
	} else {
		log.Warn("could not fetch argocd clusters for dashboard", "error", clustersErr)
	}

	// Application stats from ArgoCD — only count addon apps (not bootstrap/infrastructure)
	// Addon apps follow the pattern: {addon-name}-{cluster-name}
	appStats := models.DashboardApplicationStats{}
	if appsErr == nil {
		for _, app := range apps {
			if !gitStats.validAddonApps[app.Name] {
				continue
			}

			appStats.Total++
			switch app.SyncStatus {
			case "Synced":
				appStats.BySyncStatus.Synced++
			case "OutOfSync":
				appStats.BySyncStatus.OutOfSync++
			default:
				appStats.BySyncStatus.Unknown++
			}

			switch app.HealthStatus {
			case "Healthy":
				appStats.ByHealthStatus.Healthy++
			case "Progressing":
				appStats.ByHealthStatus.Progressing++
			case "Degraded":
				appStats.ByHealthStatus.Degraded++
			default:
				appStats.ByHealthStatus.Unknown++
			}
		}
	} else {
		log.Warn("could not fetch argocd applications for dashboard", "error", appsErr)
	}

	// Addon stats — EnabledDeployments is a plain count of addons enabled
	// on some cluster, as configured in Git. No fake "N/N" ratio (dashboard
	// UX review 2026-08-01, finding H5: the old TotalDeployments field was
	// always set equal to EnabledDeployments, so the card always read N/N).
	addonStats := models.DashboardAddonStats{
		TotalAvailable:     gitStats.totalAvailable,
		EnabledDeployments: gitStats.enabledDeployments,
	}

	// Bootstrap app health — the root ArgoCD application that drives all addon deployments.
	// If unreachable, report "Unknown" rather than failing the whole dashboard request.
	bootstrapHealth := "Unknown"
	bootstrapSync := "Unknown"
	if bootstrapApp != nil {
		if bootstrapApp.HealthStatus != "" {
			bootstrapHealth = bootstrapApp.HealthStatus
		}
		if bootstrapApp.SyncStatus != "" {
			bootstrapSync = bootstrapApp.SyncStatus
		}
	} else if bootstrapErr != nil {
		log.Warn("could not fetch bootstrap app status for dashboard", "error", bootstrapErr)
	}

	return &models.DashboardStatisticsResponse{
		Connections:        connStats,
		Clusters:           clusterStats,
		Applications:       appStats,
		Addons:             addonStats,
		BootstrapAppHealth: bootstrapHealth,
		BootstrapAppSync:   bootstrapSync,
	}, nil
}

// GetPullRequests returns active and completed PRs from the Git provider.
func (s *DashboardService) GetPullRequests(ctx context.Context, gp gitprovider.GitProvider) (*models.DashboardPullRequestsResponse, error) {
	activePRs, err := gp.ListPullRequests(ctx, "open")
	if err != nil {
		return nil, err
	}

	closedPRs, err := gp.ListPullRequests(ctx, "closed")
	if err != nil {
		return nil, err
	}

	toModel := func(prs []gitprovider.PullRequest) []models.PullRequest {
		result := make([]models.PullRequest, 0, len(prs))
		for _, pr := range prs {
			result = append(result, models.PullRequest{
				ID:           pr.ID,
				Title:        pr.Title,
				Description:  pr.Description,
				Author:       pr.Author,
				Status:       pr.Status,
				SourceBranch: pr.SourceBranch,
				TargetBranch: pr.TargetBranch,
				URL:          pr.URL,
				CreatedAt:    pr.CreatedAt,
				UpdatedAt:    pr.UpdatedAt,
				ClosedAt:     pr.ClosedAt,
			})
		}
		return result
	}

	return &models.DashboardPullRequestsResponse{
		ActivePRs:    toModel(activePRs),
		CompletedPRs: toModel(closedPRs),
	}, nil
}
