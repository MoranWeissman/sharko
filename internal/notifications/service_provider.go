package notifications

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/service"
)

// helmIndexCache caches a fetcher per repo URL with a timestamp so we only
// refetch the index once per checker interval (~30 min). The helm.Fetcher
// already caches the parsed index in-memory, but we want an explicit TTL to
// handle cases where the same Fetcher is reused across many check cycles.
type helmIndexEntry struct {
	fetchedAt time.Time
	fetcher   *helm.Fetcher
}

// ServiceProvider implements VersionProvider using the AddonService and
// ConnectionService already present in the server. It derives version drift
// by comparing each cluster's deployed version against the catalog version,
// and fetches the latest Helm chart version from each addon's Helm repo.
type ServiceProvider struct {
	connSvc  *service.ConnectionService
	addonSvc *service.AddonService

	mu          sync.Mutex
	indexCache  map[string]*helmIndexEntry // key: repoURL
	cacheTTL    time.Duration
}

// NewServiceProvider creates a ServiceProvider from the server's existing
// service instances. Both parameters are required.
func NewServiceProvider(connSvc *service.ConnectionService, addonSvc *service.AddonService) *ServiceProvider {
	return &ServiceProvider{
		connSvc:    connSvc,
		addonSvc:   addonSvc,
		indexCache: make(map[string]*helmIndexEntry),
		cacheTTL:   30 * time.Minute,
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

	// Also load the catalog so we know each addon's repoURL and chart name for
	// Helm version lookups.
	catalog, err := p.addonSvc.ListAddons(ctx, gp)
	if err != nil {
		log.Printf("[notifications] provider: could not load addon catalog, skipping Helm version check: %v", err)
		catalog = nil
	}

	// Build a quick lookup from addon name → catalog entry.
	type catalogKey struct {
		repoURL string
		chart   string
	}
	catalogByName := make(map[string]catalogKey, len(catalog))
	for _, entry := range catalog {
		if entry.RepoURL != "" && entry.Chart != "" {
			catalogByName[entry.Name] = catalogKey{repoURL: entry.RepoURL, chart: entry.Chart}
		}
	}

	infos := make([]VersionInfo, 0, len(matrix.Addons))
	for _, row := range matrix.Addons {
		clusterVersions := make(map[string]string, len(row.Cells))
		for clusterName, cell := range row.Cells {
			clusterVersions[clusterName] = cell.Version
		}

		info := VersionInfo{
			AddonName:       row.AddonName,
			CatalogVersion:  row.CatalogVersion,
			ClusterVersions: clusterVersions,
		}

		// Fetch latest Helm version if the addon has a Helm repo (not git-path based).
		if key, ok := catalogByName[row.AddonName]; ok {
			latest, fetchErr := p.fetchLatestVersion(ctx, key.repoURL, key.chart)
			if fetchErr != nil {
				log.Printf("[notifications] provider: could not fetch latest version for %s from %s: %v",
					row.AddonName, key.repoURL, fetchErr)
			} else {
				info.LatestVersion = latest
			}
		}

		infos = append(infos, info)
	}
	return infos, nil
}

// fetchLatestVersion returns the latest version of chartName from repoURL,
// using a time-bounded cache so the same index is not fetched repeatedly
// within the cache TTL window.
func (p *ServiceProvider) fetchLatestVersion(ctx context.Context, repoURL, chartName string) (string, error) {
	fetcher := p.getFetcher(repoURL)

	versions, err := fetcher.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", nil
	}

	// Helm repo index entries are sorted newest-first by convention, but we
	// sort by semver to be safe.
	sorted := make([]string, 0, len(versions))
	for _, v := range versions {
		if v.Version != "" {
			sorted = append(sorted, v.Version)
		}
	}
	if len(sorted) == 0 {
		return "", nil
	}
	sort.Slice(sorted, func(i, j int) bool {
		return semverGreater(sorted[i], sorted[j])
	})
	return sorted[0], nil
}

// getFetcher returns a cached helm.Fetcher for the given repoURL. A new
// Fetcher (with a fresh in-memory index cache) is created when the TTL expires,
// forcing a re-fetch of the repo index on the next ListVersions call.
func (p *ServiceProvider) getFetcher(repoURL string) *helm.Fetcher {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.indexCache[repoURL]
	if ok && time.Since(entry.fetchedAt) < p.cacheTTL {
		return entry.fetcher
	}

	// Create a fresh Fetcher; this also clears the in-memory index cache for
	// this repo so the next call will refetch index.yaml.
	f := helm.NewFetcher()
	p.indexCache[repoURL] = &helmIndexEntry{
		fetchedAt: time.Now(),
		fetcher:   f,
	}
	return f
}

// semverGreater returns true when version a is greater than b.
// It uses the same parseSemverParts helper defined in checker.go.
func semverGreater(a, b string) bool {
	aParts := parseSemverParts(a)
	bParts := parseSemverParts(b)

	// Pad to same length.
	for len(aParts) < 3 {
		aParts = append(aParts, 0)
	}
	for len(bParts) < 3 {
		bParts = append(bParts, 0)
	}

	for i := 0; i < 3; i++ {
		if aParts[i] != bParts[i] {
			return aParts[i] > bParts[i]
		}
	}
	return false // equal
}
