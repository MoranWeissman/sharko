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
	return name == BootstrapRootAppName ||
		name == BootstrapRootAppNameLegacy ||
		strings.HasPrefix(name, ConnectivityCheckAppPrefix)
}

// BootstrapRootAppNameLegacy is the v3 root Application's name. v4 Wave 1
// Story 4.2 renamed the bootstrap app from this to "sharko-engine", but the
// rename only changes what NEW bootstraps create — every repo bootstrapped
// before it still runs an Application called this, and that Application is
// still the thing whose health the dashboard tile, the repo-status probe,
// and the post-merge refresh are asking about. Only ever READ.
const BootstrapRootAppNameLegacy = "cluster-addons-bootstrap"

// BootstrapAppNames lists the bootstrap Application names to look for, most
// current first. Callers probing/refreshing "the bootstrap app" walk this
// slice and take the first hit, so a v3 cluster is found by its legacy name
// instead of being reported as "bootstrap missing" while it is perfectly
// healthy.
func BootstrapAppNames() []string {
	return []string{BootstrapRootAppName, BootstrapRootAppNameLegacy}
}

// BootstrapRootAppPath is the canonical commit path of the ArgoCD root
// application YAML in the GitOps repo: a root file called
// "sharko-engine.yaml". The v4 seed (BuildV4SeedFiles) commits the engine
// pin at exactly this path, and the API layer polls the same path to
// detect a successful PR merge (isPRMerged) and to gate the
// already-initialized check (repo_status.go, init_status.go).
//
// The folder went away with the rest of the v4 layout (design doc
// 2026-07-31-catalog-approved-model.md §2) — one file needs no folder. The
// name is "sharko-engine.yaml", not "engine.yaml" (v4 naming polish): it is
// the ArgoCD Application that deploys the sharko-engine chart, so the file
// name matches the chart it pins instead of the generic word "engine".
//
// This is the one file in the repo that is a REAL Kubernetes manifest: an
// ArgoCD Application that actually gets applied to the hub cluster. So it
// keeps the full apiVersion/kind/metadata/spec form, unlike every file
// Sharko merely READS out of git (see internal/schema's package comment for
// that split).
//
// This is also EnginePinPath (internal/orchestrator/enginepin.go) — kept as
// two names because they answer two different questions (what bootstrap
// writes vs. what the pin-bump machinery edits), but they MUST stay the
// same literal value; enginepin_test.go pins the equality.
const BootstrapRootAppPath = "sharko-engine.yaml"

// V3BootstrapMarkerPath is the file every v3 bootstrap PR wrote and every
// v3 repo therefore still has: the bootstrap Helm chart's Chart.yaml. It
// was the "is this repo initialized?" probe until v4 Wave 1 Story 4.2
// re-pointed that check at the engine pin.
//
// It is still READ (never written) because the engine pin alone answers the
// wrong question for a v3 repo: a perfectly healthy, fully-populated v3
// repo has no engine pin, so an engine-pin-only probe reports it as "never
// bootstrapped" and the first-run wizard invites the user to Initialize —
// which would seed the v4 folder tree on top of a live v3 repo. Recognising
// this marker is what keeps that from happening (see repo_status.go,
// init_status.go, and InitRepo's already-initialized refusal).
const V3BootstrapMarkerPath = "bootstrap/Chart.yaml"

// V3SecondaryMarkerPath is the second v3 signal, checked when the primary
// one is absent: the v3 cluster registry. A repo that Sharko adopted rather
// than bootstrapped (or one whose bootstrap chart was reorganised by hand)
// may have no bootstrap/Chart.yaml but definitely has this file the moment
// a single cluster is registered.
const V3SecondaryMarkerPath = "configuration/managed-clusters.yaml"

// RepoFormatV3 / RepoFormatV4 are the values of the additive `format` field
// the repo-state probes report, so the UI can say which layout it is
// looking at instead of inferring it from a bare boolean.
const (
	RepoFormatV3 = "v3"
	RepoFormatV4 = "v4"
)
