---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - docs/design/2026-04-20-v1.23-catalog-extensibility.md
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v1.23'
user_name: 'Moran'
date: '2026-04-20'
---

# Sharko v1.23 — Catalog Extensibility — Epic Breakdown

## Overview

This document decomposes the v1.23 design doc (`docs/design/2026-04-20-v1.23-catalog-extensibility.md`) into implementable epics and stories. v1.23 opens three extensibility paths on top of v1.21's embedded catalog:

1. **Third-party private catalogs** — `SHARKO_CATALOG_URLS` env + optional Settings editor, fetched / validated / merged under the embedded catalog.
2. **Per-entry cosign signing** — schema v1.1 `signature:` field, load-time verification, trust policy, release-pipeline signing.
3. **Trusted-source scanning bot** — nightly GitHub Action scans CNCF Landscape + AWS EKS Blueprints and opens PRs against `catalog/addons.yaml`.

**Scope frame (from design §2, §3, §4):** all three subsystems are opt-in. The embedded catalog remains the baseline. No breaking changes.

**Lean-workflow expectations** (`feedback_lean_workflow.md`, `feedback_always_involve_test_devops_docs.md`):
- One agent per bundle; the agent brief is complete when writing code.
- Every epic involves **test-engineer**, **devops-agent**, **docs-writer** alongside implementation agents.
- No release per fix — bundle on `design/v1.23-extensibility`; cut `v1.23.0` only at the milestone.

**Open questions** (design §7) to resolve during execution — do not defer, assign each to its story:
1. `SHARKO_CATALOG_URLS` persistence (env-only vs ConfigMap + Settings editor) — Story V123-1.1 decision.
2. Per-entry signature refresh cadence (every reload vs on fetch) — Story V123-2.2 decision.
3. Scanning bot PR target (draft-to-main vs `catalog-updates` branch) — Story V123-3.4 decision.

---

## Requirements Inventory

### Functional Requirements

**FR-V123-1** — Support `SHARKO_CATALOG_URLS` env var (comma-separated HTTPS URLs) listing third-party catalog sources. (Design §2.1)

**FR-V123-2** — On startup and every 1 h (configurable via `SHARKO_CATALOG_REFRESH_INTERVAL`), fetch each configured URL, validate the YAML against `catalog/schema.json` (v1.1), and merge under the embedded catalog. (§2.2)

**FR-V123-3** — Merge conflict rule: when a third-party entry's `name` collides with an embedded entry, embedded wins. The third-party tile in Browse surfaces an "overridden by internal curation" note. (§2.2)

**FR-V123-4** — Each in-memory catalog entry carries a `source` field (`embedded` or `<URL>`). Browse tiles and the detail page render a source badge. (§2.3)

**FR-V123-5** — Third-party fetch failure is non-fatal: Sharko keeps the last-successful snapshot per URL and surfaces status in the sources API. (§2.2, §2.5)

**FR-V123-6** — Expose `GET /api/v1/catalog/sources` returning `[{url, status: ok|stale|failed, last_fetched, entry_count, verified}]` — includes `embedded` with status always `ok`. (§2.6)

**FR-V123-7** — Expose `POST /api/v1/catalog/sources/refresh` (Tier 2, audited) forcing immediate refresh of all sources. (§2.6)

**FR-V123-8** — Bump catalog schema to v1.1: add optional `signature:` field on each entry. v1.0 catalogs continue to load (unknown-field tolerance). (§3.2, §5)

**FR-V123-9** — When a catalog entry has a `signature.bundle` URL, fetch the Sigstore bundle and verify it covers the entry's canonical serialization at load time. (§3.3)

**FR-V123-10** — Extract the signing OIDC identity from the verified bundle; match against the trust policy; set the in-memory `verified` value to one of: `true`, `false` (unsigned), `"signature-mismatch"`, `"untrusted-identity"`. (§3.3)

**FR-V123-11** — Honor `SHARKO_CATALOG_TRUSTED_IDENTITIES` (comma-separated regex list) with a secure default covering CNCF workflow identities + Sharko's own release workflow. (§3.4)

**FR-V123-12** — UI: render a "Verified — `<issuer>`" chip on verified entries, a neutral "Unsigned" chip on unsigned, a warning chip on mismatch/untrusted. Provide a `signed` pseudo-filter in Browse. (§3.5)

**FR-V123-13** — Release pipeline: on tag push, a new GitHub Action step signs every embedded catalog entry (cosign keyless via GitHub OIDC) and materializes the signatures into `catalog/addons.yaml` in the release commit. (§3.6)

**FR-V123-14** — `scripts/catalog-scan.mjs` skeleton: Node ESM, loads the current `catalog/addons.yaml`, has per-source plugin shape. (§4.4)

**FR-V123-15** — CNCF Landscape scanner plugin: fetches `https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml`, filters by category + maturity (graduated + incubating), normalizes to Sharko schema. (§4.2, §4.4)

**FR-V123-16** — AWS EKS Blueprints scanner plugin: enumerates `lib/addons/*` in `aws-quickstart/cdk-eks-blueprints`, extracts metadata. (§4.2, §4.4)

**FR-V123-17** — Scan script opens one PR per run with labels `catalog-scan` + `needs-review`. PR body is a markdown table per proposed change including pre-computed Scorecard, license, chart-resolvability signals. (§4.4, §4.6)

**FR-V123-18** — `.github/workflows/catalog-scan.yml` — cron `0 4 * * *` daily; uses `GITHUB_TOKEN`; caches upstream responses per run; no commit if no changes detected. (§4.3)

### NonFunctional Requirements

**NFR-V123-1** — Stateless principle preserved: third-party catalog bytes and signature verifications are **in-memory only**; no disk / ConfigMap / DB persistence. A restarted pod re-fetches on startup. (Design §2.7, inherits NFR-V121-1)

**NFR-V123-2** — Third-party fetch respects standard proxy env vars (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`). (§2.2)

**NFR-V123-3** — Fetch and verify are non-fatal: a broken URL or failed signature verification never brings Sharko down or drops the embedded catalog. (§2.2, §2.5)

**NFR-V123-4** — All new `/api/v1/catalog/*` endpoints have complete swaggo annotations; `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` regen lands with the same PR. (CLAUDE.md Code Rules)

**NFR-V123-5** — `POST /api/v1/catalog/sources/refresh` classifies as Tier 2 in `internal/api/pattern_tier.go`; audit event name `catalog_sources_refreshed`; `TestTierCoverage` + `TestAuditCoverage` pass.

**NFR-V123-6** — Cosign verification uses the existing cosign Go library path (`github.com/sigstore/cosign/v2/pkg/cosign` or equivalent); no shelling out to the `cosign` CLI. (§3.3)

**NFR-V123-7** — The scanning bot must never auto-merge: the workflow only opens PRs; merge is always a human action. (§4.1)

**NFR-V123-8** — Rate-limit safety: the bot uses `GITHUB_TOKEN` for GitHub API; caches upstream responses per-run; exponential backoff on 429 / 5xx. (§4.5)

**NFR-V123-9** — Quality gates pass on every commit: `go build ./...`, `go vet ./...`, `go test ./...`, `cd ui && npm run build`, `cd ui && npm test -- --run`. No `--no-verify`; no hook skipping.

**NFR-V123-10** — Forbidden-content policy continues to hold (`CLAUDE.md`).

**NFR-V123-11** — Catalog schema v1.1 is backward-compatible: v1.21 / v1.22 Sharko binaries reading a v1.1 YAML keep loading (unknown-field tolerance). v1.23 reading a v1.0 YAML also works (signature is optional).

### UX Design Requirements

**UX-DR-V123-1** — Source badge on every Browse tile: `Internal` / `<URL-host>` / `Embedded`. Tooltip shows full source URL + last-fetch status. (Design §2.3)

**UX-DR-V123-2** — Detail page shows the full source + fetch status for the entry.

**UX-DR-V123-3** — Verified chip styling: green checkmark for `verified: true`; neutral for `false`; warning for mismatch/untrusted. Issuer identity on hover. (§3.5)

**UX-DR-V123-4** — `signed` pseudo-filter in Browse tab `curated_by` multi-select. (§3.5)

**UX-DR-V123-5** — Optional Settings → Catalog Sources editor (open question §7.1): list editor mirroring `SHARKO_CATALOG_URLS`, with add / remove / test-fetch actions. If decision is env-only (leaning), the Settings page shows a read-only view. (§2.1)

### Additional Requirements (Architecture / Infrastructure)

- New Go package dir: `internal/catalog/sources/` — fetcher, merger, status store (all in-memory).
- New Go package dir: `internal/catalog/signing/` — cosign verification wrapper, trust policy.
- Extend `catalog/schema.json` → v1.1 with optional `signature:` object. Loader (`internal/catalog/loader.go`) tolerates v1.0.
- Extend `internal/catalog/loader.go` to expose `source` + `verified` fields on loaded entries.
- New `.github/workflows/catalog-scan.yml` — scheduled workflow.
- New `scripts/catalog-scan.mjs` — Node ESM, no new deps beyond what's in `ui/package.json` or a standalone `scripts/package.json`.
- Extend `.github/workflows/release.yml` — add a job that signs embedded catalog entries post-build.

---

## Requirements Coverage Map

| Req | Covered by |
|---|---|
| FR-V123-1 | V123-1 Story 1.1 |
| FR-V123-2 | V123-1 Story 1.2 |
| FR-V123-3 | V123-1 Story 1.3 |
| FR-V123-4 | V123-1 Story 1.4 |
| FR-V123-5 | V123-1 Story 1.2 |
| FR-V123-6 | V123-1 Story 1.5 |
| FR-V123-7 | V123-1 Story 1.6 |
| FR-V123-8 | V123-2 Story 2.1 |
| FR-V123-9 | V123-2 Story 2.2 |
| FR-V123-10 | V123-2 Story 2.2 |
| FR-V123-11 | V123-2 Story 2.3 |
| FR-V123-12 | V123-2 Story 2.4 |
| FR-V123-13 | V123-2 Story 2.5 |
| FR-V123-14 | V123-3 Story 3.1 |
| FR-V123-15 | V123-3 Story 3.2 |
| FR-V123-16 | V123-3 Story 3.3 |
| FR-V123-17 | V123-3 Story 3.4 |
| FR-V123-18 | V123-3 Story 3.4 |
| NFR-V123-1 | V123-1 Story 1.1, 1.2 |
| NFR-V123-2 | V123-1 Story 1.2 |
| NFR-V123-3 | V123-1 Story 1.2; V123-2 Story 2.2 |
| NFR-V123-4 | Per-story quality gate (V123-1.5, V123-1.6) |
| NFR-V123-5 | V123-1 Story 1.6 |
| NFR-V123-6 | V123-2 Story 2.2 |
| NFR-V123-7 | V123-3 Story 3.4 |
| NFR-V123-8 | V123-3 Story 3.4 |
| NFR-V123-9 | Per-story |
| NFR-V123-10 | Per-epic code-review pass |
| NFR-V123-11 | V123-2 Story 2.1 |
| UX-DR-V123-1 | V123-1 Story 1.7 |
| UX-DR-V123-2 | V123-1 Story 1.7 |
| UX-DR-V123-3 | V123-2 Story 2.4 |
| UX-DR-V123-4 | V123-2 Story 2.4 |
| UX-DR-V123-5 | V123-1 Story 1.8 |

No requirement is uncovered.

---

## Epic List

### Epic V123-1: Third-party private catalogs
**Goal:** Ship the `SHARKO_CATALOG_URLS` override — fetch, validate, merge, source-attribute, and expose status via `GET /api/v1/catalog/sources`. UI surfaces source badges on Browse tiles + detail.
**FRs:** FR-V123-1..7. **NFRs:** NFR-V123-1, 2, 3, 5.
**Rationale:** Ship Subsystem A first — it's the largest user-visible surface and V123-2's verified-badge UI plugs into tiles rendered here.

### Epic V123-2: Per-entry cosign signing
**Goal:** Schema v1.1 `signature:` field, load-time verification via cosign library, trust policy, UI verified badge, release pipeline signs embedded entries.
**FRs:** FR-V123-8..13. **NFRs:** NFR-V123-6, 11.
**Rationale:** Layered on Epic 1's source attribution. Independent of Epic 3.

### Epic V123-3: Trusted-source scanning bot
**Goal:** `scripts/catalog-scan.mjs` + `.github/workflows/catalog-scan.yml` + CNCF Landscape + EKS Blueprints scanners + PR-opening logic with rich body.
**FRs:** FR-V123-14..18. **NFRs:** NFR-V123-7, 8.
**Rationale:** Independent of Epics 1 + 2 — runs entirely in CI. Can proceed in parallel once Epic 1's schema + loader are stable.

### Epic V123-4: Documentation + release cut
**Goal:** User + operator + developer docs for v1.23; CHANGELOG entry; single PR `design/v1.23-extensibility → main`; tag `v1.23.0`.
**Rationale:** Final epic — ties together docs from the prior three and gates the release.

---

## Epic V123-1: Third-party private catalogs

Ship the fetch / validate / merge / source-attribute pipeline and expose it via the sources API + UI.

### Story V123-1.1: Resolve open question §7.1 + wire `SHARKO_CATALOG_URLS` env

As a **Sharko operator**,
I want to configure third-party catalog URLs via an environment variable,
So that I can extend the catalog without forking Sharko.

**Acceptance Criteria:**

**Given** the open question §7.1 is resolved (env-only vs ConfigMap persistence)
**When** the decision lands
**Then** it is recorded in the design doc and this story updated. Leaning env-only for v1.23.

**Given** `SHARKO_CATALOG_URLS=https://a.example.com/cat.yaml,https://b.example.com/cat.yaml`
**When** Sharko starts
**Then** the env is parsed into a list of URLs and made available to the loader.

**Given** `SHARKO_CATALOG_REFRESH_INTERVAL=30m`
**When** Sharko starts
**Then** the refresh cadence overrides the 1 h default.

**Given** the env is unset or empty
**When** Sharko starts
**Then** only the embedded catalog is loaded; no fetch loop runs.

**Technical notes:**
- File: `internal/config/catalog_sources.go` — parser + validator (HTTPS-only enforcement).
- Reject non-HTTPS URLs with a startup error.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/architect.md` (the env-vs-ConfigMap decision is an interface-design trade-off).
**Effort:** S.
**Dependencies:** none.

---

### Story V123-1.2: Fetch loop + YAML validation + last-successful snapshot

As the **catalog subsystem**,
I want a resilient fetch loop that pulls configured URLs on startup + on a cadence, validates each against the schema, and keeps a last-successful snapshot on failure,
So that a broken third-party URL never drops the catalog.

**Acceptance Criteria:**

**Given** a configured URL returns valid YAML matching `catalog/schema.json` v1.1
**When** the fetch runs
**Then** the entries are parsed and stored in memory as the current snapshot for that URL with status `ok`.

**Given** a configured URL returns 5xx or times out
**When** the fetch runs
**Then** the previous successful snapshot is retained, status is set to `stale`, and a non-fatal error is logged.

**Given** a URL returns YAML that fails schema validation
**When** the fetch runs
**Then** the prior snapshot is retained, status is set to `failed`, and the validation error is stored for the `GET /api/v1/catalog/sources` status payload.

**Given** `HTTPS_PROXY` is set
**When** a fetch runs
**Then** the HTTP client respects the proxy (standard Go `net/http.ProxyFromEnvironment`).

**Given** a fresh start with no successful fetches yet
**When** the API is asked for the source's entries
**Then** the source contributes zero entries (status `failed`); the embedded catalog still serves.

**Technical notes:**
- File: `internal/catalog/sources/fetcher.go`. Uses `internal/catalog/loader.go` for schema validation (shared validator).
- In-memory only (NFR-V123-1). No disk cache.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** L.
**Dependencies:** V123-1.1.

---

### Story V123-1.3: Merge under embedded catalog + conflict rule

As the **catalog subsystem**,
I want to merge third-party snapshots under the embedded catalog with the "embedded wins on name conflict" rule,
So that internal curation always overrides external sources.

**Acceptance Criteria:**

**Given** the embedded catalog has an entry `name: cert-manager`
**When** a third-party source also provides `name: cert-manager`
**Then** the embedded entry is the one exposed via search / list / detail.

**Given** the conflict happens
**When** the Browse tile renders the third-party version (it shouldn't — embedded wins — but if the override is relaxed in future, the note is already in place)
**Then** the in-memory record of the third-party entry marks it as `overridden: true` so the UI can surface an explanation if ever shown.

**Given** two third-party sources both define `name: internal-foo`
**When** the loader merges
**Then** alphabetical-by-URL resolution picks one, and a `conflict` note is surfaced in the sources API status.

**Technical notes:**
- File: `internal/catalog/sources/merger.go`. Pure function — takes embedded + list of snapshots, returns the effective in-memory index.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** M.
**Dependencies:** V123-1.2.

---

### Story V123-1.4: Source attribution on in-memory entries

As an **API consumer**,
I want every catalog entry to carry its source,
So that the UI can render a source badge and the audit trail is clear.

**Acceptance Criteria:**

**Given** the merged index
**When** an entry is read via `catalog.Entry`
**Then** it exposes a `source` field set to `embedded` or the full source URL.

**Given** the `GET /api/v1/catalog/addons/<name>` response
**When** rendered
**Then** the response JSON includes `source: "embedded"` or `source: "https://..."`.

**Given** the schema struct
**When** examined
**Then** `source` is a computed field (not persisted in YAML — derived from which snapshot contributed the entry).

**Technical notes:**
- File: `internal/catalog/loader.go` (expose `Source()` on the entry) + `internal/api/catalog.go` (include in response).
- Regen swagger on this story.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/docs-writer.md` (swagger).
**Effort:** S.
**Dependencies:** V123-1.3.

---

### Story V123-1.5: `GET /api/v1/catalog/sources` endpoint + swagger

As an **API consumer**,
I want a read endpoint listing every catalog source with its fetch status,
So that the UI (and monitoring) can surface health.

**Acceptance Criteria:**

**Given** `GET /api/v1/catalog/sources`
**When** called
**Then** returns `[{url: "embedded", status: "ok", last_fetched: null, entry_count: N, verified: true}, {url: "https://...", status: "ok|stale|failed", last_fetched: <timestamp>, entry_count: N, verified: false}]`.

**Given** a source has no successful fetch ever
**Then** `entry_count: 0` and `status: "failed"`.

**Given** swaggo annotations land
**When** `swag init` regen runs
**Then** `docs/swagger/swagger.json` contains the endpoint.

**Technical notes:**
- File: `internal/api/catalog_sources.go`.
- Read-only — no audit / tier.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** V123-1.4.

---

### Story V123-1.6: `POST /api/v1/catalog/sources/refresh` — Tier 2 force refresh

As an **admin**,
I want to force-refresh all catalog sources without waiting for the next cadence tick,
So that I can test a newly added URL immediately.

**Acceptance Criteria:**

**Given** `POST /api/v1/catalog/sources/refresh`
**When** called
**Then** every configured source is re-fetched synchronously; the response is the same shape as `GET /api/v1/catalog/sources` after the refresh completes.

**Given** the handler is registered
**When** `TestTierCoverage` runs
**Then** the route is classified as Tier 2 via `internal/api/pattern_tier.go`.

**Given** the handler emits audit
**When** `TestAuditCoverage` runs
**Then** the event name is `catalog_sources_refreshed` with detail `{urls: [...], status: {url: status}}`.

**Given** swagger regen runs
**Then** the endpoint appears with full annotations.

**Technical notes:**
- File: `internal/api/catalog_sources_refresh.go` + tier + audit wiring.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** V123-1.5.

---

### Story V123-1.7: UI — source badge on Browse tiles + detail page

As an **operator**,
I want a source badge on every tile and on the addon detail page,
So that I can tell which entries are Sharko-curated and which come from my org's private catalog.

**Acceptance Criteria:**

**Given** a Browse tile renders an embedded entry
**Then** a small "Internal" badge appears (Sharko-curated); hover tooltip: "Source: Sharko embedded catalog".

**Given** a Browse tile renders a third-party entry
**Then** the badge shows the URL host (e.g., "catalogs.example.com"); hover tooltip shows the full URL and last-fetched timestamp from the sources API.

**Given** the addon detail page
**When** the entry is third-party
**Then** a "Source" section shows full URL + fetch status + last-fetched timestamp.

**Given** the theme rules
**Then** the badge uses the Sharko blue-tinted palette (`frontend-expert.md` rules); no gray utilities.

**Technical notes:**
- Files: `ui/src/components/SourceBadge.tsx` (new), consumed by `ui/src/components/MarketplaceBrowseTab.tsx` and `ui/src/views/AddonDetail.tsx`.
- Fetch sources via `ui/src/services/api.ts.listCatalogSources()` (new method).

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V123-1.5.

---

### Story V123-1.8: UI — optional Settings → Catalog Sources view (read-only if env-only)

As an **admin**,
I want to see configured catalog sources in Settings,
So that I can verify what the env has set without SSHing to the pod.

**Acceptance Criteria:**

**Given** the decision from Story V123-1.1 is env-only
**When** Settings is rendered
**Then** a new "Catalog Sources" section shows a read-only list from `GET /api/v1/catalog/sources` with status chips + refresh button (calling `POST /api/v1/catalog/sources/refresh`).

**Given** the decision is ConfigMap-editable (not leaning)
**When** Settings is rendered
**Then** the section is a full editor — add / remove / test-fetch actions persisting to the ConfigMap. (Implement only if V123-1.1 chooses this.)

**Given** the a11y bar from V122-1 is retained
**When** the section is added
**Then** axe CI passes — no serious/critical violations.

**Technical notes:**
- File: `ui/src/views/Settings.tsx` (extend).

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V123-1.1 (decision), V123-1.5 + V123-1.6.

---

### Story V123-1.9: Tests — unit + integration for fetch/merge/source attribution

As the **quality pipeline**,
I want unit tests for each new Go package + an integration test that spins up a local HTTPS server serving a third-party YAML and verifies the full loop,
So that third-party catalogs cannot regress silently.

**Acceptance Criteria:**

**Given** `internal/catalog/sources/fetcher_test.go`
**Then** unit cases cover: happy fetch, 5xx, timeout, invalid YAML, schema violation, proxy env respected.

**Given** `internal/catalog/sources/merger_test.go`
**Then** unit cases cover: embedded wins on conflict, alphabetical resolution on third-party-vs-third-party conflict, source attribution on every entry.

**Given** an integration test under `internal/catalog/sources/integration_test.go`
**Then** it spins up `httptest.NewTLSServer`, serves a multi-entry YAML, configures Sharko with that URL, and asserts entries appear in the merged index with correct source.

**Role file:** `.claude/team/test-engineer.md`.
**Effort:** M.
**Dependencies:** V123-1.4.

---

## Epic V123-2: Per-entry cosign signing

Schema v1.1 + load-time verification + trust policy + UI badge + release-pipeline signing.

### Story V123-2.1: Schema v1.1 — add optional `signature:` field

As the **catalog subsystem**,
I want the schema to accept an optional `signature:` object on each entry without breaking v1.0 catalogs,
So that verified and unverified entries coexist.

**Acceptance Criteria:**

**Given** `catalog/schema.json` is updated to schema version v1.1
**Then** each entry accepts optional `signature: {bundle: <URL>}`.

**Given** a catalog missing the `signature` field
**When** loaded
**Then** the entry is accepted (backward-compatible).

**Given** a catalog with `signature: {bundle: "not-a-url"}`
**When** loaded
**Then** the loader rejects the entry with a clear error naming the offending entry.

**Given** a v1.0 schema catalog
**When** loaded by v1.23
**Then** it loads without warning.

**Given** a v1.1 schema catalog
**When** loaded by v1.21 (older Sharko)
**Then** the `signature` field is tolerated as "unknown field" per v1.21 §4.2; no runtime error.

**Technical notes:**
- Files: `catalog/schema.json` (bump + field), `internal/catalog/loader.go` (struct field).

**Role file:** `.claude/team/architect.md` + `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** none.

---

### Story V123-2.2: Load-time verification via cosign library + open question §7.2

As the **catalog loader**,
I want to fetch + verify each entry's Sigstore bundle using the cosign Go library and annotate the in-memory entry with the `verified` outcome,
So that third-party catalogs can prove provenance at ingest.

**Acceptance Criteria:**

**Given** the open question §7.2 resolves (re-verify on every reload vs only on fetch)
**Then** the decision lands here; leaning "on fetch" — re-verify only when the catalog YAML bytes change.

**Given** an entry with a valid `signature.bundle` URL
**When** the loader runs
**Then** the bundle is fetched, verified against the entry's canonical serialization, and the in-memory entry gets `verified: true` + `signature_identity: <OIDC issuer/subject>`.

**Given** an entry with a mismatched bundle
**Then** `verified: "signature-mismatch"`.

**Given** an entry whose identity doesn't match the trust policy
**Then** `verified: "untrusted-identity"`.

**Given** an entry with no signature
**Then** `verified: false` (unsigned); the entry is still accepted.

**Given** the cosign library is the mechanism
**Then** no shelling out to the `cosign` CLI (NFR-V123-6).

**Technical notes:**
- File: `internal/catalog/signing/verify.go`. Uses `github.com/sigstore/cosign/v2/pkg/cosign` (resolve current API via context7 MCP at implementation time).
- Canonical serialization: deterministic YAML marshal of the entry (Go's `yaml.v3` with stable map ordering) is the message signed. Document this in the developer-guide doc.

**Role file:** `.claude/team/go-expert.md` + `.claude/team/security-auditor.md` (signing is security-sensitive).
**Effort:** L.
**Dependencies:** V123-2.1.

---

### Story V123-2.3: Trust policy via `SHARKO_CATALOG_TRUSTED_IDENTITIES`

As a **Sharko operator**,
I want to configure which Sigstore signing identities are trusted,
So that I can pin trust to CNCF workflows, my own org, or a specific publisher.

**Acceptance Criteria:**

**Given** `SHARKO_CATALOG_TRUSTED_IDENTITIES` is unset
**When** the verifier runs
**Then** it uses the defaults: `https://github.com/cncf/.*/.github/workflows/.*`, `https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/tags/v.*`.

**Given** the env var is set to `<defaults>,https://github.com/myorg/.*/.github/workflows/.*`
**When** the verifier runs
**Then** all three regexes are active (defaults + custom).

**Given** an entry signed by `https://github.com/evil-org/foo/.github/workflows/release.yml@refs/tags/v1.0.0`
**When** no regex matches
**Then** `verified: "untrusted-identity"`.

**Technical notes:**
- File: `internal/catalog/signing/trust.go`. Regex list loaded at startup + re-loaded on config refresh.

**Role file:** `.claude/team/go-expert.md`.
**Effort:** S.
**Dependencies:** V123-2.2.

---

### Story V123-2.4: UI — Verified badge + `signed` pseudo-filter

As an **operator**,
I want tiles to show a verified / unsigned / mismatch chip and a way to filter to signed entries only,
So that I can quickly narrow to trusted content.

**Acceptance Criteria:**

**Given** `verified: true`
**Then** a green "Verified — `<issuer>`" chip appears on the tile; issuer identity in tooltip.

**Given** `verified: false`
**Then** a neutral "Unsigned" chip appears.

**Given** `verified: "signature-mismatch"` or `"untrusted-identity"`
**Then** a warning chip appears with a tooltip explaining the state and linking to the operator doc on signature verification.

**Given** the Browse tab's `curated_by` multi-select
**When** I select the new `signed` pseudo-filter
**Then** only entries with `verified: true` are shown.

**Given** a11y
**Then** chip contrast meets WCAG 2.1 AA (same bar V122-1 set) — the axe suite picks it up.

**Technical notes:**
- File: `ui/src/components/VerifiedBadge.tsx` (new), extend `ui/src/components/MarketplaceBrowseTab.tsx`.

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** V123-2.2.

---

### Story V123-2.5: Release pipeline — sign embedded catalog entries

As the **Sharko maintainer**,
I want the release workflow to sign every embedded catalog entry using keyless OIDC,
So that downstream consumers of our catalog can verify provenance.

**Acceptance Criteria:**

**Given** `.github/workflows/release.yml` is updated with a new `sign-catalog-entries` job running after the build
**When** a tag is pushed
**Then** the job iterates every entry in `catalog/addons.yaml`, signs each using cosign keyless (GitHub OIDC), and materializes the signatures into the same YAML + attaches them as `catalog-signatures/` release assets.

**Given** the materialized entries
**When** the release commit is cut
**Then** `catalog/addons.yaml` in the release has `signature: {bundle: <release-asset-URL>}` on every entry.

**Given** a v1.23 Sharko loading the released binary
**Then** embedded entries load `verified: true` against the default trust policy (matching the Sharko release workflow identity).

**Given** a v1.21 / v1.22 Sharko reading the v1.23 release binary's embedded catalog
**Then** the `signature` field is tolerated as unknown; no runtime error.

**Technical notes:**
- File: `.github/workflows/release.yml` (new job). Uses `sigstore/cosign-installer@v3` + keyless OIDC already present for the image-signing step.
- Use context7 MCP for current `cosign sign-blob` flag syntax at implementation time.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** V123-2.1, V123-2.2.

---

### Story V123-2.6: Tests — verification happy / mismatch / unsigned / untrusted paths

As the **quality pipeline**,
I want unit + integration tests for each verification outcome,
So that signing logic cannot regress silently.

**Acceptance Criteria:**

**Given** test fixtures with known-good + known-mismatch + unsigned entries
**When** `go test ./internal/catalog/signing/...` runs
**Then** each outcome (`true`, `false`, `"signature-mismatch"`, `"untrusted-identity"`) is covered.

**Given** the trust-policy regex list
**When** unit tests run
**Then** table-driven cases cover: default regex hits, custom regex hits, no match.

**Role file:** `.claude/team/test-engineer.md` + `.claude/team/security-auditor.md`.
**Effort:** M.
**Dependencies:** V123-2.2, V123-2.3.

---

## Epic V123-3: Trusted-source scanning bot

Nightly GitHub Action scans upstream curated lists and opens PRs.

### Story V123-3.1: `scripts/catalog-scan.mjs` skeleton + plugin interface

As a **Sharko maintainer**,
I want a modular scanner script where each upstream source is a plugin,
So that new sources can be added without rewriting the bot.

**Acceptance Criteria:**

**Given** `scripts/catalog-scan.mjs` exists
**Then** it loads the current `catalog/addons.yaml`, iterates registered scanner plugins, diffs proposals, and emits a single changeset JSON for PR-opening logic.

**Given** the plugin interface
**Then** each plugin exports `{ name, fetch() → normalized[], }` and lives under `scripts/catalog-scan/plugins/`.

**Given** no plugins produce any diff
**When** the script runs
**Then** it exits 0 with a "no changes" log and does not open a PR.

**Given** the script runs locally
**When** `node scripts/catalog-scan.mjs --dry-run` is invoked
**Then** proposals print to stdout with no PR side effect.

**Technical notes:**
- ESM; no new runtime deps beyond what's already in `ui/package.json` (`node` built-ins + `yaml` parser).
- If dependencies are needed, add a standalone `scripts/package.json`.

**Role file:** `.claude/team/devops-agent.md` + `.claude/team/go-expert.md` (shape review for script↔Sharko schema compatibility).
**Effort:** M.
**Dependencies:** none.

---

### Story V123-3.2: CNCF Landscape scanner plugin

As the **scanning bot**,
I want a plugin that fetches the CNCF Landscape YAML and proposes adds/updates for graduated + incubating projects with Helm charts,
So that Sharko's curated set stays current with CNCF maturity signals.

**Acceptance Criteria:**

**Given** the plugin runs
**When** it fetches `https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml`
**Then** it filters by category (security, observability, networking, storage, autoscaling, gitops, database, backup, chaos, developer-tools) + maturity `graduated` or `incubating`.

**Given** a landscape item has no Helm chart reference
**Then** the plugin skips it (Helm-only per v1.21 §6 item 10).

**Given** a landscape item maps to an existing catalog entry
**When** the chart version or maintainer list has changed
**Then** the plugin proposes an update.

**Given** a landscape item doesn't exist in the catalog
**Then** the plugin proposes an add.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** V123-3.1.

---

### Story V123-3.3: AWS EKS Blueprints scanner plugin

As the **scanning bot**,
I want a plugin enumerating `lib/addons/*` in `aws-quickstart/cdk-eks-blueprints`,
So that AWS-curated addons are kept current.

**Acceptance Criteria:**

**Given** the plugin runs
**When** it queries the GitHub API for `aws-quickstart/cdk-eks-blueprints` contents under `lib/addons/`
**Then** it enumerates directories and extracts addon metadata from each (name, chart, repo).

**Given** an addon is new vs the catalog
**Then** the plugin proposes an add.

**Given** the plugin respects `GITHUB_TOKEN`
**Then** API calls authenticate; rate-limit headroom is logged.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** V123-3.1.

---

### Story V123-3.4: PR-opening logic + GitHub workflow + open question §7.3

As the **scanning bot**,
I want one PR per scan run with a rich body and appropriate labels,
So that reviewers can act on proposals quickly.

**Acceptance Criteria:**

**Given** the open question §7.3 is resolved (draft-to-main vs `catalog-updates` branch)
**When** the decision lands
**Then** it is recorded; leaning draft-to-main with the `catalog-scan` label.

**Given** a scan run produces a changeset
**When** the workflow's PR-opening step runs
**Then** a branch `catalog-scan/<YYYY-MM-DD>` is created, the catalog is updated, and a PR is opened with labels `catalog-scan` + `needs-review`.

**Given** the PR body
**Then** it contains a markdown table for each proposed change including: action (add/update), Scorecard pre-computed, license pre-checked (allow-list), chart resolvability (index.yaml check), and the upstream source that proposed it.

**Given** `.github/workflows/catalog-scan.yml`
**When** the cron `0 4 * * *` fires
**Then** the workflow runs the script and opens PRs.

**Given** the workflow never auto-merges (NFR-V123-7)
**Then** the `PULL_REQUESTS_AUTOMERGE` permission is not granted.

**Given** rate limits
**When** upstream returns 429 / 5xx
**Then** exponential backoff retries up to 3 times.

**Technical notes:**
- Files: `.github/workflows/catalog-scan.yml`, `scripts/catalog-scan/pr-open.mjs`.
- PR-open uses `gh pr create` with a HEREDOC body (same pattern as Sharko's other gh CLI usage).

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** V123-3.2, V123-3.3.

---

### Story V123-3.5: Runbook + docs for reviewers

As a **reviewer on a `catalog-scan` PR**,
I want a runbook describing what the bot is doing, how to read the PR body, and how to reject a proposed entry,
So that review is fast and consistent.

**Acceptance Criteria:**

**Given** `docs/site/developer-guide/catalog-scan-runbook.md` exists
**Then** it documents: cron schedule, sources scanned, signal fields in the PR body, how to close-without-merge vs edit-and-merge, how to add a new scanner plugin.

**Given** `mkdocs.yml` is updated
**Then** the runbook appears under Developer Guide.

**Given** `mkdocs build --strict` runs
**Then** no warnings.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** V123-3.4.

---

## Epic V123-4: Documentation + release cut

Ties together user, operator, developer docs for the three subsystems; cuts `v1.23.0`.

### Story V123-4.1: `user-guide/catalog-sources.md` + `user-guide/verified-signatures.md`

As a **Sharko user**,
I want user-facing docs for third-party catalogs and verified badges,
So that the features are discoverable.

**Acceptance Criteria:**

**Given** `docs/site/user-guide/catalog-sources.md` exists
**Then** it documents `SHARKO_CATALOG_URLS`, behavior under fetch failure, source-badge meaning, and the Settings read-only view.

**Given** `docs/site/user-guide/verified-signatures.md` exists
**Then** it documents what Verified / Unsigned / mismatch chips mean, how to inspect the issuer, and when to trust unsigned entries.

**Given** `mkdocs build --strict`
**Then** both pages render clean.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V123-1.7, V123-2.4.

---

### Story V123-4.2: `operator/catalog-trust-policy.md` + update `operator/supply-chain.md`

As an **operator**,
I want a doc explaining `SHARKO_CATALOG_TRUSTED_IDENTITIES`, the defaults, how to extend, and how to verify a signature outside Sharko,
So that I can audit catalog trust independently.

**Acceptance Criteria:**

**Given** `docs/site/operator/catalog-trust-policy.md` exists
**Then** it documents the env var, default regex list, `cosign verify-blob` commands for manual verification, and the decision record from open question §7.2.

**Given** `docs/site/operator/supply-chain.md` is updated
**Then** it adds a section on the new release-pipeline per-entry signing (pointing at the `catalog-signatures/` release assets).

**Role file:** `.claude/team/docs-writer.md` + `.claude/team/security-auditor.md`.
**Effort:** M.
**Dependencies:** V123-2.3, V123-2.5.

---

### Story V123-4.3: `developer-guide/catalog-scan-plugins.md` + update `contributing-catalog.md`

As a **contributor**,
I want a doc on writing a new scanner plugin + how the bot's PRs relate to the manual contribution flow,
So that I can extend the scanner and know when to open a manual PR.

**Acceptance Criteria:**

**Given** `docs/site/developer-guide/catalog-scan-plugins.md` exists
**Then** it documents the plugin interface, the test harness, and how to add a new plugin.

**Given** `docs/site/developer-guide/contributing-catalog.md` is updated
**Then** it adds a section on the bot: "if the change can come from CNCF Landscape / EKS Blueprints, expect a bot PR; if not, open a manual PR."

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V123-3.5.

---

### Story V123-4.4: `bmad-code-review` + `security-auditor` sweep

As the **tech lead**,
I want a fresh-context adversarial review + a dedicated security sweep (Subsystem B is security-sensitive),
So that v1.23 ships without signing regressions or forbidden content.

**Acceptance Criteria:**

**Given** Epics V123-1 / V123-2 / V123-3 are all `done` on `design/v1.23-extensibility`
**When** `bmad-code-review` runs
**Then** Blind Hunter / Edge Case Hunter / Acceptance Auditor produce findings; must-fix items are remediated before PR.

**Given** `security-auditor` is dispatched specifically for the signing path
**Then** the review covers canonical serialization, bundle fetch error paths, trust-policy regex injection risk, and the release-pipeline signing step.

**Given** forbidden-content grep runs
**Then** zero matches for `scrdairy|merck|msd\.com|mahi-techlabs|merck-ahtl` or real AWS account IDs.

**Role file:** `.claude/team/code-reviewer.md` + `.claude/team/security-auditor.md`.
**Effort:** M.
**Dependencies:** V123-1.9, V123-2.6, V123-3.5.

---

### Story V123-4.5: CHANGELOG + PR `design/v1.23-extensibility → main` + tag `v1.23.0`

As a **release engineer**,
I want one PR landing all three epics + docs + planning artifacts, then a `v1.23.0` tag,
So that the release follows the "bundle on a single working branch" rule.

**Acceptance Criteria:**

**Given** all V123 stories (except this one) are `done`
**When** the PR opens from `design/v1.23-extensibility` to `main`
**Then** the title describes v1.23 catalog extensibility as a single unit; the body lists each epic + delivered increments.

**Given** CI passes and the release workflow runs
**Then** `v1.23.0` is tagged; multi-arch signed images ship (inheriting v1.22's supply-chain posture); embedded catalog entries ship signed (V123-2.5).

**Given** the sprint-status.yaml is updated
**Then** every V123 epic is `done`.

**Role file:** `.claude/team/devops-agent.md` + `.claude/team/docs-writer.md` (CHANGELOG).
**Effort:** S.
**Dependencies:** V123-4.1, V123-4.2, V123-4.3, V123-4.4.

---

## Sequencing / Dependency Graph

```
V123-1 Third-party catalogs           ──┐
  1.1 env + open-q §7.1                 │
  1.2 fetch loop + validation           │
  1.3 merge + conflict                  │
  1.4 source attribution                │   (parallel with V123-2, V123-3)
  1.5 GET /sources                      │
  1.6 POST /sources/refresh             │
  1.7 UI source badge                   │
  1.8 Settings view                     │
  1.9 tests                             │
                                        │
V123-2 Per-entry cosign signing        ──┤
  2.1 schema v1.1                       │
  2.2 cosign verify + open-q §7.2       │   depends on V123-1.4 (source attribution shape)
  2.3 trust policy                      │
  2.4 UI verified badge                 │
  2.5 release pipeline signing          │
  2.6 tests                             │
                                        │
V123-3 Scanning bot                    ──┤
  3.1 script skeleton                   │
  3.2 CNCF Landscape                    │   independent of V123-1 + V123-2
  3.3 EKS Blueprints                    │
  3.4 PR-open + workflow + open-q §7.3  │
  3.5 runbook                           │
                                        │
V123-4 Docs + release                   ◄┘
  4.1 user docs
  4.2 operator docs
  4.3 developer docs
  4.4 code-review + security-auditor
  4.5 CHANGELOG + PR + tag v1.23.0
```

**Parallelization opportunities:**
- V123-1.2 (fetch loop) || V123-2.1 (schema bump) — disjoint files.
- V123-3 (scanning bot) runs entirely in CI — zero conflict with Epics 1 + 2.
- V123-2.5 (release pipeline) || V123-1.7 (UI badge) — disjoint files + disjoint skills.

---

## Quality Gates per Story

Per `.claude/team/tech-lead.md` CHECK + `feedback_lean_workflow.md`:

**Backend (Go):**
- `go build ./...`
- `go vet ./...`
- `go test ./...`
- Any `@Router` touched → `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` regen.
- New mutating handler → `TestTierCoverage` + `TestAuditCoverage` pass.

**Frontend:**
- `cd ui && npm run build`
- `cd ui && npm test -- --run`
- axe CI suite still green (V122-1 bar).
- No `text-gray-*` / `bg-gray-*` / `border-gray-*` without `dark:` prefix.

**CI / scripts:**
- `node scripts/catalog-scan.mjs --dry-run` runs clean on the main branch's catalog.
- `.github/workflows/catalog-scan.yml` syntax validated via `yamllint` or a dry run.

**Docs:**
- `mkdocs build --strict` passes on every docs PR.

**Cross-cutting:**
- Forbidden-content grep on every epic.
- `code-reviewer` + `security-auditor` at epic completion into `design/v1.23-extensibility`.

---

## Out of Scope (v1.23 — explicitly deferred to v1.24+)

From design §6:

- UI editor for building private catalogs (defer to v1.24+).
- Multi-source conflict resolution beyond "embedded wins" (defer — alphabetical is the v1.23 rule).
- GKE marketplace / Azure AKS scanning (defer to v1.24 — API auth complexity).
- Air-gap workflow for signature verification (defer — requires outbound Rekor).
- Automated rollback of bot-introduced entries (defer — manual revert PR).
- Fine-grained RBAC on "which third-party catalogs a user can see" (V2.x scoped RBAC roadmap).

---

**End of v1.23 epic breakdown.**
