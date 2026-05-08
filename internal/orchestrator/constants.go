package orchestrator

// BootstrapRootAppName is the canonical ArgoCD application name created during
// first-run init. It MUST match metadata.name in templates/bootstrap/root-app.yaml.
//
// Drift between this constant and the template name causes Sharko to poll a
// non-existent ArgoCD application during step 4 of the first-run wizard,
// leading to a 2-minute timeout that historically (V124-14 / BUG-031) was
// silently swallowed and reported back to the wizard as success.
const BootstrapRootAppName = "cluster-addons-bootstrap"
