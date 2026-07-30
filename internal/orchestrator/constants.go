package orchestrator

import "strings"

// BootstrapRootAppName is the canonical ArgoCD application name created during
// first-run init.
//
// v4 Wave 1 Story 4.2: the bootstrap path no longer creates a
// "cluster-addons-bootstrap" root Application that fans out to a Helm
// chart of ApplicationSet templates. Instead it applies exactly ONE
// ArgoCD Application — the engine pin (design doc
// docs/design/2026-07-30-v4-data-file-format.md §2.5) — whose
// metadata.name is "sharko-engine" (see EnginePinPath /
// engineversion.BundledChartName). BuildV4SeedFiles is the single writer
// of that name; there is no separate template to drift from any more —
// the constant and the generator are the same source.
const BootstrapRootAppName = "sharko-engine"

// ConnectivityCheckAppPrefix is the prefix of the host-side ArgoCD
// connectivity-probe Application, named "connectivity-check-<clusterName>".
const ConnectivityCheckAppPrefix = "connectivity-check-"

// IsSharkoSystemApp reports whether name is one of Sharko's own ArgoCD system
// apps (the bootstrap root Application or a per-cluster connectivity-check
// probe). These apps are NOT catalog addons and must not be rendered as
// clickable addon links in the UI — doing so causes 404s because the name
// does not map to any catalog entry.
func IsSharkoSystemApp(name string) bool {
	return name == BootstrapRootAppName || strings.HasPrefix(name, ConnectivityCheckAppPrefix)
}

// BootstrapRootAppPath is the canonical commit path of the ArgoCD root
// application YAML in the GitOps repo. The v4 seed (BuildV4SeedFiles)
// commits the engine pin at exactly this path — design doc §2.5's
// "engine/application.yaml" — and the API layer polls this same path to
// detect a successful PR merge (isPRMerged) and to gate the
// already-initialized check (repo_status.go, init_status.go).
//
// This is also EnginePinPath (internal/orchestrator/enginepin.go) — kept as
// two names because they answer two different questions (what bootstrap
// writes vs. what the pin-bump machinery edits), but they MUST stay the
// same literal value; enginepin_test.go pins the equality.
const BootstrapRootAppPath = "engine/application.yaml"
