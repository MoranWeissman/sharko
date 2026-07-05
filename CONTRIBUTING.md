# Contributing to Sharko

Thank you for considering a contribution to Sharko! This document tells
you how to file issues, propose changes, run the test suite, and sign
your commits.

For project governance see [GOVERNANCE.md](GOVERNANCE.md). For
community standards see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). For
security disclosures see [SECURITY.md](SECURITY.md) — please do not
file security issues as public GitHub issues.

## Table of Contents

- [Filing Issues](#filing-issues)
- [Opening a Pull Request](#opening-a-pull-request)
- [Development Workflow](#development-workflow)
- [Building and Testing](#building-and-testing)
- [Sign-off Your Commits (DCO)](#sign-off-your-commits-dco)
- [Coding Conventions](#coding-conventions)
- [AI-Agent Collaborators](#ai-agent-collaborators)
- [Documentation](#documentation)
- [Release Process](#release-process)
- [Discussions and Response Cadence](#discussions-and-response-cadence)

## Filing Issues

Use the issue forms in the
[issue tracker](https://github.com/MoranWeissman/sharko/issues/new/choose):

- **Bug** — something is broken or behaves unexpectedly
- **Feature** — propose a new capability or enhancement
- **Docs** — a documentation page is missing, outdated, or unclear
- **Security** — redirects you to [SECURITY.md](SECURITY.md); do
  **not** file security issues publicly

Before filing, please search existing issues and the
[discussions](https://github.com/MoranWeissman/sharko/discussions)
(once enabled) for prior reports.

When filing, include:

- The Sharko version (output of `sharko version`)
- Your Kubernetes version and ArgoCD version
- Concise reproduction steps
- Expected vs actual behavior
- Relevant logs (server logs, browser console, CLI output) — redact
  any credentials

## Opening a Pull Request

1. **Open or claim an issue first** for non-trivial changes. This
   avoids wasted work on a change the maintainers would reject for
   scope reasons.
2. **Fork** the repository and create a feature branch from `main`:
   ```bash
   git checkout main && git pull
   git checkout -b feat/your-change
   ```
3. **Make your changes** following the [coding
   conventions](#coding-conventions) and the project's settled
   architecture (see
   [`.claude/team/product-manager.md`](.claude/team/product-manager.md)
   "Settled Decisions").
4. **Sign-off your commits** (see [DCO section](#sign-off-your-commits-dco))
   and use Conventional Commits messages (see below).
5. **Run the full quality gate locally** (see [Building and
   Testing](#building-and-testing)) before pushing.
6. **Push** to your fork and open a pull request against `main`.
7. **CI** runs on every PR. The PR is blocked from merge until all
   required checks pass. Address feedback by adding new commits to the
   branch — don't force-push during review unless requested.
8. **Squash merge** is the default. Your commit messages will be
   summarized into the squashed commit message; keep them descriptive.

### Conventional Commits

Sharko uses [Conventional Commits](https://www.conventionalcommits.org/)
for commit messages. The prefix categorizes the change for the
changelog and helps reviewers triage:

```
<type>(<scope>): <short imperative summary>

<optional body — what and why, not how>

<optional footer — references, breaking changes, etc.>
```

Common types:

| Type        | Use for                                                              |
| ----------- | -------------------------------------------------------------------- |
| `feat`      | A new user-visible feature                                           |
| `fix`       | A bug fix                                                            |
| `docs`      | Documentation-only change                                            |
| `refactor`  | Code change that neither fixes a bug nor adds a feature              |
| `test`      | Adding or updating tests                                             |
| `chore`     | Tooling, build, CI changes                                           |
| `perf`      | Performance improvement                                              |
| `security`  | Security fix or hardening                                            |
| `governance`| Governance, community, or process docs (this file, MAINTAINERS, etc.)|

Examples from the project history:

```
feat(catalog): per-entry cosign-keyless signing
fix(api): /clusters returns 200 + sanitized errors on missing files
docs(operator): add cluster-reconciler runbook
```

### Pull Request Title and Body

- **Title:** the same shape as a Conventional Commit message
  (`<type>(<scope>): <summary>`). Squash-merge uses this as the
  default commit message.
- **Body:** describe **what** changed and **why**, the test plan, and
  any breaking changes. The PR template walks you through the
  expected sections.

## Development Workflow

### Repository layout

```
cmd/sharko/          CLI + server entrypoints (Cobra)
cmd/schema-gen/      Schema generator for envelope-shaped YAML
internal/            All implementation packages (not importable externally)
  api/               HTTP handlers and router
  auth*, authz/      Authentication and RBAC
  argosecrets/       ArgoCD cluster-secret manager
  catalog/           Addon catalog (embedded + third-party + signing)
  clusterreconciler/ Git -> ArgoCD secret reconciler (V125-1-8)
  orchestrator/      The core workflow engine
  schema/            JSON-Schema envelope validation (V125-1-9)
  ...
ui/                  React + TypeScript + Vite frontend
charts/sharko/       Helm chart
docs/                User-facing docs (mkdocs site at docs/site/)
templates/           ApplicationSet and helm-values templates
tests/               Integration and e2e tests (in addition to package _test.go files)
.claude/team/        Per-role agent instruction files (project-specific context)
.bmad/output/        Planning artifacts (epics, stories, reviews)
```

### Branching

- All work happens on feature branches off `main`.
- **Never push directly to `main`.** Pull requests are the only path.
- Branch naming convention: `<type>/<short-summary>` —
  e.g., `feat/cluster-reconciler-trigger`, `fix/api-clusters-500`.

## Building and Testing

### Prerequisites

- Go **1.25** or later
- Node.js **20+** for the UI (`ui/`)
- `make` for the convenience targets
- `kubectl` and `helm` for the full e2e suite

### Quality gates

Run these before opening a PR — CI runs the same set and will block
merge on failures:

```bash
# Backend build + unit + race tests
go build ./...
go vet ./...
go test ./... -race -count=1

# Frontend build + tests (if you touched ui/)
cd ui && npm ci && npm run build && npm test && cd ..

# Helm template (if you touched charts/sharko/)
helm template sharko charts/sharko/

# Swagger regeneration (REQUIRED if you added or changed any
# handler with @Router annotations — see CLAUDE.md Code Rules)
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal

# Schema regeneration (REQUIRED if you changed any envelope model)
go run ./cmd/schema-gen

# YAML envelope validation (REQUIRED if you changed any
# envelope-shaped YAML under docs/)
./bin/sharko validate-config docs/site/configuration/

# Documentation site build (REQUIRED if you touched docs/site/)
mkdocs build --strict

# Fast in-process e2e (~30s, no kind)
make test-e2e-fast

# Full kind-backed e2e (~10-15 min) — run for release-gating
# changes touching orchestrator, reconciler, or secrets paths
make test-e2e

# Forbidden-content sweep (always — see CLAUDE.md Content Policy)
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" \
  --include="*.go" --include="*.ts" --include="*.yaml" . | \
  grep -v node_modules | grep -v .git/
# (Must return empty.)
```

A handful of `make` targets wrap the most common combinations:

```bash
make build           # Build the Go binary + UI
make test            # Run all unit and integration tests
make test-e2e-fast   # Fast in-process e2e harness
make test-e2e        # Full kind-backed e2e harness
make lint            # go vet + UI build check
make demo            # Run Sharko locally against mock backends
```

### Demo and hot-reload

```bash
make demo            # Mock backends, no Kubernetes cluster required
                     # UI at http://localhost:8080
                     # Login: admin/admin (admin) or qa/sharko (viewer)

make dev             # Hot-reload dev mode
                     # Frontend: http://localhost:5173
                     # Backend:  http://localhost:8080
```

## Sign-off Your Commits (DCO)

Sharko uses the [Developer Certificate of Origin (DCO)](https://developercertificate.org/).
Every commit must include a `Signed-off-by:` trailer that matches the
commit author. The trailer is a legal attestation that you wrote the
patch (or have the right to contribute it) under the project's open
source license.

### How to sign off

Use `git commit -s` (short for `--signoff`); Git adds the trailer
automatically from your configured `user.name` and `user.email`:

```bash
git commit -s -m "feat(api): add cluster-tagging endpoint"
```

The resulting commit message will end with:

```
feat(api): add cluster-tagging endpoint

Signed-off-by: Your Name <your.email@example.com>
```

If you forgot to sign off and have not yet pushed, you can amend:

```bash
git commit --amend -s --no-edit
```

If you have a series of commits to sign off, rebase with the signoff
exec:

```bash
git rebase --signoff main
```

### What you are attesting

By signing off, you certify the contents of the [DCO
text](https://developercertificate.org/) — most importantly that you
have the legal right to contribute the code and that you are doing so
under the project's open source license (Apache-2.0, see
[LICENSE](LICENSE)).

### CI enforcement

The [DCO GitHub App](https://github.com/apps/dco) runs as a required
status check on every PR. Unsigned commits will block merge. The check
provides one-click guidance on how to add the missing trailer.

### `Signed-off-by:` vs `Co-Authored-By:` (important distinction)

> **Note:** `Signed-off-by:` is a **legal attestation** under the
> Developer Certificate of Origin and is **REQUIRED** on every commit
> to this repository.
>
> `Co-Authored-By:` is a separate **multi-author attribution trailer**
> (used by GitHub to display multiple authors on the commit). We do
> **NOT** use `Co-Authored-By:` in this repository — see
> [CLAUDE.md](CLAUDE.md) "Git Rules". All commits are authored solely
> by the contributor whose `Signed-off-by:` they bear, and any
> AI-assistance attribution belongs in the PR description, not in the
> commit metadata.

If you previously contributed to a repo that used `Co-Authored-By:`
for AI-assisted commits, please do **not** carry that habit here.

## Coding Conventions

### Go

- Follow standard Go style (`gofmt` / `goimports`).
- Run `go vet ./...` and address every warning.
- Keep package APIs minimal — favor unexported helpers and only export
  what callers genuinely need.
- Tests live next to the code (`foo.go` -> `foo_test.go`). Use table-
  driven tests where it improves coverage clarity.
- Concurrency: reconcilers in `internal/clusterreconciler`,
  `internal/prtracker`, `internal/argosecrets`, and `internal/secrets`
  use a single-goroutine + `sync.Once` design. Test seams are
  per-instance `Deps` fields, never package-level vars (race hazard
  under `t.Parallel()`).

### TypeScript / React

- Follow the existing component patterns under `ui/src/`.
- Use shadcn/ui primitives + Tailwind utilities; do not introduce new
  CSS frameworks.
- Run `npm run build` and `npm test` locally; fix all warnings.

### YAML (Sharko-owned, envelope-shaped)

- New Sharko-owned YAML files **must** be envelope-shaped
  (`apiVersion: sharko.io/v1`, `kind: ...`, `metadata: {...}`,
  `spec: {...}`). The `sharko.io/v1` group name is a
  Kubernetes-convention identifier, not a web address — nothing is
  ever fetched from it.
- Always read via the envelope-aware loaders
  (`models.LoadManagedClusters`, `catalog.LoadAddonCatalog`); never
  hand-roll `yaml.Unmarshal` for these files.
- Always write via `models.SaveManagedClusters` / catalog-equivalent
  writers (envelope-emitting).
- Regenerate schemas with `go run ./cmd/schema-gen` after any model
  change — the dual-write helper updates both `docs/schemas/` and
  `internal/schema/`. The `schemas-up-to-date` CI gate catches
  drift.

### Helm chart (`charts/sharko/`)

- Run `helm template sharko charts/sharko/` after any change.
- Keep `values.yaml` keys in their existing logical groups; do not
  introduce new top-level groups without a maintainer discussion.

## AI-Agent Collaborators

Sharko is developed with help from AI coding agents (Claude Code,
specifically). The project documents this collaboration explicitly:

- **Role files in `.claude/team/`** — each role (tech-lead,
  go-expert, k8s-expert, frontend-expert, test-engineer,
  code-reviewer, security-auditor, etc.) has a `.md` file that
  serves as a stable, project-specific system prompt. When an
  AI agent is dispatched to do work, the relevant role file is
  included in the prompt. This keeps agent output aligned with
  project-specific constraints (the orchestrator pattern, the
  ownership-label gate, the schema-envelope discipline, the
  PR-only Git flow, etc.).
- **Planning artifacts in `.bmad/output/`** — sprint plans, epics,
  story breakdowns, and post-implementation reviews live here. They
  are intentionally version-controlled so the planning history is
  visible.
- **`CLAUDE.md`** — the top-level instruction file that any AI
  collaborator reads at session start. Note in particular the
  **"Never add Co-Authored-By trailers"** rule — see the [DCO
  section](#sign-off-your-commits-dco) above.

You do **not** need to use AI agents to contribute. Human
contributions are equally welcome and follow the same review process.
If you do use AI assistance, please:

- Disclose it in the PR description (the PR template has a checkbox).
- Treat the AI as a collaborator, not as the author — you take
  responsibility for the code via your DCO sign-off.
- Do not add `Co-Authored-By:` trailers for AI assistance (see DCO
  section).

## Documentation

If your change is user-visible or operator-visible, **update the
documentation in the same PR**. Documentation that drifts from code is
worse than no documentation.

- **User-facing CLI / UI changes** → `docs/site/user-guide/...`
- **Operator / install / configuration changes** → `docs/site/operator/...`
- **Developer-facing changes (architecture, internals, testing)** →
  `docs/site/developer-guide/...`
- **API changes** → swagger annotations in handler files, then
  `swag init` to regenerate `docs/swagger/`
- **README quick-reference tables** → keep CLI commands and API
  endpoints lists in sync

### Runbooks: verified-by-execution

Runbooks under `docs/site/developer-guide/` and `docs/site/operator/`
must carry a `> **Verified:** ...` header at the top with the date
the runbook was last end-to-end executed. Reviewers reject runbook
PRs that:

- add a runbook without the header,
- modify a runbook without updating the header date, or
- carry a header date older than the most recent commit to the
  runbook file.

**Author runbooks by execution.** Read-only inspection of code is
not enough — write the steps as you run them.

### `mkdocs --strict`

The docs site is built with `mkdocs build --strict`. Strict mode
fails on unresolved internal links, missing nav entries, and orphan
pages. Run it locally before pushing doc changes; CI mirrors this.

## Release Process

Releases are cut by maintainers. The process is documented in
`docs/site/developer-guide/` and is summarized here:

- **Version bumps** — patch for fixes, minor for features, major for
  breaking changes. **Never retag** an existing version
  (see [CLAUDE.md](CLAUDE.md) "Git Rules").
- **Pre-release line** — `v1.x` is pre-release; production launch
  is `v2.0.0` (see
  [`.claude/team/product-manager.md`](.claude/team/product-manager.md)).
- **Release cadence** — fixes are bundled on a working branch and
  cut as a release at a real milestone, not per-fix.

## Discussions and Response Cadence

Once GitHub Discussions are enabled on the repository
([admin step in progress — see GOVERNANCE.md](GOVERNANCE.md)),
project Q&A, design discussion, and roadmap input belong there
rather than in the issue tracker.

**Response cadence:**

- **Best-effort response within 5 business days** on Discussions
  threads and non-urgent issues.
- For **urgent issues** (production-blocking bugs, security
  questions), use the issue tracker — security disclosures go to
  the email in [SECURITY.md](SECURITY.md), not to Discussions.
- The maintainer team is small (currently a single lead maintainer
  — see [MAINTAINERS.md](MAINTAINERS.md)). Responses may be slower
  during release cycles; please be patient.

Thanks for contributing to Sharko!
