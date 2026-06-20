package orchestrator

import "strings"

// BootstrapRootAppName is the canonical ArgoCD application name created during
// first-run init. It MUST match metadata.name in templates/bootstrap/root-app.yaml.
//
// Drift between this constant and the template name causes Sharko to poll a
// non-existent ArgoCD application during step 4 of the first-run wizard,
// leading to a 2-minute timeout.
const BootstrapRootAppName = "cluster-addons-bootstrap"

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
// application YAML in the GitOps repo. The orchestrator commits the file at
// this path (CollectBootstrapFiles strips the "bootstrap/" prefix from
// repo-root files like root-app.yaml, configuration/, repository-secret.yaml,
// and README) and the API layer polls this path to detect a successful PR
// merge (isPRMerged) and to gate the already-initialized check.
//
// The orchestrator and the API layer MUST stay in sync — drift guarded by
// templates_test.go's TestCollectBootstrapFiles_RootAppPath_MatchesConstant.
const BootstrapRootAppPath = "root-app.yaml"
