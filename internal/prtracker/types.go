package prtracker

import "time"

// Canonical Operation codes used by the dashboard PR panel filter chips.
// V125-1-6 unified the previously-ad-hoc Operation strings into a small
// fixed enum so the FE can categorize each PR into one of four buckets
// (Clusters / Addons / Init / AI) without parsing free-form text.
//
// New PR-creating paths MUST use one of these constants — do not invent
// new ad-hoc strings, as the FE filter chips silently drop unknown
// operations into the "default" gray bucket.
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
