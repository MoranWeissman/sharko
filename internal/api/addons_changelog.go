package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/helm"
)

// buildChangelogText converts a slice of changelog entries into a human-readable
// string suitable for inclusion in an AI prompt.
func buildChangelogText(addonName string, entries []changelogVersionEntry) string {
	var sb strings.Builder
	sb.WriteString("Addon: " + addonName + "\n\nVersions:\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- %s", e.Version))
		if e.AppVersion != "" {
			sb.WriteString(fmt.Sprintf(" (app: %s)", e.AppVersion))
		}
		if e.Created != "" {
			sb.WriteString(fmt.Sprintf(", released: %s", e.Created))
		}
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("\n  %s", e.Description))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// changelogVersionEntry is a single version entry returned by the changelog endpoint.
type changelogVersionEntry struct {
	Version     string `json:"version"`
	AppVersion  string `json:"app_version,omitempty"`
	Created     string `json:"created,omitempty"`
	Description string `json:"description,omitempty"`
}

// handleGetAddonChangelog godoc
//
// @Summary Get addon version changelog
// @Description Returns chart versions between two semver versions for comparison. When ?ai_summary=true and an AI provider is configured, an AI-generated summary is included.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param from query string false "Starting version (current) — exclusive lower bound"
// @Param to query string false "Target version — inclusive upper bound"
// @Param ai_summary query string false "Set to 'true' to include an AI-generated summary (requires AI provider)"
// @Success 200 {object} map[string]interface{} "Changelog between versions"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /addons/{name}/changelog [get]
func (s *Server) handleGetAddonChangelog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	fromVersion := r.URL.Query().Get("from")
	toVersion := r.URL.Query().Get("to")

	// Validate provided semver params
	if fromVersion != "" {
		if _, err := parseSemver(fromVersion); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'from' version: %v", err))
			return
		}
	}
	if toVersion != "" {
		if _, err := parseSemver(toVersion); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'to' version: %v", err))
			return
		}
	}

	// Get catalog to resolve chart name and repo URL
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	catalogData, err := gp.GetFileContent(r.Context(), "configuration/addons-catalog.yaml", "main")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("fetching addons catalog: %v", err))
		return
	}

	parser := config.NewParser()
	addons, err := parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("parsing addons catalog: %v", err))
		return
	}

	var chartName, repoURL, currentVersion string
	for _, a := range addons {
		if a.Name == name {
			chartName = a.Chart
			repoURL = a.RepoURL
			currentVersion = a.Version
			break
		}
	}
	if chartName == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf("addon %q not found in catalog", name))
		return
	}

	// Use from/to defaults from catalog if not provided
	if fromVersion == "" {
		fromVersion = currentVersion
	}

	// Fetch all available versions from the Helm repo
	fetcher := helm.NewFetcher()
	chartVersions, err := fetcher.ListVersions(r.Context(), repoURL, chartName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing chart versions: %v", err))
		return
	}

	// Filter versions: > from (exclusive) and <= to (inclusive).
	// If to is empty, include all versions > from.
	var filtered []changelogVersionEntry
	for _, cv := range chartVersions {
		cmp, err := compareSemver(cv.Version, fromVersion)
		if err != nil {
			// Skip versions that can't be parsed
			continue
		}
		if cmp <= 0 {
			// version <= from: skip
			continue
		}

		if toVersion != "" {
			cmpTo, err := compareSemver(cv.Version, toVersion)
			if err != nil {
				continue
			}
			if cmpTo > 0 {
				// version > to: skip
				continue
			}
		}

		filtered = append(filtered, changelogVersionEntry{
			Version:     cv.Version,
			AppVersion:  cv.AppVersion,
			Created:     cv.Created,
			Description: cv.Description,
		})
	}

	if filtered == nil {
		filtered = []changelogVersionEntry{}
	}

	response := map[string]interface{}{
		"addon_name":             name,
		"current_version":        fromVersion,
		"target_version":         toVersion,
		"versions":               filtered,
		"total_versions_between": len(filtered),
	}

	if r.URL.Query().Get("ai_summary") == "true" && s.aiClient != nil && s.aiClient.IsEnabled() {
		changelog := buildChangelogText(name, filtered)
		prompt := fmt.Sprintf("Summarize these Helm chart version changes for %s, highlighting breaking changes and security fixes:\n\n%s", name, changelog)
		summary, err := s.aiClient.SimplePrompt(r.Context(), prompt)
		if err == nil {
			response["ai_summary"] = summary
		}
	}

	writeJSON(w, http.StatusOK, response)
}

// semverParts holds the numeric parts of a semver string.
type semverParts struct {
	major, minor, patch int
	pre                 string // pre-release suffix, e.g. "alpha.1"
}

// parseSemver parses a semver string (with or without leading "v") into its components.
// It accepts "MAJOR", "MAJOR.MINOR", and "MAJOR.MINOR.PATCH" (with optional pre-release).
func parseSemver(v string) (semverParts, error) {
	v = strings.TrimPrefix(v, "v")
	// Split pre-release
	var pre string
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		pre = v[idx+1:]
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return semverParts{}, fmt.Errorf("invalid semver %q", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semverParts{}, fmt.Errorf("invalid major in %q: %w", v, err)
	}

	minor := 0
	if len(parts) >= 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return semverParts{}, fmt.Errorf("invalid minor in %q: %w", v, err)
		}
	}

	patch := 0
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return semverParts{}, fmt.Errorf("invalid patch in %q: %w", v, err)
		}
	}

	return semverParts{major: major, minor: minor, patch: patch, pre: pre}, nil
}

// compareSemver compares two semver strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Pre-release versions are considered lower than the release version.
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}

	if pa.major != pb.major {
		return cmpInt(pa.major, pb.major), nil
	}
	if pa.minor != pb.minor {
		return cmpInt(pa.minor, pb.minor), nil
	}
	if pa.patch != pb.patch {
		return cmpInt(pa.patch, pb.patch), nil
	}
	// Equal numeric parts — compare pre-release.
	// No pre-release > has pre-release.
	switch {
	case pa.pre == "" && pb.pre == "":
		return 0, nil
	case pa.pre == "":
		return 1, nil
	case pb.pre == "":
		return -1, nil
	default:
		if pa.pre < pb.pre {
			return -1, nil
		}
		if pa.pre > pb.pre {
			return 1, nil
		}
		return 0, nil
	}
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
