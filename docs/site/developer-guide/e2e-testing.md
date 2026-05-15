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
| **Helm-mode e2e (V125-1-13)** | Wave-D `cluster_test_*` files with `make test-e2e-helm` | every PR (`e2e.yml::helm-mode-e2e`) | ~5–8 min |
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
| `harness.StartSharko(t, cfg)` | Boot Sharko in-process (default) via `httptest.NewServer`, or via `helm install` into a kind cluster (`SharkoModeHelm` / `E2E_SHARKO_MODE=helm`; see [Full-fidelity Helm mode](#full-fidelity-helm-mode-v125-1-13)). |
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

## Full-fidelity Helm mode (V125-1-13)

Helm mode boots a real Sharko Docker image into a kind cluster via
`helm install` and talks to it through a `kubectl port-forward`. It catches
integration bugs that in-process mode cannot — UI dropdown ↔ backend factory
drift, server-startup gating, ProviderConfig field reuse across ReinitializeFromConnection,
ArgoCD secret reconciliation timing — at the cost of ~3–5 min per test
(docker build dominates, ~1–3 min cold; ~20s warm via the
`SHARKO_E2E_IMAGE_TAG` cache).

Three primitives carry the mode end-to-end:

| Primitive | File | Role |
|---|---|---|
| `installSharkoHelm(t, kindCluster, cfg)` | `tests/e2e/harness/sharko_helm.go` | docker build → kind load → `helm upgrade --install` → `kubectl rollout status`. Returns `*HelmHandle`. |
| `bootstrapHelmSharkoAuth(t, helmHandle)` | `tests/e2e/harness/sharko_helm_auth.go` | Reads `sharko-initial-admin-secret` via client-go → spawns `kubectl port-forward svc/sharko -n <ns> :80` (random local port) → polls `/api/v1/health` → POSTs `/api/v1/auth/login` for a JWT. Returns `*AuthBundle`. |
| `StartSharko(t, cfg)` with `cfg.Mode = SharkoModeHelm` | `tests/e2e/harness/sharko.go` | Wires the two primitives above into the same `*Sharko` shape the in-process path returns, so the typed API client (`apiclient.go`) is mode-agnostic. |

### When to use Helm mode

- Testing the **auto-default provider path** — `rest.InClusterConfig()` only
  succeeds inside a real pod, so the V125-1-10.7 ungate fix is invisible to
  in-process tests.
- Testing **UI ↔ backend contract end-to-end** — provider-type dropdown
  changes, registration wizard flows that hit the live router stack.
- Reproducing **operator-flow bugs** — port-forward, admin-secret bootstrap,
  ArgoCD reconciliation timing, RBAC against a real ServiceAccount.
- **Regression-pinning** a fix that came out of a live dev install — the
  three Wave-D tests (`TestClusterTest_*`) are exactly this: each pins a
  V125-1-10.x bug that the in-process suite missed.

### When NOT to use Helm mode

- Pure in-process logic tests — use the default `SharkoModeInProcess`.
- Anything that doesn't need the real K8s API surface.
- Any test where in-process can prove the same invariant in 30s vs Helm
  mode's 5 min — Helm mode is for things in-process *cannot* prove.

### Running locally

Prerequisites (all on `PATH`):

- `docker` — daemon must be reachable (the test skips with a clear message
  if `docker info` fails).
- `kind` — install via `brew install kind` or
  [kind.sigs.k8s.io](https://kind.sigs.k8s.io/).
- `helm` — install via `brew install helm`.
- `kubectl` — install via `brew install kubectl` or your distro's package.

Then:

```bash
make test-e2e-helm
```

This runs the three Wave-D Helm-mode tests
(`TestClusterTest_ArgoCDProvider`,
`TestClusterTest_ProviderAutoDefault_HappyPath`,
`TestClusterTest_ProviderCrossContamination_NamespaceSwitch`) with
`E2E_SHARKO_MODE=helm` and a `SHARKO_E2E_IMAGE_TAG` pinned to your current
short SHA, so back-to-back runs at the same commit reuse the previously-built
image (the harness probes containerd via `docker exec <kind>-control-plane
crictl images`).

To re-target a single Helm-mode test:

```bash
SHARKO_E2E_IMAGE_TAG=e2e-$(git rev-parse --short HEAD) \
  E2E_SHARKO_MODE=helm \
  GOTMPDIR=/tmp \
  go test -tags=e2e -timeout=20m -v \
  -run '^TestClusterTest_ProviderAutoDefault_HappyPath$' \
  ./tests/e2e/lifecycle/...
```

### Writing a Helm-mode test

The minimum shape (lifted from
`tests/e2e/lifecycle/cluster_test_provider_autodefault_test.go` —
the V125-1-13.5 regression pin for V125-1-10.7's `serve.go` ungate):

```go
//go:build e2e

package lifecycle

import (
    "os/exec"
    "testing"
    "time"

    "github.com/MoranWeissman/sharko/tests/e2e/harness"
)

func TestMyHelmModeRegression(t *testing.T) {
    // ---- prereq guards: skip cleanly when host can't run kind+helm ----
    if _, err := exec.LookPath("kind"); err != nil {
        t.Skip("kind not installed; install via `brew install kind`")
    }
    if _, err := exec.LookPath("kubectl"); err != nil {
        t.Skip("kubectl not installed")
    }
    if _, err := exec.LookPath("docker"); err != nil {
        t.Skip("docker not installed (required by kind)")
    }
    if _, err := exec.LookPath("helm"); err != nil {
        t.Skip("helm not installed; install via `brew install helm`")
    }
    if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
        t.Skipf("docker daemon not reachable: %v\noutput: %s", err, out)
    }

    // ---- safety: clean up stale e2e clusters from prior failed runs ----
    harness.DestroyAllStaleE2EClusters(t)

    // ---- provision topology + install ArgoCD ----
    clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 0})
    t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
    mgmt := clusters[0]
    harness.WaitClusterReady(t, mgmt, 90*time.Second)
    harness.InstallArgoCD(t, mgmt)

    // ---- start Sharko via Helm — the load-bearing line is Mode + MgmtCluster ----
    gitfake := harness.StartGitFake(t)
    sharko := harness.StartSharko(t, harness.SharkoConfig{
        Mode:        harness.SharkoModeHelm,
        MgmtCluster: &mgmt,
        GitFake:     gitfake,
        // HelmOverrides: extra `--set key=value` pairs; nil keeps chart defaults.
    })
    sharko.WaitHealthy(t, 60*time.Second)
    admin := harness.NewClient(t, sharko)

    // ---- assert against the live HTTP surface ----
    var resp map[string]any
    admin.GetJSON(t, "/api/v1/providers", &resp)
    // ...
}
```

Conventions:

- Always include the **prereq + skip block** above. CI provisions all four
  binaries; local laptops may not. An explicit `t.Skip` is the contract.
- Always call **`harness.DestroyAllStaleE2EClusters(t)`** before
  `ProvisionTopology` — a panicked previous run can leave kind clusters
  behind that will starve docker of memory.
- Use **`harness.SharkoModeHelm` explicitly** rather than relying on
  `E2E_SHARKO_MODE=helm`. The env-var override is for `make test-e2e-helm`'s
  blanket re-routing; per-test code should be unambiguous.
- The typed API client (`apiclient.go`) is **mode-agnostic** — `admin.GetJSON`,
  `admin.PostJSON`, `admin.Do` all work identically against the
  port-forwarded URL.
- Document **why** the test needs Helm mode in a comment near the top — the
  in-process boot path is faster and reviewers will ask. The Wave-D files
  are the canonical examples ("Why SharkoModeHelm is required" subsections).

### Troubleshooting

**Test hangs on `helm upgrade --install` for 5 minutes then fails with
rollout timeout.** The Sharko pod failed to become Ready. The harness
auto-dumps `kubectl describe deployment/sharko` + `kubectl logs --tail=30`
on rollout-wait failure (see `dumpDeploymentState` in
`tests/e2e/harness/sharko_helm.go`); read those lines in the failed test
output. Most common cause: the Docker image build smuggled a config error
(env var or flag) that the binary rejects on boot.

**`docker build` runs every single test on a stable SHA.** The image cache
key is `SHARKO_E2E_IMAGE_TAG`. `make test-e2e-helm` defaults it to
`e2e-$(git rev-parse --short HEAD)`. If you're running `go test` directly
without setting it, the harness generates a fresh `e2e-<8-hex>` tag per
invocation — you pay the full build every time. Export
`SHARKO_E2E_IMAGE_TAG=e2e-$(git rev-parse --short HEAD)` once in your shell
and back-to-back runs hit the cache.

**`port-forward never printed Forwarding line within 30s`.** Either the
Sharko pod isn't actually Ready (rare — `helm --wait` + `kubectl rollout
status` gate this) or another `kubectl port-forward` is fighting for the
target svc. The harness uses the `:<svcPort>` random-local-port form
(`startSharkoPortForward` in `sharko_helm_auth.go`) so the host-side bind
should not collide; if it does, run `lsof -i -P | grep kubectl` to find
the offender.

**`secret <ns>/sharko-initial-admin-secret not found within 60s`.** The
auth-store writes that Secret during boot, but the write can race a few
hundred ms behind the readiness probe in practice. The harness already
polls for 60s; if you're hitting this, the auth-store is wedged — check
`kubectl logs -n sharko deployment/sharko` for an init failure (e.g.
panic in `cmd/sharko/serve.go` writeInitialAdminSecretCLI path).

**Test passes in `make test-e2e-helm` but the cluster `test` flow fails
with "git unreachable".** Host ↔ pod network gotcha. The in-cluster Sharko
pod cannot reach a `127.0.0.1:NNNN` URL on your laptop — that loopback
address means localhost *inside the pod*. Tests that drive a register or
sync flow through the real git path either (a) skip the assertion (the
Wave-D `_HappyPath` test does this — it asserts on `/providers`
introspection instead of round-tripping through git) or (b) front the
GitFake with a routable address (an open follow-up; see Known
limitations).

**`make test-e2e-helm` fails clean on the first run, passes on the
second.** Stale image in containerd from a previous SHA. `make kind-down`
clears every `sharko-e2e-*` kind cluster (and with it the containerd
storage). The cleanup hook runs on `t.Cleanup` so a panicked test or
`^C` is the usual cause.

### CI wiring

`.github/workflows/e2e.yml` declares two jobs:

| Job | Trigger | Runtime | What it runs |
|---|---|---|---|
| `e2e` | label `e2e`, nightly, manual | ~10–15 min | `make test-e2e-report` — full suite. |
| `helm-mode-e2e` (V125-1-13.8) | **every** PR push | ~5–8 min | `make test-e2e-helm` — Wave-D subset. |

The split is deliberate. `helm-mode-e2e` is unlabelled and PR-blocking
because the maintainer chose runtime-honesty over speed: the real Helm
boot path is the path that ships, so a PR that breaks it should not be
mergeable. The `e2e` lane stays label-gated because its 15-min cost would
make every PR slower without a proportional bug-catch payoff over the
unit/UI matrix.

`helm-mode-e2e` installs `kind` + `kubectl` + `helm` (via
`azure/setup-helm@v4`) on top of the ubuntu-latest runner's pre-installed
`docker`, sets `SHARKO_E2E_IMAGE_TAG=e2e-${{ github.sha }}` so the
docker-build cache hits on workflow re-runs of the same commit, and uploads
deployment + ArgoCD diagnostics on failure (see the `Diagnostics on
failure` step in `e2e.yml`).

### Provider-types contract (V125-1-13.7)

A separate guard catches a class of UI ↔ backend drift that even Helm
mode would miss without explicit assertions: the Settings →
SecretsProviderSection dropdown is generated from the backend factory at
build time, not hardcoded.

| Concern | Location |
|---|---|
| Source of truth | `internal/providers/provider.go::New` switch |
| Generator | `cmd/gen-provider-types` (parses the AST, emits TS) |
| Generated artifact | `ui/src/generated/provider-types.ts` (gitignored? **No** — checked in) |
| CI guard | `Provider Types Up To Date` job in `.github/workflows/ci.yml` |
| Local regenerate | `make generate-provider-types` |

When you add a new provider arm to the `New()` switch, also run
`make generate-provider-types` and commit the regenerated TS file. CI's
`provider-types-up-to-date` job runs the generator and `git diff
--exit-code` — a stale TS file fails the PR with a one-line remediation
hint pointing at this same command.

The pattern mirrors the swagger-docs-up-to-date check
(`make` runs `swag init`, CI diffs the result). Both are zero-runtime
contracts — the generator is invoked at build/CI time, never at server
start.

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

## Reports

The e2e suite emits two machine-readable reports — coverage HTML and
JUnit XML — both written to `_dist/` (gitignored). Locally they're an
opt-in convenience target; in CI they're uploaded as artifacts and
surfaced inline as PR check-run summaries.

### First-time setup

```bash
make install-test-tools
```

Installs `gotest.tools/gotestsum` (Go's standard JUnit-emitting test
runner) via `go install`, which writes the binary to
`$(go env GOPATH)/bin/gotestsum` (typically `~/go/bin/gotestsum`). Only
needed once per machine.

You do **not** need to add `$(go env GOPATH)/bin` to your `$PATH` — the
report targets resolve `gotestsum` from that location automatically
(falling back to whatever `PATH` provides if you've already wired it up
yourself). So `make install-test-tools && make test-e2e-report` works
out of the box on a fresh checkout.

### Local report targets

| Target | Output | When to use |
|---|---|---|
| `make test-e2e-coverage` | `_dist/e2e-coverage.html` | Full suite + see which `internal/*` lines the e2e tests hit |
| `make test-e2e-fast-coverage` | `_dist/e2e-coverage.html` | Same, fast lane only (~30 s) |
| `make test-e2e-junit` | `_dist/e2e-junit.xml` | JUnit XML for CI tooling / IDE test panels |
| `make test-e2e-report` | both at once | One-shot for CI parity locally |

The key flag in every coverage target is `-coverpkg=./internal/...,./cmd/...`.
Without it, the profile only measures `tests/e2e/*` itself (useless —
the e2e tests are always 100% covered by definition). With it, the
report shows which lines of sharko's actual production code got
executed by the suite.

After running, the targets print absolute `file://` URLs that iTerm2,
Terminal.app, and the VSCode integrated terminal auto-linkify — Cmd+click
opens the report in your browser. Sample tail of `make test-e2e-report`:

```
==> JUnit XML:        file:///Users/you/code/sharko/_dist/e2e-junit.xml
==> Coverage HTML:    file:///Users/you/code/sharko/_dist/e2e-coverage.html
```

If your terminal doesn't linkify, fall back to `open` / `xdg-open`:

```bash
open _dist/e2e-coverage.html      # macOS
xdg-open _dist/e2e-coverage.html  # Linux
```

The bottom of the test output also prints a one-line summary like
`total: (statements) 42.7%` from `go tool cover -func | tail -1`.

### CI integration

`.github/workflows/e2e.yml` runs `make test-e2e-report` instead of
`make test-e2e`, so every triggered run produces both reports. Two
artifacts and one check-run drop out of each run:

- **`e2e-coverage-<run>`** — `e2e-coverage.html`, downloadable from
  the run's Summary page (Artifacts section). 30-day retention.
- **`e2e-junit-<run>`** — `e2e-junit.xml`, ditto. 30-day retention.
- **`E2E Test Results`** check-run — published by
  [`dorny/test-reporter`](https://github.com/dorny/test-reporter)
  using the `java-junit` reporter (Go's gotestsum-emitted JUnit shape
  is compatible). Renders inline on the PR's Checks tab with
  per-test pass/fail and stack traces. `fail-on-error: false` so a
  reporting hiccup never masks the underlying suite result.

The `dorny` step needs `checks: write` and `pull-requests: write` on
the job; both are declared at the top of `e2e.yml`.

## Env-var matrix

| Variable | Default | What it does |
|---|---|---|
| `E2E_KIND_IMAGE` | `kindest/node:v1.31.0` | kindest/node image used by `ProvisionTopology`. Override to test against a different K8s minor. |
| `E2E_KIND_BIN` | `kind` | Path to the `kind` binary. |
| `E2E_KUBECTL_BIN` | `kubectl` | Path to the `kubectl` binary. |
| `E2E_SHARKO_MODE` | `in-process` | `helm` switches `StartSharko` to the real `helm install` path (V125-1-13). See [Full-fidelity Helm mode](#full-fidelity-helm-mode-v125-1-13). |
| `SHARKO_E2E_IMAGE_TAG` | unset (fresh `e2e-<8-hex>` per call) | When set AND the tagged image is already in the kind node's containerd, skip the docker build + kind load roundtrip. `make test-e2e-helm` defaults this to `e2e-<short-sha>`. |
| `E2E_HELM_BIN` | `helm` | Path to the `helm` binary. |
| `E2E_DOCKER_BIN` | `docker` | Path to the `docker` binary. |
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
- **Helm-mode tests cannot reach the host's GitFake from inside the pod.**
  The `harness.GitFake` listens on `127.0.0.1:NNNN` on the host machine;
  inside the in-cluster Sharko pod that loopback address points at the
  pod itself, not the host. Wave-D Helm-mode tests sidestep this by
  asserting on read-only introspection endpoints
  (`GET /api/v1/providers`) rather than driving register/sync flows that
  would round-trip through git. A routable host-network mode (or moving
  GitFake into the cluster as a Service) is a follow-up.
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
