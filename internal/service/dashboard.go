package service

import (
	"context"
	"log"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// DashboardService handles dashboard-related operations.
type DashboardService struct {
	parser    *config.Parser
	connSvc   *ConnectionService
}

// NewDashboardService creates a new DashboardService.
func NewDashboardService(connSvc *ConnectionService) *DashboardService {
	return &DashboardService{
		parser:  config.NewParser(),
		connSvc: connSvc,
	}
}

// GetStats returns aggregated dashboard statistics.
func (s *DashboardService) GetStats(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.DashboardStatisticsResponse, error) {
	// Connection stats
	connList, err := s.connSvc.List()
	if err != nil {
		return nil, err
	}
	connStats := models.DashboardConnectionStats{
		Total:  len(connList.Connections),
		Active: connList.ActiveConnection,
	}

	// Parse Git config
	clusterData, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return nil, err
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, err
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return nil, err
	}

	// Cluster stats from ArgoCD
	argocdClusters, err := ac.ListClusters(ctx)
	clusterStats := models.DashboardClusterStats{
		Total: len(repoCfg.Clusters),
	}
	if err == nil {
		argocdMap := make(map[string]bool)
		for _, c := range argocdClusters {
			if c.ConnectionState == "Successful" {
				argocdMap[c.Name] = true
			}
		}
		for _, c := range repoCfg.Clusters {
			if argocdMap[c.Name] {
				clusterStats.ConnectedToArgocd++
			}
		}
		clusterStats.DisconnectedFromArgocd = clusterStats.Total - clusterStats.ConnectedToArgocd
	} else {
		log.Printf("Warning: could not fetch ArgoCD clusters for dashboard: %v", err)
	}

	// Application stats from ArgoCD — only count addon apps (not bootstrap/infrastructure)
	// Addon apps follow the pattern: {addon-name}-{cluster-name}
	// Build a set of valid addon app names from catalog × clusters
	validAddonApps := make(map[string]bool)
	for _, addon := range repoCfg.Addons {
		for _, cluster := range repoCfg.Clusters {
			if cluster.Labels[addon.AppName] == "enabled" {
				validAddonApps[addon.AppName+"-"+cluster.Name] = true
			}
		}
	}

	appStats := models.DashboardApplicationStats{}
	apps, err := ac.ListApplications(ctx)
	if err == nil {
		for _, app := range apps {
			if !validAddonApps[app.Name] {
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
		log.Printf("Warning: could not fetch ArgoCD applications for dashboard: %v", err)
	}

	// Addon stats — only count enabled deployments
	addonStats := models.DashboardAddonStats{
		TotalAvailable: len(repoCfg.Addons),
	}
	for _, cluster := range repoCfg.Clusters {
		for _, addon := range repoCfg.Addons {
			if cluster.Labels[addon.AppName] == "enabled" {
				addonStats.EnabledDeployments++
			}
		}
	}
	addonStats.TotalDeployments = addonStats.EnabledDeployments

	return &models.DashboardStatisticsResponse{
		Connections:  connStats,
		Clusters:     clusterStats,
		Applications: appStats,
		Addons:       addonStats,
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
