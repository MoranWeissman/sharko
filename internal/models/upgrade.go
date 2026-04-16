package models

// AvailableVersion represents a single version available for a Helm chart.
type AvailableVersion struct {
	Version    string `json:"version"`
	AppVersion string `json:"app_version,omitempty"`
}

// UpgradeCheckRequest is the request body for the upgrade check endpoint.
type UpgradeCheckRequest struct {
	AddonName     string `json:"addon_name"`
	TargetVersion string `json:"target_version"`
}

// UpgradeCheckResponse is the API response for an upgrade impact check.
type UpgradeCheckResponse struct {
	AddonName      string               `json:"addon_name"`
	Chart          string               `json:"chart"`
	CurrentVersion string               `json:"current_version"`
	TargetVersion  string               `json:"target_version"`
	TotalChanges   int                  `json:"total_changes"`
	Added          []ValueDiffEntry     `json:"added"`
	Removed        []ValueDiffEntry     `json:"removed"`
	Changed        []ValueDiffEntry     `json:"changed"`
	Conflicts      []ConflictCheckEntry `json:"conflicts"`
	ReleaseNotes   string               `json:"release_notes,omitempty"`

	// BaselineUnavailable is true when the current version is not available
	// in the Helm repository and no suitable fallback was found.
	BaselineUnavailable bool   `json:"baseline_unavailable,omitempty"`
	// BaselineNote describes which baseline version was used for comparison
	// (e.g., when falling back to a nearby version or when no baseline is available).
	BaselineNote string `json:"baseline_note,omitempty"`
}

// ValueDiffEntry represents a single value difference between chart versions.
type ValueDiffEntry struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

// ConflictCheckEntry represents a conflict between configured values and changed defaults.
type ConflictCheckEntry struct {
	Path            string `json:"path"`
	ConfiguredValue string `json:"configured_value"`
	OldDefault      string `json:"old_default"`
	NewDefault      string `json:"new_default"`
	Source          string `json:"source"` // "global" or cluster name
}

// AvailableVersionsResponse is the API response for listing available chart versions.
type AvailableVersionsResponse struct {
	AddonName      string             `json:"addon_name"`
	Chart          string             `json:"chart"`
	RepoURL        string             `json:"repo_url"`
	CurrentVersion string             `json:"current_version,omitempty"` // catalog default version
	Versions       []AvailableVersion `json:"versions"`
}

// UpgradeRecommendations contains smart upgrade suggestions for an addon.
type UpgradeRecommendations struct {
	CurrentVersion string `json:"current_version"`

	// Legacy fields — kept for backwards compatibility with existing UI consumers.
	NextPatch    string `json:"next_patch,omitempty"`    // same major.minor, higher patch
	NextMinor    string `json:"next_minor,omitempty"`    // same major, higher minor, latest patch of that minor
	LatestStable string `json:"latest_stable,omitempty"` // latest non-prerelease version overall

	// Cards provides structured, security-aware upgrade options for richer UI rendering.
	Cards []RecommendationCard `json:"cards,omitempty"`
	// Recommended is the version string of the card that Sharko considers the best upgrade path.
	Recommended string `json:"recommended,omitempty"`
}

// RecommendationCard is a single upgrade option with security and breaking-change metadata.
type RecommendationCard struct {
	Label           string `json:"label"`                      // e.g. "Patch", "Latest in 1.x", "Latest Stable"
	Version         string `json:"version"`
	HasSecurity     bool   `json:"has_security"`               // one or more versions along the path fix security issues
	HasBreaking     bool   `json:"has_breaking"`               // crossing a major boundary or flagged as breaking
	CrossMajor      bool   `json:"cross_major"`                // version major differs from current major
	AdvisorySummary string `json:"advisory_summary,omitempty"` // e.g. "2 security fixes", "breaking change detected"
	IsRecommended   bool   `json:"is_recommended"`             // true for the single recommended card
	Reason          string `json:"reason,omitempty"`           // human-readable explanation, only set on the recommended card
}
