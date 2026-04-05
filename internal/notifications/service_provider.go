package notifications

import (
	"context"
	"log"

	"github.com/MoranWeissman/sharko/internal/service"
)

// ServiceProvider implements VersionProvider using the AddonService and
// ConnectionService already present in the server. It derives version drift
// by comparing each cluster's deployed version against the catalog version.
// Helm repo checks (LatestVersion) are deferred to a future enhancement.
type ServiceProvider struct {
	connSvc  *service.ConnectionService
	addonSvc *service.AddonService
}

// NewServiceProvider creates a ServiceProvider from the server's existing
// service instances. Both parameters are required.
func NewServiceProvider(connSvc *service.ConnectionService, addonSvc *service.AddonService) *ServiceProvider {
	return &ServiceProvider{
		connSvc:  connSvc,
		addonSvc: addonSvc,
	}
}

// GetVersionInfo implements VersionProvider. It fetches the version matrix
// and converts it into []VersionInfo that the Checker understands.
// When a provider is not configured yet (no active connection) it returns an
// empty slice without an error so the Checker stays idle rather than logging
// a spurious failure every 30 minutes.
func (p *ServiceProvider) GetVersionInfo(ctx context.Context) ([]VersionInfo, error) {
	gp, err := p.connSvc.GetActiveGitProvider()
	if err != nil {
		// No connection configured — not an error worth logging loudly.
		log.Printf("[notifications] provider: no active git provider, skipping check: %v", err)
		return nil, nil
	}

	ac, err := p.connSvc.GetActiveArgocdClient()
	if err != nil {
		log.Printf("[notifications] provider: no active ArgoCD client, skipping check: %v", err)
		return nil, nil
	}

	matrix, err := p.addonSvc.GetVersionMatrix(ctx, gp, ac)
	if err != nil {
		return nil, err
	}

	infos := make([]VersionInfo, 0, len(matrix.Addons))
	for _, row := range matrix.Addons {
		clusterVersions := make(map[string]string, len(row.Cells))
		for clusterName, cell := range row.Cells {
			clusterVersions[clusterName] = cell.Version
		}
		infos = append(infos, VersionInfo{
			AddonName:       row.AddonName,
			CatalogVersion:  row.CatalogVersion,
			ClusterVersions: clusterVersions,
			// LatestVersion is intentionally left empty; Helm repo checks are
			// a future enhancement (TODO: add Helm index lookup here).
		})
	}
	return infos, nil
}
