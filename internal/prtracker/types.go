package prtracker

import "time"

// PRInfo describes a tracked pull request.
type PRInfo struct {
	PRID       int       `json:"pr_id"`
	PRUrl      string    `json:"pr_url"`
	PRBranch   string    `json:"pr_branch"`
	PRTitle    string    `json:"pr_title"`
	PRBase     string    `json:"pr_base"`
	Cluster    string    `json:"cluster,omitempty"`
	Operation  string    `json:"operation"` // register, remove, adopt, etc.
	User       string    `json:"user"`
	Source     string    `json:"source"` // ui, cli, api
	CreatedAt  time.Time `json:"created_at"`
	LastStatus string    `json:"last_status"` // open, merged, closed
	LastPolled time.Time `json:"last_polled_at"`
}
