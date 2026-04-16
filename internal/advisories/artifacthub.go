package advisories

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	userAgent        = "sharko/1.17.0"
	httpTimeout      = 5 * time.Second
	repoNameCacheTTL = 24 * time.Hour
)

// artifactHubBaseURL is the ArtifactHub API base URL.
// It is a variable (not const) so tests can override it with httptest.NewServer.
var artifactHubBaseURL = "https://artifacthub.io/api/v1"

// setArtifactHubBaseURL overrides the ArtifactHub base URL. Used in tests only.
func setArtifactHubBaseURL(u string) {
	artifactHubBaseURL = u
}

// artifactHubSource queries ArtifactHub for Helm chart advisory data.
type artifactHubSource struct {
	client       *http.Client
	repoCache    map[string]repoCacheEntry // repoURL → ArtifactHub repo name
	repoCacheMu  sync.Mutex
}

type repoCacheEntry struct {
	name    string
	fetchAt time.Time
}

// ahPackageSummary is the JSON shape returned by /packages/helm/{repo}/{chart}.
type ahPackageSummary struct {
	Name              string             `json:"name"`
	AvailableVersions []ahVersionEntry   `json:"available_versions"`
}

type ahVersionEntry struct {
	Version                string `json:"version"`
	TS                     int64  `json:"ts"`
	Prerelease             bool   `json:"prerelease"`
	ContainsSecurityUpdates bool  `json:"contains_security_updates"`
}

// ahRepoSearchResult is returned by /repositories/search?url=...
type ahRepoSearchResult struct {
	Repositories []ahRepository `json:"repositories"`
}

type ahRepository struct {
	Name string `json:"name"`
}

func newArtifactHubSource(client *http.Client) *artifactHubSource {
	c := *client // copy to set timeout without mutating caller's client
	c.Timeout = httpTimeout
	return &artifactHubSource{
		client:    &c,
		repoCache: make(map[string]repoCacheEntry),
	}
}

// Get fetches advisory data from ArtifactHub for the given chart.
func (a *artifactHubSource) Get(ctx context.Context, repoURL, chart string) ([]Advisory, error) {
	repoName, err := a.resolveRepoName(ctx, repoURL)
	if err != nil {
		return nil, fmt.Errorf("artifacthub: resolving repo name for %q: %w", repoURL, err)
	}

	pkg, err := a.fetchPackage(ctx, repoName, chart)
	if err != nil {
		return nil, fmt.Errorf("artifacthub: fetching package %q/%q: %w", repoName, chart, err)
	}

	advisories := make([]Advisory, 0, len(pkg.AvailableVersions))
	for _, av := range pkg.AvailableVersions {
		if av.Prerelease {
			continue
		}
		adv := Advisory{
			Version:             av.Version,
			ContainsSecurityFix: av.ContainsSecurityUpdates,
		}
		if av.ContainsSecurityUpdates {
			adv.Summary = "contains security updates"
		}
		advisories = append(advisories, adv)
	}
	return advisories, nil
}

// resolveRepoName maps a Helm repo URL to an ArtifactHub repository slug.
// Results are cached for 24 hours.
func (a *artifactHubSource) resolveRepoName(ctx context.Context, repoURL string) (string, error) {
	a.repoCacheMu.Lock()
	entry, ok := a.repoCache[repoURL]
	a.repoCacheMu.Unlock()

	if ok && time.Since(entry.fetchAt) < repoNameCacheTTL {
		return entry.name, nil
	}

	searchURL := fmt.Sprintf("%s/repositories/search?url=%s", artifactHubBaseURL, url.QueryEscape(repoURL))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting repo search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading repo search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("repo search returned status %d", resp.StatusCode)
	}

	var result ahRepoSearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing repo search response: %w", err)
	}

	if len(result.Repositories) == 0 {
		return "", fmt.Errorf("no ArtifactHub repository found for URL %q", repoURL)
	}

	name := result.Repositories[0].Name
	slog.Debug("artifacthub: resolved repo name", "url", repoURL, "name", name)

	a.repoCacheMu.Lock()
	a.repoCache[repoURL] = repoCacheEntry{name: name, fetchAt: time.Now()}
	a.repoCacheMu.Unlock()

	return name, nil
}

// fetchPackage retrieves the package summary (including all available_versions) from ArtifactHub.
func (a *artifactHubSource) fetchPackage(ctx context.Context, repoName, chart string) (*ahPackageSummary, error) {
	pkgURL := fmt.Sprintf("%s/packages/helm/%s/%s", artifactHubBaseURL,
		url.PathEscape(repoName), url.PathEscape(chart))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pkgURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting package: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading package response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("chart %q not found on ArtifactHub (repo %q)", chart, repoName)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("package request returned status %d", resp.StatusCode)
	}

	var pkg ahPackageSummary
	if err := json.Unmarshal(body, &pkg); err != nil {
		return nil, fmt.Errorf("parsing package response: %w", err)
	}

	return &pkg, nil
}

// releaseNotesSource parses Helm repo index.yaml annotations as a fallback.
// It looks for the "artifacthub.io/changes" annotation and scans for security/breaking keywords.
type releaseNotesSource struct {
	client *http.Client
}

func newReleaseNotesSource(client *http.Client) *releaseNotesSource {
	c := *client
	c.Timeout = httpTimeout
	return &releaseNotesSource{client: &c}
}

// securityKeywords are substrings that indicate a security-related release note.
var securityKeywords = []string{
	"cve", "security", "vulnerability", "patch", "vuln", "exploit",
}

// breakingKeywords are substrings that indicate a breaking-change release note.
var breakingKeywords = []string{
	"breaking", "deprecated", "deprecat", "removed", "incompatible",
}

// Get fetches the Helm repo index and extracts advisory data from chart annotations.
func (r *releaseNotesSource) Get(ctx context.Context, repoURL, chart string) ([]Advisory, error) {
	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching index.yaml: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("index.yaml returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading index.yaml: %w", err)
	}

	entries, err := parseHelmIndex(body, chart)
	if err != nil {
		return nil, err
	}

	advisories := make([]Advisory, 0, len(entries))
	for _, e := range entries {
		notes := e.releaseNotes
		adv := Advisory{
			Version:      e.version,
			ReleaseNotes: notes,
		}
		lower := strings.ToLower(notes)
		for _, kw := range securityKeywords {
			if strings.Contains(lower, kw) {
				adv.ContainsSecurityFix = true
				adv.Summary = "possible security fix (keyword match)"
				break
			}
		}
		for _, kw := range breakingKeywords {
			if strings.Contains(lower, kw) {
				adv.ContainsBreaking = true
				break
			}
		}
		advisories = append(advisories, adv)
	}
	return advisories, nil
}

// indexEntry holds parsed data from a single Helm chart version entry.
type indexEntry struct {
	version      string
	releaseNotes string
}

// helmIndexYAML is a minimal YAML struct for parsing index.yaml.
type helmIndexEntry struct {
	Version     string            `yaml:"version"`
	Annotations map[string]string `yaml:"annotations"`
}

type helmIndexYAML struct {
	Entries map[string][]helmIndexEntry `yaml:"entries"`
}

// parseHelmIndex extracts version+annotation data from a raw index.yaml body.
func parseHelmIndex(data []byte, chart string) ([]indexEntry, error) {
	// We use a minimal parser to avoid pulling in large dependencies.
	// gopkg.in/yaml.v3 is already in go.mod.
	var idx helmIndexYAML
	// Use encoding/json-style unmarshal via yaml.v3 (already a dep)
	if err := unmarshalYAML(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index.yaml: %w", err)
	}

	versions, ok := idx.Entries[chart]
	if !ok {
		return nil, nil // chart not in index — not an error, just no data
	}

	entries := make([]indexEntry, 0, len(versions))
	for _, v := range versions {
		notes := ""
		if v.Annotations != nil {
			notes = v.Annotations["artifacthub.io/changes"]
			if notes == "" {
				notes = v.Annotations["artifacthub.io/changelog"]
			}
		}
		entries = append(entries, indexEntry{
			version:      v.Version,
			releaseNotes: notes,
		})
	}
	return entries, nil
}
