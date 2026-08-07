---
stepsCompleted: ['step-01-validate-prerequisites', 'step-02-design-epics', 'step-03-create-stories', 'step-04-final-validation']
inputDocuments:
  - .bmad/output/planning-artifacts/prd-sharko-v4.md
  - .bmad/output/architecture/2026-07-25-sharko-oss-professional-design.md
  - docs/design/2026-07-20-current-architecture.md
workflowType: 'epic-planning'
version: 'v4.0.0'
project_name: 'sharko'
user_name: 'Moran'
date: '2026-07-29'
---

# Sharko v4.0.0 - Epic Breakdown

> **Amendment (2026-08-08) — read the catalog parts as history, not current truth.**
> **Decision history:** this plan originally specified the **"delta" catalog model** — a curated catalog shipped by Sharko merged under the user's file, the user's file holding only their entries and overrides and winning on conflict, and the migration converting a full-copy catalog into that delta file. It was **built as written and then replaced on 2026-07-31** by the **approved-list model**: the org's catalog file holds the full, self-contained list of approved addons (chart, repo, version, namespace, settings, needed secrets); the curated list survives only as the read-only discovery window ("Marketplace") that supplies display knowledge and can never add an addon or a deployment field; the migration writes a full approved list. The affected requirement and story texts below (FR19, planning note 1, Epic 3 / Story 3.2, Story 5.2) have been **rewritten to current truth** so no future agent builds the old model from this file — the original delta wording lives only in this note. Current truth sources: the amended PRD (2026-08-08) + design doc `2026-07-31-catalog-approved-model`.

## Overview

This document provides the complete epic and story breakdown for Sharko v4.0.0, decomposing the requirements from the v4 PRD (the capability contract: 44 FRs, 20 NFRs) and the 2026-07 design decisions into implementable stories. v4 is a "redefinition MVP": much is shipped and gets re-verified/re-pointed; the big new build is the engine chart + migration + takeover surface.

## Requirements Inventory

### Functional Requirements

FR1: A platform engineer can install Sharko with Helm on the hub cluster and complete a guided setup.
FR2: Sharko can bootstrap the user's repo with one PR containing only empty data folders, an engine version pin, and a README — nothing else.
FR3: The user can upgrade the engine by merging a Sharko-opened pin-bump PR; the engine never updates silently.
FR4: A v3 user can migrate to the v4 format by merging exactly one Sharko-opened PR.
FR5: The user can remove Sharko completely; nothing running breaks, no data is stranded, and the docs prove it.
FR6: Anyone can pull, read, and locally render the engine chart before trusting it — it's public and signed.
FR7: A user can explore the UI in demo mode without connecting anything.
FR8: A user can set up, edit, and test Sharko's connections — git provider, ArgoCD, vault — and see their health.
FR9: Humans authenticate with a login; sessions expire.
FR10: Every actor — human or token — carries a role from Sharko's coarse role model, and every write action is gated by it. Fine-grained (per-cluster / per-addon) permissions are explicitly not in v4; the docs state this limit.
FR11: Machine clients authenticate with scoped, expiring API tokens that can be created, renewed, and revoked.
FR12: An engineer or machine client can register a cluster with credentials; Sharko creates the ArgoCD cluster secret, stamped as Sharko-managed.
FR13: The user can run a takeover preflight on a cluster ArgoCD already manages: a plain-language report of four checks — who owns the existing secret, which ApplicationSets are not deletion-safe, what Applications target the cluster, and name collisions.
FR14: Takeover registration can keep the cluster's existing name and server address, and preserves the old secret's legacy labels by default.
FR15: After migration, the user can drop the preserved legacy labels with one action.
FR16: The user can unregister a cluster; Sharko warns about the consequences first (including legacy labels still preserved) and requires explicit confirmation.
FR17: A user can browse the catalog and see each addon's knowledge: description, chart location, required values with plain descriptions, needed secrets, known quirks, docs link.
FR18: A user can add their in-house chart as a first-class catalog entry through any door — same versions, upgrades, and flows as public addons.
FR19 *(amended 2026-08-08)*: The org's catalog file holds the full, self-contained list of approved addons; the curated list only decorates approved entries with display knowledge and never adds an addon or a deployment field.
FR20: Version freshness is maintained on a schedule (default daily) and on demand; the UI always shows "last checked."
FR21: An outside contributor can propose a curated catalog entry through a template-guided PR.
FR22: A user can enable or disable an addon on a cluster; the change is a PR touching small readable data files.
FR23: A user can set Helm values per addon — globally and per cluster — as free-form values files.
FR24: A user can override deployment settings (namespace, ignore-differences, sync options, prune behavior) as declared data per addon and per cluster; catalog entries supply known-quirk defaults.
FR25: The engine turns data files into ArgoCD ApplicationSets with zero user-visible template logic, and ships deletion-safe by default.
FR26: A user can see the version matrix: which addon version runs on which cluster, and what newer versions exist.
FR27: A user can upgrade an addon on many clusters in one action, producing one reviewable PR.
FR28: A user can run an upgrade check before committing: version comparison, impact, and security advisories — deterministic, no AI required.
FR29: With an AI key configured, a user can additionally get a plain-language upgrade summary.
FR30: Anything the UI can do, the API can do; the CLI wraps the same API.
FR31: Every write, through any door, follows the same pipeline: validate → preview → PR. Validation catches missing or invalid required values and unresolvable references where Sharko can check them; the preview shows the exact files and content the PR will contain. No door bypasses the gate.
FR32: Any client — human or machine — can follow a write from PR-opened through merged to deployed.
FR33: A user can run `sharko validate` in their own CI; failures name file, reason, and line.
FR34: Sharko detects drift between git and live cluster state within its stated window and shows a read-only diff.
FR35: From the drift view, a user can trigger a one-time re-sync — make the cluster match git now — without enabling self-heal.
FR36: A user can opt in to self-heal; when on, Sharko re-applies only its own addon-label keys — never anything else.
FR37: When something breaks, diagnostics say what, where, and why in plain words — including parser errors with file and line.
FR38: Sharko emits Kubernetes events for failures and important transitions, never including secret values.
FR39: A user can see the whole fleet's state on one dashboard.
FR40: Every change is traceable: who asked, through which door, which PR, what merged.
FR41: Malformed or broken input never crashes Sharko and never half-applies — every change lands all-or-nothing.
FR42: Sharko syncs addon secrets from the user's vault to the remote clusters that need them, and repairs/rotates them automatically on its stated schedule.
FR43: Secret values never appear in logs, git files, API responses, previews, or events.
FR44: With an AI key, a user can work through a chat assistant that uses the same permissions and the same PR gate as every other door; without a key, the feature is absent, not broken.

### NonFunctional Requirements

NFR1: Label/assignment drift is detected within 30 seconds; merges trigger reconcile immediately rather than waiting for the next cycle.
NFR2: Addon secrets are checked and repaired on a 5-minute default cycle (configurable); a manual trigger is available.
NFR3: Catalog version freshness: scheduled refresh (default daily) + on-demand; the UI never hides how stale its data is ("last checked").
NFR4: The quickstart holds its promise: install → first addon deployed in under 30 minutes on a fresh cluster.
NFR5: Release-to-release performance is gated: no API path may regress its p99 latency by more than 20% (enforced in CI).
NFR6: Stored credentials are encrypted at rest; secret values never appear in logs, git files, previews, events, or API responses (enforced by tests).
NFR7: Release artifacts — server images and the engine chart — are signed; an SBOM is published with each release (or the claim is dropped).
NFR8: The threat model is a public doc, linked from the README.
NFR9: API tokens expire by default and are renewable before expiry; expiry defaults are documented.
NFR10: The AI assistant's access is bounded by the caller's role; its transparency note states what data leaves the cluster when a key is configured.
NFR11: Sharko going down never touches the fleet — only Sharko's automation pauses.
NFR12: All reconcilers recover cleanly from a restart — no manual intervention, no partial state.
NFR13: High availability is NOT promised in v4: single replica, documented honestly as a limit.
NFR14: Designed and tested for fleets of roughly 10–50+ clusters; dashboard and matrix stay usable at 50 clusters × dozens of addons.
NFR15: Git-at-scale behavior is documented with honest guidance.
NFR16: Supported ArgoCD versions stated per release; integration uses only ArgoCD's stable public contracts.
NFR17: Supported git providers: GitHub, Azure DevOps, Gitea. Supported secret backends: AWS Secrets Manager, Kubernetes Secrets.
NFR18: Images ship for amd64 and arm64.
NFR19: Supported Kubernetes version range stated per release.
NFR20: Every release passes: build, CI-honest tests, helm lint, strict docs build, the perf gate, and the claims-vs-code audit.

### Additional Requirements

From the 2026-07-25 design doc (locked decisions with build implications):

- AR1: **Operator surgical removal to a shelf branch.** The ClusterAddons CRD + controller ship in the current build and must come OUT of v4's visible surface: api/v1alpha1/, internal/operator/, the chart CRD + operator RBAC, serve.go manager wiring, playground drive-mode targets. Shelf-branch first (code preserved), removal on a working branch; build + CI-honest tests + helm lint/template stay green after. NOT a git revert.
- AR2: **Engine chart shape.** Source lives in the Sharko monorepo; published as a signed OCI artifact to the same registry as the server image. The user-repo pin is a small ArgoCD Application referencing `oci://…/sharko-engine@version`. Bootstrap plants a seed: empty data folders + the pin + README.
- AR3: **Scanner stays parked.** internal/catalog/artifacthub.go + draft PR #389 must not ship half-alive — verify nothing user-visible references it in v4.
- AR4: **README edits ride the docs rewrite:** cut the "API-driven building block for IDP" jargon line; rank the ~20-bullet feature list into headline + "also includes" fold.
- AR5: From the current-architecture doc: one `sharko serve` process with in-process reconcilers; single-writer consolidation done (one writer of the ArgoCD cluster secret); file envelope `sharko.dev/v1` (legacy `sharko.io/v1` fallback) — the v4 migration + validate work builds on this envelope.
- AR6: **Existing assets to reuse, not rebuild:** kind playground (`make operator-playground-up`, now Gitea-backed), demo mode, ~142-page mkdocs site, threat model doc (2026-06-02), CI perf gate.

### UX Design Requirements

None — no UX design document exists for v4. The UI is shipped; v4 UI work is re-pointing existing screens at new capabilities (takeover flow, settings-as-data, freshness display) and is captured inside the relevant stories.

### Verified at planning (code checks, 2026-07-29)

1. **Catalog seeding** *(amended 2026-08-08)*: the user's catalog file IS the full catalog — and under the approved-list model that is the intended end state, not a problem to restructure away. The migration writes a full approved list. (The original note here called for converting to the delta model — see the decision-history note at the top.) → shapes Epic 3 + Epic 5.
2. **Catalog write path:** already git-native (handleAddAddon → git provider → PR, with dry-run preview + Tier-2 per-user attribution). No gap story.
3. **Preflight RBAC:** chart ClusterRole already reads `applications` + `appprojects` (argoproj.io) and secrets cluster-wide. Missing exactly one read: `applicationsets`. Small widening in the Takeover epic.
4. **`sharko doctor`:** does not exist. LOCKED: preflight ships standalone inside the takeover flow; connections health (FR8) covers product-health needs. No doctor branding in v4.
5. **Batch upgrade:** addon version is catalog-global today — one PR upgrades all clusters running the addon, but subset selection ("12 of 14") needs per-cluster version pins, which do NOT exist. Pins land in the Engine Chart's data model; the subset one-PR write + UI land in Fleet Upgrades.

### FR Coverage Map

FR1: Epic 4 — guided install + setup
FR2: Epic 4 — seed bootstrap PR
FR3: Epic 2 — engine pin-bump upgrades
FR4: Epic 5 — one-PR v3→v4 migration
FR5: Epic 9 — kill-Sharko re-verified
FR6: Epic 2 — engine public, signed, locally renderable
FR7: Epic 4 — demo mode
FR8: Epic 8 — connections manage/test/health
FR9: Epic 8 — human login + session expiry
FR10: Epic 8 — coarse role gating stated + audited
FR11: Epic 8 — token lifecycle (create/renew/revoke, expiry)
FR12: Epic 4 — cluster registration
FR13: Epic 6 — takeover preflight (4 checks)
FR14: Epic 6 — takeover registration (name/address + legacy labels)
FR15: Epic 6 — one-action legacy-label drop
FR16: Epic 6 — unregister warn + confirm
FR17: Epic 3 — catalog entry knowledge
FR18: Epic 3 — internal addons first-class
FR19: Epic 3 — approved-list model *(amended 2026-08-08; originally delta)*
FR20: Epic 3 — freshness schedule + "last checked"
FR21: Epic 3 — contributor PR template
FR22: Epic 4 — enable/disable addon → PR
FR23: Epic 2 — values files (global + per cluster)
FR24: Epic 2 — deployment-settings-as-data
FR25: Epic 2 — engine renders data files, deletion-safe
FR26: Epic 7 — version matrix
FR27: Epic 7 — subset-selection one-PR upgrade (pins from Epic 2)
FR28: Epic 7 — deterministic upgrade check
FR29: Epic 7 — optional AI upgrade summary
FR30: Epic 9 — door-parity audit
FR31: Epic 4 — validate → preview → PR pipeline (semantic validation, exact-files preview)
FR32: Epic 4 — PR tracking to merged-and-deployed
FR33: Epic 2 — `sharko validate` with line numbers on the new format
FR34: Epic 9 — drift detection re-verified
FR35: Epic 8 — one-time re-sync
FR36: Epic 9 — self-heal re-verified (own label keys only)
FR37: Epic 9 — diagnostics re-verified
FR38: Epic 9 — events re-verified
FR39: Epic 9 — dashboard re-verified
FR40: Epic 9 — traceability/audit re-verified
FR41: Epic 8 — all-or-nothing bad-input audit
FR42: Epic 9 — addon-secrets engine re-verified
FR43: Epic 9 — no-secret-values rule re-verified
FR44: Epic 9 — AI assistant re-verified (same permissions + PR gate)

Additional requirements: AR1 → Epic 1 · AR2 → Epic 2 · AR3, AR4 → Epic 10 · AR5 → Epic 5 · AR6 → all (reuse rule).
NFR homes: NFR3 → Epic 3 · NFR4 → Epic 4 · NFR9 → Epic 8 · NFR7, NFR8, NFR13, NFR15, NFR18, NFR20 → Epic 10 · the rest are standing constraints verified in Epic 9.

## Epic List

### Epic 1: Operator Removal (opening round)
Clear the deck: the shelved ClusterAddons operator (CRD + controller, ~3,600 lines) comes out of the build, the chart, and the playground — preserved intact on a shelf branch. Nothing later carries dead weight.
**Traces:** AR1. **Wave 0.**

### Epic 2: The Engine Chart — data-only repos that deploy addons
Sharko's ApplicationSet logic becomes a versioned, signed, public OCI chart. The user's repo holds only readable data files (assignments, values, settings, per-cluster version pins) and one pin. Deletion-safe by default. `sharko validate` understands the new format with line numbers.
**FRs:** FR3, FR6, FR23, FR24, FR25, FR33 · AR2. **Wave 1. THE elephant: pre-named ~6-7 stories, format/settings-schema design story FIRST.**

### Epic 3: Catalog — approved-list model, internal addons, contribution path *(amended 2026-08-08; originally the delta model — see the decision-history note at the top)*
Curated catalog maintained + versioned by Sharko; the user's file holds only their entries and overrides, and wins on conflict. Extended entries (required values, needed secrets, quirks). In-house charts first-class. Freshness + "last checked." Contributor PR template.
**FRs:** FR17, FR18, FR19, FR20, FR21 · NFR3. **Wave 1.**

### Epic 4: Day Zero — install, bootstrap, doors
Guided setup, seed bootstrap PR, cluster registration, first addon enabled through the full pipeline (semantic validation + exact-files preview + PR tracking), demo mode. The ≤30-minute quickstart proven on the playground.
**FRs:** FR1, FR2, FR7, FR12, FR22, FR31, FR32 · NFR4. **Wave 1. Engine-independent stories may start while Epic 2 is mid-flight.**

### Epic 5: The One-PR Migration from v3
A v3 repo converts in exactly one Sharko-opened PR: scaffolded templates out, engine pin in, catalog carried over as a full approved list *(amended 2026-08-08; originally "converted to the delta model")*, files on the current envelope. Depends on Epics 2 + 3.
**FRs:** FR4 · AR5. **Wave 2.**

### Epic 6: Takeover — safe brownfield migration
Preflight (4 checks; +one new RBAC read: applicationsets), takeover registration (same name/address, legacy labels preserved), one-action label drop, unregister warn-and-confirm, the migration playbook docs. Depends on the engine (workload adoption).
**FRs:** FR13, FR14, FR15, FR16. **Wave 2.**

### Epic 7: Fleet Upgrades — the signature moment
Version matrix on the new data model, subset selection → one reviewable PR (per-cluster pins from Epic 2), deterministic upgrade check + optional AI summary re-verified.
**FRs:** FR26, FR27, FR28, FR29. **Wave 2.**

### Epic 8: Access & Robustness
Connections manage/test/health, human login + session expiry, coarse-role gating stated and audited, token lifecycle (expiry/renew/revoke), one-time re-sync from the drift view, all-or-nothing bad-input audit. A basket of small independent stories — ideal for parallel dispatch.
**FRs:** FR8, FR9, FR10, FR11, FR35, FR41 · NFR9. **Wave 2.**

### Epic 9: The Re-Verification Sweep — everything still true
Every shipped promise re-proven under the new engine: kill-Sharko, drift, self-heal, diagnostics, events, dashboard, audit trail, addon-secrets engine, no-secret-values rule, AI assistant, door parity.
**FRs:** FR5, FR30, FR34, FR36–FR40, FR42, FR43, FR44. **Wave 3.**

### Epic 10: The Docs & Honesty Release
Docs rewritten to the new identity (incl. the comparison page + README edits), the stated-limits & transparency page (multi-tenancy, single replica, coarse roles, git-at-scale, AI transparency), SBOM real or dropped, threat model surfaced, arm64 fixed, scanner confirmed parked, claims-vs-code audit.
**Traces:** AR3, AR4 · NFR7, NFR8, NFR13, NFR15, NFR18, NFR20. **Wave 3.**

## Build Order (4 waves, 4 checkpoints)

- **Wave 0:** Epic 1 (Operator Removal). **Checkpoint: everything green with the operator gone** (build, CI-honest tests, helm lint/template).
- **Wave 1:** Epics 2 + 3 + 4 (the core). **Checkpoint: full kind-playground walk — install → first addon ≤30 min.**
- **Wave 2:** Epics 5 + 6 + 7 + 8 (meeting the world). **Checkpoint: Moran's own walkthrough, incl. brownfield takeover.**
- **Wave 3:** Epics 9 + 10 (the close). **Checkpoint: claims-vs-code audit passes → v4.0.0 on Moran's word.**

## Epic 1: Operator Removal

Clear the deck: the shelved ClusterAddons operator comes out of the build, the chart, and the playground — preserved intact on a shelf branch.

### Story 1.1: Shelve the operator code and remove it from the build

As the maintainer,
I want the ClusterAddons operator code preserved on a shelf branch and removed from the main build,
So that no future work carries dead weight.

**Acceptance Criteria:**

**Given** main at the current commit
**When** the shelf branch is created and `api/v1alpha1/`, `internal/operator/`, and the serve.go manager wiring are removed on a working branch
**Then** `go build`, `go vet`, and the CI-honest test suite pass with zero operator references
**And** the shelf branch still holds the complete operator code untouched.

### Story 1.2: Remove the operator from the Helm chart

As a user installing Sharko,
I want the chart to contain no CRD and no operator permissions,
So that what I install matches what the product actually is.

**Acceptance Criteria:**

**Given** the chart with the operator pieces present
**When** the ClusterAddons CRD file, the operator Role/RoleBinding, and related values are removed
**Then** `helm lint` and `helm template` pass
**And** the rendered output contains no `sharko.dev` CRD and no operator permissions.

### Story 1.3: Clean the playground and docs of drive-mode

As a user trying Sharko on my laptop,
I want the playground and docs to show only what exists,
So that I never follow instructions for a removed feature.

**Acceptance Criteria:**

**Given** the playground with drive-on/drive-off targets
**When** those targets and the drive-mode docs are removed (make targets renamed `playground-*`, old names kept as quiet aliases)
**Then** `make playground-up/status/down` work end to end
**And** no doc page references drive mode except a short "shelved" note in the changelog.

## Epic 2: The Engine Chart — data-only repos that deploy addons

Sharko's ApplicationSet logic becomes a versioned, signed, public OCI chart; the user's repo holds only data files and one pin.

### Story 2.1: Design the data-file format and settings schema

As the maintainer,
I want the complete v4 data-file format designed and written down,
So that every later story builds against one agreed contract.

**Acceptance Criteria:**

**Given** the PRD's engine requirements
**When** the format spec is written
**Then** it defines the per-cluster assignment file (addons on/off, per-cluster version pin, per-addon settings overrides), global + per-cluster values files, the org catalog file (the full approved list — amended 2026-08-08), and the engine pin (a small ArgoCD Application referencing the OCI chart)
**And** it includes worked examples for prod-eu and staging-us, the settings escape ladder, and how the schema versions with the engine — all on the `sharko.dev/v1` envelope.

### Story 2.2: The engine renders data files into ApplicationSets

As a platform engineer,
I want deployments generated from my data files by a generic engine,
So that my repo holds no template logic at all.

**Acceptance Criteria:**

**Given** a fixture repo in the new format
**When** the engine chart renders
**Then** the output ApplicationSets deploy exactly the enabled addons per cluster with the right values layering (chart defaults → global → per-cluster)
**And** the templates contain zero per-addon conditionals
**And** `preserveResourcesOnDeletion` is on by default.

### Story 2.3: Per-cluster version pins and settings pass-through

As a platform engineer,
I want per-cluster addon versions and deployment settings declared as data,
So that quirks and rollouts are per-cluster decisions, not template edits.

**Acceptance Criteria:**

**Given** a cluster file pinning cert-manager to an older version with a webhook ignore-diff setting
**When** the engine renders
**Then** that cluster gets exactly the pinned version and the declared settings while other clusters follow the catalog default
**And** catalog quirk defaults apply wherever the user hasn't overridden them.

### Story 2.4: Publish the engine — OCI, signed, locally renderable

As a skeptical engineer,
I want to pull, read, and render the engine chart before trusting it,
So that nothing about my deployments is hidden.

**Acceptance Criteria:**

**Given** a tagged engine release
**When** the release pipeline runs
**Then** the chart is pushed to the public registry and cosign-signed
**And** `helm pull` plus a local render with the fixture repo works as documented.

### Story 2.5: Engine upgrades are pin-bump PRs

As a platform engineer,
I want engine upgrades to arrive as reviewable PRs,
So that my deploy logic never changes without my merge.

**Acceptance Criteria:**

**Given** a newer engine version exists
**When** I request the upgrade (or the scheduled check finds it)
**Then** Sharko opens a PR changing only the pin
**And** nothing changes in my clusters until I merge it.

### Story 2.6: `sharko validate` understands the new format

As a teammate editing files by hand,
I want validation with exact file, reason, and line,
So that my CI catches my mistakes before any merge.

**Acceptance Criteria:**

**Given** a repo with one broken data file
**When** `sharko validate` runs in CI
**Then** it fails naming the file, the reason, and the line
**And** a valid repo passes with exit code 0
**And** a broken file never half-applies anywhere in Sharko.

## Epic 3: Catalog — approved-list model, internal addons, contribution path *(amended 2026-08-08; originally the delta model — see the decision-history note at the top)*

*(amended 2026-08-08)* The org's catalog file is the full approved list — complete, self-contained entries. The curated list is a read-only discovery window supplying display knowledge only.

### Story 3.1: Extended catalog entries — the addon's knowledge

As a teammate who isn't the fleet expert,
I want each catalog entry to tell me what the addon needs,
So that I can enable it correctly without asking anyone.

**Acceptance Criteria:**

**Given** a catalog entry for external-dns
**When** I open it in any door
**Then** I see description, chart location, version source, required values with plain descriptions, needed secrets, known quirks, and a docs link
**And** the entry format matches the Story 2.1 spec with no per-addon JSON schemas.

### Story 3.2: The approved-list model — the org's file is the whole catalog *(rewritten 2026-08-08; the original delta-model story is preserved in the decision-history note at the top)*

As a platform engineer,
I want my catalog file to hold the full, self-contained list of addons my org approved,
So that nothing runs in my org that nobody chose, and my repo alone tells the whole story.

**Acceptance Criteria:**

**Given** a curated entry for cert-manager that my org has NOT approved
**When** Sharko loads the catalog
**Then** cert-manager does not appear in my catalog — only in the read-only discovery window (Marketplace)
**And** when my org approves it, my git file gains a full, self-contained entry (chart, repo, version, namespace, settings, needed secrets)
**And** the curated list only decorates my approved entries with display knowledge (description, docs link, known quirks) and never supplies a deployment field.

### Story 3.3: Internal addons are first-class

As an IDP developer,
I want my in-house chart in the catalog next to Datadog,
So that our internal service gets the same versions, upgrades, and flows.

**Acceptance Criteria:**

**Given** a private OCI chart reference added through any door
**When** the entry is saved (via PR, like everything)
**Then** the addon appears in the catalog, is assignable to clusters, and shows in the version matrix
**And** version checking degrades gracefully when registry credentials are absent (shows "unknown", never errors).

### Story 3.4: Freshness — scheduled and on-demand, always dated

As a platform engineer,
I want version data refreshed on a schedule and on demand,
So that I know exactly how fresh what I'm seeing is.

**Acceptance Criteria:**

**Given** the catalog with version sources configured
**When** the daily refresh runs or I click refresh
**Then** available versions update and every catalog/matrix view shows "last checked" with a real timestamp
**And** the schedule is configurable with daily as default.

### Story 3.5: Community contribution path

As an outside contributor,
I want a template-guided way to propose a curated catalog entry,
So that adding an addon to Sharko is a small PR, not a reverse-engineering project.

**Acceptance Criteria:**

**Given** the Sharko repo on GitHub
**When** a contributor opens a new catalog-entry PR
**Then** a PR template walks them through the required fields
**And** a short guide documents the acceptance rules
**And** CI validates the proposed entry's format automatically.

## Epic 4: Day Zero — install, bootstrap, doors

Fresh install to first addon in under 30 minutes, through the full validate → preview → PR pipeline.

### Story 4.1: Guided setup on the v4 flow

As a platform engineer installing Sharko,
I want the setup screen to connect git and ArgoCD and get me to a working state,
So that the product guides me instead of a README scavenger hunt.

**Acceptance Criteria:**

**Given** a fresh Helm install on the hub
**When** I complete the setup screen (git repo, ArgoCD, optional vault)
**Then** connections are saved, tested, and shown healthy
**And** the next step (bootstrap) is offered automatically.

### Story 4.2: The seed bootstrap PR

As a platform engineer,
I want Sharko's bootstrap PR to plant only a seed,
So that my repo stays mine and readable from day one.

**Acceptance Criteria:**

**Given** a connected empty (or new) repo
**When** I accept the bootstrap
**Then** one PR contains exactly: empty data folders, the engine pin, and a README explaining the layout — nothing else
**And** merging it makes ArgoCD pick up the engine and Sharko report ready
**And** the old v3 template scaffolding is gone from the bootstrap path.

### Story 4.3: Enable an addon through the sharpened pipeline

As a teammate,
I want enabling an addon to validate my inputs and show me exactly what will change,
So that my worst case is a rejected PR, never a broken cluster.

**Acceptance Criteria:**

**Given** an addon with required values and a needed secret
**When** I submit with a missing value or an unresolvable reference (where Sharko can check it)
**Then** validation blocks with a plain-words error before any PR
**And** when inputs are valid, the preview shows the exact files and content the PR will contain
**And** the merged PR results in the addon deploying via the engine.

### Story 4.4: Register a cluster and track the change to deployed

As an engineer or a portal,
I want registration and every write to be trackable from PR to deployed,
So that both humans and machines know when the change is real.

**Acceptance Criteria:**

**Given** valid cluster credentials
**When** I register through UI or API
**Then** the Sharko-stamped cluster secret is created and the cluster appears on the dashboard
**And** the response carries the PR reference, and its status can be followed to merged-and-deployed by any client.

### Story 4.5: Demo mode on the v4 format

As a curious engineer,
I want to explore the UI with nothing connected,
So that I can judge Sharko before giving it anything real.

**Acceptance Criteria:**

**Given** Sharko started in demo mode
**When** I browse clusters, catalog, matrix, and previews
**Then** all demo data reflects the v4 data-file format
**And** no real connection is required or touched.

### Story 4.6: The 30-minute quickstart, proven

As a first-time user,
I want the quickstart to actually take under 30 minutes,
So that the first promise Sharko makes me is a kept one.

**Acceptance Criteria:**

**Given** the rewritten quickstart doc and the kind playground
**When** the full walk runs (install → setup → bootstrap → register → enable → merge → deployed → green dashboard)
**Then** it completes in under 30 minutes on a laptop
**And** this walk is the Wave 1 checkpoint, recorded as the release demo basis.

## Epic 5: The One-PR Migration from v3

A v3 repo converts in exactly one Sharko-opened PR.

### Story 5.1: Detect a v3 repo and preview the conversion

As a v3 user,
I want Sharko to recognize my repo's format and show me the migration plan first,
So that I know exactly what will change before anything does.

**Acceptance Criteria:**

**Given** a connected v3-format repo (scaffolded templates, full-copy catalog)
**When** Sharko inspects it
**Then** the UI/API clearly report "v3 format — migration available" with a dry-run preview listing every file the migration PR will add, convert, or remove
**And** normal operations that would WRITE the old format are blocked with a plain "migrate to continue" message, while read views keep working.

### Story 5.2: The migration PR — templates out, pin in, catalog carried over as a full approved list *(rewritten 2026-08-08; originally "catalog to delta" — see the decision-history note at the top)*

As a v3 user,
I want one PR that converts my whole repo,
So that migration is a merge, not a project.

**Acceptance Criteria:**

**Given** a v3 repo that passed the preview
**When** I accept the migration
**Then** exactly one PR removes the scaffolded templates, adds the engine pin, converts assignment/values files to the Story 2.1 format, and carries the catalog over as a full approved list on the new format *(amended 2026-08-08)*
**And** the PR applies all-or-nothing; a merge conflict or failure leaves the repo untouched
**And** after merge, all addons keep running unchanged (verified on the playground).

### Story 5.3: Migration docs and the way back

As a careful v3 user,
I want honest migration docs including the rollback,
So that I can commit to the merge knowing my exit.

**Acceptance Criteria:**

**Given** the migration guide
**When** a v3 user follows it
**Then** it states plainly: what changes, what `git revert` restores, and that v3 keeps working until they choose to migrate
**And** the guide is verified against a real conversion on the playground.

## Epic 6: Takeover — safe brownfield migration

Bringing clusters ArgoCD already manages under Sharko without deleting anything.

### Story 6.1: Preflight backend — the four checks

As a platform engineer with a live fleet,
I want Sharko to check what could go wrong before I touch anything,
So that danger is found before the change, not after.

**Acceptance Criteria:**

**Given** a cluster ArgoCD manages that Sharko doesn't
**When** preflight runs
**Then** it reports: who owns the existing secret (tracking labels / ownerReferences), which ApplicationSets are not deletion-safe, the full list of Applications targeting the cluster, and any name collision
**And** it reads only well-defined metadata/spec fields — never user templates
**And** the chart adds exactly one new permission: read on `applicationsets`.

### Story 6.2: Preflight report — plain language, re-runnable

As the same engineer,
I want the preflight results in words I can act on,
So that "fix these three ApplicationSets first" is a task, not a research project.

**Acceptance Criteria:**

**Given** preflight findings
**When** I view them in UI or API
**Then** each finding says what it means and what to do about it in plain words
**And** I can re-run preflight after fixing and see it go green.

### Story 6.3: Takeover registration — same name, labels preserved, confirm gate

As the same engineer,
I want the swap to keep my cluster's identity and my old labels,
So that nothing referencing the cluster breaks and nothing prunes.

**Acceptance Criteria:**

**Given** a green (or acknowledged) preflight
**When** I confirm the takeover
**Then** Sharko deletes nothing before my explicit confirmation, then retires the old secret and creates its own with the same name, same server address, and all legacy labels preserved by default
**And** the legacy labels being carried are shown to me before the swap.

### Story 6.4: Drop the legacy labels when migration is done

As the same engineer, weeks later,
I want to remove the transition scaffolding with one action,
So that the cluster ends fully clean.

**Acceptance Criteria:**

**Given** a taken-over cluster whose addons have all moved to Sharko
**When** I choose "drop legacy labels"
**Then** the preserved labels are removed from Sharko's secret in one confirmed action
**And** the action warns me if any old ApplicationSet still selects on them.

### Story 6.5: Unregister with eyes open

As a platform engineer,
I want unregistering a cluster to tell me the consequences first,
So that removal is a decision, not an accident.

**Acceptance Criteria:**

**Given** a registered cluster (including one with preserved legacy labels)
**When** I request unregister
**Then** Sharko lists what will happen — including that old ApplicationSets selecting preserved labels may react — and requires explicit confirmation
**And** nothing is deleted before that confirmation.

### Story 6.6: The migration playbook

As every brownfield user,
I want the numbered recipe for moving a live fleet,
So that the safe path is written down, not tribal knowledge.

**Acceptance Criteria:**

**Given** the docs site
**When** the playbook chapter lands
**Then** it contains the numbered recipe (deletion-safety first, parity check, secret swap, per-addon adoption cutover, label drop, next cluster), the Helm-CLI takeover chapter, and the honest downtime statement
**And** "values are copy-paste" is stated early
**And** the playbook is verified against a real takeover run before release.

## Epic 7: Fleet Upgrades — the signature moment

See outdated addons across the fleet, upgrade a chosen subset with one reviewed PR.

### Story 7.1: The version matrix on the new data model

As a platform engineer,
I want to see which addon version runs where and what's newer,
So that "are we patched?" has an instant answer.

**Acceptance Criteria:**

**Given** clusters with per-cluster version pins (and some following catalog defaults)
**When** I open the matrix
**Then** it shows the actual per-cluster versions, the newest available, and "last checked"
**And** existing matrix views are re-pointed to the v4 data model with no stale v3 reads left.

### Story 7.2: Subset upgrade — one action, one PR

As a platform engineer,
I want to select which clusters to upgrade and get one reviewable PR,
So that a fleet CVE response is minutes, not a day.

**Acceptance Criteria:**

**Given** cert-manager outdated on 12 of 14 clusters
**When** I select those 12 and confirm the upgrade
**Then** one PR updates exactly those 12 per-cluster pins (small readable diffs)
**And** the preview shows every file before the PR opens
**And** clusters not selected are untouched.

### Story 7.3: The deterministic upgrade check on v4

As a careful engineer,
I want the pre-upgrade check working exactly as before on the new model,
So that impact and advisories inform the merge decision.

**Acceptance Criteria:**

**Given** a planned upgrade
**When** I run the check
**Then** version comparison, impact, and security advisories work against the v4 data files with no AI required
**And** the existing check endpoints are verified, not rebuilt.

### Story 7.4: The optional AI summary on v4

As a user with an AI key,
I want the plain-language upgrade summary still working,
So that the optional layer stays optional and honest.

**Acceptance Criteria:**

**Given** an AI key configured
**When** I request the summary on an upgrade check
**Then** it summarizes the already-computed result (never computes its own facts)
**And** with no key, the flow works identically minus the summary.

## Epic 8: Access & Robustness

The small hard promises: who can do what, and nothing breaks halfway.

### Story 8.1: Connections you can test

As a platform engineer,
I want to test each connection and see its health,
So that "Sharko is quietly broken" can't happen.

**Acceptance Criteria:**

**Given** configured git, ArgoCD, and vault connections
**When** I open connection settings
**Then** each shows current health and has a working "test" action with a plain-words result
**And** a failing connection surfaces in diagnostics, not just here.

### Story 8.2: Token lifecycle — expiry, renew, revoke

As a machine-door owner,
I want tokens that expire by default and can be renewed and revoked,
So that a leaked token is a bounded problem.

**Acceptance Criteria:**

**Given** the API token surface
**When** I create a token
**Then** it has a scope and a default expiry (documented), and I can list, renew, and revoke tokens via UI and API
**And** an expired or revoked token is refused with a clear error.

### Story 8.3: Login and sessions, verified

As a human user,
I want login and session expiry to work as documented,
So that the human door is as accountable as the machine door.

**Acceptance Criteria:**

**Given** the existing login
**When** the session exceeds its lifetime
**Then** it expires and requires re-login
**And** the behavior and defaults are documented.

### Story 8.4: The role gate audit

As the maintainer,
I want proof that every write action checks a role,
So that FR10's promise is verified, not assumed.

**Acceptance Criteria:**

**Given** the full list of write endpoints and AI write tools
**When** the audit runs
**Then** every write maps to an authz action with a required role, with a test asserting the mapping is complete
**And** the roles and their meaning are documented, including the "no fine-grained permissions in v4" limit.

### Story 8.5: One-time re-sync from the drift view

As an on-call engineer,
I want a "make it match git now" button,
So that I can fix drift once without turning on self-heal.

**Acceptance Criteria:**

**Given** a cluster showing label drift
**When** I trigger re-sync
**Then** Sharko re-applies its own addon-label keys once — nothing else — and the drift clears
**And** self-heal's setting is unchanged by this action.

### Story 8.6: The all-or-nothing audit

As the maintainer,
I want every file-reading path checked against the bad-input rule,
So that "never crash, never half-apply" is tested truth.

**Acceptance Criteria:**

**Given** the inventory of every file Sharko reads (git data files, catalog, settings)
**When** each is fed malformed input in tests
**Then** Sharko never crashes and never applies a partial result
**And** each error names file, reason, and line where the parser knows it
**And** gaps found are fixed within this story.

## Epic 9: The Re-Verification Sweep — everything still true

Every shipped promise re-proven under the new engine.

> Scope note (locked 2026-07-30): the standalone live-Gitea e2e story from the Gitea
> provider epic folds into this epic — the playground-based verification stories here
> already exercise the real Gitea PR loop, so they carry that coverage instead of a
> separate suite. Gitea support itself stays (playground backbone + supported provider
> per NFR17).

### Story 9.1: Kill-Sharko, re-proven on v4

As a skeptical adopter,
I want the clean-exit promise verified against v4 reality,
So that the headline guarantee stays true.

**Acceptance Criteria:**

**Given** a playground fleet running addons via the engine
**When** Sharko is deleted
**Then** ArgoCD keeps running everything, no data is stranded, and only the automation stops
**And** the kill-Sharko doc is updated with the v4 verification.

### Story 9.2: The watching stack under the new engine

As an on-call engineer,
I want drift, self-heal, diagnostics, events, and the dashboard verified on v4,
So that the eyes still see after the engine change.

**Acceptance Criteria:**

**Given** the v4 playground
**When** the verification matrix runs (induced drift, self-heal on/off, broken file, failure events, dashboard states)
**Then** each behaves per its FR: drift ≤30s with read-only diff, self-heal touches only own label keys, diagnostics name file/reason/line, events carry no secret values, dashboard reflects it all.

### Story 9.3: Door parity and traceability audit

As the maintainer,
I want proof the UI has no private powers and every change is traceable,
So that the API-first promise holds.

**Acceptance Criteria:**

**Given** the full UI action inventory
**When** the parity audit runs
**Then** every UI write maps to a public API endpoint (documented in the OpenAPI spec)
**And** sampled changes show the full trace: who, which door, which PR, what merged.

### Story 9.4: Addon-secrets engine and the no-secrets sweep

As a security-minded engineer,
I want the secrets engine verified on v4 and the no-secret-values rule re-swept,
So that the most sensitive promise is the most tested one.

**Acceptance Criteria:**

**Given** an addon with declared needed secrets on the playground
**When** the secret changes in the vault
**Then** the remote-cluster secret rotates within the stated window
**And** a sweep of logs, events, API responses, previews, and git files during the whole flow finds zero secret values.

### Story 9.5: The AI assistant on the v4 format

As a user with an AI key,
I want the assistant working correctly against the new data files,
So that the assistant doesn't lag the three doors. *(amended 2026-08-08 — the assistant is a client of the doors' shared pipeline, not a door)*

**Acceptance Criteria:**

**Given** the assistant's read and write tools
**When** they operate on a v4 repo
**Then** reads reflect the new file layout and writes produce correct v4-format PRs through the same pipeline
**And** the role gating and PR-only writes are re-verified
**And** with no key, nothing references the assistant.

## Epic 10: The Docs & Honesty Release

The words match the code, the supply chain matches the words.

### Story 10.1: Docs rewritten to the v4 identity

As every future reader,
I want the docs to describe the product Sharko now is,
So that learning Sharko means reading, not archaeology.

**Acceptance Criteria:**

**Given** the existing docs site
**When** the rewrite lands
**Then** identity, engine, catalog, migration, and takeover are documented as built; the README loses the jargon line and gains the ranked feature list; the comparison page (gitops-bridge, Sveltos, raw ApplicationSets) is live and honest
**And** quickstart leads with Kubernetes Secrets, not cloud-specific setup
**And** `mkdocs build --strict` passes.

### Story 10.2: The stated-limits and transparency page

As an evaluating engineer,
I want Sharko's limits on one honest page,
So that I find them from the docs, not from an incident.

**Acceptance Criteria:**

**Given** the docs site
**When** the page lands
**Then** it states plainly: single connection config (no multi-tenancy), single replica (no HA), coarse roles (no fine-grained permissions), git-at-scale guidance, and the AI transparency note (what leaves the cluster when a key is configured)
**And** each limit links to its roadmap status.

### Story 10.3: Supply-chain honesty — SBOM, signing, threat model

As a security reviewer,
I want the security claims to be mechanically true,
So that trust needs no favors.

**Acceptance Criteria:**

**Given** the release pipeline
**When** a release runs
**Then** an SBOM is actually generated and attached (or every SBOM claim is removed from the docs), images and the engine chart are cosign-signed, and the threat model lives in docs with a README link
**And** the claims-vs-code audit covers these claims explicitly.

### Story 10.4: arm64 restored

As a laptop and Graviton user,
I want multi-arch images that actually ship,
So that the playground and modern nodes just work.

**Acceptance Criteria:**

**Given** the release pipeline
**When** a release runs
**Then** amd64 and arm64 images are published and the playground runs on Apple-silicon without emulation
**And** the docs state the supported architectures.

### Story 10.5: Nothing half-alive — the scanner check

As the maintainer,
I want confirmation that no parked feature shows a face,
So that the "nothing half-alive" rule is verifiably met.

**Acceptance Criteria:**

**Given** the parked scanner code and the shelved operator
**When** the surface sweep runs (UI, API spec, docs, chart)
**Then** nothing user-visible references either
**And** anything found is removed within this story.

### Story 10.6: The claims-vs-code audit — the release gate

As the maintainer,
I want every documented claim tested against the code,
So that v4.0.0 ships with a 100% honest surface.

**Acceptance Criteria:**

**Given** the finished docs and the finished code
**When** the audit sweeps every documented feature, timing, and guarantee
**Then** 100% pass, or the claim is fixed or removed before the tag
**And** the audit result is recorded as the final Wave 3 checkpoint artifact.
