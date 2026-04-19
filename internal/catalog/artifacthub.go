package catalog

// artifacthub.go — minimal ArtifactHub HTTP client used by the v1.21 search +
// package-detail proxy endpoints. Mirrors the patterns in
// internal/advisories/artifacthub.go (timeout, User-Agent, body limit) but
// stays in this package because the advisory client is private and serves a
// different shape (security advisories vs. browse metadata).
//
// What we DON'T do:
//   - We do not proxy chart tarballs. ArgoCD pulls those at deploy time. We
//     only proxy ArtifactHub's metadata API.
//   - We do not require an API token. ArtifactHub read endpoints are public.
//
// Error classification (returned by the client) lets the API layer decide
// whether to serve from cache (rate-limited / 5xx / network) or pass through
// (404 / malformed).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ArtifactHubBaseURL is exported so tests can swap it via httptest. Production
// code never overwrites it after init.
var ArtifactHubBaseURL = "https://artifacthub.io/api/v1"

// SetArtifactHubBaseURL is a test hook.
func SetArtifactHubBaseURL(u string) {
	ArtifactHubBaseURL = u
}

const (
	ahUserAgent      = "sharko-marketplace/1.21"
	ahHTTPTimeout    = 8 * time.Second
	ahMaxBody        = 4 * 1024 * 1024
	ahHealthEndpoint = "/" // ArtifactHub doesn't expose a tiny health endpoint; the API root returns 200.
)

// AHErrClass classifies upstream failures so the API layer can decide what to
// do (retry / serve stale / pass through).
type AHErrClass string

const (
	AHErrNotFound     AHErrClass = "not_found"     // 404 — pass through to the user
	AHErrRateLimited  AHErrClass = "rate_limited"  // 429 — back off + serve stale if any
	AHErrServerError  AHErrClass = "server_error"  // 5xx — serve stale if any
	AHErrTimeout      AHErrClass = "timeout"       // context deadline / network — serve stale if any
	AHErrMalformed    AHErrClass = "malformed"     // unparseable body — pass through, don't cache
	AHErrInvalidInput AHErrClass = "invalid_input" // caller error — return 400
)

// ArtifactHubError is the typed error returned by the client. The API layer
// matches on Class to decide between 404 / 502 / 503 / cache-fallback.
type ArtifactHubError struct {
	Class      AHErrClass
	Status     int
	Underlying error
	Message    string
}

func (e *ArtifactHubError) Error() string {
	if e.Message != "" {
		return string(e.Class) + ": " + e.Message
	}
	if e.Underlying != nil {
		return string(e.Class) + ": " + e.Underlying.Error()
	}
	return string(e.Class)
}

func (e *ArtifactHubError) Unwrap() error { return e.Underlying }

// IsArtifactHubClass reports whether err is an ArtifactHubError with the
// given class.
func IsArtifactHubClass(err error, class AHErrClass) bool {
	var ahe *ArtifactHubError
	if !errors.As(err, &ahe) {
		return false
	}
	return ahe.Class == class
}

// ArtifactHubClient wraps an *http.Client with the User-Agent, timeout, and
// body-limit conventions documented above.
type ArtifactHubClient struct {
	HTTP    *http.Client
	BaseURL string
}

// NewArtifactHubClient returns a client with sensible defaults. Pass in a
// custom *http.Client only if you need a tuned transport (e.g., proxy).
func NewArtifactHubClient(client *http.Client) *ArtifactHubClient {
	if client == nil {
		client = &http.Client{Timeout: ahHTTPTimeout}
	} else {
		// Don't mutate the caller's client; copy and stamp our timeout if not
		// already set.
		c := *client
		if c.Timeout == 0 {
			c.Timeout = ahHTTPTimeout
		}
		client = &c
	}
	return &ArtifactHubClient{
		HTTP:    client,
		BaseURL: ArtifactHubBaseURL,
	}
}

// ─── Search ────────────────────────────────────────────────────────────────

// AHSearchPackage is one result row from /packages/search?ts_query_web=...
// We pick the fields the UI actually renders; anything else is dropped to keep
// the payload small.
type AHSearchPackage struct {
	PackageID         string  `json:"package_id"`
	Name              string  `json:"name"`
	NormalizedName    string  `json:"normalized_name,omitempty"`
	DisplayName       string  `json:"display_name,omitempty"`
	Description       string  `json:"description,omitempty"`
	LogoImageID       string  `json:"logo_image_id,omitempty"`
	Version           string  `json:"version,omitempty"`
	AppVersion        string  `json:"app_version,omitempty"`
	Stars             int     `json:"stars,omitempty"`
	Repository        AHRepo  `json:"repository"`
}

// AHRepo is the trimmed repository shape ArtifactHub embeds in each search hit
// + package-detail response.
type AHRepo struct {
	RepositoryID      string `json:"repository_id,omitempty"`
	Kind              int    `json:"kind"`
	Name              string `json:"name"`
	DisplayName       string `json:"display_name,omitempty"`
	URL               string `json:"url,omitempty"`
	OrganizationName  string `json:"organization_name,omitempty"`
	UserAlias         string `json:"user_alias,omitempty"`
	VerifiedPublisher bool   `json:"verified_publisher,omitempty"`
	Official          bool   `json:"official,omitempty"`
}

// ahSearchResponse is the envelope returned by /packages/search.
type ahSearchResponse struct {
	Packages []AHSearchPackage `json:"packages"`
}

// SearchHelm queries ArtifactHub for Helm packages matching q. Limit caps the
// number of returned hits (ArtifactHub pages by default; we just take page 1
// and let the UI handle "load more" later if we ever need to).
func (c *ArtifactHubClient) SearchHelm(ctx context.Context, q string, limit int) ([]AHSearchPackage, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, &ArtifactHubError{Class: AHErrInvalidInput, Message: "empty query"}
	}
	if limit <= 0 || limit > 60 {
		limit = 20
	}
	// kind=0 → Helm; ts_query_web is ArtifactHub's full-text search param.
	v := url.Values{}
	v.Set("ts_query_web", q)
	v.Set("kind", "0")
	v.Set("limit", fmt.Sprintf("%d", limit))
	v.Set("offset", "0")
	pURL := c.BaseURL + "/packages/search?" + v.Encode()

	body, status, err := c.do(ctx, pURL)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusOK:
		// fall through
	case status == http.StatusNotFound:
		return nil, &ArtifactHubError{Class: AHErrNotFound, Status: status}
	case status == http.StatusTooManyRequests:
		return nil, &ArtifactHubError{Class: AHErrRateLimited, Status: status}
	case status >= 500:
		return nil, &ArtifactHubError{Class: AHErrServerError, Status: status}
	default:
		return nil, &ArtifactHubError{Class: AHErrServerError, Status: status, Message: fmt.Sprintf("unexpected status %d", status)}
	}

	var resp ahSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &ArtifactHubError{Class: AHErrMalformed, Underlying: err}
	}
	return resp.Packages, nil
}

// ─── Package detail ────────────────────────────────────────────────────────

// AHPackage is the slimmed-down package detail shape returned by
// /packages/helm/{repo}/{chart}. We expose only the fields the UI consumes.
type AHPackage struct {
	PackageID         string             `json:"package_id"`
	Name              string             `json:"name"`
	NormalizedName    string             `json:"normalized_name,omitempty"`
	DisplayName       string             `json:"display_name,omitempty"`
	Description       string             `json:"description,omitempty"`
	HomeURL           string             `json:"home_url,omitempty"`
	Readme            string             `json:"readme,omitempty"`
	Version           string             `json:"version,omitempty"`
	AppVersion        string             `json:"app_version,omitempty"`
	License           string             `json:"license,omitempty"`
	Stars             int                `json:"stars,omitempty"`
	Maintainers       []AHMaintainer     `json:"maintainers,omitempty"`
	Repository        AHRepo             `json:"repository"`
	AvailableVersions []AHVersionMeta    `json:"available_versions,omitempty"`
	Links             []AHLink           `json:"links,omitempty"`
	Keywords          []string           `json:"keywords,omitempty"`
}

// AHMaintainer is the maintainer entry in a package detail response.
type AHMaintainer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// AHLink is a single labeled link in a package detail response.
type AHLink struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// AHVersionMeta is one version entry in available_versions.
type AHVersionMeta struct {
	Version    string `json:"version"`
	TS         int64  `json:"ts,omitempty"`
	Prerelease bool   `json:"prerelease,omitempty"`
}

// GetPackage fetches a single package by /helm/{repo}/{chart}. The "pkgID"
// here is the repo-name + chart-name pair carried in our route — ArtifactHub
// addresses packages by (repo-name, chart-name) for Helm kind.
func (c *ArtifactHubClient) GetPackage(ctx context.Context, repoName, chartName string) (*AHPackage, error) {
	if strings.TrimSpace(repoName) == "" || strings.TrimSpace(chartName) == "" {
		return nil, &ArtifactHubError{Class: AHErrInvalidInput, Message: "repo and chart name are required"}
	}
	pURL := fmt.Sprintf("%s/packages/helm/%s/%s",
		c.BaseURL, url.PathEscape(repoName), url.PathEscape(chartName))

	body, status, err := c.do(ctx, pURL)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusOK:
		// fall through
	case status == http.StatusNotFound:
		return nil, &ArtifactHubError{Class: AHErrNotFound, Status: status}
	case status == http.StatusTooManyRequests:
		return nil, &ArtifactHubError{Class: AHErrRateLimited, Status: status}
	case status >= 500:
		return nil, &ArtifactHubError{Class: AHErrServerError, Status: status}
	default:
		return nil, &ArtifactHubError{Class: AHErrServerError, Status: status, Message: fmt.Sprintf("unexpected status %d", status)}
	}

	var pkg AHPackage
	if err := json.Unmarshal(body, &pkg); err != nil {
		return nil, &ArtifactHubError{Class: AHErrMalformed, Underlying: err}
	}
	return &pkg, nil
}

// ─── Health probe ──────────────────────────────────────────────────────────

// Probe issues a tiny GET against the API root to detect whether ArtifactHub
// is reachable from this process. Returns nil on 2xx/3xx, classified error
// otherwise.
func (c *ArtifactHubClient) Probe(ctx context.Context) error {
	_, status, err := c.do(ctx, c.BaseURL+ahHealthEndpoint)
	if err != nil {
		return err
	}
	switch {
	case status >= 200 && status < 400:
		return nil
	case status == http.StatusTooManyRequests:
		return &ArtifactHubError{Class: AHErrRateLimited, Status: status}
	case status >= 500:
		return &ArtifactHubError{Class: AHErrServerError, Status: status}
	}
	// 4xx other than 429 still means the host is reachable; the probe path may
	// not exist on that ArtifactHub deployment. Treat as healthy.
	return nil
}

// ─── HTTP plumbing ─────────────────────────────────────────────────────────

func (c *ArtifactHubClient) do(ctx context.Context, fullURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, &ArtifactHubError{Class: AHErrInvalidInput, Underlying: err}
	}
	req.Header.Set("User-Agent", ahUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Distinguish context cancellation / timeout from generic network errors
		// — both surface as AHErrTimeout because the user can't tell them apart
		// and the recovery is the same (serve stale).
		return nil, 0, &ArtifactHubError{Class: AHErrTimeout, Underlying: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, ahMaxBody))
	if err != nil {
		return nil, resp.StatusCode, &ArtifactHubError{Class: AHErrTimeout, Status: resp.StatusCode, Underlying: err}
	}
	return body, resp.StatusCode, nil
}
