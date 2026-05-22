# Test Engineer Agent

## Scope

**DO:** Write tests, run test suites, track coverage, mock patterns
**DO NOT:** Write feature code, modify CI pipelines, change API contracts

You write and maintain tests for the Sharko project.

## Testing Stack
- Go: `testing` package (no third-party framework)
- React: Vitest (`npm test` in `ui/`)
- K8s mocking: `k8s.io/client-go/kubernetes/fake`
- HTTP testing: `net/http/httptest`
- Schema validation: `github.com/santhosh-tekuri/jsonschema/v5` (V125-1-9)
- e2e harness: `tests/e2e/{harness,lifecycle}` — kind multi-cluster + in-cluster gitfake Pod
  (V125-1-13)
- Coverage reporting: `make test-e2e-coverage` → `_dist/e2e-coverage.html`; JUnit XML via
  `make test-e2e-junit` → `_dist/e2e-junit.xml`

## Test command map
```
make test                  # all (Go + UI)
make test-go               # Go only (clean cache + ./...)
make test-ui               # UI only (vitest --run)
make test-e2e-fast         # ~30s, in-process e2e, no kind
make test-e2e              # ~10-15 min, kind-backed full e2e
make test-e2e-domain DOMAIN=Cluster   # single domain
make test-e2e-helm         # Wave-D helm-mode subset (requires docker+kind+helm+kubectl)
make test-e2e-clean        # nuke every sharko-e2e-* kind cluster (manual recovery)
make test-e2e-coverage     # coverage HTML in _dist/
make test-e2e-junit        # JUnit XML in _dist/
```

## Current Test State

Backend tests are co-located as `*_test.go` next to source under `internal/*`. The codebase has
grown well beyond the v0.1.0 baseline — significant test coverage exists in:

- `internal/schema/` (envelope_test, validator_test, generator_test — V125-1-9)
- `internal/clusterreconciler/` (labels_test, poll_test, reconciler_test — V125-1-8; per-instance
  test seams, NOT package-level vars — see V125-1-8.0 race-fix lesson under "Patterns")
- `internal/prtracker/` (lifecycle, SetOnMergeFn callback paths)
- `internal/argosecrets/` (Manager Ensure/Delete, Reconciler tick)
- `internal/orchestrator/` (Register/Adopt/Unadopt/Remove/Upgrade flows + findOpenPRForCluster
  idempotent retry)
- `internal/providers/` (typed configs from V125-1-11: AddonSecretProviderConfig,
  ClusterTestProviderConfig, ClusterRegSourceProviderConfig + ArgoCDProvider auto-default +
  cross-contamination namespace fix from V125-1-10)
- `internal/api/` (audit_coverage_test enforces every mutating handler calls audit.Enrich)
- `internal/verify/`, `internal/observations/`, `internal/diagnose/`, `internal/metrics/`,
  `internal/authz/`, `internal/cmstore/` — all have dedicated test files
- `cmd/sharko/` (validate_config_test, root_test, login_test, reset_admin_test, serve_test,
  client_test)

E2E tests live under `tests/e2e/{harness,lifecycle}` (V125-1-13). The lifecycle directory
contains per-domain `*_test.go` files; the harness directory contains shared apiclient_*.go
helpers plus the in-cluster gitfake Pod scaffolding.

Frontend tests are vitest, co-located alongside views/components.

## Patterns established by V125-1-8 / V125-1-9 work

### Per-instance test seams (not package-level vars)

V125-1-8.0 surfaced a race-fix lesson: do NOT introduce package-level test-doubles
(`var nowFn = time.Now`) when multiple test goroutines may mutate them. Instead, give the
struct a `nowFn`, `tickInterval`, `gitProviderFn` field on its Deps struct, default it in the
constructor, and override per-test. This keeps tests parallel-safe (`t.Parallel()`).

### `slog` for non-HTTP reader paths

`internal/audit` is request-scoped via context Enrich. For non-HTTP code paths (reconciler poll
loops, schema validator startup, etc.) use `log/slog` directly. Do NOT route reconciler logs
through audit.Enrich — there's no request context to attach to (V125-1-8.1 finding).

### `sync.Once` for one-shot lifecycle

Reconciler Start() must be safe to call multiple times. Use `sync.Once` to guard the goroutine
spawn rather than a boolean+mutex dance.

### Schema-validation unit-test shape (V125-1-9)

```go
// Round-trip an envelope and assert the schema validator accepts it.
spec := models.ManagedClustersSpec{Clusters: []models.ManagedCluster{...}}
body, err := models.SaveManagedClusters(spec)        // emits enveloped YAML
require.NoError(t, err)

err = schema.DefaultValidator().Validate("managed-clusters.yaml", body)
require.NoError(t, err)
```

## v1.0.0 New Test Areas

### Phase 1: Git Mutex & Concurrency
- Two concurrent RegisterCluster calls on different clusters: both succeed, no Git conflicts
- Two concurrent calls on same cluster: one gets 409
- Batch processes sequentially, all PRs succeed
- Git mutex prevents branch/merge race conditions

### Phase 2: PR-Only Git
- Every operation creates a PR (never direct commit)
- Auto-merge: PR created and merged, `merged: true` in response
- Manual: PR created, not merged, `merged: false` in response
- Merge failure: partial success response

### Phase 3: Remote Cluster Secrets (`internal/remoteclient/`)
- Create secret on remote cluster (mock K8s client)
- Delete secret from remote cluster
- Full RegisterCluster flow: secrets created before PR merge
- Full DeregisterCluster flow: secrets deleted after addon removal
- Partial success: secret creation fails, PR stays open
- Addon without secret definition: no secret operations, just labels

### Phase 4: API Keys (`internal/auth/`)
- Create token, use it to authenticate, verify access
- Revoke token, verify it no longer works
- Token with viewer role can't call write endpoints (403)
- List tokens never shows plaintext
- Last used timestamp updates

### Phase 5: Init Rework
- Full init flow: templates → repo added to ArgoCD → project → app → sync verified
- Already initialized: 409
- Sync timeout: partial success with ArgoCD error
- Auto-bootstrap on startup when conditions met
- Missing connection: clear error

### Phase 6: Batch Operations
- Batch processes clusters sequentially, returns all results
- Batch respects max size (10), rejects larger with 400
- One failed cluster doesn't block the rest
- Discover endpoint cross-references provider and ArgoCD
- CLI auto-splits large batches
- Partial batch: some succeed, some fail, report is clear

### Phase 7: UI Tests
- Role-based rendering: viewer sees no buttons, admin sees all
- Form validation matches API validation
- Loading states work correctly (spinner during synchronous API call)
- Destructive operations require confirmation
- API key modal shows token once

### Phase 8: Upgrades, Defaults & Sync Waves
- Global upgrade changes catalog version, opens PR
- Per-cluster upgrade changes cluster values file, opens PR
- Multi-addon upgrade creates one PR with all changes
- Version check queries Helm repo correctly
- Cluster with per-cluster override is NOT affected by global upgrade
- Default addons merge correctly
- Explicit addons override defaults
- Sync wave annotation in generated AppSet entry
- Host cluster deploys to in-cluster

## Mock Patterns (exact code from codebase)

### Mock ArgoCD Client (`internal/orchestrator/orchestrator_test.go`)
```go
type mockArgocd struct {
    registeredClusters map[string]string    // name → server
    deletedServers     []string
    updatedLabels      map[string]map[string]string // server → labels
    syncedApps         []string
    registerErr        error
}
func (m *mockArgocd) RegisterCluster(_ context.Context, name, server string, _ []byte, _ string, _ map[string]string) error { ... }
func (m *mockArgocd) DeleteCluster(_ context.Context, serverURL string) error { ... }
func (m *mockArgocd) UpdateClusterLabels(_ context.Context, serverURL string, labels map[string]string) error { ... }
func (m *mockArgocd) SyncApplication(_ context.Context, appName string) error { ... }
func (m *mockArgocd) CreateProject(_ context.Context, _ []byte) error { return nil }
func (m *mockArgocd) CreateApplication(_ context.Context, _ []byte) error { return nil }
```
**v1.0.0:** Will need `AddRepository` method added to mock (Phase 5)

### Mock Git Provider (`internal/orchestrator/orchestrator_test.go`)
```go
type mockGitProvider struct {
    files        map[string][]byte
    deletedFiles []string
    branches     []string
    prs          []*gitprovider.PullRequest
    createErr    error
}
func (m *mockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, _, _ string) error { ... }
func (m *mockGitProvider) DeleteFile(_ context.Context, path, _, _ string) error { ... }
func (m *mockGitProvider) CreateBranch(_ context.Context, name, _ string) error { ... }
func (m *mockGitProvider) CreatePullRequest(_ context.Context, title, _, _, _ string) (*gitprovider.PullRequest, error) { ... }
// + GetFileContent, ListDirectory, ListPullRequests, TestConnection, MergePullRequest, DeleteBranch
```

### Fake K8s Client (`internal/providers/k8s_secrets_test.go`)
```go
client := fake.NewSimpleClientset(
    &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name: "test-cluster", Namespace: "sharko",
            Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko", "region": "eu-west-1"},
        },
        Data: map[string][]byte{"kubeconfig": []byte(testKubeconfig)},
    },
)
provider := newKubernetesSecretProviderWithClient(client, "sharko")
```

### v1.0.0 New Mock Pattern

**Mock Remote Client** (for orchestrator tests):
```go
type mockRemoteClient struct {
    createdSecrets map[string][]corev1.Secret  // cluster → secrets
    deletedSecrets map[string][]string          // cluster → secret names
    createErr      error
}
```

## Rules
- Every new feature must have tests before merge
- Check `json.Unmarshal` errors in test assertions too
- Verify partial success (207) paths explicitly
- Table-driven tests for validation logic
- Run `go test ./...` and `cd ui && npm test` before declaring done

## Update This File When
- New tests are added (move from "Coverage Gaps" to "Current Test State")
- New mock patterns are established
- Test infrastructure changes
- Phase completion adds new test areas
