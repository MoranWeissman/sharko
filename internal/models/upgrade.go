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
	AddonName string             `json:"addon_name"`
	Chart     string             `json:"chart"`
	RepoURL   string             `json:"repo_url"`
	Versions  []AvailableVersion `json:"versions"`
}
