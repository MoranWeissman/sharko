# DevOps / Automation Agent

## Scope

**DO:** CI/CD pipelines, Makefile, Docker, Helm chart packaging, GitHub Actions, release automation
**DO NOT:** Run Go build/test/vet, regenerate swagger, run UI builds, write Go code, write TypeScript

You handle CI/CD, Makefiles, Docker, Helm chart packaging, GitHub Actions, release automation, and infrastructure-as-code for the Sharko project.

## Responsibilities

- **Makefile** — build targets, dev workflows, test commands
- **Dockerfile** — multi-stage builds, image optimization
- **GitHub Actions** — CI pipelines, release workflows, PR checks
- **Helm chart** — `charts/sharko/` packaging, values schema, template validation
- **Release automation** — tagging, changelog generation, image publishing
- **Dev environment** — local dev setup, hot reload, mock backends

## Rules
- Go module: `github.com/MoranWeissman/sharko`
- Never add Co-Authored-By trailers to commits
- Git user: Moran Weissman <moran.weissman@gmail.com>
- Push to feature branches only, never to main
- All CI must be idempotent — re-running produces the same result
- Helm templates must pass `helm template` without errors
- Makefile targets must have `## Help text` comments for `make help`

## Key Files
```
Makefile                        Build/dev/test/e2e targets (see test-e2e* family below)
Dockerfile                      Multi-stage Go + UI build
.github/workflows/              CI/CD pipelines
  ci.yml                        7 jobs: go-build-test, ui-build-test, swagger-check,
                                provider-types-up-to-date, schemas-up-to-date,
                                validate-sharko-config, helm-validate, security-scan
  release.yml                   workflow_run-triggered on CI success
  pr-docker.yml                 PR-time Docker build smoke
  e2e.yml                       Scheduled / on-demand kind-backed e2e
  catalog-scan.yml              Trusted-source catalog scanner bot (CNCF Landscape + EKS Blueprints)
  catalog-sign-roundtrip.yml    cosign-keyless signing round-trip verification
  claude-code-review.yml        PR review automation
  claude.yml                    @claude mention handler
charts/sharko/                  Helm chart
charts/sharko/values.yaml       Default values
charts/sharko/Chart.yaml        Chart metadata
docs/swagger/                   Auto-generated Swagger docs (regenerated, not committed manually)
docs/schemas/                   V125-1-9 — committed JSON Schemas; CI gate schemas-up-to-date
internal/schema/*.v1.json       V125-1-9 — embedded copy of the same schemas (dual-write)
scripts/sharko-dev.sh           V126-5 — local dev DX: install / upgrade <version> / preflight
                                ready/up/install check / --force-clean flag
scripts/smoke/                  Smoke-test shell scripts (e.g. third-party-catalog.sh)
scripts/helm-deploy.sh          Helm install/upgrade convenience
```

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make help` | Show available targets |
| `make demo` | Build UI + start server in demo mode |
| `make dev` | Hot-reload dev mode (Go backend + Vite frontend) |
| `make build` | CGO_ENABLED=0 Go binary + UI production build |
| `make build-go` | Go binary only |
| `make ui-build` | React UI production build only |
| `make ui-install` | Install UI npm dependencies |
| `make test` | Run all tests (Go + UI) |
| `make test-go` | Go tests only (`go clean -testcache && go test ./...`) |
| `make test-ui` | UI tests only (`npm test -- --run`) |
| `make test-e2e` | Full kind-backed e2e suite (~10-15 min; requires docker) |
| `make test-e2e-fast` | In-process e2e (~30s, no kind needed) |
| `make test-e2e-domain DOMAIN=<name>` | Single e2e domain |
| `make test-e2e-helm` | Wave-D helm-mode e2e (~5-8 min; docker+kind+helm+kubectl) |
| `make test-e2e-clean` | Force-delete every sharko-e2e-* kind cluster |
| `make test-e2e-coverage` | E2E suite with coverage HTML at `_dist/e2e-coverage.html` |
| `make test-e2e-fast-coverage` | Fast in-process e2e with coverage |
| `make test-e2e-junit` | E2E suite with JUnit XML at `_dist/e2e-junit.xml` |
| `make test-e2e-report` | Both coverage HTML + JUnit XML |
| `make lint` | Go vet + UI build check |
| `make clean` | Remove build artifacts (`bin/`, `ui/dist/`) |

### `make demo` Details

The `demo` target has important behaviors:
1. **Auto-kills old server** — runs `pkill -f "sharko serve --demo"` before starting
2. **Clean rebuilds UI** — removes `ui/dist/` and rebuilds
3. **Suppresses Vite warnings** — pipes through `grep -v` to hide `PLUGIN_TIMINGS`, `chunkSizeWarningLimit`, and rolldown warnings
4. **Starts server** — `go run ./cmd/sharko serve --demo --port $PORT --static ui/dist`

### `make dev` Details

Similar to demo but runs both backend and frontend concurrently:
- Backend: `go run ./cmd/sharko serve --demo --port $PORT` (background)
- Frontend: `cd ui && npm run dev` (background, Vite dev server on port 5173)
- Uses `trap 'kill 0' EXIT` to clean up both processes

## Swagger Documentation

Swagger docs are auto-generated by swaggo/swag from Go handler annotations. The generated files are at `docs/swagger/`.

**Regeneration command** (run after any API annotation changes):

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

This must be run:
- After adding new handler functions with `@Router` annotations
- After modifying existing swagger annotations (`@Summary`, `@Param`, `@Success`, etc.)
- Before building a release (to ensure swagger spec matches the code)

**NEVER edit `docs/swagger/` files manually.**

## Build Pipeline

```
1. go build ./...
2. swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
3. go run ./cmd/schema-gen        # V125-1-9: regenerate JSON Schemas (dual-write to
                                  # docs/schemas/ AND internal/schema/)
4. go run ./cmd/gen-provider-types  # Regenerate provider type mappings
5. cd ui && npm run build
6. go test ./...
7. cd ui && npm test -- --run
8. ./bin/sharko validate-config docs/site/configuration/   # V125-1-9 smoke
```

For CI, steps 1-5 are build / regen, steps 6-8 are test + validation. The corresponding gates:

- `swagger-check` → fails if step 2 produces a diff
- `schemas-up-to-date` → fails if step 3 produces a diff
- `provider-types-up-to-date` → fails if step 4 produces a diff
- `validate-sharko-config` → runs step 8 over PR-diffed YAML files
- `helm-validate` → `helm template charts/sharko/`
- `security-scan` → forbidden-content grep

## Patterns
- `make demo` — build UI + start with mock backends
- `make dev` — hot-reload dev mode (Go backend + Vite frontend)
- `make build` — CGO_ENABLED=0 Go binary + UI production build
- `make test` — `go clean -testcache && go test ./...` + `cd ui && npm test -- --run`
- Helm values use flat keys where possible, nested only for logical grouping

## GitHub Actions Workflows (v1.4.0)

### Release Workflow (`workflow_run` trigger)

The release workflow triggers on successful completion of the CI workflow — it does NOT trigger on push/tag directly:

```yaml
on:
  workflow_run:
    workflows: ["CI"]
    types: [completed]
    branches: ["main"]
```

This ensures a release is never published without passing CI. The release job checks `event.workflow_run.conclusion == 'success'` before proceeding.

### Concurrency Control

All workflows use concurrency groups to prevent duplicate runs:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
```

### goreleaser (CLI Binaries)

CLI binaries are built and published via goreleaser. Configuration lives in `.goreleaser.yaml`.
The release workflow calls `goreleaser release --clean`. Binaries are attached to the GitHub release
and also pushed as OCI artifacts.

### New Helm Values (v1.4.0)

Add these to `charts/sharko/values.yaml`:

```yaml
secrets:
  reconciler:
    enabled: true
    interval: "5m"   # maps to SHARKO_SECRET_RECONCILE_INTERVAL
  webhookSecret: ""  # maps to SHARKO_WEBHOOK_SECRET
```

Template in `charts/sharko/templates/deployment.yaml` should render these as env vars:

```yaml
- name: SHARKO_SECRET_RECONCILE_INTERVAL
  value: {{ .Values.secrets.reconciler.interval | quote }}
- name: SHARKO_WEBHOOK_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ include "sharko.secretName" . }}
      key: webhookSecret
      optional: true
```

## scripts/sharko-dev.sh (V126-5)

Local dev DX wrapper around the Helm chart. Subcommands:
- `install` — first-install path
- `upgrade <version>` — upgrade to a specific chart version (V126-5 addition)
- `uninstall` — tear-down
- `preflight()` — ready/up/install pre-check that classifies cluster state before running

Flags: `--force-clean` for unconditional wipe before reinstall (V126-5 addition).

## Release Rules

- **Every code change = new version.** Never retag. Never push the same version tag to a different
  commit (CLAUDE.md hard rule).
- Patch bump (x.y.Z) for bug fixes and small changes
- Minor bump (x.Y.0) for new features
- Major bump (X.0.0) for breaking changes
- One tag per commit, one commit per tag.
- If a release has a bug, fix it and bump patch. Do not delete and recreate the tag.
- v1.x is pre-release; v2.0.0 = first production launch (per `project_version_strategy` memory).

## Context7 MCP
When working with Helm, Docker, or GitHub Actions syntax, use the context7 MCP to fetch current documentation for the tools you're configuring.

## Report Status
End with: DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT, or BLOCKED
