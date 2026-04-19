# Developer Guide

The full developer guide — covering build, project layout, key packages, the curated catalog (v1.21), AI annotation pipeline, security primitives, supply-chain signing, and "how to add an X" recipes — lives at the repository root so it stays close to `go.mod`, `Makefile`, and the source tree it documents.

Read it on GitHub: [`docs/developer-guide.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/developer-guide.md).

What's in there:

- **Building from source** — Go and UI builds, demo + dev modes, Docker.
- **Project structure** — directory layout for `cmd/`, `internal/`, `ui/`, `charts/`.
- **Key packages** — `orchestrator`, `remoteclient`, `providers`, `argocd`, `gitprovider`, `api`, `prtracker`, `auth`, `ai`, `verify`, `observations`, `diagnose`, `metrics`, `cmstore`, `authz`, `audit`.
- **How-to recipes** — adding a new API endpoint, adding an auditable endpoint, adding a CLI command, writing a custom secrets provider.
- **Testing patterns** — mock interfaces, fake K8s client, test layout.
- **PR workflow** — per-PR Docker builds and the `pr-docker.yml` flow.
- **Curated Catalog (v1.21)** — embedded YAML, loader, search, scorecard refresh, REST surface, ArtifactHub proxy/cache, security primitives.
- **Release supply-chain** — cosign keyless signing wiring in `release.yml` and `.goreleaser.yaml`.

> The developer guide is intentionally kept outside the MkDocs `docs/site/` tree so PRs that touch a Go package can update the matching guide section in the same diff. The MkDocs site links out to GitHub for the canonical render.

## UI accessibility target

New UI surfaces shipped from v1.21 onward target **WCAG 2.1 AA** — keyboard navigation, focus rings on every interactive element, semantic landmarks (`role="navigation"` / `role="main"`), and contrast ratios that pass `axe-core` with zero violations on the Marketplace pages. Existing pages predate the target and are tracked for a v1.22 retrofit (per design §9 out-of-scope list).

When adding a new page or component, run the existing axe-core suite (`cd ui && npm test -- a11y`) and extend it with a fresh `describe(...)` block for your page. The reference template is the Marketplace block in `ui/src/__tests__/a11y.test.tsx`.
