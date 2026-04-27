// Package api — v1.21 QA Bundle 4 Fix #3b: fetch the upstream project's
// README (distinct from the Helm chart README).
//
//   GET /api/v1/catalog/addons/{name}/project-readme
//   GET /api/v1/catalog/remote/{repo}/{name}/project-readme
//
// Why this exists: the Marketplace detail view already renders the Helm
// chart README (from ArtifactHub), but maintainer feedback:
//
//   "README is for the helm chart which is good, but I was aiming to README
//    of the tool itself too. do you think readme of the helm chart is enough?
//    sometimes its a bit lacking."
//
// The chart's README is often a three-line "install with helm" blurb;
// the project's README is the thing a new user actually wants. We already
// know how to reach the project's GitHub repo (via Chart.yaml sources /
// homepage) — this endpoint wraps that as a server-side proxy so the
// browser doesn't hit the GitHub API directly (auth + CORS reasons).
//
// Cache: 1 h fresh / 24 h stale, piggybacks on packageCache with a
// "proj-readme:<owner>/<repo>" key so multiple charts sharing the same
// GitHub repo (e.g. cert-manager chart & controller) hit a single entry.

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// projectReadmeResponse is the wire shape returned to the browser.
type projectReadmeResponse struct {
	// Readme is the decoded markdown body. Empty when the lookup failed
	// or the repo doesn't publish a README — the UI shows an explicit
	// empty state rather than a spinning loader.
	Readme string `json:"readme"`

	// SourceURL is the resolved GitHub URL of the README (e.g.
	// https://github.com/cert-manager/cert-manager/blob/master/README.md).
	// Surfaced so the UI can render a "View on GitHub" link.
	SourceURL string `json:"source_url,omitempty"`

	// Available is true when we successfully resolved a GitHub repo AND
	// fetched a non-empty README. False means either (a) the chart's
	// homepage wasn't a GitHub URL, or (b) the GitHub README API returned
	// 404 / the repo has no README.md.
	Available bool `json:"available"`

	// Reason carries a short explanation when Available is false so the
	// UI can render a concrete message instead of "something went wrong".
	Reason string `json:"reason,omitempty"`
}

// handleGetCuratedProjectReadme godoc
//
// @Summary Get the upstream project README for a curated catalog addon
// @Description Fetches the project's own README from GitHub (distinct from the Helm chart README). Resolves via the curated entry's `source_url` or `homepage`; returns an empty body + reason when the entry doesn't point at a GitHub repo. Cached server-side so the upstream GitHub API is not hit on every page view.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Curated addon name"
// @Success 200 {object} projectReadmeResponse
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Router /catalog/addons/{name}/project-readme [get]
func (s *Server) handleGetCuratedProjectReadme(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	// V123-PR-B (H1): merged-catalog lookup so third-party snapshot entries
	// resolve here too. 404 wording unchanged so existing clients/tests
	// keep working.
	entry, ok := s.mergedCatalogGet(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not found in curated catalog")
		return
	}

	// Prefer source_url (explicitly the project repo) over homepage (which
	// is sometimes a docs site, sometimes the repo).
	candidates := []string{entry.SourceURL, entry.Homepage}
	out := fetchProjectReadme(r.Context(), candidates)
	writeJSON(w, http.StatusOK, out)
}

// handleGetRemoteProjectReadme godoc
//
// @Summary Get the upstream project README for an ArtifactHub-discovered addon
// @Description Same as the curated variant but resolves the GitHub repo from the ArtifactHub package's `home_url`. Used for Marketplace entries that came from ArtifactHub search rather than Sharko's curated list.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param repo path string true "ArtifactHub repo name"
// @Param name path string true "ArtifactHub package name"
// @Success 200 {object} projectReadmeResponse
// @Router /catalog/remote/{repo}/{name}/project-readme [get]
func (s *Server) handleGetRemoteProjectReadme(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.PathValue("repo"))
	pkg := strings.TrimSpace(r.PathValue("name"))
	if repo == "" || pkg == "" {
		writeError(w, http.StatusBadRequest, "repo and name are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ahPkg, err := ahClient.GetPackage(ctx, repo, pkg)
	if err != nil {
		// Degrade gracefully — the UI can still render the Helm chart
		// README tab, so we return "not available" rather than 502.
		writeJSON(w, http.StatusOK, projectReadmeResponse{
			Available: false,
			Reason:    "ArtifactHub package not available",
		})
		return
	}
	out := fetchProjectReadme(r.Context(), []string{ahPkg.HomeURL})
	writeJSON(w, http.StatusOK, out)
}

// fetchProjectReadme tries each URL in order, returning the first successful
// README fetch from GitHub. Falls back to an Available=false response when
// none of the URLs look like a GitHub repo or the GitHub README API fails.
//
// Caching is keyed on "proj-readme:<owner>/<repo>" so repeated page loads
// for the same addon don't re-hit GitHub.
func fetchProjectReadme(ctx context.Context, candidateURLs []string) projectReadmeResponse {
	owner, repo := firstGitHubRepoFromURLs(candidateURLs)
	if owner == "" || repo == "" {
		return projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (chart metadata does not point at a GitHub repo)",
		}
	}

	cacheKey := "proj-readme:" + owner + "/" + repo
	if v, fresh, _, ok := packageCache.Get(cacheKey); ok && fresh {
		return v.(projectReadmeResponse)
	}

	// GitHub's README endpoint. Anonymous for now — the same HTTP client
	// used elsewhere carries no auth header. We still get 60 req/hr
	// unauthenticated, well within the cache's 1h fresh window.
	readmeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme",
		url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		out := projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available",
		}
		return out
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (GitHub unreachable)",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		out := projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (repository has no README.md)",
		}
		// Cache the negative result so we don't retry every page view.
		packageCache.Put(cacheKey, out)
		return out
	}
	if resp.StatusCode >= 400 {
		return projectReadmeResponse{
			Available: false,
			Reason:    fmt.Sprintf("Project README not available (GitHub returned %d)", resp.StatusCode),
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (read failed)",
		}
	}
	var payload struct {
		Content     string `json:"content"`
		Encoding    string `json:"encoding"`
		HTMLURL     string `json:"html_url"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (unparseable response)",
		}
	}

	decoded := payload.Content
	if strings.EqualFold(payload.Encoding, "base64") {
		// GitHub wraps the base64 at 60 chars per line — remove whitespace
		// before decoding.
		clean := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, payload.Content)
		if b, derr := base64.StdEncoding.DecodeString(clean); derr == nil {
			decoded = string(b)
		}
	}

	if strings.TrimSpace(decoded) == "" {
		out := projectReadmeResponse{
			Available: false,
			Reason:    "Project README not available (empty README)",
		}
		packageCache.Put(cacheKey, out)
		return out
	}

	out := projectReadmeResponse{
		Readme:    decoded,
		SourceURL: payload.HTMLURL,
		Available: true,
	}
	packageCache.Put(cacheKey, out)
	return out
}

// firstGitHubRepoFromURLs returns (owner, repo) from the first URL in the
// slice that looks like a GitHub URL. Returns ("", "") when none match.
func firstGitHubRepoFromURLs(urls []string) (string, string) {
	for _, raw := range urls {
		if raw == "" {
			continue
		}
		owner, repo := parseGitHubOwnerRepo(raw)
		if owner != "" && repo != "" {
			return owner, repo
		}
	}
	return "", ""
}

// parseGitHubOwnerRepo extracts (owner, repo) from a GitHub URL. Accepts
// the common variants:
//
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - https://github.com/owner/repo/tree/main/...
//   - git@github.com:owner/repo.git
//
// Returns ("", "") for non-GitHub URLs or malformed inputs.
func parseGitHubOwnerRepo(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	// Strip scheme-like prefixes.
	for _, pref := range []string{"https://", "http://", "git+https://", "git+http://", "ssh://git@", "git@"} {
		if strings.HasPrefix(s, pref) {
			s = strings.TrimPrefix(s, pref)
			break
		}
	}
	// git@github.com:owner/repo — replace the colon so the split below works.
	s = strings.Replace(s, "github.com:", "github.com/", 1)
	if !strings.HasPrefix(s, "github.com/") {
		return "", ""
	}
	s = strings.TrimPrefix(s, "github.com/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", ""
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	// Strip anything trailing — query strings, fragments, extra slashes.
	if i := strings.IndexAny(repo, "?#"); i >= 0 {
		repo = repo[:i]
	}
	if owner == "" || repo == "" {
		return "", ""
	}
	return owner, repo
}
