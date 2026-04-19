package helm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChartVersion represents a version entry from a Helm repo index.
type ChartVersion struct {
	Version     string   `yaml:"version"`
	URLs        []string `yaml:"urls"`
	AppVersion  string   `yaml:"appVersion,omitempty"`
	Created     string   `yaml:"created,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Icon        string   `yaml:"icon,omitempty"`
	// Deprecated mirrors the per-version `deprecated` field from index.yaml.
	// Charts mark a version as deprecated when it should not be used (e.g. a
	// CVE landed). Surfaced in the v1.21 Paste-URL validate response.
	Deprecated bool `yaml:"deprecated,omitempty"`
}

// repoIndex represents a Helm repository index.yaml.
type repoIndex struct {
	Entries map[string][]ChartVersion `yaml:"entries"`
}

// chartMetadata holds the fields we extract from Chart.yaml.
type chartMetadata struct {
	Sources []string `yaml:"sources"`
	Home    string   `yaml:"home"`
}

// Fetcher downloads Helm chart values.yaml for comparison.
// Includes in-memory caching to avoid redundant downloads.
type Fetcher struct {
	client      *http.Client
	indexCache  map[string]*repoIndex  // key: repoURL
	valuesCache map[string]string      // key: repoURL/chart/version
	chartCache  map[string]*chartMetadata // key: repoURL/chart/version
}

// NewFetcher creates a new Helm chart fetcher with caching.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client:      &http.Client{},
		indexCache:  make(map[string]*repoIndex),
		valuesCache: make(map[string]string),
		chartCache:  make(map[string]*chartMetadata),
	}
}

// getIndex fetches and caches the repo index.
func (f *Fetcher) getIndex(ctx context.Context, repoURL string) (*repoIndex, error) {
	if idx, ok := f.indexCache[repoURL]; ok {
		return idx, nil
	}

	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	req, err := http.NewRequestWithContext(ctx, "GET", indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching index returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}

	var idx repoIndex
	if err := yaml.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parsing index: %w", err)
	}

	f.indexCache[repoURL] = &idx
	return &idx, nil
}

// ListVersions returns available versions for a chart from the repo index.
func (f *Fetcher) ListVersions(ctx context.Context, repoURL, chartName string) ([]ChartVersion, error) {
	idx, err := f.getIndex(ctx, repoURL)
	if err != nil {
		return nil, err
	}

	versions, ok := idx.Entries[chartName]
	if !ok {
		return nil, fmt.Errorf("chart %q not found in repo", chartName)
	}

	return versions, nil
}

// FindNearestVersion finds the closest available version to the target version.
// It searches for versions with the same major.minor and a lower patch, then
// falls back to the closest minor version within the same major.
// Returns empty string if no suitable fallback is found.
func (f *Fetcher) FindNearestVersion(ctx context.Context, repoURL, chartName, targetVersion string) (string, error) {
	versions, err := f.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return "", err
	}

	targetParts := parseVersion(targetVersion)
	if targetParts == nil {
		return "", nil
	}

	// Pass 1: Find highest patch within same major.minor that is <= target patch.
	var bestSameMajorMinor string
	var bestPatch int = -1
	for _, v := range versions {
		parts := parseVersion(v.Version)
		if parts == nil {
			continue
		}
		if parts[0] == targetParts[0] && parts[1] == targetParts[1] && parts[2] < targetParts[2] {
			if parts[2] > bestPatch {
				bestPatch = parts[2]
				bestSameMajorMinor = v.Version
			}
		}
	}
	if bestSameMajorMinor != "" {
		return bestSameMajorMinor, nil
	}

	// Pass 2: Find highest version within same major that is < target minor.
	var bestSameMajor string
	var bestMinor int = -1
	var bestMinorPatch int = -1
	for _, v := range versions {
		parts := parseVersion(v.Version)
		if parts == nil {
			continue
		}
		if parts[0] == targetParts[0] && parts[1] < targetParts[1] {
			if parts[1] > bestMinor || (parts[1] == bestMinor && parts[2] > bestMinorPatch) {
				bestMinor = parts[1]
				bestMinorPatch = parts[2]
				bestSameMajor = v.Version
			}
		}
	}
	return bestSameMajor, nil
}

// parseVersion extracts [major, minor, patch] from a version string like "1.16.3" or "v1.16.3".
// Returns nil if parsing fails.
func parseVersion(version string) []int {
	v := strings.TrimPrefix(version, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		return nil
	}
	result := make([]int, 3)
	for i, p := range parts {
		// Strip pre-release suffix (e.g., "3-beta.1" -> "3")
		numStr := p
		for j, ch := range p {
			if ch < '0' || ch > '9' {
				numStr = p[:j]
				break
			}
		}
		n := 0
		for _, ch := range numStr {
			n = n*10 + int(ch-'0')
		}
		result[i] = n
	}
	return result
}

// FetchValues downloads a chart archive and extracts values.yaml.
func (f *Fetcher) FetchValues(ctx context.Context, repoURL, chartName, version string) (string, error) {
	// Check cache first
	cacheKey := repoURL + "/" + chartName + "/" + version
	if cached, ok := f.valuesCache[cacheKey]; ok {
		return cached, nil
	}

	// First get the chart URL from index
	versions, err := f.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return "", err
	}

	var chartURL string
	for _, v := range versions {
		if v.Version == version && len(v.URLs) > 0 {
			chartURL = v.URLs[0]
			break
		}
	}
	if chartURL == "" {
		return "", fmt.Errorf("version %s not found for chart %s", version, chartName)
	}

	// Handle relative URLs
	if !strings.HasPrefix(chartURL, "http") {
		chartURL = strings.TrimRight(repoURL, "/") + "/" + chartURL
	}

	// Download the .tgz
	req, err := http.NewRequestWithContext(ctx, "GET", chartURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating chart request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading chart: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chart download returned %d", resp.StatusCode)
	}

	// Extract values.yaml from tar.gz
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("decompressing: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading tar: %w", err)
		}

		// values.yaml is typically at {chartName}/values.yaml
		if strings.HasSuffix(header.Name, "/values.yaml") || header.Name == "values.yaml" {
			data, err := io.ReadAll(io.LimitReader(tr, 5*1024*1024))
			if err != nil {
				return "", fmt.Errorf("reading values.yaml: %w", err)
			}
			result := string(data)
			f.valuesCache[cacheKey] = result
			return result, nil
		}
	}

	return "", fmt.Errorf("values.yaml not found in chart archive")
}

// fetchChartYAML downloads the chart archive and extracts Chart.yaml metadata.
// Results are cached per repoURL/chartName/version (15-minute effective TTL via process lifetime).
func (f *Fetcher) fetchChartYAML(ctx context.Context, repoURL, chartName, version string) (*chartMetadata, error) {
	cacheKey := repoURL + "/" + chartName + "/" + version
	if cached, ok := f.chartCache[cacheKey]; ok {
		return cached, nil
	}

	// Reuse chart URL lookup from the index.
	versions, err := f.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return nil, err
	}

	var chartURL string
	for _, v := range versions {
		if v.Version == version && len(v.URLs) > 0 {
			chartURL = v.URLs[0]
			break
		}
	}
	if chartURL == "" {
		return nil, fmt.Errorf("version %s not found for chart %s", version, chartName)
	}

	if !strings.HasPrefix(chartURL, "http") {
		chartURL = strings.TrimRight(repoURL, "/") + "/" + chartURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", chartURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating chart request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading chart: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chart download returned %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompressing: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		// Chart.yaml is at <chart-name>/Chart.yaml
		if strings.HasSuffix(header.Name, "/Chart.yaml") || header.Name == "Chart.yaml" {
			data, err := io.ReadAll(io.LimitReader(tr, 1*1024*1024))
			if err != nil {
				return nil, fmt.Errorf("reading Chart.yaml: %w", err)
			}
			var meta chartMetadata
			if err := yaml.Unmarshal(data, &meta); err != nil {
				return nil, fmt.Errorf("parsing Chart.yaml: %w", err)
			}
			f.chartCache[cacheKey] = &meta
			return &meta, nil
		}
	}

	return nil, fmt.Errorf("Chart.yaml not found in chart archive")
}

// extractGitHubRepoFromURL extracts "owner/repo" from a GitHub URL.
// Accepts https://github.com/owner/repo, https://github.com/owner/repo.git,
// and URLs with additional path segments. Returns "" if not a GitHub URL.
func extractGitHubRepoFromURL(rawURL string) string {
	if !strings.Contains(rawURL, "github.com/") {
		return ""
	}
	// Strip scheme + host
	idx := strings.Index(rawURL, "github.com/")
	if idx < 0 {
		return ""
	}
	path := rawURL[idx+len("github.com/"):]
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	// Strip any trailing query/fragment
	if i := strings.IndexAny(repo, "?#"); i >= 0 {
		repo = repo[:i]
	}
	return parts[0] + "/" + repo
}

// FetchReleaseNotes tries to get release notes for a chart version.
// Precedence: Chart.yaml sources[] → Chart.yaml home → guessGitHubRepo heuristic.
func (f *Fetcher) FetchReleaseNotes(ctx context.Context, repoURL, chartName, version string) (string, error) {
	// 1. Try Chart.yaml sources/home for a GitHub URL.
	var githubRepo string
	if meta, err := f.fetchChartYAML(ctx, repoURL, chartName, version); err == nil {
		// sources[] first
		for _, src := range meta.Sources {
			if r := extractGitHubRepoFromURL(src); r != "" {
				githubRepo = r
				break
			}
		}
		// home fallback
		if githubRepo == "" {
			if r := extractGitHubRepoFromURL(meta.Home); r != "" {
				githubRepo = r
			}
		}
	}

	// 2. Fall back to heuristic if Chart.yaml didn't yield a repo.
	if githubRepo == "" {
		githubRepo = guessGitHubRepo(repoURL, chartName)
	}

	if githubRepo == "" {
		return "Release notes not available (no GitHub repository found for this chart).", nil
	}

	// Fetch from GitHub Releases API
	// Try tag patterns: {chartName}-{version}, v{version}, {version}
	tagPatterns := []string{
		chartName + "-" + version,
		"v" + version,
		version,
	}

	for _, tag := range tagPatterns {
		notes, err := f.fetchGitHubRelease(ctx, githubRepo, tag)
		if err == nil && notes != "" {
			// Truncate to 3000 chars to keep LLM context manageable
			if len(notes) > 3000 {
				notes = notes[:3000] + "\n... (truncated)"
			}
			return notes, nil
		}
	}

	return "Release notes not found for version " + version + " (tried GitHub releases for " + githubRepo + ").", nil
}

func (f *Fetcher) fetchGitHubRelease(ctx context.Context, repo, tag string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parsing GitHub release: %w", err)
	}

	if release.Body == "" {
		return "", fmt.Errorf("empty release body")
	}
	return fmt.Sprintf("Release: %s (%s)\n\n%s", release.Name, release.TagName, release.Body), nil
}

// guessGitHubRepo maps common Helm repo URLs to GitHub repos.
func guessGitHubRepo(repoURL, chartName string) string {
	// Known mappings
	mappings := map[string]string{
		"https://helm.datadoghq.com":                          "DataDog/helm-charts",
		"https://argoproj.github.io/argo-helm":                "argoproj/argo-helm",
		"https://charts.jetstack.io":                          "cert-manager/cert-manager",
		"https://kedacore.github.io/charts":                   "kedacore/charts",
		"https://charts.external-secrets.io":                  "external-secrets/external-secrets",
		"https://kyverno.github.io/kyverno":                   "kyverno/kyverno",
		"https://istio-release.storage.googleapis.com/charts": "istio/istio",
		"https://kubernetes-sigs.github.io/external-dns":      "kubernetes-sigs/external-dns",
		"https://pileus-cloud.github.io/charts":               "pileus-cloud/charts",
	}

	for prefix, repo := range mappings {
		if strings.HasPrefix(repoURL, prefix) {
			return repo
		}
	}

	// Try to extract from GitHub Pages pattern: https://org.github.io/repo
	if strings.Contains(repoURL, ".github.io/") {
		parts := strings.SplitN(strings.TrimPrefix(repoURL, "https://"), "/", 3)
		if len(parts) >= 2 {
			org := strings.TrimSuffix(parts[0], ".github.io")
			repo := strings.TrimRight(parts[1], "/")
			return org + "/" + repo
		}
	}

	return ""
}
