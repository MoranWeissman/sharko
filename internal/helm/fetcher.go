package helm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ErrOCIVersionCheckUnsupported is returned by ListVersions (and anything
// built on it) for an oci:// repo URL. Classic Helm repos publish a plain
// HTTP index.yaml; OCI registries don't — listing tags there needs the
// registry's own API plus, for a private registry, real credentials. v4
// wave 1 Story 3.3 ("internal addons") requires this to degrade gracefully
// — an internal addon is commonly a private OCI chart, and version
// checking must show "unknown", never error. Full registry-aware version
// checking (with credentials) is Story 3.4's job; this sentinel is the line
// that story's work slots in behind. Callers that want the graceful
// "unknown" UX check errors.Is(err, ErrOCIVersionCheckUnsupported) rather
// than treating this as a real failure.
var ErrOCIVersionCheckUnsupported = errors.New("version listing is not supported for oci:// registries without registry credentials")

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
//
// A Fetcher is safe to share between goroutines. Long-lived shared ones exist
// on live paths — internal/api/catalog_versions.go keeps a package-level one
// behind the chart-versions endpoint, internal/service/upgrade.go holds one for
// the life of the service, and cmd/sharko/serve.go hands one to the catalog
// freshness scheduler — so two HTTP requests routinely land in the same Fetcher
// at the same time. Before the lock below, that produced
// "fatal error: concurrent map writes", which is an unrecoverable runtime abort:
// it kills the process, not the request. Sharko runs as a single instance with
// sessions in an in-memory map, so one such crash signs every user out.
type Fetcher struct {
	client *http.Client

	// mu guards the three cache maps below, and nothing else. It is held
	// ONLY across a map read or a map write — never across an HTTP request,
	// YAML parsing or archive extraction, all of which happen unlocked
	// between a released read lock and the write lock that stores the
	// result. Two callers asking for the same cold entry at the same moment
	// will therefore both fetch it, and the second store simply wins. That
	// is the intended trade: a duplicate download costs one request, while
	// holding a lock across a network round trip would serialise every
	// caller behind the slowest repository.
	mu          sync.RWMutex
	indexCache  map[string]*repoIndex // key: repoURL
	valuesCache map[string]string     // key: repoURL/chart/resolved-version
	// The two archive caches are keyed on the version the repo index
	// PUBLISHES, not on the spelling the caller pinned — see
	// resolveChartVersion. "1.16.3" and "v1.16.3" against a v-prefixed
	// index therefore share one entry and one download.
	chartCache map[string]*chartMetadata // key: repoURL/chart/resolved-version
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
	// Sharko never dials an address it would not save. Every other method on
	// this type reaches the network through here — ListVersions and
	// ListCharts call it directly, FindNearestVersion, FetchValues and
	// fetchChartYAML go through ListVersions — so this one check is what
	// makes "never connect using that address" true for all of them, rather
	// than a check each caller has to remember.
	//
	// It runs before the cache lookup as well as before the request: a
	// refused address must never come back from a cache slot either.
	if err := credsafe.ValidateSupportedRepoURL(repoURL); err != nil {
		return nil, err
	}

	f.mu.RLock()
	idx, ok := f.indexCache[repoURL]
	f.mu.RUnlock()
	if ok {
		return idx, nil
	}

	// OCI registries (helm 3.8+ oci:// charts, e.g. Karpenter or a private
	// in-house registry) don't publish an index.yaml over HTTP — there is
	// nothing at "<oci-url>/index.yaml" to fetch, so don't try. Return the
	// distinguishable sentinel instead of a confusing network/parse error;
	// callers that want "unknown" rather than a hard failure check
	// errors.Is(err, ErrOCIVersionCheckUnsupported).
	if strings.HasPrefix(repoURL, "oci://") {
		return nil, ErrOCIVersionCheckUnsupported
	}

	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	req, err := http.NewRequestWithContext(ctx, "GET", indexURL, nil)
	if err != nil {
		// Never "%w" on either of these (BF12). net/http hands back a
		// *url.Error, which prints the address it was given: on the build
		// with nothing masked at all, on the send with only the password
		// half of any user information hidden. The address here is a chart
		// repository, which is credential material — it is routinely
		// written https://<token>@host/org/repo — and these errors do not
		// stay inside the process: several handlers put them straight into
		// an API reply with writeError(err.Error()).
		return nil, fmt.Errorf("the chart index request could not be built (%s)",
			credsafe.PlainFailureReason(err))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the chart index request did not complete (%s)",
			credsafe.PlainFailureReason(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching index returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}

	var parsed repoIndex
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing index: %w", err)
	}

	// The parsed index is filled in completely BEFORE it is published here,
	// and nothing ever writes to it again. That is what makes it safe for
	// ListVersions to hand the same Entries slice to every caller without
	// copying it: the slice, its backing array and the map holding it are
	// all write-once, and the Unlock/RLock pair below and above is the
	// happens-before edge that guarantees a reader sees a finished object.
	// Every consumer was checked for mutation — nine call sites plus the
	// FreshnessSnapshot the slice escapes into — and none sorts it, assigns
	// through an index, or writes a field; the two that sort build a fresh
	// slice first. Cloning would be churn with nothing behind it.
	f.mu.Lock()
	f.indexCache[repoURL] = &parsed
	f.mu.Unlock()
	return &parsed, nil
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

// ListCharts returns the chart names listed under entries: in a Helm repo's
// index.yaml. v1.21 QA Bundle 1: lets the manual "Add Addon" flow show a
// chart-name dropdown after the operator validates the repo URL.
func (f *Fetcher) ListCharts(ctx context.Context, repoURL string) ([]string, error) {
	idx, err := f.getIndex(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(idx.Entries))
	for name := range idx.Entries {
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	// Stable order so cache hits and replays are deterministic.
	sort.Strings(out)
	return out, nil
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

// altVersionSpelling returns the one other spelling of a chart version that
// Sharko will accept: the same string with a single leading lowercase "v"
// added if it has none, or removed if it has exactly one.
//
// The remove direction is guarded: it returns false rather than dropping a
// leading "v" when what follows is another "v" or nothing at all. "vv1.2.3" is
// the case that matters — dropping one "v" would turn it into "v1.2.3", a
// DIFFERENT version string, and Sharko must not quietly fetch a version nobody
// pinned.
//
// The add direction validates nothing, because it does not need to: prefixing
// anything that does not already start with a lowercase "v" produces a string
// that either names a real index entry or misses. "V1.2.3" becomes "vV1.2.3",
// which no sane index publishes, so the request is refused — a capital "V"
// never reaches "v1.2.3", and no case folding happens anywhere on this path.
func altVersionSpelling(requested string) (string, bool) {
	if requested == "" {
		return "", false
	}
	if rest, hadV := strings.CutPrefix(requested, "v"); hadV {
		// One leading "v", with something after it that is not another
		// "v". Anything else is not a plain spelling of a version.
		if rest == "" || strings.HasPrefix(rest, "v") {
			return "", false
		}
		return rest, true
	}
	return "v" + requested, true
}

// pickChartVersion returns the index entry whose version string is exactly
// want and that actually has somewhere to download from. Entries Sharko cannot
// download are skipped and the scan continues, because there is nothing to
// fetch for them.
//
// "Cannot download" means two things, and it has to mean both. An entry with no
// urls: list is the obvious one. An entry whose urls: list holds an empty
// string is the other, and it is the one that is easy to lose: the code this
// replaced used the URL string itself as its not-found sentinel, so
// `chartURL == ""` caught both states at once for free. Checking only the
// length brings back a hit with an empty download address, which the
// relative-URL join in the callers turns into the repository ROOT — Sharko
// issues an HTTP GET it should never make and reports
// "decompressing: gzip: invalid header" instead of refusing cleanly.
//
// Only URLs[0] is checked because only URLs[0] is ever used.
func pickChartVersion(versions []ChartVersion, want string) *ChartVersion {
	for i := range versions {
		if versions[i].Version != want {
			continue
		}
		if len(versions[i].URLs) == 0 || versions[i].URLs[0] == "" {
			continue
		}
		return &versions[i]
	}
	return nil
}

// resolveChartVersion picks the download URL for a requested chart version
// out of a repo index's entry list, and reports which spelling of the
// version the index actually publishes.
//
// The matching rule, in full:
//
//  1. The requested string, exactly.
//  2. Only if that finds nothing: the same string with one leading
//     lowercase "v" added or removed (see altVersionSpelling).
//
// That is all of it. Chart repositories disagree about the leading "v" —
// the jetstack index publishes cert-manager as "v1.16.3" and carries no
// bare "1.16.x" entry at all — so an addon pinned to "1.16.3" used to miss
// the lookup and fail the request outright. Nothing else is attempted: no
// nearest-version selection (FindNearestVersion is a separate call the
// upgrade checker makes deliberately, and it must stay unreachable from
// here), no case folding, and prerelease and build suffixes ride along
// untouched, so "1.2.3-rc.1" never resolves to "1.2.3".
//
// FetchValues and fetchChartYAML both go through this one function. They
// used to carry a byte-identical copy of the strict-equality loop each,
// which is how the bare-version miss came to exist in two places at once.
//
// The returned resolved version is what both caches are keyed on, so the
// two spellings of a version share a single cache entry and a single
// download rather than fetching the same archive twice.
func resolveChartVersion(versions []ChartVersion, requested string) (resolved, chartURL string, ok bool) {
	// An empty pin resolves to nothing, ever. Without this, a malformed index
	// entry whose own version: field is empty would match it, and Sharko would
	// download an arbitrary archive for a version nobody wrote down. Most API
	// handlers reject an empty version with a 400 first, but two paths hand
	// one straight through: internal/orchestrator/catalog_ops.go passes
	// entry.Version from the catalog file, and internal/ai/tools.go passes
	// whatever the model put in a tool argument.
	if requested == "" {
		return "", "", false
	}
	if hit := pickChartVersion(versions, requested); hit != nil {
		return hit.Version, hit.URLs[0], true
	}
	if alt, hasAlt := altVersionSpelling(requested); hasAlt {
		if hit := pickChartVersion(versions, alt); hit != nil {
			return hit.Version, hit.URLs[0], true
		}
	}
	return "", "", false
}

// FetchValues downloads a chart archive and extracts values.yaml.
func (f *Fetcher) FetchValues(ctx context.Context, repoURL, chartName, version string) (string, error) {
	// The index is read BEFORE the values cache is consulted, because the
	// cache key holds the version the index publishes rather than the
	// spelling this caller happened to use. That costs no extra network
	// traffic on a repeat call — indexCache holds the parsed index for the
	// life of the process — and it means getIndex's address check now runs
	// on every FetchValues, including ones a cache hit used to skip.
	versions, err := f.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return "", err
	}

	resolvedVersion, chartURL, ok := resolveChartVersion(versions, version)
	if !ok {
		return "", fmt.Errorf("version %s not found for chart %s", version, chartName)
	}

	cacheKey := repoURL + "/" + chartName + "/" + resolvedVersion
	f.mu.RLock()
	cached, hit := f.valuesCache[cacheKey]
	f.mu.RUnlock()
	if hit {
		return cached, nil
	}

	// Handle relative URLs
	if !strings.HasPrefix(chartURL, "http") {
		chartURL = strings.TrimRight(repoURL, "/") + "/" + chartURL
	}

	// Download the .tgz
	req, err := http.NewRequestWithContext(ctx, "GET", chartURL, nil)
	if err != nil {
		// Address-free, same reason as getIndex above (BF12).
		return "", fmt.Errorf("the chart download request could not be built (%s)",
			credsafe.PlainFailureReason(err))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the chart download did not complete (%s)",
			credsafe.PlainFailureReason(err))
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
			// Write lock taken here and nowhere earlier: the download and
			// the tar walk above ran unlocked.
			f.mu.Lock()
			f.valuesCache[cacheKey] = result
			f.mu.Unlock()
			return result, nil
		}
	}

	return "", fmt.Errorf("values.yaml not found in chart archive")
}

// fetchChartYAML downloads the chart archive and extracts Chart.yaml metadata.
// Results are cached per repoURL/chartName/resolved-version (15-minute
// effective TTL via process lifetime).
func (f *Fetcher) fetchChartYAML(ctx context.Context, repoURL, chartName, version string) (*chartMetadata, error) {
	// Index first, then the cache — same reason as FetchValues above: the
	// cache key carries the version the index publishes, not the spelling
	// the caller used.
	versions, err := f.ListVersions(ctx, repoURL, chartName)
	if err != nil {
		return nil, err
	}

	resolvedVersion, chartURL, ok := resolveChartVersion(versions, version)
	if !ok {
		return nil, fmt.Errorf("version %s not found for chart %s", version, chartName)
	}

	cacheKey := repoURL + "/" + chartName + "/" + resolvedVersion
	f.mu.RLock()
	cached, hit := f.chartCache[cacheKey]
	f.mu.RUnlock()
	if hit {
		return cached, nil
	}

	if !strings.HasPrefix(chartURL, "http") {
		chartURL = strings.TrimRight(repoURL, "/") + "/" + chartURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", chartURL, nil)
	if err != nil {
		// Address-free, same reason as getIndex above (BF12).
		return nil, fmt.Errorf("the chart download request could not be built (%s)",
			credsafe.PlainFailureReason(err))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the chart download did not complete (%s)",
			credsafe.PlainFailureReason(err))
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
			// Same as FetchValues: the write lock covers the store only.
			// meta is finished before it is published and never written
			// again, so the pointer handed to every later caller is safe
			// to read without a lock.
			f.mu.Lock()
			f.chartCache[cacheKey] = &meta
			f.mu.Unlock()
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
	// This method carries on after fetchChartYAML fails — it falls back to
	// guessing a GitHub repository from the address — so it cannot rely on
	// getIndex's refusal having stopped it. Same rule, asked again here, not
	// a second rule.
	if err := credsafe.ValidateSupportedRepoURL(repoURL); err != nil {
		return "", err
	}

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
		// Address-free, same reason as getIndex above (BF12). The host is
		// a literal here, but the repository and tag in the path are not,
		// and the rule is about the whole address.
		return "", fmt.Errorf("the release-notes request could not be built (%s)",
			credsafe.PlainFailureReason(err))
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the release-notes request did not complete (%s)",
			credsafe.PlainFailureReason(err))
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
