---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - docs/design/2026-04-17-v1.21-catalog-discovery.md
  - docs/design/examples/addons.yaml.draft
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v1.21'
user_name: 'Moran'
date: '2026-04-19'
---

# Sharko v1.21 — Catalog & Discovery — Epic Breakdown

## Overview

This document decomposes the v1.21 design doc (`docs/design/2026-04-17-v1.21-catalog-discovery.md`, revision 2 dated 2026-04-19) into implementable epics and stories for the Sharko team. v1.21 adds discovery and smart-defaults for **Operation 1 (Add addon to the catalog)** — it does **not** change the separate Operation 2 deploy flow shipped in prior releases.

**Scope frame (from design §1.5):** v1.21 only touches the "add addon" flow. Deploying an addon on a cluster stays on the existing cluster-page flow, unchanged.

**Lean-workflow expectations** (from `feedback_lean_workflow.md` + `feedback_always_involve_test_devops_docs.md`):
- One agent per bundle of work; the agent brief is complete when writing code.
- Every epic explicitly involves **test-engineer**, **devops-agent**, and **docs-writer** alongside implementation agents.
- No release per fix — bundle on `design/v1.21-catalog`; cut `v1.21.0` only at the real milestone.
- Pre-production framing only (solo dev, zero real users); no "100 concurrent users" or "v1.x migration" ceremony.

**Source documents:**
- Design doc: `docs/design/2026-04-17-v1.21-catalog-discovery.md` (rev 2, 2026-04-19)
- Top-50 draft: `docs/design/examples/addons.yaml.draft`
- Existing V2 epics (read-only, out of v1.21 scope): `.bmad/output/planning-artifacts/epics.md`

---

## Requirements Inventory

### Functional Requirements

**FR-V121-1** — Embed a curated addon catalog (`catalog/addons.yaml`) into the Sharko binary via `//go:embed`, metadata-only, loaded + validated against `catalog/schema.json` at startup. (Design §4.1)

**FR-V121-2** — Expose the embedded catalog through a typed in-memory index + search that supports name, description, maintainer, category, `curated_by` tags, and minimum score/K8s version filters. (§4.1, §4.2)

**FR-V121-3** — Provide `GET /api/v1/catalog/addons` (list with filters) returning the curated entries. (§4.5)

**FR-V121-4** — Provide `GET /api/v1/catalog/addons/{name}` returning a single curated entry, including Scorecard score and derived tier. (§4.5, §4.6)

**FR-V121-5** — Provide `GET /api/v1/catalog/versions?repo=<url>&chart=<name>` returning the top versions from the chart's `index.yaml` (reuses `internal/helm/fetcher.go`). (§4.5)

**FR-V121-6** — Provide `GET /api/v1/catalog/search?q=<term>` returning blended curated + ArtifactHub results. (§4.5)

**FR-V121-7** — Provide `GET /api/v1/catalog/remote/{pkgID}` returning an ArtifactHub package detail, proxied through Sharko's backend. (§4.5)

**FR-V121-8** — Provide `POST /api/v1/catalog/reprobe` to force a connectivity re-check against ArtifactHub. (§4.5)

**FR-V121-9** — Implement a 3-tier in-memory cache (search 10 min / package 1 h / versions 15 min) with stale-serve up to 24 h and exponential backoff on 429. (§4.5)

**FR-V121-10** — On Addons Catalog page, replace the 4-field blank "Add Addon" modal with a 3-tab Marketplace modal: Browse (default), Search by name, Paste Helm URL. (§4.3)

**FR-V121-11** — Browse tab renders a filtered grid of curated tiles with category pill, `curated_by` multi-select, min-score slider, and min-K8s-version selector, plus a text search over name/description/maintainers. (§4.3.1)

**FR-V121-12** — Search tab calls the blended catalog-search endpoint and merges curated + ArtifactHub results; shows banner when ArtifactHub is unreachable and still serves curated matches. (§4.3.2)

**FR-V121-13** — Paste Helm URL tab validates `<repo>/index.yaml` on blur, confirms the chart exists, and reports version count / latest. (§4.3.3)

**FR-V121-14** — Configure step (shared across tabs) collects name, namespace, sync wave, chart version (from real top-5 list with "Show pre-releases" toggle); no cluster selector. (§4.3.4)

**FR-V121-15** — Duplicate guard blocks submit when `name` is already present in the user's `configuration/addons-catalog.yaml` with a "open its page" deep-link. (§4.3.4)

**FR-V121-16** — Submit opens a single Tier 2 PR that (a) appends the entry to `configuration/addons-catalog.yaml` under `applicationsets:` and (b) creates `configuration/addons-global-values/<addon>.yaml` from the smart values layer. Endpoint = existing `POST /api/v1/addons`. (§4.3.5)

**FR-V121-17** — Smart values layer fetches `values.yaml` from the chart, runs the heuristic parser to detect cluster-specific fields, and splits output into a global file + trailing commented per-cluster template block. (§4.4.1, §4.4.2)

**FR-V121-18** — Generated values files carry a header stamp with chart name, version, generation date, chart source URL, AI-annotation state, and `sharko: managed=true`. (§4.4.1)

**FR-V121-19** — When AI is configured, pre-scan `values.yaml` for secret-like content (regex list matching the v1.20 values editor guard) and hard-block upstream LLM calls on any match; otherwise call the LLM once per chart version to merge cluster-specific detections + inline annotations. (§4.4.3)

**FR-V121-20** — Honor global Settings toggle `Annotate values on generate` (default ON when AI configured) and per-addon opt-out via file header directive `# sharko: ai-annotate=off`. (§4.4.4)

**FR-V121-21** — When rendering an addon's Values tab, detect version mismatch between `addons-catalog.yaml` chart version and the generated file's header; surface a contextual banner offering "Refresh now" that re-runs the smart-values pipeline and opens a Tier 2 PR. (§4.4.5)

**FR-V121-22** — Remove the v1.20 always-visible "Pull upstream defaults" button on the Values tab. (§4.4.6)

**FR-V121-23** — When a user enables an existing addon on a new cluster (Operation 2), seed that addon's stanza inside `configuration/addons-clusters-values/<cluster>.yaml` from the global file's commented per-cluster template block. (§4.4.1 step 6)

**FR-V121-24** — Run a daily OpenSSF Scorecard refresh (04:00 UTC) for every entry with a github.com `source_url`; failures are non-fatal and the last known score survives until the next success. (§4.6)

**FR-V121-25** — UI renders the Scorecard score as a numeric value plus a derived tier label (Strong / Moderate / Weak / unknown) with appropriate colors on tiles, detail pages, and search results. (§4.6)

**FR-V121-26** — Emit Prometheus metrics `sharko_scorecard_refresh_total{status}` and `sharko_scorecard_last_refresh_timestamp`. (§4.6)

**FR-V121-27** — Add a `catalog-validate` CI workflow (`.github/workflows/catalog-validate.yml`) enforcing: JSON Schema validation, chart resolvability (every `repo` index.yaml contains the named `chart`), duplicate-name detection, license allow-list (Apache-2.0, BSD-3-Clause, MIT, MPL-2.0). (§4.9)

**FR-V121-28** — Add CODEOWNERS entry `catalog/** @MoranWeissman`. (§4.9)

**FR-V121-29** — Release pipeline adds cosign keyless signing (GitHub OIDC) for the Docker image, the Linux/macOS binaries, and the release-asset copy of `catalog/addons.yaml`. (§4.7)

**FR-V121-30** — Ship the initial ~50-entry curated catalog (draft at `docs/design/examples/addons.yaml.draft`) as `catalog/addons.yaml`. (§11, §4.1)

### NonFunctional Requirements

**NFR-V121-1** — Sharko stays **stateless**; no database, BoltDB, or ConfigMap introduced for catalog or AI cache. Git is the cache for generated values. In-memory caches only. (§4.1, §4.4.3)

**NFR-V121-2** — Catalog ships **embedded** — zero runtime network dependency for the curated list. ArtifactHub is additive; failure must degrade to curated-only + banner. (§4.1, §4.3.2, §4.5)

**NFR-V121-3** — Catalog is metadata-only; Sharko does not host, proxy, or mirror Helm charts. Air-gap for chart tarballs is the user's responsibility via their own addons-catalog.yaml entries. (§4.1, §6 item 4)

**NFR-V121-4** — New Marketplace modal and the version-mismatch banner meet **WCAG 2.1 AA** (keyboard nav, ARIA labels, 4.5:1 text / 3:1 UI contrast, focus-visible outlines, live-region search updates, dialog + tablist roles). (§4.8)

**NFR-V121-5** — All new `/api/v1/catalog/*` endpoints have complete swaggo annotations and are reflected in `docs/swagger/` after `swag init` regen. Breaking changes bump API version. (§8 "API stability")

**NFR-V121-6** — Every mutating catalog-related handler call goes through v1.20's tiered-Git path: catalog `Add` is classified as **Tier 2** in `internal/api/pattern_tier.go` and resolved via `GitProviderForTier`. (§4.3.5; `internal/api/tiered_git.go`)

**NFR-V121-7** — Every mutating catalog-related handler calls `audit.Enrich` with a catalog-scoped event name; `TestTierCoverage` + `TestAuditCoverage` continue to pass. (Team ops; `internal/api/audit_coverage_test.go`)

**NFR-V121-8** — Release artifacts (image + binaries + catalog asset) are cosign-keyless signed via GitHub OIDC identity; signature verification is documented. (§4.7, §8 "Supply chain")

**NFR-V121-9** — CycloneDX SBOM continues to include the byte-included catalog file hash without extra process. (§8 "SBOM")

**NFR-V121-10** — Governance: `catalog/**` has `CODEOWNERS`; license allow-list enforced in CI; deprecation is a schema flag, not file deletion. (§4.9, §8 "Governance")

**NFR-V121-11** — Forbidden content policy continues to hold (no `scrdairy`, `merck`, `msd.com`, `mahi-techlabs`, `merck-ahtl`, or real AWS account IDs anywhere in new code, docs, catalog entries, or CI). (`CLAUDE.md` Content Policy)

**NFR-V121-12** — LLM upstream calls are hard-blocked by a secret-leak pre-scan with no "send anyway" override; false-positive bias. (§4.4.3, Risk R4)

**NFR-V121-13** — Quality gates pass on every commit of the bundle: `go build ./...`, `go vet ./...`, `go test ./...`, `cd ui && npm run build`, `cd ui && npm test`. (`tech-lead.md` CHECK)

### Additional Requirements (Architecture / Infrastructure)

- Introduce new Go package tree: `internal/catalog/` with `embed.go`, `loader.go`, `search.go`, `heuristics.go`, `scorecard.go`. (§4.1)
- New repo-root directory: `catalog/` with `addons.yaml` + `schema.json`; add to `//go:embed` and CI path triggers.
- Extend `internal/advisories/artifacthub.go` (existing client) for package-search + package-get usage; do not duplicate the HTTP client.
- Reuse `internal/helm/fetcher.go` (`getIndex`, `ListVersions`, `FindNearestVersion`, `FetchValues`, `fetchChartYAML`) for version pickers and smart-values fetch; do not re-invent.
- Reuse `internal/ai/client.go` providers; no new AI plumbing. Respect per-Connection AI config. (§4.4.3, §4.4.4)
- Reuse `internal/api/tiered_git.go` + `internal/audit/tier.go` (v1.20); do not add a parallel tiering path. (§4.3.5)
- ArtifactHub endpoints used: `GET /packages/search?ts_query_web=<q>&kind=0`, `GET /packages/helm/{repo}/{chart}`. No token required; public endpoints. (§4.3.2, §4.5)
- OpenSSF Scorecard source: `https://api.scorecard.dev/projects/github.com/<owner>/<repo>`; daily job + in-memory cache; no persistent store. (§4.6)
- Cosign: keyless OIDC via GitHub Actions; integrate into `.github/workflows/release.yml`. (§4.7)
- Per-cluster values file naming is **one file per cluster** at `configuration/addons-clusters-values/<cluster>.yaml` (not a directory per cluster); corrected in rev 2. (§1.5, §4.3.6)

### UX Design Requirements

**UX-DR-V121-1** — "Add Addon" button on `ui/src/views/AddonCatalog.tsx` opens a modal with a **tablist** (Browse / Search by name / Paste Helm URL); Browse is the default tab. (§4.3)

**UX-DR-V121-2** — **Browse tab** renders tiles in a responsive grid, each tile showing name, description, category pill, maintainer list, license badge, `curated_by` tag chips, and a Scorecard badge (numeric + tier color). (§4.3.1, §4.6)

**UX-DR-V121-3** — **Browse tab filters**: Category pill row (single-select), `curated_by` multi-select chips, min-score slider 0-10, min-K8s-version select. Filters apply live. (§4.3.1)

**UX-DR-V121-4** — **Search tab** debounces input at 250 ms, merges curated-first + ArtifactHub-second, badges results with "Curated" vs "ArtifactHub" + verified-publisher indicator + star count. (§4.3.2)

**UX-DR-V121-5** — **Search tab connectivity banner** when ArtifactHub is unreachable: "ArtifactHub unreachable — showing curated only. Retry connectivity." with retry action wiring `POST /api/v1/catalog/reprobe`. (§4.3.2)

**UX-DR-V121-6** — **Paste Helm URL tab** shows inline validation: green check + "Found 12 versions (latest 1.20.2)" on success, inline error + remediation hint on failure. (§4.3.3)

**UX-DR-V121-7** — **Configure step** prefills from the selected source, with a version picker showing top-5 stable versions plus a "Show pre-releases" toggle; manual version entry is validated against `index.yaml`. (§4.3.4)

**UX-DR-V121-8** — **Configure step** shows read-only display info (maintainers, docs URL, license, category, Scorecard tier) so the user sees what they're adding. No cluster selector. (§4.3.4)

**UX-DR-V121-9** — **Duplicate guard modal** when `name` already exists in the user's catalog: "**cert-manager** is already in the catalog. Open its page to edit or enable it on a cluster." with a link to `/addons/cert-manager`. (§4.3.4)

**UX-DR-V121-10** — **Submit toast** on success: "PR opened — [View on GitHub]" linking to the Tier 2 PR URL. (§4.3.5)

**UX-DR-V121-11** — **Version-mismatch banner** on the Values tab (`ui/src/components/ValuesEditor.tsx`) when the catalog chart version differs from the generated-file header version: "Chart upgraded to v1.20.2 — values were generated for v1.19.0. Refresh values from upstream?" with [Refresh now] [Dismiss] actions. (§4.4.5)

**UX-DR-V121-12** — **Remove** the always-visible "Pull upstream defaults" button from `ValuesEditor.tsx`. (§4.4.6)

**UX-DR-V121-13** — **AI-not-configured banner** on the Values tab: "AI annotation not configured — values are not commented. Configure AI in Settings → AI to enable." (§4.4.3)

**UX-DR-V121-14** — **Settings → AI** adds `Annotate values on generate` toggle (default ON when AI configured); respects per-addon opt-out. (§4.4.4)

**UX-DR-V121-15** — **Accessibility**: keyboard nav through tiles (tab / shift-tab; enter opens Configure); ARIA labels on search bar, tabs, filter chips, version picker, Submit; focus-visible outlines; live-region announces search-result count updates; proper dialog + tablist ARIA roles. (§4.8)

**UX-DR-V121-16** — **Visual consistency**: follow Sharko theme — `ring-2 ring-[#6aade0]` card borders, blue-tinted light-mode palette (no gray in light mode), Quicksand for any "Sharko" brand text, `DetailNavPanel` reused where applicable. (`frontend-expert.md`)

---

## Requirements Coverage Map

| Req | Covered by |
|---|---|
| FR-V121-1 | V121-1 Story 1.1, 1.2 |
| FR-V121-2 | V121-1 Story 1.3 |
| FR-V121-3 | V121-1 Story 1.4 |
| FR-V121-4 | V121-1 Story 1.4 |
| FR-V121-5 | V121-3 Story 3.1 (versions endpoint) + V121-2 Story 2.3 (version picker consumer) |
| FR-V121-6 | V121-3 Story 3.2 |
| FR-V121-7 | V121-3 Story 3.3 |
| FR-V121-8 | V121-3 Story 3.4 |
| FR-V121-9 | V121-3 Story 3.5 |
| FR-V121-10 | V121-2 Story 2.1 |
| FR-V121-11 | V121-2 Story 2.2 |
| FR-V121-12 | V121-3 Story 3.6 |
| FR-V121-13 | V121-4 Story 4.1 |
| FR-V121-14 | V121-2 Story 2.3 |
| FR-V121-15 | V121-5 Story 5.1 |
| FR-V121-16 | V121-5 Story 5.2 |
| FR-V121-17 | V121-6 Story 6.1, 6.2 |
| FR-V121-18 | V121-6 Story 6.3 |
| FR-V121-19 | V121-7 Story 7.1, 7.2 |
| FR-V121-20 | V121-7 Story 7.3 |
| FR-V121-21 | V121-6 Story 6.4 |
| FR-V121-22 | V121-6 Story 6.5 |
| FR-V121-23 | V121-6 Story 6.6 |
| FR-V121-24 | V121-1 Story 1.5 |
| FR-V121-25 | V121-2 Story 2.4 |
| FR-V121-26 | V121-1 Story 1.5 |
| FR-V121-27 | V121-8 Story 8.2 |
| FR-V121-28 | V121-8 Story 8.3 |
| FR-V121-29 | V121-8 Story 8.1 |
| FR-V121-30 | V121-1 Story 1.1 |
| NFR-V121-1 | Enforced across all stories (no new store); V121-1, V121-3, V121-7 reviewers verify |
| NFR-V121-2 | V121-1 Story 1.1, V121-3 Story 3.5 (stale-serve), V121-3 Story 3.6 (UI banner) |
| NFR-V121-3 | V121-1 Story 1.1 (metadata-only loader contract) |
| NFR-V121-4 | V121-8 Story 8.4 + cross-cutting in V121-2, V121-6 stories |
| NFR-V121-5 | Per-story quality gate: swagger regen on any @Router-touching story (V121-1, V121-3, V121-5) |
| NFR-V121-6 | V121-5 Story 5.2 (reuses Tier 2 registration) |
| NFR-V121-7 | V121-5 Story 5.2 + V121-8 Story 8.5 (coverage test pass) |
| NFR-V121-8 | V121-8 Story 8.1 |
| NFR-V121-9 | V121-8 Story 8.1 (inherits existing SBOM) |
| NFR-V121-10 | V121-8 Story 8.2, 8.3 |
| NFR-V121-11 | V121-8 Story 8.5 (security sweep) |
| NFR-V121-12 | V121-7 Story 7.1 |
| NFR-V121-13 | Per-story quality gate |
| UX-DR-V121-1 | V121-2 Story 2.1 |
| UX-DR-V121-2 | V121-2 Story 2.2 |
| UX-DR-V121-3 | V121-2 Story 2.2 |
| UX-DR-V121-4 | V121-3 Story 3.6 |
| UX-DR-V121-5 | V121-3 Story 3.6 |
| UX-DR-V121-6 | V121-4 Story 4.1 |
| UX-DR-V121-7 | V121-2 Story 2.3 |
| UX-DR-V121-8 | V121-2 Story 2.3 |
| UX-DR-V121-9 | V121-5 Story 5.1 |
| UX-DR-V121-10 | V121-5 Story 5.3 |
| UX-DR-V121-11 | V121-6 Story 6.4 |
| UX-DR-V121-12 | V121-6 Story 6.5 |
| UX-DR-V121-13 | V121-7 Story 7.4 |
| UX-DR-V121-14 | V121-7 Story 7.3 |
| UX-DR-V121-15 | V121-8 Story 8.4 |
| UX-DR-V121-16 | Cross-cutting — enforced by code-reviewer on every UI story |

No requirement is uncovered.

---

## Epic List

### Epic V121-1: Catalog Foundation
**Goal:** Ship the embedded catalog (`catalog/addons.yaml`), its schema, the loader/search index, the Scorecard refresh job, and the read-only curated APIs — so subsequent UI epics have a stable server-side source of truth.

**FRs covered:** FR-V121-1, FR-V121-2, FR-V121-3, FR-V121-4, FR-V121-24, FR-V121-26, FR-V121-30.
**Rationale:** Nothing else works without this; it is fully shippable standalone (the CLI and API expose it even before the UI lands).

### Epic V121-2: Marketplace UI — Browse + Configure
**Goal:** Replace the blank "Add Addon" modal with a multi-tab Marketplace dialog and ship the Browse tab + shared Configure step on top of Epic 1's catalog APIs. No ArtifactHub dependency.

**FRs covered:** FR-V121-10, FR-V121-11, FR-V121-14, FR-V121-25.
**Rationale:** The Browse tab is the default surface and only depends on Epic 1; shipping it here unblocks user-visible value before ArtifactHub plumbing lands.

### Epic V121-3: ArtifactHub Proxy + Search Tab
**Goal:** Add the ArtifactHub proxy endpoints (`search`, `remote`, `reprobe`, `versions`), 3-tier cache with stale-serve + backoff, and the Search-by-name tab in the UI.

**FRs covered:** FR-V121-5, FR-V121-6, FR-V121-7, FR-V121-8, FR-V121-9, FR-V121-12.
**Rationale:** Layered on top of Epic 1 but independent of Epic 2 internals; delivers the "discover newly-popular charts" promise.

### Epic V121-4: Marketplace UI — Paste Helm URL
**Goal:** Add the power-user "Paste Helm URL" tab with live `index.yaml` validation, wired to the `versions` endpoint from Epic 3.

**FRs covered:** FR-V121-13.
**Rationale:** Small but user-valuable slice; kept separate so it can ship even if ArtifactHub work is blocked.

### Epic V121-5: Add Flow — Tier 2 PR via Existing Endpoint
**Goal:** Wire Configure-step Submit to the existing `POST /api/v1/addons` via `orchestrator.AddAddon`, reusing v1.20's Tier 2 tiered-Git + audit enrichment paths. Includes duplicate guard and success toast.

**FRs covered:** FR-V121-15, FR-V121-16.
**Rationale:** Small, high-risk slice (it's the actual mutation); isolated into its own epic so tier coverage + audit tests are its gate.

### Epic V121-6: Smart Values Layer + Version-Mismatch Banner
**Goal:** Generate global values + per-cluster template from upstream `values.yaml`, stamp the file header, detect version mismatch on render, remove the v1.20 always-visible "Pull upstream" button, and seed new-cluster stanzas on Operation 2.

**FRs covered:** FR-V121-17, FR-V121-18, FR-V121-21, FR-V121-22, FR-V121-23.
**Rationale:** Deep backend + touches Operation 2's existing `EnableAddon` path for seeding; best kept as a cohesive slice so its file-header contract is consistent.

### Epic V121-7: AI Annotate + Secret-Leak Guard
**Goal:** Plug AI annotation into the Smart Values pipeline with a hard secret-leak pre-scan, Settings toggle, per-addon opt-out, and "not configured" banner.

**FRs covered:** FR-V121-19, FR-V121-20.
**Rationale:** Depends on Epic 6 having the pipeline structure; isolated because the secret-leak guard has independent security-review needs.

### Epic V121-8: Release Hardening, CI, Accessibility, Coverage
**Goal:** Cosign keyless signing on the release pipeline, the `catalog-validate` CI workflow, CODEOWNERS, WCAG 2.1 AA audit on new pages, and passing `TestTierCoverage` + `TestAuditCoverage`.

**FRs/NFRs covered:** FR-V121-27, FR-V121-28, FR-V121-29, NFR-V121-4, NFR-V121-8, NFR-V121-10, NFR-V121-11.
**Rationale:** Cross-cutting quality epic; runs last because it audits everything else.

### Epic V121-9: Documentation
**Goal:** Ship `user-guide/marketplace.md`, `user-guide/smart-values.md`, `operator-manual/supply-chain.md`, `developer-guide/contributing-catalog.md`, and a changelog/release-notes entry; regenerate swagger across the bundle.

**FRs covered:** NFR-V121-5 (swagger), plus `.claude/team/docs-writer.md` continuous docs rule applied per-epic.
**Rationale:** Final epic to keep docs from drifting; per lean-workflow, docs-writer is also touched within each prior epic, but this epic ties it together into publishable guides.

---

## Epic V121-1: Catalog Foundation

Ship the embedded catalog and its read-only surface — loader, schema, search index, curated APIs, and daily Scorecard refresh.

### Story V121-1.1: Ship `catalog/addons.yaml` + `catalog/schema.json` + embed hook

As a **Sharko maintainer**,
I want the ~50-entry curated catalog and its JSON Schema to live in the repo and be embedded in the binary,
So that the Sharko server ships with a self-contained curated list and cannot drift from its schema.

**Acceptance Criteria:**

**Given** the Sharko repo has no `catalog/` directory
**When** I materialize `catalog/addons.yaml` from `docs/design/examples/addons.yaml.draft` and add `catalog/schema.json` covering every documented field in §4.2
**Then** `go build ./...` passes
**And** `catalog/addons.yaml` is byte-embedded via `//go:embed` from `internal/catalog/embed.go`
**And** the loader rejects entries that fail JSON Schema validation at startup with a clear error naming the offending entry.

**Given** a curated entry with an unknown field not in the schema
**When** the loader runs
**Then** the unknown field is tolerated (forward-compatible), per §4.2 "Unknown fields are tolerated."

**Given** the starter catalog ships
**When** I run `make build`
**Then** the binary contains the catalog bytes and does not read `catalog/addons.yaml` from disk at runtime.

**Technical notes:**
- Files: `catalog/addons.yaml`, `catalog/schema.json`, `internal/catalog/embed.go`, `internal/catalog/loader.go`.
- Schema fields per §4.2; enum for `category` per §4.2.1 and for `curated_by` per §4.2.
- Validate license SPDX IDs are in the allow-list warning set (enforcement lives in CI — see Story V121-8.2).

**Role file:** `.claude/team/architect.md` + `.claude/team/go-expert.md`.
**Effort:** M.
**Dependencies:** none.

---

### Story V121-1.2: Loader with startup validation + tolerate-unknown

As a **Sharko server**,
I want to parse the embedded catalog YAML into typed structs at startup and fail fast on schema violations,
So that a malformed catalog cannot silently produce a broken marketplace.

**Acceptance Criteria:**

**Given** a catalog entry missing `name`
**When** the loader initializes
**Then** the server logs a structured error naming the malformed entry and refuses to start.

**Given** a catalog entry with a duplicate `name`
**When** the loader initializes
**Then** the server refuses to start and names both duplicate entries.

**Given** a catalog with 100 well-formed entries
**When** the loader initializes on a cold start
**Then** load time is under 200 ms on a local laptop (non-contractual; sanity check).

**Technical notes:**
- File: `internal/catalog/loader.go` — typed struct `CatalogEntry` matches §4.2 shape exactly.
- Unit tests with table-driven cases: happy path, missing field, duplicate name, unknown category, unknown `curated_by` tag.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** S.
**Dependencies:** V121-1.1.

---

### Story V121-1.3: In-memory search index + filter predicate

As a **Sharko server**,
I want an in-memory index over curated entries supporting name/description/maintainers full-text and category / `curated_by` / min-score / min-K8s-version filters,
So that the API can serve Browse tab queries with no database.

**Acceptance Criteria:**

**Given** the loader has populated N entries
**When** I call `search.Query(name="cert", minScore=7.0)`
**Then** only entries whose name/description/maintainers contain "cert" (case-insensitive substring) AND whose `security_score >= 7.0` are returned.

**Given** a min-K8s-version filter of `1.25`
**When** an entry has `min_kubernetes_version: "1.23"`
**Then** it is included (caller's cluster is newer-or-equal).

**Given** `security_score: unknown`
**When** I apply `minScore=0`
**Then** the entry is included; `minScore>0` excludes it.

**Technical notes:**
- File: `internal/catalog/search.go`. Pure in-memory, no state between calls beyond the loaded index.
- Table-driven tests covering each filter permutation.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V121-1.2.

---

### Story V121-1.4: `GET /api/v1/catalog/addons` + `GET /api/v1/catalog/addons/{name}`

As an **API consumer**,
I want to list and fetch curated catalog entries via REST,
So that the UI and CLI have a stable contract for the Browse tab.

**Acceptance Criteria:**

**Given** the server is running
**When** I `GET /api/v1/catalog/addons?category=security&min_score=7.5`
**Then** I receive a JSON array of entries filtered by category + score with 200 status.

**Given** `GET /api/v1/catalog/addons/cert-manager`
**When** the entry exists
**Then** I receive the full entry including its Scorecard `security_score`, derived tier label, and `security_score_updated` timestamp with 200 status.

**Given** `GET /api/v1/catalog/addons/nonexistent`
**When** no such entry exists
**Then** I receive 404 with `{error: "not found"}`.

**Given** the endpoints are registered
**When** I run `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal`
**Then** `docs/swagger/swagger.json` contains both routes with the documented request/response shapes.

**Technical notes:**
- Files: `internal/api/catalog.go` (new) + wiring in `internal/api/router.go`.
- Full swaggo annotations per `.claude/team/go-expert.md` pattern.
- Reads-only — no audit enrichment, no tier registration.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/docs-writer.md` (swagger regen).
**Effort:** M.
**Dependencies:** V121-1.3.

---

### Story V121-1.5: Daily OpenSSF Scorecard refresh job + Prometheus metrics

As a **Sharko operator**,
I want each catalog entry's Scorecard aggregate score refreshed daily at 04:00 UTC and exposed via Prometheus,
So that the Browse tab surfaces fresh security posture without blocking on API calls at render time.

**Acceptance Criteria:**

**Given** a catalog entry has `source_url: https://github.com/cert-manager/cert-manager`
**When** the daily job runs
**Then** it calls `https://api.scorecard.dev/projects/github.com/cert-manager/cert-manager`, parses the aggregate score, and updates the in-memory value plus `security_score_updated` timestamp.

**Given** the Scorecard API returns 500 or times out
**When** the job runs
**Then** the entry retains its last-known score, a non-fatal error is logged, and `sharko_scorecard_refresh_total{status="error"}` increments.

**Given** a successful refresh cycle
**When** the job completes
**Then** `sharko_scorecard_last_refresh_timestamp` is set to the current Unix time and `sharko_scorecard_refresh_total{status="success"}` increments by the number of entries refreshed.

**Given** Sharko restarts
**When** the server boots
**Then** scores revert to whatever is baked into `catalog/addons.yaml` until the next successful refresh (no persistent store; NFR-V121-1).

**Technical notes:**
- File: `internal/catalog/scorecard.go`. Goroutine + `time.Ticker` scheduled to fire at 04:00 UTC daily.
- Metric registration via the existing `internal/metrics/` package (see `go-expert.md`).
- In-memory only; no ConfigMap, no BoltDB.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/devops-agent.md` (metric naming review).
**Effort:** M.
**Dependencies:** V121-1.2.

---

## Epic V121-2: Marketplace UI — Browse + Configure

Replace the blank "Add Addon" modal with the 3-tab Marketplace dialog and deliver the default Browse tab + the shared Configure step.

### Story V121-2.1: "Add Addon" modal shell with tablist (Browse default)

As an **operator**,
I want a tabbed modal replacing the old 4-field form,
So that discovery has a real entry point.

**Acceptance Criteria:**

**Given** I am on the Addons Catalog page
**When** I click "Add Addon"
**Then** a modal opens with three tabs in order: **Browse marketplace** (selected), **Search by name**, **Paste Helm URL**.

**Given** the modal is open
**When** I press Escape
**Then** the modal closes and focus returns to the "Add Addon" button.

**Given** the modal is open
**When** I tab through it
**Then** focus cycles through tabs → active-tab content → Cancel/Continue footer in that order with focus-visible outlines.

**Technical notes:**
- Files: `ui/src/views/AddonCatalog.tsx` (call site), new `ui/src/components/MarketplaceDialog.tsx` (tab shell + routing).
- Reuse shadcn `Dialog` + `Tabs` primitives; apply Sharko theme (`ring-2 ring-[#6aade0]`, blue-tinted light mode — `frontend-expert.md`).
- ARIA `role="dialog"`, `role="tablist"`, `role="tab"`, `aria-selected`; WAI-ARIA Authoring Practices reference.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** none (UI stub — Browse implementation is next story).

---

### Story V121-2.2: Browse tab — curated tiles with filters

As an **operator**,
I want to browse the curated catalog with filters,
So that I can find an addon that matches my category, trust tags, security posture, and K8s version.

**Acceptance Criteria:**

**Given** the Browse tab is active
**When** it first renders
**Then** it fetches `GET /api/v1/catalog/addons` and renders all entries as tiles in a responsive grid.

**Given** I click the "Security" category pill
**Then** only entries with `category=security` remain; the pill row shows the active pill; other pills toggle filters.

**Given** I set the min-score slider to 7.5
**Then** only tiles with `security_score >= 7.5` (numeric) remain; entries with `unknown` are hidden when min-score > 0.

**Given** I select `curated_by: cncf-graduated` and `curated_by: aws-eks-blueprints`
**Then** only entries with BOTH tags remain (AND semantics).

**Given** I type "cert" in the text search
**Then** tiles filter live (debounced 150 ms) to entries whose name / description / maintainers match.

**Given** I click a tile
**Then** the modal transitions to the Configure step with that entry preselected (Story V121-2.3).

**Technical notes:**
- File: `ui/src/components/MarketplaceBrowseTab.tsx`.
- API wiring via `ui/src/services/api.ts` (new method `listCatalog(filters)`), types in `ui/src/services/models.ts`.
- Tile component shows: name, description, category pill, maintainer list, license badge, `curated_by` chips, Scorecard badge (numeric + tier color from Story V121-2.4).

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** L.
**Dependencies:** V121-1.4, V121-2.1.

---

### Story V121-2.3: Configure step — name/namespace/sync-wave/version picker

As an **operator**,
I want to confirm the catalog entry's name, namespace, sync wave, and chart version before submitting,
So that I can adjust defaults without leaving the modal.

**Acceptance Criteria:**

**Given** I clicked a tile in Browse
**When** the Configure step opens
**Then** Name is pre-filled with the entry's `name`, Namespace with `default_namespace`, Sync wave with `default_sync_wave`.

**Given** the Configure step is rendered
**When** I open the version picker
**Then** it calls `GET /api/v1/catalog/versions?repo=<url>&chart=<name>` (FR-V121-5) and lists top-5 **stable** versions with a "Show pre-releases" toggle.

**Given** I toggle "Show pre-releases"
**Then** pre-release versions appear in the list (full list, no truncation).

**Given** I type a custom version
**When** it blurs
**Then** it is validated against the same `versions` payload; invalid = inline error "Version not found in index.yaml".

**Given** the Configure step is rendered
**When** I look at the read-only display info panel
**Then** I see maintainers, docs URL, license, category, and Scorecard tier pulled from the catalog entry.

**Given** I click Cancel
**Then** the modal returns to the tab I came from (Browse in this story; Search/Paste URL in their stories) without submitting.

**Technical notes:**
- File: `ui/src/components/MarketplaceConfigureStep.tsx`.
- Version picker subcomponent reusable across all three tabs (§4.3.4 "shared by all three paths").
- No cluster selector (§1.5, §4.3.4). State is lifted into `MarketplaceDialog` so tabs can hand off the candidate entry.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V121-2.2, V121-3.1 (versions endpoint).

---

### Story V121-2.4: Scorecard badge component + tier color mapping

As an **operator**,
I want each tile/result to show the OpenSSF Scorecard numeric score with a color-coded tier,
So that I can scan security posture at a glance.

**Acceptance Criteria:**

**Given** an entry has `security_score: 8.3`
**When** the badge renders
**Then** it shows "8.3" with the **Strong** tier (green) per §4.6 mapping.

**Given** an entry has `security_score: 5.5`
**Then** the badge renders "5.5 / Moderate" in amber.

**Given** `security_score: 3.0`
**Then** the badge renders "3.0 / Weak" in red.

**Given** `security_score: unknown`
**Then** the badge renders "unknown" in grey with a tooltip: "Scorecard score not yet available — refreshed daily."

**Given** the badge is keyboard-focused
**Then** a tooltip shows the `security_score_updated` timestamp and a link to the full Scorecard page.

**Technical notes:**
- File: `ui/src/components/ScorecardBadge.tsx`.
- Used in Browse tiles (V121-2.2), Search results (V121-3.6), and Configure read-only info (V121-2.3).
- Theme: blue-tinted palette for background; tier colors via explicit hex, not Tailwind gray.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** S.
**Dependencies:** V121-1.4 (API surfaces the tier field).

---

## Epic V121-3: ArtifactHub Proxy + Search Tab

Add the proxy endpoints and cache, then build the Search-by-name tab.

### Story V121-3.1: `GET /api/v1/catalog/versions` using `internal/helm/fetcher.go`

As the **Marketplace modal**,
I want a server endpoint that returns the top chart versions for a repo + chart pair,
So that the version picker does not hit the Helm repo from the browser.

**Acceptance Criteria:**

**Given** `GET /api/v1/catalog/versions?repo=https://charts.jetstack.io&chart=cert-manager`
**When** the endpoint runs
**Then** it calls `internal/helm/fetcher.go.ListVersions` (or `FindNearestVersion` as appropriate), returns `{"versions":[...], "latest_stable":"1.20.2"}` with 200.

**Given** the repo URL is malformed
**Then** the endpoint returns 400 with `{"error":"invalid repo URL"}`.

**Given** the repo is unreachable
**Then** the endpoint returns 502 with `{"error":"upstream unreachable"}` and serves from cache if a cache entry exists, adding header `X-Cache-Stale: true`.

**Given** the endpoint is registered
**When** `swag init` runs
**Then** the swagger spec contains the endpoint with query params and response schema.

**Technical notes:**
- File: `internal/api/catalog_versions.go`.
- Cache: 15-min LRU capped at 500 entries, keyed by `repo|chart` (part of V121-3.5).
- Reuses existing `internal/helm/fetcher.go`; does not re-invent index fetching.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V121-3.5 (cache infra ideally shipped same bundle).

---

### Story V121-3.2: `GET /api/v1/catalog/search` blended curated + ArtifactHub

As an **API consumer**,
I want a single search endpoint that blends curated hits and ArtifactHub hits,
So that the Search tab does not have to call two upstreams.

**Acceptance Criteria:**

**Given** `GET /api/v1/catalog/search?q=prometheus`
**When** the endpoint runs
**Then** it returns `{ "curated":[...], "artifacthub":[...] }` where `curated` is the in-memory search index result and `artifacthub` is the proxied `/packages/search?ts_query_web=prometheus&kind=0` response.

**Given** the ArtifactHub call fails
**Then** the response still includes `curated` and sets `"artifacthub_error": "<classification>"` (not 502) so the UI can render the banner.

**Given** two identical `q` queries within 10 minutes
**Then** the second call is served from cache and responds in <10 ms; verified via a unit test.

**Technical notes:**
- File: `internal/api/catalog_search.go`. Uses existing `internal/advisories/artifacthub.go` HTTP client (extend if needed).
- No API token; public endpoint.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** M.
**Dependencies:** V121-1.3, V121-3.5.

---

### Story V121-3.3: `GET /api/v1/catalog/remote/{pkgID}` — ArtifactHub package detail proxy

As the **Marketplace Search tab**,
I want the server to proxy a specific ArtifactHub package detail,
So that the browser does not call ArtifactHub directly (CORS + rate-limit control).

**Acceptance Criteria:**

**Given** `GET /api/v1/catalog/remote/helm/jetstack/cert-manager`
**Then** the endpoint fetches `https://artifacthub.io/api/v1/packages/helm/jetstack/cert-manager` and returns the JSON payload with 200.

**Given** the package ID is malformed
**Then** the endpoint returns 400.

**Given** ArtifactHub returns 404
**Then** the endpoint returns 404 passthrough.

**Given** ArtifactHub returns 429
**Then** the endpoint retries with exponential backoff up to 3 attempts, then serves from cache if present (X-Cache-Stale: true) or returns 502.

**Technical notes:**
- File: `internal/api/catalog_remote.go`.
- Cache tier: 1 h LRU 500 entries (part of V121-3.5).

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V121-3.5.

---

### Story V121-3.4: `POST /api/v1/catalog/reprobe` — force ArtifactHub connectivity re-check

As a **user stuck behind a proxy**,
I want a button that tells Sharko to retry ArtifactHub,
So that the "unreachable" banner can be dismissed without waiting.

**Acceptance Criteria:**

**Given** the ArtifactHub circuit is in "unreachable" state
**When** I `POST /api/v1/catalog/reprobe`
**Then** the server performs a lightweight GET against ArtifactHub's health endpoint, updates the circuit state, and returns `{"reachable": true|false, "last_error": "..."}`.

**Given** the reprobe succeeds
**Then** subsequent search/remote calls skip the short-circuit and actually hit ArtifactHub.

**Technical notes:**
- File: `internal/api/catalog_reprobe.go`. Keeps a single boolean + last-error timestamp in memory.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V121-3.3.

---

### Story V121-3.5: 3-tier in-memory cache with stale-serve + exponential backoff

As the **catalog subsystem**,
I want a well-tested LRU cache shared across search/remote/versions endpoints,
So that latency stays low and upstream hiccups degrade gracefully.

**Acceptance Criteria:**

**Given** a fresh cache
**When** I call search twice with the same query within 10 min
**Then** the second call does not hit ArtifactHub (verified via an interception).

**Given** a cached entry older than its TTL but under 24 h
**When** the upstream fails
**Then** the cached value is returned with `X-Cache-Stale: true`.

**Given** three consecutive 429 responses
**When** the backoff policy triggers
**Then** waits are 1 s / 2 s / 4 s (jittered) and a fourth call within the window short-circuits to stale-serve.

**Given** cache capacity is reached
**When** a new entry is added
**Then** the least-recently-used entry is evicted.

**Technical notes:**
- File: `internal/catalog/cache.go`. Tiers: search 10 min / 200 entries, package 1 h / 500 entries, versions 15 min / 500 entries (§4.5).
- Table-driven tests for each tier + eviction + stale-serve + backoff.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** none (ideally lands first inside Epic 3).

---

### Story V121-3.6: Search tab UI — blended results + unreachable banner

As an **operator**,
I want a name-search that shows curated hits first and ArtifactHub hits second, with a clear indicator when ArtifactHub is down,
So that I can find any chart, not just curated ones.

**Acceptance Criteria:**

**Given** I click the Search tab
**Then** an input field has focus, keyboard-accessible, with `aria-label="Search addons by name"`.

**Given** I type "prometheus"
**When** the input debounces for 250 ms
**Then** the UI calls `GET /api/v1/catalog/search?q=prometheus` and renders curated hits (badged "Curated") on top, ArtifactHub hits (badged "ArtifactHub" + verified-publisher icon + star count) below.

**Given** the API returns `artifacthub_error`
**Then** a banner appears above the ArtifactHub section: "ArtifactHub unreachable — showing curated only. Retry connectivity." with a "Retry" button that calls `POST /api/v1/catalog/reprobe`.

**Given** I click a result
**Then** the modal transitions to Configure with name/repo/chart prefilled from the chosen result (ArtifactHub results also fetch `/api/v1/catalog/remote/{pkgID}` for full metadata).

**Technical notes:**
- File: `ui/src/components/MarketplaceSearchTab.tsx`.
- Live-region announcement on results-count change (NFR-V121-4).

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** L.
**Dependencies:** V121-3.2, V121-3.3, V121-3.4, V121-2.3.

---

## Epic V121-4: Marketplace UI — Paste Helm URL

The power-user tab.

### Story V121-4.1: Paste URL tab with live `index.yaml` validation

As a **power user**,
I want to paste a repo URL + chart name and get instant validation,
So that I can add charts that aren't on ArtifactHub.

**Acceptance Criteria:**

**Given** I am on the Paste Helm URL tab
**When** I paste `https://charts.jetstack.io` into "Chart repo URL" and type `cert-manager` into "Chart name" and tab out
**Then** the UI calls `GET /api/v1/catalog/versions?repo=...&chart=...` and shows a green check with "Found 12 versions (latest 1.20.2)".

**Given** I paste a URL missing `index.yaml`
**Then** the UI shows an inline error: "Could not fetch `index.yaml` at `<URL>/index.yaml`. Check the repo URL." with a link to Helm repo basics.

**Given** the chart name is not in the index
**Then** the UI shows: "Chart `<name>` not found in this repo. Did you mean `<nearest-match>`?"

**Given** validation succeeds
**When** I click Continue
**Then** the modal transitions to Configure with name/repo/chart prefilled from the inputs.

**Technical notes:**
- File: `ui/src/components/MarketplacePasteURLTab.tsx`.
- Reuses the `versions` endpoint from Story V121-3.1.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V121-3.1, V121-2.3.

---

## Epic V121-5: Add Flow — Tier 2 PR via Existing Endpoint

Wire Submit to the existing `POST /api/v1/addons` through v1.20 tiered-Git.

### Story V121-5.1: Duplicate-guard on Submit

As an **operator**,
I want the modal to block submitting an addon that already exists in my catalog,
So that I don't accidentally open a no-op PR.

**Acceptance Criteria:**

**Given** my `configuration/addons-catalog.yaml` already contains `cert-manager`
**When** the Configure step's Submit is clicked for `cert-manager`
**Then** Submit is disabled with an inline message: "**cert-manager** is already in the catalog. Open its page to edit or enable it on a cluster." with a link `/addons/cert-manager`.

**Given** I rename the candidate in the Name field to a name not in the catalog
**Then** Submit becomes enabled again.

**Technical notes:**
- File: UI check uses the existing addon list endpoint; no new API.
- Server defense-in-depth: `POST /api/v1/addons` already checks duplicates in `orchestrator.AddAddon` — assert this remains true.

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/go-expert.md` (server-side assert).
**Effort:** S.
**Dependencies:** V121-2.3.

---

### Story V121-5.2: Submit wires to `POST /api/v1/addons` with Tier 2 attribution

As an **operator**,
I want Submit to open a Tier 2 PR via the existing add-addon endpoint,
So that attribution and audit behave identically to v1.20's other Tier 2 writes.

**Acceptance Criteria:**

**Given** a valid Configure state
**When** I click Submit
**Then** the UI calls `POST /api/v1/addons` with the chosen fields; the server resolves the Git token via `GitProviderForTier` (`internal/api/tiered_git.go`) for Tier 2.

**Given** the handler runs
**When** the audit middleware emits
**Then** the event name is `addon_added_from_catalog` (or existing `addon_added` event extended with detail), and `TestAuditCoverage` passes with this handler included.

**Given** the handler runs
**Then** `TestTierCoverage` (`internal/api/pattern_tier.go`) classifies the route as Tier 2 (matches existing v1.20 registration).

**Given** Tier 2 is used and the user has no PAT configured
**Then** the server proceeds with the service token (attribution mode = service per v1.20), and the audit entry's attribution mode reflects that.

**Technical notes:**
- File: `ui/src/components/MarketplaceConfigureStep.tsx` (Submit handler), `internal/api/addons_write.go` (verify audit.Enrich call site names "catalog"-attributable detail).
- No new mutation code — **reuse** `orchestrator.AddAddon` (`internal/orchestrator/addon.go:16-99`).

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** V121-5.1.

---

### Story V121-5.3: Success toast with PR link

As an **operator**,
I want a toast that confirms the PR opened and links to it,
So that I can click through to watch CI or merge it.

**Acceptance Criteria:**

**Given** Submit returns `pr_url`
**Then** a toast appears: "PR opened — [View on GitHub]" linking to `pr_url`, auto-dismissing after 8 s.

**Given** Submit returns a partial-success (PR opened but not merged) response
**Then** the toast says "PR opened (auto-merge failed) — [View on GitHub]" in amber.

**Given** the toast is dismissed
**Then** focus returns to the "Add Addon" button on the catalog page.

**Technical notes:**
- File: reuse existing toast primitive in `ui/src/`. Keep ARIA `role="status"` for screen-reader announcement.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** S.
**Dependencies:** V121-5.2.

---

## Epic V121-6: Smart Values Layer + Version-Mismatch Banner

Generate annotated values files; detect stale versions; remove the v1.20 always-visible button.

### Story V121-6.1: Heuristic cluster-specific-field detector

As the **smart values layer**,
I want a pure function that takes a `values.yaml` tree and returns the set of dotted paths considered cluster-specific,
So that global/template split is deterministic and testable.

**Acceptance Criteria:**

**Given** a YAML with `ingress.host`, `replicaCount`, `resources.requests.cpu`
**When** I run the heuristic
**Then** all three paths are returned as cluster-specific.

**Given** a YAML with `image.repository`
**When** I run the heuristic
**Then** it is NOT flagged as cluster-specific.

**Given** every pattern from §4.4.2
**When** the test suite runs
**Then** each pattern has at least one positive and one negative case.

**Technical notes:**
- File: `internal/catalog/heuristics.go`. Glob list lives here; schema-PR can extend (§4.4.2 "Decision #2").
- Case-insensitive match on full dotted path.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** V121-1.2.

---

### Story V121-6.2: Split + emit global values file + per-cluster template block

As the **Add Addon pipeline**,
I want the generated global file to contain upstream defaults with cluster-specific fields commented out at their original position, and a trailing commented template block,
So that Operation 2 (deploy) can seed a cluster stanza cleanly.

**Acceptance Criteria:**

**Given** upstream `values.yaml` with `ingress.host: example.com`
**When** the pipeline emits `addons-global-values/<addon>.yaml`
**Then** the global file has `# ingress:\n#   host: <cluster-specific>` in place of the real value.

**Given** the pipeline emits the global file
**Then** the file ends with a `# --- per-cluster overrides template ---` block matching the format in §4.4.1 step 4.

**Given** the chart's `values.yaml` has deeply nested maps
**When** the pipeline emits
**Then** commented placeholders preserve original indentation and sibling ordering.

**Technical notes:**
- File: `internal/catalog/values_split.go`. Must preserve comments from upstream where present (reuse techniques from `internal/gitops/yaml_mutator.go`).

**Role file:** `.claude/team/go-expert.md`.
**Effort:** L.
**Dependencies:** V121-6.1.

---

### Story V121-6.3: File header stamp

As a **future render**,
I want every generated file to carry a self-describing header,
So that the version-mismatch detector and the `sharko: managed=true` signal work.

**Acceptance Criteria:**

**Given** a newly generated `addons-global-values/<addon>.yaml`
**Then** the first non-blank lines contain, in order:
```
# Generated by Sharko from <chart>@<version> on <ISO-date>
# Chart source: <repo URL>
# AI annotation: <enabled|disabled>
# sharko: managed=true
```

**Given** a user hand-edits the header and removes `sharko: managed=true`
**When** the version-mismatch detector runs (V121-6.4)
**Then** it suppresses the banner and tooltip explains: "Sharko is not managing this file (missing `sharko: managed=true` header)."

**Technical notes:**
- File: `internal/catalog/header.go` (write + parse).

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V121-6.2.

---

### Story V121-6.4: Version-mismatch detection + Values tab banner

As an **operator looking at Values**,
I want a banner when my catalog version is ahead of the generated file,
So that I know to refresh.

**Acceptance Criteria:**

**Given** `addons-catalog.yaml` says `cert-manager@v1.20.2` and the generated file header says `v1.19.0`
**When** I open the addon's Values tab
**Then** a banner reads: "Chart upgraded to v1.20.2 — values were generated for v1.19.0. Refresh values from upstream?" with `[Refresh now]` and `[Dismiss]` buttons.

**Given** I click "Refresh now"
**Then** the UI calls a new `POST /api/v1/addons/{name}/values/refresh` (or reuses existing refresh endpoint, implementer's choice) which regenerates via Stories V121-6.1/2/3 and opens a **Tier 2 PR** rewriting `addons-global-values/<addon>.yaml`.

**Given** the regeneration preserves existing per-cluster files in `configuration/addons-clusters-values/<cluster>.yaml`
**Then** no per-cluster file is modified.

**Given** the header's `sharko: managed=true` line is missing
**Then** the banner is suppressed.

**Technical notes:**
- Files: `internal/api/values_refresh.go`, `ui/src/components/ValuesEditor.tsx` (banner).
- Tier 2 registration in `internal/api/pattern_tier.go`; audit enrich event `values_refreshed_from_upstream`.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V121-6.3.

---

### Story V121-6.5: Remove v1.20 always-visible "Pull upstream defaults" button

As an **operator**,
I want the old ever-present button gone,
So that the UI isn't misleading when there's no mismatch.

**Acceptance Criteria:**

**Given** `ui/src/components/ValuesEditor.tsx` previously rendered a "Pull upstream defaults" button
**When** this story lands
**Then** the button is removed and its handler is deleted; the endpoint it called is unchanged (still used by Story V121-6.4's banner).

**Given** an existing test asserts the button's presence
**Then** it is deleted or rewritten to assert its absence.

**Technical notes:**
- File: `ui/src/components/ValuesEditor.tsx`, `ui/src/components/__tests__/ValuesEditor.test.tsx`.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** S.
**Dependencies:** V121-6.4 (banner must ship in same bundle so users don't lose the feature).

---

### Story V121-6.6: Seed per-cluster stanza from template on Operation 2

As an **operator enabling an existing addon on a new cluster**,
I want my cluster's stanza in `configuration/addons-clusters-values/<cluster>.yaml` to start from the template block,
So that I don't have to hand-write the skeleton.

**Acceptance Criteria:**

**Given** `addons-global-values/cert-manager.yaml` contains the commented per-cluster template
**When** I enable `cert-manager` on cluster `prod-eu` (Operation 2, existing `EnableAddon`)
**Then** `configuration/addons-clusters-values/prod-eu.yaml` gains a `cert-manager:` stanza with the template's fields uncommented (single file per cluster, §1.5).

**Given** `prod-eu.yaml` already has a `cert-manager:` stanza
**Then** this story does NOT touch it — merge only if the stanza doesn't exist.

**Given** `prod-eu.yaml` has other addon stanzas
**Then** only the `cert-manager:` stanza is added; no other addons are touched.

**Technical notes:**
- Files: `internal/orchestrator/addon_ops.go` (`EnableAddon` / `generateClusterValues` — extend to read the template block). Preserves Operation 2 scope — this is the only v1.21 change to the deploy flow, and it's additive.
- Behind a feature flag if risky; coordinate with `k8s-expert.md` role.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/k8s-expert.md`.
**Effort:** M.
**Dependencies:** V121-6.2, V121-6.3.

---

## Epic V121-7: AI Annotate + Secret-Leak Guard

Plug the AI annotator into the smart-values pipeline; secret-leak guard is mandatory.

### Story V121-7.1: Pre-call secret-leak scanner

As the **AI annotator**,
I want a regex-based scanner over raw `values.yaml` that hard-blocks the upstream call on any match,
So that secrets never leave Sharko.

**Acceptance Criteria:**

**Given** the v1.20 values-editor regex list (sk-, ghp_, AKIA, AIza, 40+ char base64, PEM blocks, api.?key / token / secret / password / credential assignments)
**When** the scanner runs against a `values.yaml` containing any matching pattern
**Then** it returns `{blocked: true, matches: [...redacted summary...]}` and no upstream call is made.

**Given** the scanner blocks
**Then** the UI surfaces a banner on the Configure step: "A secret-like pattern was detected in this chart's values. AI annotation is blocked for this chart." with "What was detected?" detail disclosure.

**Given** no match
**Then** the scanner returns `{blocked: false}` and the pipeline continues.

**Technical notes:**
- File: `internal/catalog/ai_guard.go`. Import the exact regex list from v1.20 (verify the file path during implementation, ~`internal/api/values_extra.go` or its helper).

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md` + `.claude/team/code-reviewer.md` (security-auditor also reviews per NFR-V121-12).
**Effort:** M.
**Dependencies:** V121-6.1.

---

### Story V121-7.2: LLM call — merge cluster-specific detections + inline annotations

As the **smart-values pipeline**,
I want the LLM to (a) return additional cluster-specific paths beyond the heuristic and (b) annotate each top-level field with a one-line comment,
So that the generated file is both well-split and well-documented.

**Acceptance Criteria:**

**Given** AI is configured (provider != `none`) and the pre-scan passed
**When** the pipeline runs for a new chart@version
**Then** the LLM is called **once** with a deterministic prompt ("For each top-level YAML field below, answer 'cluster-specific: yes/no' with a one-line reason + a one-line description.") and the response is parsed.

**Given** the LLM returns new cluster-specific paths not in the heuristic set
**Then** they are **unioned** into the cluster-specific set (LLM is additive, not subtractive).

**Given** the LLM returns per-field descriptions
**Then** each corresponding line in the emitted global file has `#` comment on the line above it.

**Given** the same chart@version is regenerated later
**Then** the annotated file in Git is the cache (no ConfigMap, no BoltDB; NFR-V121-1). A fresh call only happens if the file header's chart version changes.

**Technical notes:**
- File: `internal/catalog/ai_annotate.go`. Reuses `internal/ai/client.go` providers per-Connection.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** L.
**Dependencies:** V121-7.1, V121-6.2.

---

### Story V121-7.3: Settings toggle + per-addon opt-out

As an **admin**,
I want a global toggle controlling AI annotation on generate, plus a per-addon opt-out via file header directive,
So that I can disable annotation for a specific chart without disabling AI globally.

**Acceptance Criteria:**

**Given** Settings → AI, a new row "Annotate values on generate"
**When** AI is configured
**Then** the toggle defaults to ON; flipping OFF persists via the existing AI config store.

**Given** the toggle is OFF
**When** Submit runs
**Then** the pipeline skips the LLM call and emits the file with `# AI annotation: disabled` header.

**Given** a generated file's header contains `# sharko: ai-annotate=off`
**When** the version-mismatch refresh pipeline runs later
**Then** it respects the directive and does not re-annotate (the directive is preserved in the regenerated header).

**Technical notes:**
- Files: `ui/src/views/Settings.tsx` (toggle), `internal/ai/config.go` (field add), `internal/catalog/ai_annotate.go` (respect directive).

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/go-expert.md`.
**Effort:** M.
**Dependencies:** V121-7.2, V121-6.3.

---

### Story V121-7.4: "AI not configured" banner on Values tab

As an **operator**,
I want a banner on the Values tab when AI is off,
So that I understand why my values file has no comments.

**Acceptance Criteria:**

**Given** AI provider is `none` or unset
**When** the Values tab renders a file whose header says `# AI annotation: disabled`
**Then** a banner appears: "AI annotation not configured — values are not commented. Configure AI in Settings → AI to enable." with a link to Settings → AI.

**Given** AI is configured
**Then** the banner is suppressed.

**Technical notes:**
- File: `ui/src/components/ValuesEditor.tsx`.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** S.
**Dependencies:** V121-7.3.

---

## Epic V121-8: Release Hardening, CI, Accessibility, Coverage

Cross-cutting quality bar.

### Story V121-8.1: Cosign keyless signing on release pipeline

As a **Sharko maintainer**,
I want the image, binaries, and catalog asset cosign-signed via GitHub OIDC,
So that supply-chain posture lifts toward CNCF Incubation criteria.

**Acceptance Criteria:**

**Given** `.github/workflows/release.yml` is updated
**When** a release is cut
**Then** `cosign sign --keyless` runs against `ghcr.io/moranweissman/sharko:<tag>`, each binary in the GitHub release, and a copy of `catalog/addons.yaml` attached as a release asset.

**Given** the release completes
**Then** the generated SBOM (CycloneDX, already emitted) still includes the catalog's file hash.

**Given** verification docs live at `docs/operator-manual/supply-chain.md`
**Then** copy-pasting the `cosign verify` command against the published image succeeds locally.

**Technical notes:**
- File: `.github/workflows/release.yml` + `docs/operator-manual/supply-chain.md` (new; docs-writer).

**Role file:** `.claude/team/devops-agent.md` + `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** none.

---

### Story V121-8.2: `catalog-validate` CI workflow

As a **CI pipeline**,
I want to block PRs that break the catalog's schema, license allow-list, chart resolvability, or duplicate-name rule,
So that bad entries cannot merge.

**Acceptance Criteria:**

**Given** `.github/workflows/catalog-validate.yml` runs on every PR touching `catalog/**`
**When** an entry fails JSON Schema
**Then** CI fails with a human-readable error naming the entry.

**Given** an entry's `repo` URL doesn't resolve or its `chart` is absent from `index.yaml`
**Then** CI fails with the fetch URL and what it found.

**Given** two entries share a `name`
**Then** CI fails.

**Given** an entry's `license` is not in the allow-list (Apache-2.0, BSD-3-Clause, MIT, MPL-2.0)
**Then** CI posts a PR comment flagging the entry for human review (warn, not block — per §4.9 "flags for human review").

**Technical notes:**
- File: `.github/workflows/catalog-validate.yml` + a small validation CLI at `cmd/catalog-validate/main.go` or a script under `scripts/`.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** V121-1.1.

---

### Story V121-8.3: CODEOWNERS + deprecation schema flag

As a **project governance**,
I want `catalog/**` gated by CODEOWNERS and deprecation handled by a schema flag (not file deletion),
So that changes require maintainer review and users see soft-deprecation cycles.

**Acceptance Criteria:**

**Given** a PR touches `catalog/**`
**Then** CODEOWNERS requires `@MoranWeissman` review before merge.

**Given** an entry sets `deprecated: true` and optionally `superseded_by: <name>`
**When** the Browse tab renders
**Then** the tile appears greyed-out with a "Deprecated" chip and, if set, a link to the successor.

**Given** a deprecated entry
**Then** it remains in the catalog for one minor release before removal (documented in `developer-guide/contributing-catalog.md`).

**Technical notes:**
- Files: `.github/CODEOWNERS`, `ui/src/components/MarketplaceBrowseTab.tsx` (deprecated rendering), `internal/catalog/loader.go` (expose `deprecated` field).

**Role file:** `.claude/team/devops-agent.md` + `.claude/team/frontend-expert.md` + `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** V121-2.2, V121-1.1.

---

### Story V121-8.4: WCAG 2.1 AA audit on new pages — axe CI + manual keyboard sweep

As an **accessibility reviewer**,
I want automated and manual accessibility checks against the new Marketplace modal + version-mismatch banner,
So that we meet the WCAG 2.1 AA commitment for new pages.

**Acceptance Criteria:**

**Given** `axe-core` or `@axe-core/react` is added to the UI test harness
**When** I run `npm test`
**Then** axe checks the Marketplace modal and ValuesEditor banner and reports zero serious/critical violations.

**Given** a manual keyboard-only sweep is documented in `developer-guide/accessibility.md`
**Then** the sweep covers: open modal → tab through tabs → tab into tile grid → open tile → tab through Configure → Submit → toast → close modal → focus-return.

**Given** color contrast is measured
**Then** all new text meets ≥4.5:1 and all new UI components meet ≥3:1 (shadcn defaults satisfy; verify with axe).

**Technical notes:**
- Files: `ui/src/__tests__/a11y.test.tsx` + `docs/developer-guide/accessibility.md`.
- Existing pages are NOT retrofitted in v1.21 (§4.8) — retrofit is v1.22 backlog.

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** V121-2.x, V121-3.6, V121-4.1, V121-6.4.

---

### Story V121-8.5: Tier + audit coverage + content-policy sweep

As the **quality pipeline**,
I want `TestTierCoverage`, `TestAuditCoverage`, and the forbidden-content grep to pass,
So that every new mutating handler is classified and every mutation is audited.

**Acceptance Criteria:**

**Given** all v1.21 new mutating handlers land
**When** `go test ./internal/api/...` runs
**Then** `TestTierCoverage` and `TestAuditCoverage` both pass without allowlist additions (or any allowlist addition has a justification comment per `.claude/team/go-expert.md`).

**Given** the forbidden-content grep from `.claude/team/code-reviewer.md` runs against the v1.21 diff
**Then** zero matches are found for `scrdairy|merck|msd\.com|mahi-techlabs|merck-ahtl` or any real AWS account ID.

**Given** swagger regen runs
**Then** `docs/swagger/` is up-to-date and CI's stale-swagger check passes.

**Technical notes:**
- Dispatch `code-reviewer` + `security-auditor` once per epic landing into `design/v1.21-catalog`.
- Swagger regen: `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal`.

**Role file:** `.claude/team/code-reviewer.md` + `.claude/team/security-auditor.md`.
**Effort:** S.
**Dependencies:** all other epics.

---

## Epic V121-9: Documentation

Ship the user-facing and contributor-facing docs for v1.21.

### Story V121-9.1: `user-guide/marketplace.md`

As a **new Sharko user**,
I want a guide for the Marketplace modal (Browse / Search / Paste URL) and the Configure step,
So that I can add my first addon end-to-end.

**Acceptance Criteria:**

**Given** `docs/user-guide/marketplace.md`
**When** I follow the guide against a `make demo` instance
**Then** each numbered step produces the screenshot/outcome described (reviewer validates once during docs review).

**Given** the guide ends with "What happens after Submit"
**Then** it links to the existing deploy-flow guide (Operation 2) without duplicating it.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V121-2.x, V121-3.6, V121-4.1, V121-5.x.

---

### Story V121-9.2: `user-guide/smart-values.md`

As an **operator**,
I want to understand how the smart values layer splits global vs per-cluster, what the version-mismatch banner means, and how to opt out of AI annotation,
So that I can tune the behavior to my repo.

**Acceptance Criteria:**

**Given** `docs/user-guide/smart-values.md`
**Then** it documents: file header format, heuristic list (with a note it can be extended), LLM behavior + opt-out directive, version-mismatch banner, per-cluster template seeding.

**Given** a reader wants to disable AI annotation for one chart
**Then** the guide shows the exact `# sharko: ai-annotate=off` directive and where to place it.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V121-6.x, V121-7.x.

---

### Story V121-9.3: `operator-manual/supply-chain.md` + `developer-guide/contributing-catalog.md`

As a **platform engineer / contributor**,
I want supply-chain verification steps and a catalog contribution guide,
So that I can verify Sharko's signatures and submit new addons.

**Acceptance Criteria:**

**Given** `docs/operator-manual/supply-chain.md`
**Then** it includes copy-paste `cosign verify` commands for image + binaries + catalog asset, plus the SBOM download path.

**Given** `docs/developer-guide/contributing-catalog.md`
**Then** it covers: schema fields (§4.2), curated_by criteria, license allow-list, quarterly refresh PR cadence, `deprecated` + `superseded_by` pattern, CI gates.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V121-8.1, V121-8.2, V121-8.3.

---

### Story V121-9.4: CHANGELOG + release notes for v1.21.0

As a **release consumer**,
I want a single release-notes entry summarizing v1.21,
So that I know what changed at a glance.

**Acceptance Criteria:**

**Given** the bundle is ready to cut
**When** the release workflow runs
**Then** `CHANGELOG.md` (or equivalent release-notes mechanism used by the release workflow) has a `v1.21.0` section listing: new Marketplace modal, ArtifactHub search proxy, smart values layer, OpenSSF Scorecard integration, cosign-signed releases, catalog-validate CI, removal of the always-visible "Pull upstream defaults" button.

**Role file:** `.claude/team/docs-writer.md` + `.claude/team/devops-agent.md`.
**Effort:** S.
**Dependencies:** all prior epics.

---

## Sequencing / Dependency Graph

High-level order on the `design/v1.21-catalog` branch (bundle, not per-release):

```
V121-1 Catalog Foundation
   │
   ├──► V121-2 Marketplace UI — Browse + Configure
   │        │
   │        └──► V121-5 Add Flow — Tier 2 PR (reuses v1.20 tiered_git.go)
   │
   ├──► V121-3 ArtifactHub Proxy + Search Tab
   │        │
   │        └──► V121-4 Paste URL (reuses versions endpoint)
   │
   └──► V121-6 Smart Values Layer + Version-Mismatch Banner
            │
            └──► V121-7 AI Annotate + Secret-Leak Guard

V121-8 Release Hardening + CI + A11y + Coverage  (runs continuously and gates merge)
V121-9 Documentation                              (runs with each epic and ties together at release)
```

**Critical dependency:** V121-1 must land first — everything reads from the embedded catalog and typed API. V121-6 (smart values) and V121-2 (Browse UI) can proceed in parallel once V121-1's API surface is stable. V121-5 (Submit) cannot land until V121-2.3 (Configure step) exists, and it strictly **reuses** `internal/api/tiered_git.go` + `orchestrator.AddAddon` — do not fork a new mutation path.

**Parallelization opportunities** (per `tech-lead.md` parallel-execution rules, if worktrees are used):
- V121-2 (Browse UI) || V121-6 (smart values backend) — different files, different agents.
- V121-3 (ArtifactHub) || V121-7 (AI guard) — different files.

---

## Quality Gates per Story

Per `.claude/team/tech-lead.md` CHECK section + lean-workflow expectations, every story's agent must pass these before declaring DONE:

**Backend (Go):**
- `go build ./...`
- `go vet ./...`
- `go test ./...` (`-race` optional)
- If any `@Router` changed: `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` and commit the regenerated `docs/swagger/`.
- If any new mutating handler: `TestTierCoverage` + `TestAuditCoverage` must pass without unjustified allowlist additions.

**Frontend:**
- `cd ui && npm run build`
- `cd ui && npm test -- --run`
- No `text-gray-*` / `bg-gray-*` / `border-gray-*` without a `dark:` prefix (light-mode palette rule, `frontend-expert.md` + `code-reviewer.md`).
- Card borders use `ring-2 ring-[#6aade0]`, not `border-*` (`frontend-expert.md`).

**Catalog / CI:**
- Any change in `catalog/**`: `catalog-validate` CI must pass.

**Cross-cutting:**
- Forbidden-content grep: zero matches for `scrdairy|merck|msd\.com|mahi-techlabs|merck-ahtl` or real AWS account IDs.
- Trust `go build` + `npm run build` over LSP (`feedback_lean_workflow.md` rule 4).
- Dispatch `test-engineer` + `devops-agent` + `docs-writer` per epic (`feedback_always_involve_test_devops_docs.md`).
- Dispatch `code-reviewer` + `security-auditor` at epic completion into the bundle branch; for V121-7 (AI guard) also dispatch `security-auditor` at story completion.

---

## Out of Scope (v1.21 — explicitly deferred)

From design §9, the following are **not** in v1.21 and should not surface in story scope creep:

- Third-party / private catalog repositories — single curated catalog in main repo for v1.21.
- Automated source scanning (upstream `values.yaml` for CVEs / leaked credentials) at catalog-maintenance time — manual review for v1.21.
- Non-Helm addons (raw manifests, Kustomize, OLM operators) — Helm-only for v1.21.
- Per-entry cosign signatures on catalog entries — release-level only (§6 item 7).
- WCAG AA retrofit on existing pages (Dashboard, ClusterDetail, AddonDetail, Settings) — v1.22 backlog.
- Bundle / composition flows ("install prometheus + grafana + loki as a stack") — v1.22 candidate.
- Telemetry-backed popularity ranking from real Sharko installs — requires telemetry design.
- Webhook from ArtifactHub on new chart versions — V3.
- Fine-grained RBAC on "which catalog entries a user can see" — V2.x scoped RBAC roadmap.
- Direct install from catalog (no PR) — explicitly rejected; breaks Sharko's mutation model.
- Any change to Operation 2 (deploy flow) beyond the additive per-cluster stanza seeding in Story V121-6.6.

---

**End of v1.21 epic breakdown.**
