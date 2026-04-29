# Changelog

All notable changes to Sharko are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-release notice.** All `v1.x` tags are pre-release builds intended for
> evaluation, testing, and early adoption feedback. The first production-ready
> release will be **`v2.0.0`**, which has not shipped yet. Expect breaking
> changes between minor versions until GA. Pin to a specific patch tag.

## [v1.23.0-pre.0] - 2026-04-29

> First tag cut with the auto-prerelease release flag (`prerelease: auto`).
> Four throwaway release-candidate tags (`v1.23.0-rc.0` through `v1.23.0-rc.3`)
> were burned during this release debugging production-only bugs that the
> dry-run pipeline could not surface — TUF cache path under read-only
> filesystem, Sigstore bundle format mismatch (legacy v1 vs modern Bundle),
> trust-regex SAN encoding for `workflow_run` certs, and a GoReleaser
> dirty-tree check that fired on the catalog-swap commit. None of those tags
> were promoted; the pre-release auto-flag was added so transient debug tags
> stop polluting the GitHub "Latest release" surface.

### Added — Epic V123-1: Third-party catalog sources

- `SHARKO_CATALOG_URLS` env parser with runtime SSRF guard
  (V123-1.1).
- Periodic fetcher with snapshot store and `SidecarVerifier`
  interface (V123-1.2).
- Catalog merger with **embedded-wins** conflict rule and `Origin`
  metadata on every entry (V123-1.3).
- `Source` field on `CatalogEntry`; per-entry source attribution
  threaded through catalog handlers (V123-1.4).
- `GET /api/v1/catalog/sources` endpoint exposing source state and
  fetch metadata (V123-1.5).
- `POST /api/v1/catalog/sources/refresh` Tier-2 force-refresh
  (V123-1.6).
- `SourceBadge` component on Browse tiles + AddonDetail (V123-1.7).
- Admin-only **Catalog Sources** section in Settings (V123-1.8).
- Integration suite: every-entry-has-Origin merger test, fetcher
  timeout + invalid-YAML gap cases, full-loop integration
  (V123-1.9).

### Added — Epic V123-2: Per-entry cosign-keyless signing

- Catalog schema `v1.1` — optional `signature` field on entries
  (V123-2.1).
- `internal/catalog/signing` package: cosign-keyless verifier built
  on `sigstore-go`; canonical entry serialization for sidecar
  bundle verification (V123-2.2).
- `Verified` + `SignatureIdentity` fields on `CatalogEntry`,
  surfaced over the API and in JSON responses (V123-2.2).
- Verifier wired into server startup; embedded catalog loads via
  `LoadBytesWithVerifier` (V123-2.2).
- `SHARKO_CATALOG_TRUSTED_IDENTITIES` env parser with `<defaults>`
  expansion token (V123-2.3).
- `feat(serve): wire trust policy from env at startup` — one
  canonical loader path threaded via `SetTrustPolicy()` /
  `SetEntryVerifyFunc()` (V123-2.3).
- Operator guide: `docs/site/operator/catalog-trust-policy.md`
  (V123-2.3).
- `VerifiedBadge` component on Browse tile + AddonDetail; **Signed
  only** filter on Marketplace (V123-2.4).
- `cmd/catalog-sign` CLI tool; release pipeline `sign-catalog-entries`
  job that GoReleaser consumes for the embedded catalog (V123-2.5).
- Verification tests: log-recorder + outcome-matrix table test,
  trust-policy no-match explicit case, loader+verifier integration
  suite, coverage backfill above the 80% floor (V123-2.6).

### Added — Epic V123-3: Trusted-source scanning bot

- `scripts/catalog-scan/` skeleton + plugin contract; diff,
  changeset, http, logger libs; `make catalog-scan` target
  (V123-3.1).
- `cncf-landscape` plugin — fetch, filter, map (V123-3.2).
- `aws-eks-blueprints` plugin — list, extract, propose adds
  (V123-3.3).
- `pr-open.mjs` + signal pre-compute + YAML edit, plus the
  `.github/workflows/catalog-scan.yml` daily workflow (V123-3.4).
- Reviewer runbook: `docs/site/developer-guide/catalog-scan-runbook.md`
  (V123-3.5).

### Added — Epic V123-4: Docs + release polish

- README pre-release banner ("Pre-release — not production-ready",
  v1.x = pre-release until v2.0.0) (PR-G).
- Release workflow `prerelease: auto` flag — any tag with a
  pre-release identifier (`-rc`, `-pre`, `-alpha`, etc.) is
  auto-flagged in the GitHub release UI so transient debug tags do
  not steal "Latest release" (PR #316).

### Changed

- `refactor(v1.23): catalog loader + signing cleanup (M1+M5)` —
  collapsed redundant loader paths and tightened the
  `internal/catalog/sources` ↔ `internal/catalog/signing` boundary
  (`sources` MUST NOT import `signing`); single canonical
  `signing.LoadTrustPolicyFromEnv()` is the only source of truth
  for trust config.

### Fixed

- `fix(scanner): catalog-scan polish bundle (M2+M3+M4+M7+L2)` —
  scanner robustness: stricter signal heuristics, clearer logging,
  PR-body diff rendering (PR #315).
- `fix(sources): SSRF hardening — DialContext pinning + redirect
  re-check (M6+L1)` — closed the redirect-after-pin SSRF window in
  the third-party catalog fetcher (PR #314).
- `fix(signing): default trust regex matches workflow_run cert SAN
  (rc.2 blocker)` — workflow URL in cert SAN uses
  `job_workflow_ref` style, not the triggering tag (PR #312).
- `fix(v1.23): real-runtime V123-2 blockers` — TUF cache path
  pinned to a writable dir for read-only-rootfs pods; Sigstore
  bundle format upgraded from legacy v1 to modern Bundle (PR #311).
- `fix(release): skip GoReleaser dirty-tree check for V123-2.5
  catalog swap` — release pipeline tolerates the sign-then-swap
  catalog file because the swap is intentional, not drift (PR #310).
- `fix(v1.23): pre-tag HIGH bundle (H1+H2+H4+H5+H6)` — pre-tag
  hardening pass on V123 surface (PR #308).
- `fix(v1.23): resolve pre-tag BLOCKERS B1+B2+B3` — pre-tag blocker
  pass on V123 surface (PR #307).

### Security

- Trust-policy regex semantics: operator-supplied regexes MUST be
  anchored. The `<defaults>` token expands to a conservative
  CNCF-org + Sharko-release-workflow pattern.
- Per-entry cosign-keyless verification on every embedded catalog
  entry; signing-identity match drives the **Verified** badge on
  the API and UI.
- SSRF guard on third-party catalog URL fetcher: `DialContext`
  pinning to a known-safe address, re-check after redirect.

## [v1.22.0] - 2026-04-21

> Merged-not-tagged per the project's release-cadence rule (no
> single fix gets its own tag; bundle on a working branch and cut
> at a milestone). Shipped via the `dev/v1.22` merge to `main` on
> 2026-04-21.

### Added

- WCAG 2.1 AA retrofit on the v1.20 pages (V122-1).
- Audit-log retention banner + operator audit-log retention guide
  (V122-3).
- Multi-arch (amd64 + arm64) Docker image publishing in the
  release workflow (V122-2).
- Playwright screenshot generator + demo fixtures; 5 Sharko UI
  screenshots embedded into docs pages and the homepage hero
  (V122-4).

### Fixed

- `fix(rbac): grant nodes get/list so /api/v1/cluster/nodes
  returns 200` (v1.21.8 carry-over).
- `fix(ui): stop PerClusterAddonOverridesEditor re-mount storm on
  Config tab` (v1.21.8 carry-over).

## [v1.21.7] - 2026-04-21

### Added

- BMAD planning artifacts for v1.22 + v1.23.
- `docs(bmad): mandatory enforcement` — CLAUDE.md hard rule +
  UserPromptSubmit hook.

### Changed

- Read-the-Docs nav polish — collapsible side nav, hide duplicate
  sidebar title (v1.21.5).
- Read-the-Docs left sidebar nav + mascot favicon (v1.21.4).

## [v1.21.0] - 2026-04-20

> Shipped as a single working-branch merge covering the catalog +
> marketplace + smart-values surface, plus seven docs + RTD polish
> patch tags (`v1.21.1` through `v1.21.7`) over the following ~24
> hours. Patch tags consolidated into this entry to keep the
> changelog readable.

### Added — Catalog & discovery

- Curated 45-addon marketplace (Browse + Configure UI, in-page
  detail view + README, Add-to-catalog rename, in-catalog badge,
  view-in-catalog action).
- ArtifactHub HTTP client + cache + error taxonomy.
- `Marketplace Search` tab with curated/external result split;
  Tier-1 registry; Paste-Helm-URL marketplace tab (Epic V121-4).
- Smart-values parser + header + per-cluster template.
- `feat(values): diff-and-merge from upstream` (preserve user
  changes).
- `feat(values): AI annotate function with token budget +
  heuristic union`; per-addon AI-annotate opt-out toggle in
  Settings.
- `feat(orchestrator): seed smart values on AddAddon`; drop dead
  pull-upstream helper.
- Version-mismatch banner + refresh action; `refresh_from_upstream`
  flag on the API.
- Unified addon-state model via `useAddonStates` hook; merged-PRs
  view with toggle on Dashboard; separate Progressing widget +
  clickable addon links.
- `feat(api): add source field to addon_added audit + 409 on
  duplicate` (V121-5).
- `chore(release): add cosign keyless signing to release workflow`
  — first cosign-keyless integration; precursor to V123-2.

### Changed

- Values format migration: write global values at top level —
  drop legacy `<addon>:` wrapper. Legacy-wrap detection banner +
  Migrate action on the Values tab. `unwrap-globals` migration
  endpoint.
- WCAG 2.1 AA audit + axe-core test on marketplace pages.

### Fixed

- `fix(orchestrator): unwrap chart-name-rooted upstream values`
  (velero double-wrap).
- `fix(upgrades): never recommend downgrade + on-latest message`.
- `fix(prs): merged PR endpoint + recent-changes panel matching`.
- `fix(marketplace): detail page layout + tool README tab +
  scrollbar + Unknown badge`.
- `fix(ui): unify Catalog Only / Not Deployed terminology`.
- `fix(ui): version picker shows top 10 + pre-releases toggle
  works`.
- `fix(ci): docker login in helm-package job so cosign can auth to
  GHCR` (v1.21.1).

### Security

- SSRF guard on URL-fetching catalog endpoints.
- `feat(security): hard-block values-file send when secret-leak
  detected`.
- `feat(security): audit-log secret-leak blocks for grep-ability`.

[v1.23.0-pre.0]: https://github.com/MoranWeissman/sharko/releases/tag/v1.23.0-pre.0
[v1.22.0]: https://github.com/MoranWeissman/sharko/commits/main
[v1.21.7]: https://github.com/MoranWeissman/sharko/releases/tag/v1.21.7
[v1.21.0]: https://github.com/MoranWeissman/sharko/releases/tag/v1.21.0
