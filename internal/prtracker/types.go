package prtracker

import "time"

// Canonical Operation codes used by the dashboard PR panel filter chips.
// The FE categorizes each PR into one of four buckets (Clusters /
// Addons / Init / AI) by matching this string.
//
// New PR-creating paths MUST use one of these constants — ad-hoc
// strings get dropped into the "default" gray bucket by the FE.
const (
	// Init bucket
	OpInitRepo = "init-repo"

	// Clusters bucket
	OpRegisterCluster = "register-cluster"
	OpRemoveCluster   = "remove-cluster"
	OpUpdateCluster   = "update-cluster"
	OpAdoptCluster    = "adopt-cluster"
	OpUnadoptCluster  = "unadopt-cluster"

	// Addons bucket
	OpAddonAdd       = "addon-add"
	OpAddonRemove    = "addon-remove"
	OpAddonEnable    = "addon-enable"
	OpAddonDisable   = "addon-disable"
	OpAddonConfigure = "addon-configure"
	OpAddonUpgrade   = "addon-upgrade"
	OpValuesEdit     = "values-edit"
	OpAIAnnotate     = "ai-annotate"

	// AI assistant write-tool bucket
	OpAIToolEnable = "ai-tool-enable"
	OpAIToolDisable = "ai-tool-disable"
	OpAIToolUpdate  = "ai-tool-update"
)

// PRInfo describes a tracked pull request.
type PRInfo struct {
	PRID       int       `json:"pr_id"`
	PRUrl      string    `json:"pr_url"`
	PRBranch   string    `json:"pr_branch"`
	PRTitle    string    `json:"pr_title"`
	PRBase     string    `json:"pr_base"`
	Cluster    string    `json:"cluster,omitempty"`
	Addon      string    `json:"addon,omitempty"`
	Operation  string    `json:"operation"` // see Op* constants above — canonical enum
	User       string    `json:"user"`
	Source     string    `json:"source"` // ui, cli, api
	CreatedAt  time.Time `json:"created_at"`
	LastStatus string    `json:"last_status"` // open, merged, closed
	LastPolled time.Time `json:"last_polled_at"`
}
