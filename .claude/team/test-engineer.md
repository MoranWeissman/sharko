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

## Current Test State (verified)

### Backend — 30 tests passing
```
internal/api/
  health_test.go           TestHealthEndpoint, TestCORSHeaders, TestConnectionsListEmpty

internal/argocd/
  client_write_test.go     TestSyncApplication_Success, TestSyncApplication_Non200ReturnsError,
                           TestRefreshApplication_Hard, TestRefreshApplication_Normal

internal/config/
  k8s_store_test.go        12 tests (SaveAndList, GetConnection, Delete, Active, FirstDefault,
                           Update, Persist, WrongKey, DeleteActive, IsDefault, Empty)
  parser_test.go           TestParseClusterAddons, TestParseAddonsCatalog, TestGetEnabledAddons,
                           TestParseClusterValues, TestParseAll

internal/crypto/
  crypto_test.go           TestEncryptDecrypt, TestDecryptWrongKey, TestEncryptEmptyKey, TestDecryptEmptyKey

internal/gitops/
  yaml_mutator_test.go     (tests exist)

internal/gitprovider/
  github_write_test.go     (tests exist)
  azuredevops_impl_test.go (tests exist)

internal/models/
  connection_test.go       (tests exist)

internal/orchestrator/
  orchestrator_test.go     11 tests (RegisterCluster direct/PR/partial/providerFail/invalidName,
                           DeregisterCluster, UpdateClusterAddons, AddAddon, RemoveAddon,
                           GenerateClusterValues, GenerateClusterValues_NoAddons)

internal/providers/
  k8s_secrets_test.go      4 tests (GetCredentials valid/missing/missingKey, ListClusters)
  provider_test.go         6 tests (factory routing for k8s-secrets, kubernetes, aws-sm, 
                           aws-secrets-manager, unknown, empty)

internal/service/
  addon_test.go            (tests exist)

internal/helm/
  diff_test.go             (tests exist)
  fetcher_test.go          (tests exist)
```

### Frontend — 105 tests passing across 19 test files
Located in `ui/src/views/__tests__/` and component test files.

## Coverage Gaps (needs tests)

### High Priority (existing code, no tests)
- `internal/api/clusters_write.go` — no HTTP-level tests for POST/DELETE/PATCH cluster handlers
- `internal/api/addons_write.go` — no HTTP-level tests for POST/DELETE addon handlers
- `internal/api/init.go` — no test for handleInit
- `internal/api/fleet.go` — no test for handleGetFleetStatus
- `internal/api/system.go` — no tests for handleGetProviders, handleTestProvider, handleGetConfig

### Medium Priority (existing code)
- `internal/argocd/client_write.go` — RegisterCluster, DeleteCluster, UpdateClusterLabels, CreateProject, CreateApplication not tested with httptest
- `internal/orchestrator/init.go` — InitRepo not tested (needs embedded FS + mock git + mock argocd)
- `cmd/sharko/` — no CLI command tests (would need mock HTTP server)

### Low Priority
- `internal/ai/` — no tests (complex, provider-dependent)
- `internal/auth/store.go` — no tests

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
