# End-to-End Testing

The Sharko end-to-end (e2e) suite is a Go-native test pack under `tests/e2e/`
that boots a real `sharko` HTTP server (in-process or via `helm install` into
a kind cluster), hits it with a typed API client, and asserts on the wire
responses. It supersedes the old `tests/e2e/setup.sh` + `kubectl port-forward`
shell flow and the legacy three-test stub from V1.x.

This guide is the developer reference: what's there, how to run it, how to
add to it, what knobs CI exposes. For the wider testing picture (unit /
component / API contract layers), see the [Testing Guide](testing-guide.md).

## Overview

The suite lives under `tests/e2e/` and is built around a re-usable harness:

```text
tests/e2e/
  harness/        — boot primitives (kind, sharko, GitFake, GitMock, API client)
  lifecycle/      — per-domain test files (cluster, catalog, auth, …)
```

Three-tier framing:

| Tier | What | Trigger | Cost |
|---|---|---|---|
| **Unit / component** | `internal/**/*_test.go`, `ui/src/**/__tests__/` | every PR (`ci.yml`) | seconds |
| **In-process e2e** | `tests/e2e/` with `make test-e2e-fast` | local + opt-in | ~30 s |
| **Full e2e (kind + argocd)** | `tests/e2e/` with `make test-e2e` | label `e2e`, nightly, manual | ~10–15 min |

Every file in `tests/e2e/` carries the `//go:build e2e` build tag, so the
default `go test ./...` excludes the suite. Opt in via `-tags=e2e`.

## Quick start

The fastest feedback loop, no docker / no kind required:

```bash
make test-e2e-fast
```

Runs the in-process tests only (~30 s on a laptop after first build). The
Sharko server is wired in-memory via `httptest`, the git server is the
in-memory `harness.GitFake`, and the GitHub provider is the in-memory
`harness.MockGitProvider`. No external services are touched.

The full lane that exercises kind + argocd + the real Helm chart:

```bash
make test-e2e
```

Requires `docker`, `kind`, `kubectl`. Takes ~10–15 min on a laptop. Use the
opt-in PR label `e2e` to run this in CI without paying the cost on every PR.

To run a single domain:

```bash
make test-e2e-domain DOMAIN=Cluster
make test-e2e-domain DOMAIN=Auth
```

The `DOMAIN` value is plugged straight into `go test -run`, so any pattern
that matches the top-level test names in `tests/e2e/lifecycle/*_test.go`
or `tests/e2e/harness/*_test.go` works.

## Architecture

The harness exposes a small set of primitives. Tests compose them as needed:

| Primitive | Purpose |
|---|---|
| `harness.ProvisionTopology(t, req)` | Provision a kind topology (1 mgmt + N targets). Sentinel-labelled for safe destroy. |
| `harness.InstallArgoCD(t, c)` | Install ArgoCD's stable release into a kind cluster. |
| `harness.StartSharko(t, cfg)` | Boot Sharko in-process (default) via `httptest.NewServer`, or in helm mode (deferred — see Known limitations). |
| `harness.StartGitFake(t)` | In-memory `go-git` HTTP smart-protocol server hosting one repo. The URL fed into Sharko's git config. |
| `harness.StartGitMock(t)` | In-memory `gitprovider.GitProvider` mock — overrides the real GitHub API for read/write paths. |
| `harness.NewClient(t, sharko, user, pass)` | Typed HTTP client that owns auth state (login + retry-on-401). |
| `harness.SeedUsers(t, sharko, users)` | Seed bootstrap users via the in-process `*api.Server`, bypassing the login rate limiter. |

Per-domain typed-client extensions live in `tests/e2e/harness/apiclient_<domain>.go`
(one file per slice — `apiclient_cluster.go`, `apiclient_catalog.go`, etc.).
They import `internal/models` and `internal/orchestrator` directly, so
schema drift breaks the harness at compile time — zero codegen, zero drift.

The full primitive surface is documented in `tests/e2e/harness/doc.go`.

## Writing a new e2e test

Create `tests/e2e/lifecycle/<domain>_test.go`. Minimum shape:

```go
//go:build e2e

package lifecycle

import (
    "testing"
    "time"

    "github.com/MoranWeissman/sharko/tests/e2e/harness"
)

func TestMyDomain(t *testing.T) {
    git := harness.StartGitFake(t)
    mock := harness.StartGitMock(t)
    sharko := harness.StartSharko(t, harness.SharkoConfig{
        Mode:        harness.SharkoModeInProcess,
        GitFake:     git,
        GitProvider: mock,
    })
    sharko.WaitHealthy(t, 10*time.Second)
    harness.SeedUsers(t, sharko, harness.DefaultTestUsers())
    admin := harness.NewClient(t, sharko, "admin", sharko.AdminPass)

    t.Run("HappyPath", func(t *testing.T) {
        got := admin.GetSomething(t, "param")
        if got.Field != "expected" {
            t.Fatalf("Field=%q want %q", got.Field, "expected")
        }
    })

    t.Run("InvalidInput_400", func(t *testing.T) {
        admin.PostSomethingExpect(t, badPayload{}, 400)
    })
}
```

Conventions:

- One top-level `TestXxx` per domain. Group endpoint-level scenarios as
  `t.Run("Subtest", ...)` so `make test-e2e-domain DOMAIN=Xxx` runs the
  whole slice as a unit.
- Add new endpoints to the typed client as a method on `*Client` in
  `tests/e2e/harness/apiclient_<domain>.go`, named after the handler
  (`GetClusters`, `PostAddonValues`, …).
- Don't fall back to raw `http.Client` — if the endpoint isn't in the
  typed client yet, add it. The test reads better and the next person
  writing a similar test gets the helper for free.

## Multi-cluster topology

Tests that need real Kubernetes use `harness.ProvisionTopology`:

```go
func TestSomethingNeedingClusters(t *testing.T) {
    // Skip when kind / docker / kubectl aren't on PATH. The harness
    // sets clear t.Skip messages, but explicit skips up-front make
    // CI logs faster to triage.
    if _, err := exec.LookPath("kind"); err != nil {
        t.Skip("kind not installed")
    }

    clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{
        NumTargets: 2, // 1 mgmt + 2 targets
    })
    t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
    mgmt, t1, t2 := clusters[0], clusters[1], clusters[2]
    _ = mgmt; _ = t1; _ = t2
}
```

Cluster names follow `sharko-e2e-{role}-{runID}` where `role` is `mgmt` or
`target-N` (1-indexed) and `runID` is a short timestamp+random tag.
Per-cluster kubeconfigs are written under `t.TempDir()` so they vanish on
test cleanup.

**Sentinel-label safety:** every node in every harness-provisioned cluster
carries `e2e.sharko.io/test=true` plus run-id and role labels.
`harness.DestroyAllStaleE2EClusters` enumerates kind clusters and destroys
ONLY those carrying the sentinel — your dev cluster from
`scripts/sharko-dev.sh` is never touched. Don't broaden the destroy
criteria; the sentinel is the safety contract.

## CI integration

`.github/workflows/e2e.yml` runs the full lane on three triggers:

- **`workflow_dispatch`** — manual ad-hoc runs from the Actions tab.
- **PR labelled `e2e`** — adding the `e2e` label to a pull request runs
  the suite against that PR. Cheap PRs stay on the unit/UI matrix in
  `ci.yml`; only label-tagged PRs pay the e2e tax.
- **Nightly schedule** — `0 3 * * *` (3am UTC) catches drift in the
  kind / argocd / helm chart triplet between PRs.

The job: checkout → set up Go (`go-version-file: go.mod`) → install kind +
kubectl → `make test-e2e` → cleanup stale `sharko-e2e-*` kind clusters.
Concurrency group `e2e-${ref}` cancels in-progress runs when a new commit
lands.

The fast in-process lane is **not** wired into `ci.yml` today — it runs
locally via `make test-e2e-fast`. Wiring it into the unit pipeline once
the suite stabilises is a follow-up.

## Env-var matrix

| Variable | Default | What it does |
|---|---|---|
| `E2E_KIND_IMAGE` | `kindest/node:v1.31.0` | kindest/node image used by `ProvisionTopology`. Override to test against a different K8s minor. |
| `E2E_KIND_BIN` | `kind` | Path to the `kind` binary. |
| `E2E_KUBECTL_BIN` | `kubectl` | Path to the `kubectl` binary. |
| `E2E_SHARKO_MODE` | `in-process` | `helm` switches `StartSharko` to the helm-install path (currently deferred — see Known limitations). |
| `E2E_OFFLINE` | `0` | When `1`, tests skip live network catalog reads (ArtifactHub, GitHub raw fetches). Useful in air-gapped CI runners. |
| `E2E_SKIP_KIND` | unset | When set, kind-required tests skip themselves regardless of binary availability. Use on saturated dev hosts where Docker Desktop is at capacity. |
| `E2E_AI_API_KEY` | unset | When set, `TestAIInvocation` actually invokes the AI provider; otherwise it skips. |
| `E2E_GIT_BACKEND` | `mock` | `github` switches the git provider to live GitHub (foundation hook present, full wiring deferred — see Known limitations). |
| `GOTMPDIR` | `/tmp` (set by Make) | Forces go-test temp dirs out of `/var/folders` on macOS to avoid disk-pressure flakes. |

## Troubleshooting

**`make test-e2e-fast` hangs on a kind-using test.** The fast lane regex
explicitly excludes the five kind-required top-level tests
(`TestHarnessKindMultiCluster`, `TestPerClusterAddonLifecycle`,
`TestClusterLifecycle`, `TestConnectionsDiscoverAndTest`,
`TestFleetStatusWithArgocd`). If you've added a new kind-using test,
extend the exclusion in the `test-e2e-fast` target's `-run` regex.

**`kind create cluster` fails with `kubeadm init: killed`.** OOM. Docker
Desktop's allocated memory isn't enough. Bump it (Settings → Resources →
Memory) to at least 8 GB, or run `make kind-down` to clean stale clusters
that may be holding RAM.

**Four-or-more-cluster tests time out on macOS.** Docker Desktop on
Apple Silicon saturates around 4 simultaneous kind clusters. Run kind-using
tests serially (`go test -p 1 ...`) or split the topology into two
sequential `ProvisionTopology` calls.

**`make test-e2e-fast` is slow (~60 s).** First run pays the `go build`
cost for the e2e build tag. Subsequent runs (no code changes) hit the
test cache and finish in ~10 s. Use `-count=1` if you genuinely want a
fresh run.

**`Resource temporarily unavailable` from kind.** Docker socket
contention. `kind get clusters` and `kind delete cluster` from another
shell can collide with the harness's parallel provisioning. Run
`make kind-down` to fully reset, then re-run.

**Stale clusters from a previous run.** The harness destroys what it
created via `t.Cleanup`, but a panicked test or `^C` can leave clusters
behind. `make kind-down` enumerates `sharko-e2e-*` and destroys them all.

## Known limitations

- **No demo argocd in the harness yet.** Tests that need argocd call
  `harness.InstallArgoCD(t, c)` against a real kind cluster. A faster
  in-process argocd shim is on the V2.x backlog.
- **Helm-mode `StartSharko` is deferred.** `SharkoModeHelm` and
  `E2E_SHARKO_MODE=helm` currently `t.Skip()` with a clear diagnostic.
  Story 7-1.10 is the placeholder; until it lands, every test runs
  in-process.
- **Helm `Fetcher` has no test seam.** Preview-merge and annotate paths
  reach the network for chart pulls. Tests that hit those paths skip
  when the network is unavailable; a `Fetcher` interface + mock is the
  next refactor.
- **`/connections/test-credentials` builds a fresh GH provider per call.**
  The harness's `MockGitProvider` injection is bypassed for that one
  endpoint. Tests assert on the request-shape error (4xx) rather than
  on a mocked success path.
- **`E2E_GIT_BACKEND=github` is a placeholder.** The flag is parsed and
  threaded through but the live-GitHub backing isn't fully wired. Do
  not rely on it in V124.

## Coverage

Per-domain test-file map (top-level test → file → endpoint count):

| Top-level test | File | Endpoints exercised |
|---|---|---|
| `TestHealthEndpoint`, `TestFoundationStack`, `TestHarness*` | `harness/*_test.go` | harness self-tests |
| `TestClusterLifecycle` | `lifecycle/cluster_test.go` | 21 subtests, 15 cluster endpoints |
| `TestPerClusterAddonLifecycle` | `lifecycle/addon_cluster_test.go` | per-cluster addon CRUD + sync |
| `TestAddonAdmin`, `TestAddonSecretsLifecycle` | `lifecycle/addon_admin_test.go` | catalog + custom addon admin |
| `TestCatalogReads`, `TestMarketplaceAddFlow` | `lifecycle/catalog_test.go` | catalog reads + marketplace add |
| `TestAuthFlow`, `TestAuthUpdatePassword`, `TestTokensCRUD`, `TestRBACEnforcement` | `lifecycle/auth_test.go` | login + token CRUD + role enforcement |
| `TestConnectionsCRUDAndInit`, `TestConnectionsDiscoverAndTest` | `lifecycle/init_test.go` | connections + initial setup |
| `TestAIConfig`, `TestAIInvocation` | `lifecycle/ai_test.go` | AI provider config + invocation |
| `TestGlobalValuesEditor`, `TestPerClusterValuesOverride` | `lifecycle/values_test.go` | global + per-cluster values + AI opt-out |
| `TestPRTracking`, `TestNotificationsLifecycle` | `lifecycle/pr_test.go` | PR tracker + notifications |
| `TestDashboardAndReadsInProcess`, `TestFleetStatusWithArgocd` | `lifecycle/dashboard_test.go` | dashboard + observability + reads |

For the broader strategy and gap analysis, see the design doc
`docs/design/2026-05-13-test-coverage-strategy.md` in the repo (not
published to the docs site).
