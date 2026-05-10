package orchestrator

// BootstrapRootAppName is the canonical ArgoCD application name created during
// first-run init. It MUST match metadata.name in templates/bootstrap/root-app.yaml.
//
// Drift between this constant and the template name causes Sharko to poll a
// non-existent ArgoCD application during step 4 of the first-run wizard,
// leading to a 2-minute timeout that historically (V124-14 / BUG-031) was
// silently swallowed and reported back to the wizard as success.
const BootstrapRootAppName = "cluster-addons-bootstrap"

// BootstrapRootAppPath is the canonical commit path of the ArgoCD root
// application YAML in the GitOps repo. The orchestrator commits the file at
// this path (CollectBootstrapFiles strips the "bootstrap/" prefix from
// repo-root files like root-app.yaml, configuration/, repository-secret.yaml,
// and README) and the API layer polls this path to detect a successful PR
// merge (isPRMerged) and to gate the already-initialized check
// (runInitOperation, V124-15 / BUG-034).
//
// The orchestrator and the API layer MUST stay in sync — drift guarded by
// templates_test.go's TestCollectBootstrapFiles_RootAppPath_MatchesConstant.
//
// Pre-V124-20 / BUG-045: pollPRMerge probed "bootstrap/root-app.yaml" while
// CollectBootstrapFiles emits the file at "root-app.yaml" at repo root. The
// GitHub provider only logs on 200, so 404s were silent — the wizard hung
// forever on "Waiting for PR merge" while the file sat at the correct path
// the whole time.
const BootstrapRootAppPath = "root-app.yaml"
