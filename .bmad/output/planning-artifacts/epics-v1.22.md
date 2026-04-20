---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - (retroactive — no upstream design doc; this IS the record of v1.22)
  - .bmad/output/planning-artifacts/epics-v1.21.md (§ "Out of Scope" listed v1.22 carryovers)
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v1.22'
user_name: 'Moran'
date: '2026-04-20'
---

# Sharko v1.22 — Marketplace Polish — Epic Breakdown

## Overview

This document is a **retroactive** epic breakdown for v1.22. Three of four epics were already committed on `dev/v1.22` at the time BMAD enforcement was adopted (2026-04-20 — see commit `1a71f00 docs(bmad): mandatory enforcement`), and the planning artifacts are being filled in so the historical record matches the BMAD flow going forward. Epic V122-4 is still in-progress (partial work in `git stash@{0}`) and is written forward-looking.

**v1.22 shape.** v1.22 is the "marketplace polish" release. v1.21 shipped the Marketplace modal, smart values layer, and OpenSSF Scorecard integration; v1.22 closes the four carryover items flagged in v1.21's §9 "Out of scope" and in the project roadmap:

1. **WCAG 2.1 AA retrofit** on the v1.20 pages (v1.21 shipped a11y only on the new Marketplace pages).
2. **Multi-arch Docker image** (amd64 + arm64) so ARM K8s nodes (Graviton, Ampere) and arm64 dev machines can pull natively.
3. **Audit-log retention banner + operator docs** — explaining the stateless two-stream design so operators don't think audit entries are getting lost on pod restart.
4. **Docs screenshots + stub-page content fill** — the RTD site needed real screenshots of each UI surface and several stub pages needed to either be filled in or trimmed from nav.

**Retrospective framing.** Three epics (V122-1, V122-2, V122-3) are `done` — committed to `dev/v1.22` local branch, not yet pushed. Epic V122-4 is `in-progress` with partial work stashed. This doc captures exactly what was shipped (acceptance tests against the actual commits) plus the remaining V122-4 stories as forward-looking plans.

**Lean-workflow expectations** (from `feedback_lean_workflow.md`, `feedback_always_involve_test_devops_docs.md`, `feedback_release_cadence.md`):
- Bundle on `dev/v1.22`; cut `v1.22.0` only when all four epics are done.
- One agent per bundle; every epic explicitly involves **test-engineer**, **devops-agent**, and **docs-writer** alongside implementation agents.
- No intermediate releases — single PR `dev/v1.22 → main`, single tag.

**Source artifacts:**
- Committed work: `9fced6b` (V122-1), `078b389` (V122-3), `4124de8` (V122-2) on `dev/v1.22`
- Stashed work: `git stash@{0}` — agent-v1.22-partial: screenshots + playwright script + mock_git + ui/package.json
- v1.21 carryover list: `.bmad/output/planning-artifacts/epics-v1.21.md` §14 "Out of Scope"
- Roadmap: memory file `project_sharko_roadmap.md` v1.22 candidates

---

## Requirements Inventory

### Functional Requirements

**FR-V122-1** — All five pre-v1.21 top-level UI pages (AddonDetail, ClusterDetail, Settings, AuditViewer, Connections) pass an automated axe-core scan under the WCAG 2.1 AA rule set (wcag2a / wcag2aa / wcag21a / wcag21aa) with zero serious-or-critical violations. (Retroactive — matches commit `9fced6b`.)

**FR-V122-2** — The Sharko Docker image is published as a multi-arch manifest list covering `linux/amd64` and `linux/arm64` under each release tag. Pulling the image on an arm64 node selects the arm64 variant automatically. (Retroactive — matches commit `4124de8`.)

**FR-V122-3** — `cosign sign` is invoked once against the manifest digest, and the signature covers every per-arch image under the manifest. (Retroactive — matches commit `4124de8`.)

**FR-V122-4** — The Audit Log page renders a persistent info banner at the top explaining the two-stream audit architecture (in-memory ring buffer last-1000 + structured stdout for the cluster log pipeline) and linking to the new operator audit-log retention doc. (Retroactive — matches commit `078b389`.)

**FR-V122-5** — `docs/site/operator/audit-log.md` documents the stateless-by-design choice and provides copy-paste setups for Loki, Splunk, ELK, CloudWatch, and GCP Logging. Wired into `mkdocs.yml` under Operator Manual. (Retroactive — matches commit `078b389`.)

**FR-V122-6** — Playwright-based docs-screenshot scaffolding: `scripts/docs-screenshots.mjs` drives a real browser against a `make demo` instance and produces 5 canonical screenshots: dashboard, marketplace-browse, marketplace-detail, cluster-detail, audit-log. An `npm run docs:screenshots` task wires it into the UI package. (Partial — stashed; awaiting execution.)

**FR-V122-7** — Generated screenshots are committed under `docs/site/assets/screenshots/` and embedded into the relevant docs pages (landing page hero, user-guide/marketplace, user-guide/dashboard, operator/audit-log, user-guide/clusters). (Not started.)

**FR-V122-8** — Stub docs pages are either filled in with real content (`getting-started/installation.md`, `getting-started/first-run.md`, `user-guide/connections.md`, `user-guide/clusters.md`, `user-guide/addons.md`, `user-guide/marketplace.md`) or explicitly stubbed "Coming in v1.23" / trimmed from nav if deferred. (Not started.)

### NonFunctional Requirements

**NFR-V122-1** — Accessibility: zero serious-or-critical axe violations on the five pre-v1.21 pages under the WCAG 2.1 AA rule set. Measured by the new `a11y-v120-pages.test.tsx` suite; failures fail CI. (Retroactive.)

**NFR-V122-2** — Build cost budget: multi-arch builds add at most ~2× wall-time to the release workflow (qemu-based arm64 cross-compile on amd64 GitHub runners). Go binary stays native (CGO_ENABLED=0) — qemu cost is paid by the Alpine final-stage only. (Retroactive.)

**NFR-V122-3** — Supply chain: cosign signature verifies against the manifest list digest; per-arch verification works via `cosign verify ghcr.io/moranweissman/sharko:<tag>` with no change to the verify command documented in `operator/supply-chain.md`. (Retroactive.)

**NFR-V122-4** — Docs site stability: `mkdocs build --strict` passes after each epic lands. Screenshots are committed at reasonable file size (target <1 MB each, lossless PNG). (Forward-looking — V122-4.)

**NFR-V122-5** — Quality gates per commit: `go build ./...`, `go vet ./...`, `go test ./...`, `cd ui && npm run build`, `cd ui && npm test -- --run` all pass. No `--no-verify`; no hook skipping. (Cross-cutting — every story.)

**NFR-V122-6** — Forbidden-content policy continues to hold on every v1.22 diff. (Cross-cutting — `CLAUDE.md` Content Policy.)

**NFR-V122-7** — Every new @Router-touching endpoint (none expected in v1.22 — polish release) would trigger swagger regen per `CLAUDE.md` rule. Confirmed zero new endpoints in v1.22.

### UX Design Requirements

**UX-DR-V122-1** — AuditViewer filter form: every form input (the 6 inputs — Action / Source / Result selects plus User / Cluster / Since text inputs) has a visible label programmatically associated via `id` + `htmlFor`. (Retroactive — matches commit `9fced6b`; fixes the `select-name (critical)` axe violation.)

**UX-DR-V122-2** — AuditViewer page heading is an `<h1>`, not `<h2>`, so the route has exactly one h1 — heading-hierarchy nit fixed before the axe suite scan. (Retroactive — matches commit `078b389`.)

**UX-DR-V122-3** — Audit Log info banner: always-visible (info severity, not dismissible — the two-stream architecture is permanent, not resolvable); explains both streams; link to operator docs page. (Retroactive — matches commit `078b389`.)

**UX-DR-V122-4** — Docs landing page (`docs/site/index.md`) swaps its hero visual for a real dashboard screenshot once V122-4.3 lands. Marketplace / Dashboard / Clusters / Audit Log user-guide pages each embed at least one real screenshot of the feature they document. (Forward-looking — V122-4.)

### Additional Requirements (Architecture / Infrastructure)

- `ui/src/__tests__/a11y-v120-pages.test.tsx` — new axe-core test suite, scoped to the five pre-v1.21 pages, runs under the same WCAG rule set as the existing `a11y.test.tsx` for v1.21 pages. Same color-contrast caveat (jsdom can't compute layout colors). (Retroactive.)
- `.github/workflows/release.yml` — `docker/setup-qemu-action@v3` added; `docker/build-push-action@v6` configured with `platforms: linux/amd64,linux/arm64`. (Retroactive.)
- `docs/site/operator/audit-log.md` — new operator doc (78 lines), wired into `mkdocs.yml`. (Retroactive.)
- `docs/site/operator/supply-chain.md` — updated with multi-arch notes and `docker buildx imagetools inspect` verification command. (Retroactive.)
- `scripts/docs-screenshots.mjs` — 213-line Playwright script; headless browser against a `make demo` instance; produces 5 PNGs at fixed viewport. (Stashed.)
- `internal/demo/mock_git.go` — demo fixtures for Playwright; 11 lines added. (Stashed.)
- `ui/package.json` — `docs:screenshots` npm script. (Stashed.)

---

## Requirements Coverage Map

| Req | Covered by |
|---|---|
| FR-V122-1 | V122-1 Story 1.1 |
| FR-V122-2 | V122-2 Story 2.1 |
| FR-V122-3 | V122-2 Story 2.1 (cosign signs manifest digest) |
| FR-V122-4 | V122-3 Story 3.1 |
| FR-V122-5 | V122-3 Story 3.2 |
| FR-V122-6 | V122-4 Story 4.1 |
| FR-V122-7 | V122-4 Story 4.2, 4.3 |
| FR-V122-8 | V122-4 Story 4.4, 4.5 |
| NFR-V122-1 | V122-1 Story 1.1 |
| NFR-V122-2 | V122-2 Story 2.1 |
| NFR-V122-3 | V122-2 Story 2.2 |
| NFR-V122-4 | V122-4 Story 4.2, 4.3 |
| NFR-V122-5 | Per-story quality gate |
| NFR-V122-6 | V122-4 Story 4.6 (code-reviewer sweep) |
| NFR-V122-7 | Confirmed zero new @Router — no story needed |
| UX-DR-V122-1 | V122-1 Story 1.2 |
| UX-DR-V122-2 | V122-3 Story 3.1 (heading promotion in same commit as banner) |
| UX-DR-V122-3 | V122-3 Story 3.1 |
| UX-DR-V122-4 | V122-4 Story 4.3 |

No requirement is uncovered.

---

## Epic List

### Epic V122-1: WCAG 2.1 AA retrofit of v1.20 pages
**Status:** `done` — committed in `9fced6b` on `dev/v1.22`.
**Goal:** Close the a11y gap v1.21 explicitly deferred (§4.8, §9 item "WCAG AA retrofit"). Add an axe-core test suite covering all five pre-v1.21 top-level pages and fix any serious/critical violations it surfaces.
**FRs/NFRs/UX covered:** FR-V122-1, NFR-V122-1, UX-DR-V122-1.

### Epic V122-2: Multi-arch Docker image
**Status:** `done` — committed in `4124de8` on `dev/v1.22`.
**Goal:** Publish `linux/amd64` + `linux/arm64` manifest list per release tag; cosign signature covers the manifest digest. Documented in operator supply-chain doc.
**FRs/NFRs covered:** FR-V122-2, FR-V122-3, NFR-V122-2, NFR-V122-3.

### Epic V122-3: Audit log retention banner + operator docs
**Status:** `done` — committed in `078b389` on `dev/v1.22`.
**Goal:** Make the stateless two-stream audit architecture visible in the UI (persistent banner) and documented for operators (new `operator/audit-log.md` with copy-paste setups for Loki / Splunk / ELK / CloudWatch / GCP Logging).
**FRs/UX covered:** FR-V122-4, FR-V122-5, UX-DR-V122-2, UX-DR-V122-3.

### Epic V122-4: Docs screenshots + stub-page content fill
**Status:** `in-progress` — partial work in `git stash@{0}`.
**Goal:** Fill the RTD site with real screenshots + real content. Playwright scaffolding stashed; need to execute, restore the stash, commit screenshots, embed them in docs, and fill/decide-fate-of stub pages.
**FRs/NFR/UX covered:** FR-V122-6, FR-V122-7, FR-V122-8, NFR-V122-4, UX-DR-V122-4.

---

## Epic V122-1: WCAG 2.1 AA retrofit of v1.20 pages

**Status:** `done` (commit `9fced6b`).

v1.21 shipped an axe-core suite (`a11y.test.tsx`) deliberately scoped to the new Marketplace pages, with a header note deferring the pre-v1.21 pages to v1.22. This epic closes that gap.

### Story V122-1.1: Add axe-core test covering AddonDetail, ClusterDetail, Settings, AuditViewer, Connections  `[done]`

As a **Sharko maintainer**,
I want an automated axe-core sweep over every pre-v1.21 top-level page,
So that accessibility regressions are caught in CI and the WCAG 2.1 AA commitment from v1.21 is retroactively honored across the whole UI.

**Acceptance Criteria:**

**Given** `ui/src/__tests__/a11y-v120-pages.test.tsx` exists
**When** `npm test -- --run` runs
**Then** each of AddonDetail, ClusterDetail, Settings, AuditViewer, and Connections renders under the test harness and is scanned against the `wcag2a / wcag2aa / wcag21a / wcag21aa` rule sets.

**Given** any page has a serious or critical violation
**When** the test runs
**Then** the test fails with a diagnostic naming the offending rule + DOM node.

**Given** the suite ran against the committed v1.22 code
**When** CI completes
**Then** all five pages pass with zero serious-or-critical violations. Net UI test count: 157 → 162 passing.

**Technical notes:**
- File: `ui/src/__tests__/a11y-v120-pages.test.tsx` (344 lines — verified in commit).
- Same color-contrast caveat as the existing v1.21 suite (jsdom can't compute layout colors; contrast is verified manually + on the real browser in V122-4's Playwright pass).

**Role file:** `.claude/team/test-engineer.md` + `.claude/team/frontend-expert.md`.
**Effort:** M.
**Dependencies:** none.
**Commit:** `9fced6b test(a11y): WCAG 2.1 AA retrofit for v1.20 pages (V122-1)`.

---

### Story V122-1.2: Fix AuditViewer filter form input/label associations  `[done]`

As a **screen-reader user on the Audit Log page**,
I want every filter input programmatically associated with its visible label,
So that I can understand what each filter controls.

**Acceptance Criteria:**

**Given** the AuditViewer filter form has 6 inputs (Action / Source / Result selects + User / Cluster / Since text inputs)
**When** axe scans the page
**Then** no input produces a `select-name (critical)` or `label (serious)` violation.

**Given** the user tabs into the Action select
**When** a screen reader reads the focused control
**Then** the visible label "Action" is announced.

**Given** the fix lands
**When** the rest of the page is scanned
**Then** no other serious/critical violations surface on this page (the four other pages were already clean — the existing codebase had solid a11y discipline, per commit message).

**Technical notes:**
- File: `ui/src/views/AuditViewer.tsx` — 18 lines changed; `id` + `htmlFor` added on every label/input pair.
- Other four pages (AddonDetail, ClusterDetail, Settings, Connections) passed clean on first scan — no fixes needed.

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/test-engineer.md`.
**Effort:** S.
**Dependencies:** V122-1.1 (the failing test that prompted this fix).
**Commit:** `9fced6b` (same commit).

---

## Epic V122-2: Multi-arch Docker image

**Status:** `done` (commit `4124de8`).

ARM K8s nodes (Graviton, Ampere) and arm64 developer machines couldn't pull the Sharko image natively — it was amd64-only. This epic turns on multi-arch publishing.

### Story V122-2.1: `release.yml` publishes `linux/amd64` + `linux/arm64` manifest list  `[done]`

As a **Sharko operator on an arm64 cluster**,
I want to `docker pull ghcr.io/moranweissman/sharko:<tag>` on an arm64 node and receive the arm64 variant without manual platform juggling,
So that the image runs natively on Graviton / Ampere / arm64 dev machines.

**Acceptance Criteria:**

**Given** `.github/workflows/release.yml` is updated
**When** a release is cut
**Then** `docker/setup-qemu-action@v3` is invoked before the build step and `docker/build-push-action@v6` is configured with `platforms: linux/amd64,linux/arm64`.

**Given** the release completes
**When** `docker buildx imagetools inspect ghcr.io/moranweissman/sharko:<tag>` runs
**Then** the manifest list contains at least two entries — one for `linux/amd64` and one for `linux/arm64`.

**Given** the manifest list is published
**When** `cosign sign --keyless` runs once against the manifest digest
**Then** the single signature covers every per-arch image under the list (cosign's manifest-digest signing semantics).

**Given** the Go binary stays native (CGO_ENABLED=0)
**When** the multi-arch build runs
**Then** cross-compile wall-time cost is paid by the Alpine final stage only — Go itself is not cross-compiled under qemu.

**Technical notes:**
- File: `.github/workflows/release.yml` — 16 lines changed (+14 / -2).
- `setup-qemu-action` is the mechanism — Go is native, qemu is only for the final-stage `apk` / `busybox` operations.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** M.
**Dependencies:** none.
**Commit:** `4124de8 ci(release): publish multi-arch (amd64+arm64) Docker image (V122-2)`.

---

### Story V122-2.2: Document multi-arch in `operator/supply-chain.md`  `[done]`

As an **operator verifying image provenance**,
I want the supply-chain doc to note the new multi-arch publishing and show the exact `buildx imagetools inspect` verify command,
So that I can confirm the arches my cluster needs are covered by the tag I'm pulling.

**Acceptance Criteria:**

**Given** `docs/site/operator/supply-chain.md` is updated
**Then** it contains a "Multi-arch publishing" section noting `linux/amd64` + `linux/arm64` are the published platforms as of v1.22.

**Given** the section is rendered
**Then** it includes a copy-paste `docker buildx imagetools inspect ghcr.io/moranweissman/sharko:<tag>` command and a sample output showing both arch entries.

**Given** the cosign verify command is re-confirmed
**Then** the doc notes that the single keyless signature covers all arches (no per-arch verify needed).

**Technical notes:**
- File: `docs/site/operator/supply-chain.md` — 19 lines changed (+18 / -1).

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** V122-2.1.
**Commit:** `4124de8` (same commit).

---

## Epic V122-3: Audit log retention banner + operator docs

**Status:** `done` (commit `078b389`).

Operators were surprised when audit entries vanished after a pod restart. Sharko emits each audit record on two streams — an in-memory ring buffer (last 1000, used by the UI/SSE) and structured stdout (persisted by their cluster log pipeline). The UI didn't say so. This epic surfaces the architecture in the UI + docs.

### Story V122-3.1: Persistent info banner on AuditViewer explaining two-stream architecture  `[done]`

As an **operator on the Audit Log page**,
I want a persistent banner explaining where audit records live and why they disappear on pod restart,
So that I understand I need a cluster log pipeline (not Sharko) to persist audit history.

**Acceptance Criteria:**

**Given** the Audit Log page is rendered
**Then** an info-severity banner is always visible at the top explaining: "Audit entries are emitted on two streams: (1) an in-memory ring buffer that backs this UI (last 1000 events, reset on pod restart), and (2) structured JSON to stdout for your cluster log pipeline (persistent)."

**Given** the banner is rendered
**Then** it links to `docs/site/operator/audit-log.md` for copy-paste setups (Loki / Splunk / ELK / CloudWatch / GCP Logging).

**Given** the banner is info-severity
**Then** it is **not** dismissible — the two-stream architecture is permanent, not a resolvable condition.

**Given** the AuditViewer page heading
**Then** it is an `<h1>` (promoted from `<h2>`) so the route has exactly one h1 — fixes a heading-hierarchy nit flagged by the incoming V122-1 axe sweep.

**Technical notes:**
- File: `ui/src/views/AuditViewer.tsx` — 27 lines changed (+25 / -2).

**Role file:** `.claude/team/frontend-expert.md`.
**Effort:** S.
**Dependencies:** none.
**Commit:** `078b389 feat(audit): retention banner + operator audit-log retention guide (V122-3)`.

---

### Story V122-3.2: New `operator/audit-log.md` retention guide + mkdocs nav entry  `[done]`

As a **platform engineer setting up a Sharko cluster**,
I want a single doc page explaining the stateless-by-design choice and giving copy-paste setups for the common cluster log pipelines,
So that I can plug audit stdout into my existing Loki / Splunk / ELK / CloudWatch / GCP Logging without guessing.

**Acceptance Criteria:**

**Given** `docs/site/operator/audit-log.md` exists
**Then** it explains the two-stream design and references V2 PRD FR-7.3 "stateless-by-design".

**Given** the doc provides copy-paste setups
**Then** it includes at minimum: Loki (via promtail / Grafana Agent), Splunk (via the HTTP Event Collector or the Splunk OTel Collector), ELK (via Filebeat), CloudWatch (via the aws-for-fluent-bit DaemonSet), GCP Logging (via the Cloud Logging agent DaemonSet).

**Given** `mkdocs.yml` is updated
**Then** the new page appears under "Operator Manual" in the site nav.

**Given** the docs site builds
**When** `mkdocs build --strict` runs
**Then** no broken links or missing files are reported.

**Technical notes:**
- Files: `docs/site/operator/audit-log.md` (+78 lines, new), `mkdocs.yml` (+1 line — nav entry).

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V122-3.1 (banner links to this page — ship together).
**Commit:** `078b389` (same commit).

---

## Epic V122-4: Docs screenshots + stub-page content fill

**Status:** `in-progress`. Playwright scaffolding + generated PNGs stashed in `git stash@{0}` (see `git stash show -u --stat stash@{0}`). Remaining stories below are forward-looking.

This epic fills the RTD site with real screenshots + real content for the stub user-guide pages. Without it, the docs site shows "Coming soon" on half the pages and has no real visual of the UI.

### Story V122-4.1: Playwright screenshot scaffolding (script + npm task + demo fixtures)  `[ready-for-dev]`

As a **Sharko docs maintainer**,
I want a single `npm run docs:screenshots` command that spins up a `make demo` instance and produces every documented-page screenshot deterministically,
So that docs stay visually current without hand-screenshotting.

**Acceptance Criteria:**

**Given** the stash is restored
**Then** `scripts/docs-screenshots.mjs` exists (213 lines — verified in `git stash show -u --stat stash@{0}`) and is a Playwright ESM script that launches a headless chromium, points at the local `make demo` Sharko instance, and captures the five canonical viewports.

**Given** `ui/package.json` adds the `docs:screenshots` script
**When** I run `npm run docs:screenshots` from `ui/`
**Then** the script runs end-to-end and writes PNGs to `docs/site/assets/screenshots/`.

**Given** `internal/demo/mock_git.go` contains the new demo fixtures
**Then** the `make demo` instance renders deterministic content (same marketplace addons, same audit entries, same cluster set on every run) so screenshots don't churn.

**Given** the script runs successfully once
**Then** the five expected PNGs exist: `dashboard.png`, `marketplace-browse.png`, `marketplace-detail.png`, `cluster-detail.png`, `audit-log.png`.

**Technical notes:**
- Files (from stash): `scripts/docs-screenshots.mjs` (new, 213 lines), `ui/package.json` (+3 / -1 = 2 lines changed — npm script), `internal/demo/mock_git.go` (+11 lines — fixtures).
- Restore sequence: `git stash pop stash@{0}` **after** the three prior epics are confirmed pushed to the PR branch (don't mix stash restore with committed history).

**Role file:** `.claude/team/frontend-expert.md` + `.claude/team/devops-agent.md` (CI surface — decide if the script runs in CI or just locally).
**Effort:** M.
**Dependencies:** none (stash is self-contained).

---

### Story V122-4.2: Commit 5 generated screenshots + restore stash  `[ready-for-dev]`

As a **Sharko docs maintainer**,
I want the five generated PNGs committed under `docs/site/assets/screenshots/` so the docs site references real files,
So that `mkdocs build` doesn't 404 and the images ship with the site.

**Acceptance Criteria:**

**Given** the stash is restored
**Then** the five PNGs land in `docs/site/assets/screenshots/`: `dashboard.png` (~150 KB), `marketplace-browse.png` (~670 KB), `marketplace-detail.png` (~390 KB), `cluster-detail.png` (~150 KB), `audit-log.png` (~280 KB).

**Given** the PNGs are committed
**Then** each is under 1 MB and lossless PNG.

**Given** the commit lands
**Then** the next `mkdocs build --strict` passes (no missing-image warnings).

**Technical notes:**
- File sizes above are from the stash inspection — real files.
- Per `CLAUDE.md` git safety rule: do not `git add -A`; add each PNG by path.

**Role file:** `.claude/team/devops-agent.md` + `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** V122-4.1.

---

### Story V122-4.3: Embed screenshots into docs pages  `[ready-for-dev]`

As a **Sharko docs reader**,
I want screenshots embedded on the landing page and in every user-guide page that documents a feature,
So that I can see what the feature actually looks like without installing Sharko.

**Acceptance Criteria:**

**Given** `docs/site/index.md` has a hero visual placeholder
**When** this story lands
**Then** the placeholder is replaced with `dashboard.png` (or a composite hero if visual design prefers).

**Given** `docs/site/user-guide/marketplace.md`
**Then** it embeds `marketplace-browse.png` and `marketplace-detail.png` inline with the matching narrative section.

**Given** `docs/site/user-guide/dashboard.md`
**Then** it embeds `dashboard.png` at the top.

**Given** `docs/site/operator/audit-log.md` (from Epic V122-3)
**Then** it embeds `audit-log.png` as the "what you see" reference.

**Given** `docs/site/user-guide/clusters.md`
**Then** it embeds `cluster-detail.png`.

**Given** `mkdocs build --strict` runs after embedding
**Then** all image references resolve (no missing-file warnings).

**Technical notes:**
- Use relative paths (`../assets/screenshots/<file>.png` from `docs/site/user-guide/*`) or the `{{ assets }}/screenshots/...` pattern if the mkdocs-material config uses one.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** M.
**Dependencies:** V122-4.2.

---

### Story V122-4.4: Fill stub content for 6 user-guide pages  `[ready-for-dev]`

As a **new Sharko user reading the docs**,
I want real content on Installation, First Run, Connections, Clusters, Addons, and Marketplace pages,
So that I can follow a path from install → first cluster → first addon without bouncing off a "Coming soon" stub.

**Acceptance Criteria:**

**Given** `docs/site/getting-started/installation.md`
**Then** it contains install instructions covering the Helm chart + the `docker run` path + prereqs (K8s version, ArgoCD, Git provider access).

**Given** `docs/site/getting-started/first-run.md`
**Then** it walks a new operator from first login → first Connection → first cluster registered → first addon deployed.

**Given** `docs/site/user-guide/connections.md`
**Then** it documents the Git + Cloud Connection types, the Tier 1 / Tier 2 PAT model (per v1.20 attribution design), and how to test a Connection.

**Given** `docs/site/user-guide/clusters.md`
**Then** it documents cluster registration (direct + discovery modes per V2 Epic 3), adoption (V2 Epic 4), drift recovery, and cleanup/removal (V2 Epic 6).

**Given** `docs/site/user-guide/addons.md`
**Then** it documents the Operation 1 (add addon to catalog) + Operation 2 (deploy addon on cluster) split from the v1.21 design (§1.5), values editing (v1.20), and smart-values (v1.21 §4.4).

**Given** `docs/site/user-guide/marketplace.md`
**Then** it walks the 3-tab modal from the v1.21 design (Browse / Search / Paste Helm URL) → Configure → Submit → toast.

**Given** `mkdocs build --strict` runs
**Then** all cross-links between these pages resolve.

**Technical notes:**
- Mostly prose. Lean on existing design docs (v1.20, v1.21) for authority; don't duplicate — link to them from the developer guide.
- Verify no real AWS account IDs, no `scrdairy|merck|msd\.com|mahi-techlabs|merck-ahtl` content leaks in.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** L.
**Dependencies:** V122-4.3 (embed screenshots first so the prose references concrete visuals).

---

### Story V122-4.5: Decide fate of CLI Reference / API Reference / Architecture sections  `[ready-for-dev]`

As a **Sharko docs reader**,
I want the nav to not advertise sections that are empty,
So that I'm not led to "Coming soon" pages.

**Acceptance Criteria:**

**Given** the current `mkdocs.yml` nav includes "CLI Reference" / "API Reference" / "Architecture" stubs
**When** this story lands
**Then** for each section, one of two outcomes:
  - **Stub with explicit "Coming in v1.23 / v2" content** — the page exists, explains what's planned and the timeline, and links to the relevant design doc if one exists.
  - **Trim from nav** — the file is deleted and the `mkdocs.yml` nav entry is removed.

**Given** a stub-with-content outcome
**Then** the page is honest about its state (no false "under construction" placeholder — explain what's planned and when).

**Given** `mkdocs build --strict` runs
**Then** no nav entry points at a missing file; no orphan file exists outside the nav.

**Technical notes:**
- CLI Reference: candidate to stub with "v1.23 roadmap" since we'd benefit from auto-generated reference (cobra-based tooling). Low priority.
- API Reference: swagger is already generated → could be a link out to the served `/swagger` endpoint on the demo instance, rather than a separate doc page.
- Architecture: Could link to the v1.21 design doc + V2 implementation plan as the architecture reference until a dedicated `arch-diagrams` doc exists.

**Role file:** `.claude/team/docs-writer.md`.
**Effort:** S.
**Dependencies:** none.

---

### Story V122-4.6: Run `bmad-code-review` on the 3 completed epics before shipping  `[ready-for-dev]`

As the **tech lead**,
I want a fresh-context adversarial code review against V122-1, V122-2, V122-3 before the PR is cut,
So that any issues the implementing agent missed are caught before `v1.22.0` is tagged.

**Acceptance Criteria:**

**Given** the three commits (`9fced6b`, `4124de8`, `078b389`) are on `dev/v1.22`
**When** `bmad-code-review` runs
**Then** the review covers the three diffs end-to-end and surfaces any Blind Hunter / Edge Case Hunter / Acceptance Auditor findings.

**Given** any finding in the "must-fix" triage category
**Then** a remediation commit lands on `dev/v1.22` before the PR opens.

**Given** `CLAUDE.md` forbidden-content grep runs
**Then** zero matches against `scrdairy|merck|msd\.com|mahi-techlabs|merck-ahtl` or real AWS account IDs across the v1.22 diff.

**Technical notes:**
- Invoke the `bmad-code-review` skill; do not hand-roll a review.

**Role file:** `.claude/team/code-reviewer.md` + `.claude/team/security-auditor.md`.
**Effort:** S.
**Dependencies:** V122-1, V122-2, V122-3 (all done); V122-4.4 (if docs changes introduce new forbidden content).

---

### Story V122-4.7: Single PR `dev/v1.22 → main`, tag `v1.22.0`  `[ready-for-dev]`

As a **release engineer**,
I want one PR from `dev/v1.22` to `main` landing all four epics plus this planning bundle, then a `v1.22.0` tag,
So that the release follows the "bundle on a single working branch, cut release only at a real milestone" rule from `feedback_release_cadence.md`.

**Acceptance Criteria:**

**Given** Stories V122-1.1/1.2, V122-2.1/2.2, V122-3.1/3.2, V122-4.1 through V122-4.6 are all `done`
**When** the PR opens
**Then** it has a title describing v1.22 polish as a single unit and a body listing each epic + the delivered increments.

**Given** CI passes on the PR
**When** it merges
**Then** `v1.22.0` is tagged via the existing release workflow; the release publishes multi-arch images, signed binaries, and the CHANGELOG entry.

**Given** the release completes
**Then** the BMAD sprint-status.yaml marks all V122 epics `done` and all V122 stories `done`.

**Technical notes:**
- No `--no-verify`; no force-push; no retag of an existing version — per `CLAUDE.md` Git Rules.

**Role file:** `.claude/team/devops-agent.md`.
**Effort:** S.
**Dependencies:** V122-4.6.

---

## Sequencing / Dependency Graph

```
V122-1 WCAG retrofit          [done]
V122-2 Multi-arch image        [done]
V122-3 Audit retention docs    [done]
              │
              ▼
V122-4 Docs screenshots + content fill  [in-progress]
  ├─ 4.1 Playwright scaffolding     [ready-for-dev — stash restore]
  ├─ 4.2 Commit PNGs
  ├─ 4.3 Embed in docs pages
  ├─ 4.4 Fill stub content
  ├─ 4.5 Decide CLI/API/Arch sections
  ├─ 4.6 bmad-code-review sweep (gates PR)
  └─ 4.7 PR + tag v1.22.0
```

The three committed epics landed in parallel on `dev/v1.22` (their diffs are disjoint). V122-4 is the finishing epic — it gates the release.

---

## Quality Gates per Story

Same bar as v1.21 (`.claude/team/tech-lead.md` CHECK):

**Backend (Go):**
- `go build ./...`
- `go vet ./...`
- `go test ./...`
- No `@Router` changes expected in v1.22 — if any slip in, `swag init` regen is mandatory.

**Frontend:**
- `cd ui && npm run build`
- `cd ui && npm test -- --run`
- axe CI suite passes (the V122-1 suite gates every v1.22 merge from now on).
- No `text-gray-*` / `bg-gray-*` / `border-gray-*` without `dark:` prefix.

**Docs:**
- `mkdocs build --strict` passes.
- No broken cross-links.

**Cross-cutting:**
- Forbidden-content grep on every epic.
- `code-reviewer` + `security-auditor` before PR (Story V122-4.6).

---

## Out of Scope (v1.22 — explicitly deferred)

- **Third-party private catalogs** — v1.23 (see `docs/design/2026-04-20-v1.23-catalog-extensibility.md` §2).
- **Per-entry cosign signing** — v1.23 (§3).
- **Automated trusted-source scanning bot** — v1.23 (§4).
- **Fine-grained RBAC** — V2.x scoped RBAC roadmap.
- **SSO** — V2 hardening.
- **HA / threat model / operator mode** — V2 / V3+ backlog.

---

**End of v1.22 epic breakdown.**
