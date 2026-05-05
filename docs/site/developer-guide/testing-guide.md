# Testing Guide

> **Verified:** Not verified end-to-end since authoring; review pending. Cross-reference text was updated alongside V124-3.10 to qualify `admin/admin` as demo-only and reflect V124-3.7's env-var-driven e2e creds, but the full layer-by-layer walk against a fresh image has not been re-executed since the original write-up. Treat any layer-specific command as authoritative ONLY if you can match it against the layer's source files (e.g. `tests/e2e/setup.sh`, `tests/api/*.hurl`, `cmd/sharko/*.go`).

The single canonical reference for testing Sharko — what we have, what we don't, the exact commands to run, and which tool fits which layer. Use this when you want to verify a release candidate, when you add a feature and need to know what tests to write, or when you're about to copy a pattern from somewhere else and want to check that we already have one.

It is paired with the [Catalog Scan Runbook](catalog-scan-runbook.md) (operational doc for the daily scanner) — same voice, same level of detail, same "every command is copy-pasteable" rule.

---

## Why a testing guide

Sharko has shipped 23 versions through a healthy CI pipeline (`go test`, `vitest`, `helm lint`, `swag init` diff check, forbidden-content scan), but the pipeline historically focused on **Layers 1–3**. It does not yet exercise the running binary against a live HTTP surface on every PR. Layers 4–6 exist (`tests/e2e/`, manual `docker run` smoke passes) but are either skeletal or run by hand. Before V2.0.0 GA we want every release candidate to walk every layer once, with clear pass/fail signals.

### The testing pyramid for Sharko

```
                           ┌─────────────────────┐
                           │   L7 UI E2E (gap)   │   real browser, slowest, deferred to V2
                           └─────────────────────┘
                       ┌─────────────────────────────┐
                       │   L6 Kind E2E (skeletal)    │   kind + ArgoCD + Sharko, ~5 min
                       └─────────────────────────────┘
                   ┌──────────────────────────────────────┐
                   │   L5 Local Docker smoke              │   `docker run` + curl/Hurl, < 60 s
                   └──────────────────────────────────────┘
               ┌────────────────────────────────────────────────┐
               │   L4 API contract (Hurl) — proposed            │   running binary, no cluster
               └────────────────────────────────────────────────┘
           ┌────────────────────────────────────────────────────────┐
           │   L1–L3 Unit / component / integration                 │   ~93 Go + ~33 UI + 8 Node
           └────────────────────────────────────────────────────────┘
```

| Layer | Where it lives | Cost per run | What it catches | CI today? |
|---|---|---|---|---|
| L1 Unit (Go) | `internal/**/*_test.go` | ms | Logic bugs in pure functions, packages | Yes (`go-build-test`) |
| L2 Component (Vitest) | `ui/src/**/__tests__/*.test.{ts,tsx}` | ms–s | Rendering, props, accessibility, hooks | Yes (`ui-build-test`) |
| L3 Catalog scanner (Node) | `scripts/**/*.test.mjs` | ms | Diff/signals/yaml-edit logic, plugin parsers | Yes (`catalog-scan.yml` job) |
| L4 API contract (proposed Hurl) | `tests/api/*.hurl` | seconds | Wire-format regressions, swagger drift, auth flows | Not yet |
| L5 Local Docker smoke | `scripts/smoke/local-docker.sh` (proposed) | < 60 s | Image actually boots, demo mode works, basic auth + reads | Not yet |
| L6 Kind E2E | `tests/e2e/` | ~5 min | Helm chart + ArgoCD wiring + real K8s | Manual only (`workflow_dispatch`) |
| L7 UI E2E | (gap) | minutes | Real-browser flows, regression of the SPA | None |

The point of the pyramid is the cost / coverage trade-off. We catch 95% of regressions in milliseconds (L1–L3). Layers 4–6 catch the integration bugs that unit tests cannot — wrong content type, wrong status code, wrong port-forward, wrong Helm value default. We don't run them on every PR; we run them when it matters (release candidates, weekly cron, or pre-launch).

---

## Glossary

Plain definitions with one Sharko example each. Use these terms consistently in commits, PR descriptions, and issue triage.

| Term | Definition | Sharko example |
|---|---|---|
| **Unit test** | Pure function, no I/O. Tests one function in isolation. Fast (sub-millisecond). | `internal/crypto/crypto_test.go::TestEncryptDecrypt` |
| **Component test** | A single package + its in-process collaborators with mocks. Exercises a subsystem, not just a function. | `internal/api/security_headers_test.go` — full router but no real DB or K8s |
| **Integration test** | Multiple packages + real or in-process backing services (httptest server, fake K8s client). | `internal/argocd/client_write_test.go` against `httptest.Server` impersonating ArgoCD |
| **Contract / API test** | External HTTP calls against a *running* Sharko (Docker container or kind cluster). Validates wire format. | Currently a gap — fill via Hurl (Layer 4) |
| **E2E test** | Full stack: real K8s, real ArgoCD, real Helm install, real Sharko process. | `tests/e2e/e2e_test.go` against the kind cluster from `tests/e2e/setup.sh` |
| **Smoke test** | Tiniest "is it alive and basically working" check. Health, login, list addons. Should run in < 60 s. | `make demo` then `curl /api/v1/health` — formalize as Layer 5 |
| **UI E2E** | Real browser against a running app. Clicks, forms, navigation. | None today — Vitest covers components only. Recommended: Playwright (Layer 7) |

---

## Layer 1 — Unit & component tests (Go)

### Run them

```bash
# Full suite, with race detector and a fresh test cache (recommended locally)
go test -race -count=1 ./...

# With coverage profile (matches CI exactly)
go test -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | tail -1   # total %

# Only one package
go test -race ./internal/orchestrator/...

# Only one test
go test -race -run TestSyncApplication_Success ./internal/argocd/...

# Through the Makefile
make test-go
```

CI runs `go test -coverprofile=coverage.out -covermode=atomic ./...` in the `go-build-test` job (`.github/workflows/ci.yml`). The coverage artifact is uploaded with 14-day retention.

### Where they live

93 test files across `internal/` packages. Highlights:

| Package | What it covers |
|---|---|
| `internal/api/` | HTTP handler tests via `httptest.NewRequest` + `httptest.NewRecorder` (24 test files) |
| `internal/orchestrator/` | The two-operation catalog/deploy model end-to-end with mocks (16 test files) |
| `internal/argocd/` | ArgoCD client with `httptest.Server` impersonating the ArgoCD API |
| `internal/argosecrets/` | Cluster-secret manager + reconciler |
| `internal/audit/` | Audit log writer / reader |
| `internal/auth/` | Tokens (admin + per-user PAT) |
| `internal/authz/` | Tier-based authorization |
| `internal/catalog/` | Loader, search, scorecard, ArtifactHub adapter |
| `internal/catalog/signing/` | Cosign keyless verification (TUF root, trust policy) |
| `internal/catalog/sources/` | Third-party catalog fetcher / merger |
| `internal/config/` | Catalog parser, K8s ConfigMap-backed connection store |
| `internal/crypto/` | AES-GCM envelope encryption |
| `internal/diagnose/` | Cluster diagnose probes |
| `internal/gitprovider/` | GitHub + Azure DevOps providers, attribution mode |
| `internal/gitops/` | YAML mutators (preserves comments + ordering) |
| `internal/helm/` | Helm chart fetch + diff |
| `internal/notifications/` | Provider abstraction + checker |
| `internal/observations/` | Cluster observation cache + status rollup |
| `internal/operations/` | Operations queue store |
| `internal/providers/` | Cluster discovery providers (k8s-secrets, EKS) |
| `internal/prtracker/` | PR tracker store |
| `internal/remoteclient/` | Per-cluster K8s client cache |
| `internal/secrets/` | Secrets reconciler |
| `internal/security/` | URL guard (SSRF protection) |
| `internal/service/` | Addon service, tiered Git resolution, upgrade |
| `internal/verify/` | Stage-1 image verification |

### Pattern A — HTTP handler with `httptest`

Reference: `internal/api/security_headers_test.go`. Build a router, fire a request, assert on the recorder.

```go
package api

import (
    "net/http/httptest"
    "strings"
    "testing"
)

func TestSecurityHeadersPresent(t *testing.T) {
    srv := newTestServer()
    router := NewRouter(srv, nil)

    req := httptest.NewRequest("GET", "/api/v1/health", nil)
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)

    tests := []struct {
        header string
        want   string
    }{
        {"X-Content-Type-Options", "nosniff"},
        {"X-Frame-Options", "DENY"},
        {"Referrer-Policy", "strict-origin-when-cross-origin"},
    }
    for _, tt := range tests {
        got := w.Header().Get(tt.header)
        if got != tt.want {
            t.Errorf("header %s: got %q, want %q", tt.header, got, tt.want)
        }
    }
}
```

`newTestServer()` is the package-internal helper that wires a `*Server` with mock dependencies — re-use it for any new handler test.

### Pattern B — outbound HTTP with `httptest.Server`

Reference: `internal/argocd/client_write_test.go`. Spin up an in-process server that impersonates ArgoCD and point the client at it.

```go
ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        t.Errorf("expected POST, got %s", r.Method)
    }
    if r.URL.Path != "/api/v1/applications/my-app/sync" {
        t.Errorf("unexpected path: %s", r.URL.Path)
    }
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte(`{}`))
}))
defer ts.Close()

c := NewClient(ts.URL, "test-token", false)
err := c.SyncApplication(context.Background(), "my-app")
```

Use this whenever Sharko makes outbound HTTP. No real ArtifactHub, no real GitHub, no real ArgoCD. The handler asserts on the request shape; the client response asserts behavior.

### Pattern C — fake K8s client

Reference: `internal/providers/k8s_secrets_test.go`. Use `k8s.io/client-go/kubernetes/fake` to seed objects, then call into the production code with the fake clientset.

```go
client := fake.NewSimpleClientset(&corev1.Secret{
    ObjectMeta: metav1.ObjectMeta{
        Name:      "cluster-1",
        Namespace: "sharko",
    },
    Data: map[string][]byte{"kubeconfig": kubeconfig},
})
provider := newKubernetesSecretProviderWithClient(client, "sharko")

kc, err := provider.GetCredentials("cluster-1")
```

The provider has a constructor variant that accepts an injected client (`newKubernetesSecretProviderWithClient`) — this is the standard pattern. If a new package needs K8s and you don't see one, add it.

### Pattern D — interface mocks for orchestration

Reference: `internal/orchestrator/orchestrator_test.go`. Hand-rolled mocks (no mock library) implement the same interfaces the real code consumes. Set fields, call the production method, assert on captured state.

```go
type mockArgocd struct {
    registeredClusters map[string]string
    syncedApps         []string
    registerErr        error
}
func (m *mockArgocd) RegisterCluster(_ context.Context, name, server string, ...) error { ... }
```

Used pervasively in `internal/orchestrator/`. When you add a new interface, follow the same shape: capture fields, return injectable errors, no behavior.

### How to add a new test

1. **Choose the package** the test belongs to (same package as the code under test, or `<pkg>_test` for a black-box test).
2. **Pick the pattern** above that matches what you're testing.
3. **Name the file** `<feature>_test.go` next to `<feature>.go`. Multiple test files per package is fine.
4. **Run it locally**: `go test -race -run TestYourThing ./internal/<pkg>/...`.
5. **Check coverage**: `go test -coverprofile=cov.out ./internal/<pkg>/... && go tool cover -html=cov.out` opens a browser view.
6. **Run the full suite**: `make test-go` before pushing.

---

## Layer 2 — Frontend component tests (Vitest)

### Run them

```bash
cd ui

# Full suite, single run (matches CI)
npm test -- --run

# Or via the package script (which is `vitest run`)
npm test

# Watch mode for local iteration
npm run test:watch

# A11y suite only
npm run a11y

# A single file
npx vitest run src/components/__tests__/StatCard.test.tsx
```

CI runs `npm run build` then `npm test -- --run` in the `ui-build-test` job. The build step is mandatory because TypeScript compilation can fail independently of Vitest.

### Stack

- **Vitest 4** — Vite-native test runner, Jest-compatible API
- **@testing-library/react** + **@testing-library/jest-dom** + **@testing-library/user-event** — accessible, behavior-driven assertions
- **jsdom** — DOM environment
- **axe-core** — accessibility linter (run via the dedicated `a11y` script)

### Where they live

33 test files. Grouped under `__tests__/` directories alongside source:

```
ui/src/__tests__/                       a11y.test.tsx, a11y-v120-pages.test.tsx
ui/src/components/__tests__/            17 component tests (StatCard, Layout, MarketplaceTab, …)
ui/src/views/__tests__/                 11 page tests (ClustersOverview, AddonDetail, Dashboard, …)
ui/src/views/settings/__tests__/        CatalogSourcesSection
ui/src/hooks/__tests__/                 useAddonStates
```

### Pattern — render + assert

Standard React Testing Library shape:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StatCard } from "../StatCard";

test("renders title and value", () => {
  render(<StatCard title="Healthy clusters" value={3} />);
  expect(screen.getByText("Healthy clusters")).toBeInTheDocument();
  expect(screen.getByText("3")).toBeInTheDocument();
});

test("calls onClick when activated", async () => {
  const user = userEvent.setup();
  const onClick = vi.fn();
  render(<StatCard title="Clusters" value={3} onClick={onClick} />);
  await user.click(screen.getByRole("button"));
  expect(onClick).toHaveBeenCalledOnce();
});
```

Prefer `getByRole` over `getByTestId`. If a query fails, the failure tells you what was rendered — that's the accessibility tree the test runner sees, and it's what assistive tech sees too.

### Pattern — accessibility with axe-core

`ui/src/__tests__/a11y.test.tsx` is the reference. New pages should add a `describe(...)` block that renders the page and asserts zero `axe` violations. WCAG 2.1 AA is the target for everything shipped from v1.21 onward.

```tsx
import { axe, toHaveNoViolations } from "vitest-axe";
expect.extend(toHaveNoViolations);

test("AddonDetail has no a11y violations", async () => {
  const { container } = render(<AddonDetail />);
  expect(await axe(container)).toHaveNoViolations();
});
```

---

## Layer 3 — Catalog-scanner Node tests

### Run them

```bash
cd scripts
npm install   # only the first time
npm test
```

The `test` script in `scripts/package.json` runs:

```
node --test 'catalog-scan/**/*.test.mjs' 'catalog-scan.test.mjs'
```

That's Node's built-in test runner (no Jest, no Vitest, no Mocha) — chosen because the catalog-scan tooling is intentionally a thin Node script with one runtime dep (`yaml`).

CI runs the same command in the `catalog-scan.yml` workflow — see the [Catalog Scan Runbook](catalog-scan-runbook.md) for the full operational doc.

### Where they live

8 test files:

```
scripts/catalog-scan.test.mjs                              # smoke + arg parsing
scripts/catalog-scan/lib/changeset.test.mjs                # adds[] + updates[] dedup
scripts/catalog-scan/lib/diff.test.mjs                     # entry-level diff
scripts/catalog-scan/lib/signals.test.mjs                  # OpenSSF Scorecard, license, chart resolvability
scripts/catalog-scan/lib/yaml-edit.test.mjs                # comment/order-preserving YAML mutator
scripts/catalog-scan/plugins/aws-eks-blueprints.test.mjs   # plugin parser fixtures
scripts/catalog-scan/plugins/cncf-landscape.test.mjs       # plugin parser fixtures
scripts/catalog-scan/pr-open.test.mjs                      # PR body rendering
```

### Pattern — `node --test`

Plain Node assertions. No imports of third-party test libraries.

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import { diffEntries } from "./diff.mjs";

test("adds entry not present in catalog", () => {
  const existing = [{ name: "argo-cd" }];
  const proposed = [{ name: "cert-manager" }];
  const out = diffEntries(existing, proposed);
  assert.deepEqual(out.adds.map(e => e.name), ["cert-manager"]);
});
```

### Plugin tests

Each plugin under `scripts/catalog-scan/plugins/` ships a fixture-driven test that pins the parser against a captured upstream snippet. Add new plugins (Bitnami, RedHat, …) the same way: capture an upstream sample, write a `*.test.mjs` next to the plugin, and add a row to the runbook table.

---

## Layer 4 — API contract tests with Hurl

**Status: proposed (not in repo yet).** This is the missing layer — tests against a *running* Sharko binary that exercise the wire format. It's the closest analog to Robot Framework's keyword-driven REST tests.

### Why Hurl

| Property | Value |
|---|---|
| Format | Plain text `.hurl` files, Git-friendly diffs |
| Engine | Single static binary (`brew install hurl` or `cargo install hurl`) |
| Auth | First-class capture/reuse of tokens, headers, JSON paths |
| CI | Exits non-zero on failure; `--report-html` for human-readable output |
| Cost | No daemon, no Postman cloud, no JS runtime |

Hurl plays the role Robot Framework does in Python shops: declarative, plain-text, runs in CI, and can target either a local Docker container (Layer 5) or a kind cluster (Layer 6). Bruno or Newman would also work; we pick Hurl because of the static-binary install and because the file format is human-readable plain text, not JSON.

### Install Hurl

- macOS: `brew install hurl`
- Linux: `cargo install hurl` (or download a binary from <https://github.com/Orange-OpenSource/hurl/releases>)
- Windows: scoop / chocolatey or download a binary
- Full instructions: <https://hurl.dev/docs/installation.html>

Verify: `hurl --version` should print 4.x or later.

### Sample — `tests/api/smoke.hurl`

A real, runnable file. Targets a Sharko started via Layer 5 on `localhost:18080` in demo mode (admin/admin — **demo only**; real Helm installs use the bootstrap credential from [Initial Credentials](../operator/installation.md#initial-credentials)).

```hurl
# tests/api/smoke.hurl — minimal smoke pack
# Run:   hurl --variable host=http://localhost:18080 --test tests/api/smoke.hurl

# 1. Health endpoint is alive
GET {{host}}/api/v1/health
HTTP 200
[Asserts]
jsonpath "$.status" == "healthy"

# 2. Login as admin
POST {{host}}/api/v1/auth/login
Content-Type: application/json
{
  "username": "admin",
  "password": "admin"
}
HTTP 200
[Asserts]
jsonpath "$.token" exists
jsonpath "$.token" isString
[Captures]
token: jsonpath "$.token"

# 3. Authenticated read — list addons in the catalog
GET {{host}}/api/v1/catalog/addons
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.addons" exists
jsonpath "$.addons[0].name" exists

# 4. Authenticated read — list clusters (demo mode seeds two)
GET {{host}}/api/v1/clusters
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.clusters" count >= 1

# 5. Negative test — viewer token cannot mutate
POST {{host}}/api/v1/auth/login
Content-Type: application/json
{ "username": "qa", "password": "sharko" }
HTTP 200
[Captures]
viewer: jsonpath "$.token"

POST {{host}}/api/v1/clusters
Authorization: Bearer {{viewer}}
Content-Type: application/json
{
  "name": "should-not-create",
  "server": "https://example.invalid"
}
HTTP 403
```

Run it:

```bash
hurl --variable host=http://localhost:18080 --test tests/api/smoke.hurl

# With an HTML report for sharing
hurl --test --report-html ./hurl-report \
  --variable host=http://localhost:18080 \
  tests/api/smoke.hurl
```

Hurl exits non-zero if any assertion fails; CI integration is just `hurl --test`.

### Recommended path forward

1. Create `tests/api/` with one `.hurl` per endpoint group (`auth.hurl`, `catalog.hurl`, `clusters.hurl`, `addons.hurl`, `prs.hurl`, `audit.hurl`).
2. Add a thin wrapper `scripts/smoke/hurl.sh` that takes a `HOST` env var and runs all `.hurl` files with `--test --report-html`.
3. Wire it into `scripts/smoke/local-docker.sh` (Layer 5) so a single command boots Sharko + runs the suite + tears down.
4. Once stable, add a CI job (likely the `e2e.yml` workflow) that runs the Hurl pack against the kind-cluster Sharko.

Target for the pack: 5–10 contract tests covering the v1.23 catalog-extensibility surface (`/api/v1/catalog/sources`, `/api/v1/catalog/validate`, signed-entry verification), then expand from there.

---

## Layer 5 — Local Docker smoke

The throwaway-tag verification pattern Moran already used during the v1.23 rc.0–rc.3 cycle, formalized into a script anyone can run.

### What you need

- Docker (or Colima / Rancher Desktop)
- A published Sharko image (any tag — `:latest`, a release tag, or a per-PR tag from `pr-docker.yml`)
- Optionally `hurl` for the assertion phase (otherwise `curl` works)

### Step 1 — pull the image

```bash
# Latest release
docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:latest

# A specific RC
docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:1.23.0-pre.0

# A per-PR build (see .github/workflows/pr-docker.yml)
docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:pr-NNN
```

`--platform linux/amd64` is needed on Apple Silicon — the image is single-arch.

### Step 2 — run in demo mode

Demo mode boots a self-contained Sharko with mock backends, two seeded clusters, and the canned admin/qa accounts. Perfect for smoke testing the binary without any real K8s connection.

```bash
docker run --rm -d \
  --platform linux/amd64 \
  --name sharko-smoke \
  -p 18080:8080 \
  ghcr.io/moranweissman/sharko:latest \
  sharko serve --demo --port 8080
```

Port 18080 on the host so it doesn't collide with anything you have running on 8080.

### Step 3 — wait for ready

```bash
# Poll until the health endpoint responds (typically < 5 s)
for i in $(seq 1 30); do
  if curl -fsS http://localhost:18080/api/v1/health > /dev/null; then
    echo "ready"
    break
  fi
  sleep 1
done
```

### Step 4 — login and grab a bearer token

!!! warning "`admin/admin` is demo-mode only"
    The `admin/admin` credential below works because the demo container ships with that user pre-seeded. Real Helm installs do NOT accept `admin/admin` — they generate a random bootstrap password (or accept an operator-supplied one). For real K8s installs see [Initial Credentials](../operator/installation.md#initial-credentials) in the operator install guide.

```bash
TOKEN=$(curl -fsS -X POST http://localhost:18080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"admin"}' \
  | jq -r .token)
echo "token: ${TOKEN:0:16}…"
```

### Step 5 — run smoke checks

Pure curl version:

```bash
curl -fsS http://localhost:18080/api/v1/health | jq .
curl -fsS http://localhost:18080/api/v1/catalog/addons \
  -H "authorization: Bearer $TOKEN" | jq '.addons | length'
curl -fsS http://localhost:18080/api/v1/clusters \
  -H "authorization: Bearer $TOKEN" | jq '.clusters | length'
curl -fsS http://localhost:18080/api/v1/config \
  -H "authorization: Bearer $TOKEN" | jq .
```

Hurl version (recommended once Layer 4 lands):

```bash
hurl --test --variable host=http://localhost:18080 tests/api/smoke.hurl
```

### Step 6 — tear down

```bash
docker rm -f sharko-smoke
```

### Wrap it in a script

Recommended: commit a `scripts/smoke/local-docker.sh` that does all of the above given a tag and exits non-zero on any failure. Skeleton:

```bash
#!/usr/bin/env bash
set -euo pipefail
TAG="${1:-latest}"
PORT="${PORT:-18080}"
NAME="sharko-smoke-$$"

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker pull --platform linux/amd64 "ghcr.io/moranweissman/sharko:$TAG"
docker run --rm -d --platform linux/amd64 \
  --name "$NAME" -p "$PORT:8080" \
  "ghcr.io/moranweissman/sharko:$TAG" sharko serve --demo --port 8080

for i in $(seq 1 30); do
  if curl -fsS "http://localhost:$PORT/api/v1/health" >/dev/null; then break; fi
  sleep 1
done

hurl --test --variable "host=http://localhost:$PORT" tests/api/smoke.hurl
```

Usage:

```bash
bash scripts/smoke/local-docker.sh 1.23.0-pre.0
```

---

## Layer 6 — Kind-cluster E2E

The full-stack integration test: kind cluster, ArgoCD installed in-cluster, Sharko installed via its own Helm chart, three Go tests against the live API.

### Prereqs

- Docker
- [kind](https://kind.sigs.k8s.io/) (`brew install kind`)
- `kubectl` and `helm`

### What `tests/e2e/setup.sh` does

Walking through `tests/e2e/setup.sh` line by line — read this so you know what state the cluster is in when tests start:

1. `kind create cluster --name sharko-e2e --wait 60s` — creates a single-node kind cluster named `sharko-e2e`, waits up to 60s for the node to become Ready.
2. `kubectl create namespace argocd` + `kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml` — installs ArgoCD's `stable` channel (latest stable release manifests). This is the standard ArgoCD install — `argocd-server`, `argocd-repo-server`, `argocd-application-controller`, etc.
3. `kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd` — blocks until ArgoCD is up.
4. `docker build -t sharko:e2e .` — builds the Sharko image from the working tree, tagged `sharko:e2e`.
5. `kind load docker-image sharko:e2e --name sharko-e2e` — sideloads the image into the kind node so `imagePullPolicy: Never` works.
6. `helm install sharko charts/sharko/ --namespace sharko --create-namespace --set image.repository=sharko --set image.tag=e2e --set image.pullPolicy=Never` — installs Sharko with the just-built image.
7. `kubectl wait --for=condition=available --timeout=60s deployment/sharko -n sharko` — blocks until Sharko is up.

After this script exits, you have a working Sharko + ArgoCD environment in kind. Sharko is reachable inside the cluster as `svc/sharko` on port 80; you'll port-forward it before running tests.

### Run the suite locally

```bash
# 1. Bring up the cluster (~2-3 min)
bash tests/e2e/setup.sh

# 2. Port-forward in the background
kubectl port-forward svc/sharko 8080:80 -n sharko &
sleep 5

# 3. Run the build-tagged Go E2E suite
go test -tags e2e ./tests/e2e/... -v -timeout 5m

# 4. Tear down
bash tests/e2e/teardown.sh
```

There's also a Makefile target that does all of the above:

```bash
make e2e
```

### Why `//go:build e2e`

`tests/e2e/e2e_test.go` starts with `//go:build e2e` — that build tag keeps the file out of the normal `go test ./...` run. You only see these tests when you pass `-tags e2e`. This is intentional: they require a running Sharko on `localhost:8080` (configurable via `SHARKO_E2E_URL`) and would fail noisily in unit-test runs.

### What it covers today

Three tests, all in `tests/e2e/e2e_test.go`:

| Test | What it checks |
|---|---|
| `TestHealthEndpoint` | `GET /api/v1/health` returns 200 and `status: healthy` |
| `TestLoginAndAuth` | `POST /api/v1/auth/login` with `$SHARKO_E2E_USERNAME` / `$SHARKO_E2E_PASSWORD` (defaults to `admin`/`admin` for demo mode; pass real bootstrap creds for kind via `SHARKO_E2E_PASSWORD=...` — see V124-3.7) returns a non-empty token |
| `TestRepoStatus` | Authenticated `GET /api/v1/repo/status` returns a JSON `initialized` boolean |

That's it. Three tests. The skeleton exists so we can grow it without re-litigating the harness.

### What should be added (V124+ targets)

| Test | Endpoint | What it proves |
|---|---|---|
| `TestRegisterCluster` | `POST /api/v1/clusters` | The PR-only Git flow opens a PR (or direct-commits in tier-1 mode) |
| `TestAddAddon_AppearsInArgoCD` | `POST /api/v1/addons` then poll ArgoCD for the application | The two-operation catalog/deploy model wires to a real ArgoCD app |
| `TestArgoCDSyncCompletes` | poll `argocd-application-controller` status | Sharko-managed apps actually sync to Healthy |
| `TestUnadoptCluster` | `POST /api/v1/clusters/{name}/unadopt` | Cleanup leaves no orphan ArgoCD resources |
| `TestSignedCatalogEntry` | enable trust policy, fetch a signed entry | Cosign verification path works against a real catalog source |

### CI policy

`.github/workflows/e2e.yml` is **manual trigger only** today (`workflow_dispatch`). The `pull_request` trigger is intentionally commented out:

```yaml
on:
  workflow_dispatch: # manual trigger only for now
  # pull_request:     # enable later when stable
  #   branches: [main]
```

The policy: don't enable PR-triggered E2E until the suite is stable and has > 5 meaningful tests. A flaky 1-test suite that runs on every PR is worse than no suite. Flip the trigger at the same time as expanding the test list (V124-1 candidate).

---

## Layer 7 — UI E2E (gap)

There is currently no UI E2E layer. Vitest covers components (React Testing Library + jsdom), but no test drives a real browser against a running Sharko.

### Recommended: Playwright

Playwright is the modern default — single-binary, three browsers (Chromium, WebKit, Firefox), good CI story, generates reports. Cypress is the alternative; both work. Pick Playwright because it's faster on CI, has better TypeScript support, and is what most new projects adopt today.

A minimal first test against Layer 5 (Docker smoke) would look like:

```ts
// ui/e2e/smoke.spec.ts
import { test, expect } from "@playwright/test";

test("admin can log in and see clusters", async ({ page }) => {
  await page.goto("http://localhost:18080");
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("admin");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: /Clusters/ })).toBeVisible();
});
```

Defer concrete adoption to the V2 hardening epic. The current Vitest a11y suite catches the high-value regressions cheaply; Playwright is for the "real browser, real navigation, real screenshots" cases.

---

## Robot Framework comparison

For readers coming from Python shops where Robot Framework is the default test harness for everything above unit tests, here's the head-to-head map.

| Need | Robot Framework (Python) | Sharko equivalent |
|---|---|---|
| Keyword-driven REST tests | `RequestsLibrary` + `.robot` files | **Hurl** (`.hurl` files) — Layer 4 |
| Browser automation | `SeleniumLibrary` / `Browser` library | **Playwright** — Layer 7 (gap) |
| Service mocking | `RobotFramework-Mocking`, `wiremock` | Go `httptest.Server` / `client-go fake` — Layer 1 patterns |
| Spinning up containerized deps | `DockerLibrary` | **testcontainers-go** if needed; today we use kind (Layer 6) or `docker run` (Layer 5) |
| Cross-service orchestration | `Process` library + listeners | `tests/e2e/setup.sh` (kind + helm) |
| Reporting | XML/HTML report by default | `go test -json` + GitHub Actions UI; `hurl --report-html` for the API layer |
| Data-driven tests | `Test Templates` | Go table-driven tests (the pattern in `internal/api/security_headers_test.go`) |

Hurl is the closest 1:1 swap for "I want to write API tests that are easy to read, easy to diff, and easy to run in CI without a Python runtime." That's why it's the proposed Layer 4.

### Why not Postman / Newman?

Postman's collection format is JSON — diffs are unreadable. The cloud-first workspaces add lock-in. Newman (the CLI runner) works fine, but you still have to author collections in Postman first. Bruno is the file-based, OSS-friendly alternative; it's a viable choice if you prefer GUI-driven authoring. Hurl wins on "no GUI, just a text file" simplicity.

### Why not k6?

k6 is excellent for **load and performance** testing — that's a separate concern from contract testing. We may add k6 later for "what happens to Sharko under N concurrent cluster registrations," but it's not the right tool for "does `/api/v1/auth/login` return the right shape."

---

## How ArgoCD tests this stack

Sharko sits on ArgoCD; their testing approach is worth knowing. Authoritative reference: <https://argo-cd.readthedocs.io/en/stable/developer-guide/test-e2e/>.

### What they do

- **Heavy unit tests** under each Go package, standard `testing` package (no Ginkgo for unit tests).
- **E2E tests** in `/test/e2e/` with their own runner, using the standard Go `testing` framework (also no Ginkgo at the E2E layer — they evaluated and chose stdlib).
- **Per-test isolation:** every E2E test gets a random 5-character ID, a dedicated namespace `argocd-e2e-ns-${id}`, a dedicated Git repo at `/tmp/argo-e2e/${id}`, and a dedicated app name. No two tests can collide on cluster state.
- **Two entry points:** `make start-e2e` runs against an in-cluster ArgoCD; `make start-e2e-local` runs against a locally-built binary.
- **Configurable ports:** every component port is overridable via env vars (`ARGOCD_E2E_APISERVER_PORT`, etc.) so you can run multiple suites side-by-side.

### What we should borrow

| Pattern | Why | When |
|---|---|---|
| Per-test isolation (random ID → namespace + app + repo path) | Prevents flake when adding tests; lets us parallelize | When the E2E suite grows past ~5 tests |
| `start-e2e` vs `start-e2e-local` split | Separates "I'm iterating on Sharko code" from "I'm testing the deployed image" | Now — `make e2e` already does the first; we need a `make e2e-local` for the second |
| Configurable ports via env | Lets contributors run E2E without colliding with their dev environment | When multiple devs hit the same dev machine |

### What's overkill for Sharko's size

ArgoCD's E2E harness has ~5 years of accumulated test infrastructure (cluster setup helpers, fixture loaders, retry primitives). At Sharko's scale we don't need that — three tests with a shared `setup.sh` is fine. We'll grow into more structure as the suite grows. Don't pre-build infrastructure for tests that don't exist yet.

---

## Recommended Sharko testing roadmap

Concrete, sequenced. This is the plan, not a wishlist.

1. **Personal smoke pass — this week.** Moran himself walks Layer 5 end-to-end against the v1.23 image. Document any rough edges in a follow-up issue. This is the gate before V2.0.0 work begins.
2. **V124-1.3 — CI roundtrip test for cosign sign/verify.** Already planned in the v1.24 polish bundle. Adds an automated verification that signed catalog entries verify after a release.
3. **Adopt Hurl for `tests/api/`.** Add 5–10 contract tests covering the v1.23 catalog-extensibility surface (`/api/v1/catalog/sources`, `/api/v1/catalog/validate`, signed-entry verification, source merging). Wire into Layer 5 so `make smoke` runs the pack.
4. **Flip `e2e.yml` from manual to PR-triggered.** Prerequisite: 3 more E2E flows (`TestRegisterCluster`, `TestAddAddon_AppearsInArgoCD`, `TestArgoCDSyncCompletes`) so the suite has real coverage. Target: V124 or V125.
5. **Defer to V2 hardening.** Playwright UI E2E, k6 load tests, chaos / disruption tests, multi-version ArgoCD compatibility matrix. None of these block V2.0.0 GA — they're V2.x maturity work for the CNCF incubation push.

---

## Quick-reference command cheatsheet

### Layer 1 — Go unit / component / integration

```bash
make test-go                                       # full Go suite (clean cache)
go test -race -count=1 ./...                       # full suite, race detector
go test -coverprofile=coverage.out -covermode=atomic ./...   # with coverage (matches CI)
go tool cover -func=coverage.out | tail -1         # coverage total
go test -race -run TestSyncApplication_Success ./internal/argocd/...   # one test
```

### Layer 2 — Vitest (UI)

```bash
make test-ui                                       # full UI suite
cd ui && npm test -- --run                         # equivalent
cd ui && npm run test:watch                        # watch mode
cd ui && npm run a11y                              # axe-core only
cd ui && npx vitest run src/components/__tests__/StatCard.test.tsx   # one file
```

### Layer 3 — Catalog scanner (Node)

```bash
cd scripts && npm install && npm test
make catalog-scan                                  # dry-run scan
make catalog-scan-pr                               # preview the PR body (requires _dist/catalog-scan/changeset.json)
```

### Layer 4 — Hurl (proposed)

```bash
brew install hurl                                  # one-time install
hurl --test --variable host=http://localhost:18080 tests/api/*.hurl
hurl --test --report-html ./hurl-report --variable host=http://localhost:18080 tests/api/*.hurl
```

### Layer 5 — Local Docker smoke

`admin/admin` works here only because `--demo` ships pre-seeded with that credential. **Real Helm installs do NOT accept `admin/admin`** — see [Initial Credentials](../operator/installation.md#initial-credentials).

```bash
docker pull --platform linux/amd64 ghcr.io/moranweissman/sharko:latest

docker run --rm -d --platform linux/amd64 \
  --name sharko-smoke -p 18080:8080 \
  ghcr.io/moranweissman/sharko:latest \
  sharko serve --demo --port 8080

curl -fsS http://localhost:18080/api/v1/health | jq .
TOKEN=$(curl -fsS -X POST http://localhost:18080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"admin"}' | jq -r .token)   # demo only
curl -fsS http://localhost:18080/api/v1/clusters \
  -H "authorization: Bearer $TOKEN" | jq .

docker rm -f sharko-smoke
```

### Layer 6 — Kind E2E

```bash
make e2e                                           # full cycle (setup + test + teardown)

# Or step-by-step:
bash tests/e2e/setup.sh
kubectl port-forward svc/sharko 8080:80 -n sharko &
sleep 5
go test -tags e2e ./tests/e2e/... -v -timeout 5m
bash tests/e2e/teardown.sh
```

### Quality gates that act as tests

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal   # regen swagger
git diff --exit-code docs/swagger/                 # fails CI if stale

helm lint charts/sharko/                           # Helm chart lint
helm template sharko charts/sharko/ --values charts/sharko/values.yaml > /dev/null   # Helm render

mkdocs build --strict                              # docs site build (this guide)
```

### Everything in one shot (release candidate)

```bash
# What a full pre-RC verification should look like once Layer 4 + Layer 5 are formalized
make test-go && \
  make test-ui && \
  (cd scripts && npm test) && \
  swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal && \
  git diff --exit-code docs/swagger/ && \
  helm lint charts/sharko/ && \
  mkdocs build --strict && \
  bash scripts/smoke/local-docker.sh "$RC_TAG" && \
  make e2e
```

If any of those exit non-zero, the RC is not ready.
