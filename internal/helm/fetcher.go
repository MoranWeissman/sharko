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
	Version    string   `yaml:"version"`
	URLs       []string `yaml:"urls"`
	AppVersion string   `yaml:"appVersion,omitempty"`
}

// repoIndex represents a Helm repository index.yaml.
type repoIndex struct {
	Entries map[string][]ChartVersion `yaml:"entries"`
}

// Fetcher downloads Helm chart values.yaml for comparison.
// Includes in-memory caching to avoid redundant downloads.
type Fetcher struct {
	client     *http.Client
	indexCache map[string]*repoIndex // key: repoURL
	valuesCache map[string]string    // key: repoURL/chart/version
}

// NewFetcher creates a new Helm chart fetcher with caching.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client:      &http.Client{},
		indexCache:  make(map[string]*repoIndex),
		valuesCache: make(map[string]string),
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

// FetchReleaseNotes tries to get release notes for a chart version.
// It attempts GitHub Releases API first, then falls back to chart metadata.
func (f *Fetcher) FetchReleaseNotes(ctx context.Context, repoURL, chartName, version string) (string, error) {
	githubRepo := guessGitHubRepo(repoURL, chartName)
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
