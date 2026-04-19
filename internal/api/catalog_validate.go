package api

// catalog_validate.go — V121-4.1: power-user "Paste Helm URL" validation
// endpoint. Confirms that an arbitrary `<repo>/index.yaml` is reachable, that
// the named chart exists in it, and returns the available versions + a few
// metadata fields the Configure modal pre-fills (description, icon).
//
// Distinct from /catalog/addons/{name}/versions:
//   • That endpoint takes a curated catalog entry name and looks the repo up
//     internally — no untrusted input enters the helm fetcher.
//   • This endpoint takes the repo URL + chart name straight from the user;
//     we validate the URL shape, classify failures, and never write anything.
//
// Failure taxonomy (mirrored from the V121-3 ArtifactHub proxy contract — keep
// the strings stable so the UI's switch table doesn't need to change per
// release):
//
//   invalid_input    — repo or chart missing / empty / not a URL
//   repo_unreachable — DNS, connection refused, TLS, non-200 from index.yaml
//   index_parse_error— index.yaml fetched but YAML is malformed
//   chart_not_found  — repo OK + parsed, but the chart isn't in entries[]
//   timeout          — context.DeadlineExceeded during the upstream call
//
// All non-input errors return HTTP 200 with `valid: false` so the UI can render
// the inline message without parsing HTTP status. invalid_input returns 400
// because the request itself is malformed.
//
// Read-only — no audit.Enrich call; not added to HandlerTier (only mutating
// handlers are tracked).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// validateRequestErrorCode enumerates the structured error codes returned in
// the JSON body when validation fails. UI keys its inline messages off these.
type validateErrorCode string

const (
	validateErrInvalidInput    validateErrorCode = "invalid_input"
	validateErrRepoUnreachable validateErrorCode = "repo_unreachable"
	validateErrIndexParseError validateErrorCode = "index_parse_error"
	validateErrChartNotFound   validateErrorCode = "chart_not_found"
	validateErrTimeout         validateErrorCode = "timeout"
)

// catalogValidateResponse is the success envelope. On `valid: false` only
// `valid`, `repo`, `chart`, `error_code`, and `message` are populated.
type catalogValidateResponse struct {
	Valid        bool                  `json:"valid"`
	Chart        string                `json:"chart"`
	Repo         string                `json:"repo"`
	Description  string                `json:"description,omitempty"`
	IconURL      string                `json:"icon_url,omitempty"`
	Versions     []catalogVersionEntry `json:"versions,omitempty"`
	LatestStable string                `json:"latest_stable,omitempty"`
	CachedAt     string                `json:"cached_at,omitempty"`
	ErrorCode    validateErrorCode     `json:"error_code,omitempty"`
	Message      string                `json:"message,omitempty"`
}

// handleValidateCatalogChart godoc
//
// @Summary Validate a Helm chart at an arbitrary repo URL
// @Description Power-user "Paste Helm URL" validator: fetches `<repo>/index.yaml`, parses it, and confirms the named chart exists. On success returns the available versions, the latest stable version, and a few metadata fields (description, icon) for the Configure modal to pre-fill. On failure returns HTTP 200 with `valid: false` and a structured `error_code` (invalid_input, repo_unreachable, index_parse_error, chart_not_found, timeout) so the UI can render an inline message without HTTP-status branching. Responses are cached server-side for 15 minutes per (repo, chart) pair — the same TTL semantics as `/catalog/addons/{name}/versions`. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param repo query string true "Helm repository base URL (e.g. https://charts.jetstack.io)"
// @Param chart query string true "Chart name as listed in the repo's index.yaml entries (e.g. cert-manager)"
// @Success 200 {object} catalogValidateResponse "Validation result — `valid` distinguishes success vs structured failure"
// @Failure 400 {object} map[string]interface{} "Missing or malformed repo / chart query parameter"
// @Router /catalog/validate [get]
func (s *Server) handleValidateCatalogChart(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	chart := strings.TrimSpace(r.URL.Query().Get("chart"))

	if repo == "" || chart == "" {
		writeError(w, http.StatusBadRequest, "repo and chart query parameters are required")
		return
	}
	if err := validateRepoURL(repo); err != nil {
		writeJSON(w, http.StatusOK, catalogValidateResponse{
			Valid:     false,
			Repo:      repo,
			Chart:     chart,
			ErrorCode: validateErrInvalidInput,
			Message:   err.Error(),
		})
		return
	}

	// Normalise the repo URL so trailing-slash variants share a cache slot and
	// the downstream fetcher gets a consistent input.
	normalisedRepo := strings.TrimRight(repo, "/")
	cacheKey := "validate|" + normalisedRepo + "|" + chart

	if cached, ok := lookupCachedVersions(cacheKey); ok {
		writeJSON(w, http.StatusOK, validateResponseFromCached(cached, chart, normalisedRepo))
		return
	}

	// 8s budget — same as the curated versions endpoint. Long enough for a
	// slow Helm repo over the public internet, short enough that the modal
	// stays responsive.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	versions, err := catalogVersionsFetcher.ListVersions(ctx, normalisedRepo, chart)
	if err != nil {
		writeJSON(w, http.StatusOK, classifyValidateError(err, normalisedRepo, chart))
		return
	}

	// Build the standard versions response (sorted, latest_stable computed)
	// and synthesize the validate envelope around it. `cached_at` and the
	// version cache are reused so a subsequent /catalog/versions call against
	// the same repo+chart hits warm.
	versionsResp := buildVersionsResponse(chart, chart, normalisedRepo, versions)
	storeCachedVersions(cacheKey, versionsResp)

	out := catalogValidateResponse{
		Valid:        true,
		Chart:        chart,
		Repo:         normalisedRepo,
		Versions:     versionsResp.Versions,
		LatestStable: versionsResp.LatestStable,
		CachedAt:     versionsResp.CachedAt,
	}
	// Description + icon come from the latest entry in the index — most charts
	// keep them stable across versions, so picking the first (newest) is a
	// reasonable default.
	if len(versions) > 0 {
		out.Description = strings.TrimSpace(versions[0].Description)
		out.IconURL = strings.TrimSpace(versions[0].Icon)
	}
	writeJSON(w, http.StatusOK, out)
}

// validateRepoURL rejects obvious garbage before we hit the network. Keeps the
// "invalid_input" branch for things the user can fix locally (typo'd scheme,
// blank host) and saves the "repo_unreachable" branch for genuine network /
// HTTP failures.
func validateRepoURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("repo URL must use http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("repo URL is missing a host")
	}
	return nil
}

// classifyValidateError turns a fetcher error into the structured 200 response
// the UI consumes. The fetcher only returns wrapped fmt.Errorf strings today,
// so we string-match the well-known prefixes — fragile but contained, and
// easier than threading a typed error through the helm package.
func classifyValidateError(err error, repo, chart string) catalogValidateResponse {
	out := catalogValidateResponse{Valid: false, Repo: repo, Chart: chart, Message: err.Error()}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		out.ErrorCode = validateErrTimeout
		out.Message = "timed out fetching index.yaml from " + repo
	case strings.Contains(err.Error(), "not found in repo"):
		out.ErrorCode = validateErrChartNotFound
		out.Message = "chart " + chart + " is not present in this repository's index.yaml"
	case strings.Contains(err.Error(), "parsing index"):
		out.ErrorCode = validateErrIndexParseError
		out.Message = "index.yaml at " + repo + " is malformed: " + stripErrorPrefix(err.Error(), "parsing index: ")
	case strings.Contains(err.Error(), "fetching index returned status"),
		strings.Contains(err.Error(), "fetching index"),
		strings.Contains(err.Error(), "reading index"):
		out.ErrorCode = validateErrRepoUnreachable
		out.Message = "could not fetch index.yaml from " + repo + ": " + err.Error()
	default:
		out.ErrorCode = validateErrRepoUnreachable
	}
	return out
}

// stripErrorPrefix is a small helper to keep error messages readable when we
// know the leading wrap from the fetcher. Returns the original on no match.
func stripErrorPrefix(msg, prefix string) string {
	if strings.HasPrefix(msg, prefix) {
		return strings.TrimPrefix(msg, prefix)
	}
	return msg
}

// validateResponseFromCached reshapes the cached versions response into the
// validate envelope. Description + icon aren't stored in the versions cache
// (would require a second cache shape) so they show up empty on warm hits;
// the modal already pre-fills them on the cold call.
func validateResponseFromCached(cached catalogVersionsResponse, chart, repo string) catalogValidateResponse {
	return catalogValidateResponse{
		Valid:        true,
		Chart:        chart,
		Repo:         repo,
		Versions:     cached.Versions,
		LatestStable: cached.LatestStable,
		CachedAt:     cached.CachedAt,
	}
}
