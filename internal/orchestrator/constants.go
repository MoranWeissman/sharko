package orchestrator

// BootstrapRootAppName is the canonical ArgoCD application name created during
// first-run init. It MUST match metadata.name in templates/bootstrap/root-app.yaml.
//
// Drift between this constant and the template name causes Sharko to poll a
// non-existent ArgoCD application during step 4 of the first-run wizard,
// leading to a 2-minute timeout.
const BootstrapRootAppName = "cluster-addons-bootstrap"

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
