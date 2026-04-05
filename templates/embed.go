package templates

import "embed"

// TemplateFS contains the addons repo bootstrap templates.
// Used by the orchestrator's InitRepo to generate new repos.
//
//go:embed all:bootstrap
var TemplateFS embed.FS
