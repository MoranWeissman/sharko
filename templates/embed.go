package templates

import "embed"

// StarterFS contains the starter addons repo template.
// Used by the orchestrator's InitRepo to generate new repos.
//
//go:embed all:starter
var StarterFS embed.FS
