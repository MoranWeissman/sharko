package service

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// gitDerivedDashboardStats is the git-sourced half of GetStats — cluster
// names, valid "<addon>-<cluster>" ArgoCD Application names, and addon
// counts — computed by EITHER the v3 branch (managed-clusters.yaml +
// addons-catalog.yaml labels) or the v4 branch (managed-clusters.yaml +
// clusters/*.yaml + catalog.yaml delta), so the downstream
// ArgoCD-side stats code (cluster connectivity, application sync/health,
// bootstrap app) runs identically regardless of repo format (Wave 2
// ride-along w2-q6 item 4).
type gitDerivedDashboardStats struct {
	clusterNames       []string
	validAddonApps     map[string]bool
	totalAvailable     int
	enabledDeployments int
}

// DashboardService handles dashboard-related operations.
type DashboardService struct {
	parser              *config.Parser
	connSvc             *ConnectionService
	managedClustersPath string // path in Git repo to managed-clusters.yaml

	// curated is the shipped curated addon catalog, wired via
	// SetCuratedCatalog. Used ONLY by gitStatsV4 to compute TotalAvailable
	// as len(catalog.MergeDelta(curated, delta)) — the same curated+delta
	// merge AddonService/UpgradeService use — instead of len(delta.Addons)
	// alone, which undercounts (or on a repo with no delta file at all,
	// zeroes) the addon count on every v4 repo that hasn't customized the
	// shipped catalog (Wave 2 review finding: "Total Available: 0" on
	// fresh v4 repos). nil is safe: every addon then merges as
	// catalog.OriginInternal, matching catalog.MergeDelta's own contract.
	curated *catalog.Catalog

	// baseBranchFn is the per-instance test seam for the configured GitOps
	// base branch, mirroring AddonService.baseBranchFn (Wave 2 ride-along
	// w2-q6 item 1). nil (tests, e2e harness) falls back to "main" via the
	// branch() helper below.
	baseBranchFn func() string
}

// SetCuratedCatalog wires in the shipped curated catalog so gitStatsV4 can
// merge a caller's catalog.yaml delta against it, the same way
// AddonService.SetCuratedCatalog does for the version matrix and catalog
// views. Pass nil (or skip the call) to leave every v4-repo addon merging
// as catalog.OriginInternal.
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
		validAddonApps: make(map[string]bool),
		totalAvailable: len(repoCfg.Addons),
	}
	for _, c := range repoCfg.Clusters {
		out.clusterNames = append(out.clusterNames, c.Name)
	}
	for _, cluster := range repoCfg.Clusters {
		for _, addon := range repoCfg.Addons {
			if cluster.Labels[addon.Name] == "enabled" {
				out.validAddonApps[addon.Name+"-"+cluster.Name] = true
				out.enabledDeployments++
			}
		}
	}
	return out, nil
}

// gitStatsV4 computes gitDerivedDashboardStats from the v4 file shape
// (managed-clusters.yaml for the cluster registry, clusters/*.yaml for
// per-cluster addon enablement, catalog.yaml for the delta's addon
// count) — the v4-aware counterpart the review flagged as missing (Wave 2
// ride-along w2-q6 item 4: "dashboard git-side stats v3-only"). Mirrors
// AddonService.getVersionMatrixV4's "missing means empty" tolerance for
// every file: a fresh v4 repo with no clusters or delta entries yet
// degrades to zeroed stats rather than a 500.
func (s *DashboardService) gitStatsV4(ctx context.Context, gp gitprovider.GitProvider) (gitDerivedDashboardStats, error) {
	out := gitDerivedDashboardStats{validAddonApps: make(map[string]bool)}

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

	// TotalAvailable is the curated+delta merged addon count — the same
	// merge AddonService/UpgradeService use — NOT len(delta.Addons) alone.
	// A repo that hasn't customized the shipped catalog yet (no
	// catalog.yaml, or one that only overrides a subset) still has
	// every curated addon "available"; counting only the delta reported 0
	// on every fresh v4 repo (Wave 2 review finding).
	deltaData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.branch())
	var delta config.AddonCatalogSpec
	if err != nil {
		if !isGitFileNotFound(err) {
			return gitDerivedDashboardStats{}, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
	} else if len(deltaData) > 0 {
		delta, err = config.LoadAddonCatalog(deltaData)
		if err != nil {
			return gitDerivedDashboardStats{}, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
		}
	}
	merged, err := catalog.MergeDelta(s.curated, delta)
	if err != nil {
		return gitDerivedDashboardStats{}, fmt.Errorf("merging catalog delta: %w", err)
	}
	out.totalAvailable = len(merged)

	// listClusterAddonsSpecs (addon.go, same package) lists clusters/*.yaml
	// and is already "missing dir means empty" tolerant.
	clusterAddons, err := listClusterAddonsSpecs(ctx, gp, s.branch())
	if err != nil {
		return gitDerivedDashboardStats{}, fmt.Errorf("reading clusters/*.yaml: %w", err)
	}
	for clusterName, spec := range clusterAddons {
		for addonName, addon := range spec.Addons {
			if addon.Enabled {
				out.validAddonApps[addonName+"-"+clusterName] = true
				out.enabledDeployments++
			}
		}
	}

	return out, nil
}

// GetStats returns aggregated dashboard statistics.
func (s *DashboardService) GetStats(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.DashboardStatisticsResponse, error) {
	log := logging.LoggerFromContext(ctx)
	// Connection stats
	connList, err := s.connSvc.List()
	if err != nil {
		return nil, err
	}
	connStats := models.DashboardConnectionStats{
		Total:  len(connList.Connections),
		Active: connList.ActiveConnection,
	}

	// v4-repo detection: identical probe to AddonService.GetVersionMatrix
	// (orchestrator.EnginePinPath resolving to non-empty content on the
	// base branch) — "no pin found" is the ordinary "not a v4 repo yet"
	// case, never a hard failure.
	var gitStats gitDerivedDashboardStats
	if pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, s.branch()); pinErr == nil && len(pinContent) > 0 {
		gitStats, err = s.gitStatsV4(ctx, gp)
	} else {
		gitStats, err = s.gitStatsV3(ctx, gp)
	}
	if err != nil {
		return nil, err
	}

	// Cluster stats from ArgoCD
	argocdClusters, err := ac.ListClusters(ctx)
	clusterStats := models.DashboardClusterStats{
		Total: len(gitStats.clusterNames),
	}
	if err == nil {
		argocdMap := make(map[string]bool)
		for _, c := range argocdClusters {
			if c.ConnectionState == "Successful" {
				argocdMap[c.Name] = true
			}
		}
		for _, name := range gitStats.clusterNames {
			if argocdMap[name] {
				clusterStats.ConnectedToArgocd++
			}
		}
		clusterStats.DisconnectedFromArgocd = clusterStats.Total - clusterStats.ConnectedToArgocd
	} else {
		log.Warn("could not fetch argocd clusters for dashboard", "error", err)
	}

	// Application stats from ArgoCD — only count addon apps (not bootstrap/infrastructure)
	// Addon apps follow the pattern: {addon-name}-{cluster-name}
	appStats := models.DashboardApplicationStats{}
	apps, err := ac.ListApplications(ctx)
	if err == nil {
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
		log.Warn("could not fetch argocd applications for dashboard", "error", err)
	}

	// Addon stats — only count enabled deployments
	addonStats := models.DashboardAddonStats{
		TotalAvailable:     gitStats.totalAvailable,
		EnabledDeployments: gitStats.enabledDeployments,
	}
	addonStats.TotalDeployments = addonStats.EnabledDeployments

	// Bootstrap app health — the root ArgoCD application that drives all addon deployments.
	// If unreachable, report "Unknown" rather than failing the whole dashboard request.
	bootstrapHealth := "Unknown"
	bootstrapSync := "Unknown"
	// v4 Wave 1 Story 4.2: the canonical bootstrap app is the engine pin
	// (orchestrator.BootstrapRootAppName = "sharko-engine"), not the old
	// v3 "cluster-addons-bootstrap" AppSet-fanout root. Reading the
	// literal here would silently report every v4 repo's dashboard tile
	// as "bootstrap missing" even when the engine is healthy.
	//
	// Both names are tried, current first: a cluster bootstrapped before the
	// v4 rename still runs "cluster-addons-bootstrap", and reading only the
	// new name would show its dashboard tile as Unknown forever.
	var bootstrapApp *models.ArgocdApplication
	var lastErr error
	for _, name := range orchestrator.BootstrapAppNames() {
		app, err := ac.GetApplication(ctx, name)
		if err == nil && app != nil {
			bootstrapApp = app
			lastErr = nil
			break
		}
		if err != nil {
			lastErr = err
		}
	}
	if bootstrapApp != nil {
		if bootstrapApp.HealthStatus != "" {
			bootstrapHealth = bootstrapApp.HealthStatus
		}
		if bootstrapApp.SyncStatus != "" {
			bootstrapSync = bootstrapApp.SyncStatus
		}
	} else if lastErr != nil {
		log.Warn("could not fetch bootstrap app status for dashboard", "error", lastErr)
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
