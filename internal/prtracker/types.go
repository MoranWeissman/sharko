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

	// OpCatalogAdd is the approval pull request that writes catalog.yaml —
	// the one door into the org's approved list.
	OpCatalogAdd = "catalog-add"
	// OpCatalogAddEnable is the combined form: the same approval pull
	// request also switches the addon on for a cluster.
	OpCatalogAddEnable = "catalog-add-enable"
	// OpCatalogEdit is a merge-semantics edit of one existing catalog.yaml
	// entry (PATCH /api/v1/catalog/addons/{name}).
	OpCatalogEdit = "catalog-edit"
	// OpCatalogDelete removes one entry from catalog.yaml (DELETE
	// /api/v1/catalog/addons/{name}).
	OpCatalogDelete = "catalog-delete"

	// AI assistant write-tool bucket
	OpAIToolEnable  = "ai-tool-enable"
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

	// FollowUp is what Sharko still has to do to the cluster's ArgoCD
	// connection once this pull request merges (remove-cluster and
	// unadopt-cluster only; nil for every other operation). Nothing that
	// cuts a cluster off happens before its pull request is merged, so the
	// choices made at request time travel with the tracked pull request
	// and the merge hook reads them back. Stored in the same ConfigMap as
	// the rest of PRInfo; older entries without it decode to nil.
	FollowUp *ClusterFollowUp `json:"follow_up,omitempty"`

	// FinishAttempts counts how many times the finish step (see
	// Tracker.SetFinishFn) failed for this pull request after it merged or
	// closed. While it is above zero the entry is kept with its terminal
	// LastStatus so the next poll tries again.
	FinishAttempts int `json:"finish_attempts,omitempty"`
}

// ClusterFollowUp is the after-merge work a remove or un-adopt pull request
// carries. It holds no secret material: only the cleanup choice and where
// to look up the cluster's credentials (a lookup key, a source name and an
// optional role ARN), all read from the registry before the pull request
// removed the cluster's entry.
type ClusterFollowUp struct {
	// Cleanup is the remove cleanup choice: "all", "git" or "none".
	// Empty for un-adopt.
	Cleanup string `json:"cleanup,omitempty"`
	// KeepConnection says the ArgoCD connection stays. After the merge
	// Sharko takes its ownership label off the Secret instead of deleting
	// it, and the orphan sweep must never delete it in between.
	KeepConnection bool `json:"keep_connection,omitempty"`
	// CredentialKey, CredentialsSource and CredentialRoleARN say where the
	// cluster's credentials live, so the addon secrets Sharko put on the
	// cluster can be removed after the merge.
	CredentialKey     string `json:"credential_key,omitempty"`
	CredentialsSource string `json:"credentials_source,omitempty"`
	CredentialRoleARN string `json:"credential_role_arn,omitempty"`

	// Finish names the ownership step that waits for this pull request:
	// FinishAdopt or FinishTakeover. Empty for remove and un-adopt. The
	// finish step reads it, not the mark on the Secret, so an adopt or a
	// take over that merged before its mark was written still finishes
	// (EP3 dry run round R, R4 and R5).
	Finish string `json:"finish,omitempty"`
	// PreserveLegacyLabels is the take over choice to keep the previous
	// owner's labels. Only read when Finish is FinishTakeover.
	PreserveLegacyLabels bool `json:"preserve_legacy_labels,omitempty"`
}

// Finish kinds carried by ClusterFollowUp.Finish.
const (
	FinishAdopt    = "adopt"
	FinishTakeover = "takeover"
)
