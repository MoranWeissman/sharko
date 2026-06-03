# Release Notes

<!--
Format for v2.x release-notes entries:
## vX.Y.Z — <theme>
### Breaking changes
### What's new
### Removed
### Security
### Bug fixes

Each bullet: one-liner summary + (PR #N) link. Detailed body lives in
the PR. Append new releases at the TOP of the v2.x stream so the most
recent release is the first thing readers see.
-->

## v2.0.0 — Production launch

**Status:** released 2026-06-03

### Breaking changes

No breaking changes — v2.0.0 is the first production release of Sharko. The v1.x development line was pre-production; v1.x installs should reinstall fresh per the [migration guide](operator/migration-v1-to-v2.md).

### What's new

- **Performance baselines + SLO targets per critical path** — p50 / p95 / p99
  measurements per phase per surface across cluster registration, addon
  cycle, catalog scan, and dashboard read paths; SLO targets + error
  budgets + burn-rate thresholds documented; a workflow_dispatch
  perf-baseline-refresh job and a comparator binary with `-emit` mode
  gate every PR against the committed baselines.
  (V2-1: PRs [#362](https://github.com/MoranWeissman/sharko/pull/362),
  [#363](https://github.com/MoranWeissman/sharko/pull/363),
  [#364](https://github.com/MoranWeissman/sharko/pull/364),
  [#365](https://github.com/MoranWeissman/sharko/pull/365))
- **100% slog logging with correlation IDs and sensitive-field redaction** —
  all internal callers migrated from stdlib `log` to `log/slog`;
  `request_id` propagated across middleware, reconciler, prtracker,
  orchestrator, and API handlers; a slog.Handler wrapper redacts tokens,
  kubeconfigs, and secret bodies before they hit any sink.
  (V2-2: PRs [#367](https://github.com/MoranWeissman/sharko/pull/367),
  [#368](https://github.com/MoranWeissman/sharko/pull/368),
  [#369](https://github.com/MoranWeissman/sharko/pull/369))
- **Prometheus telemetry for SLO surfaces** — histogram + counter
  exposition with V2-1.2-sized buckets, OpenTelemetry-conventional
  metric naming, exemplars carrying `request_id`, a Helm-shipped
  PrometheusRule template with multi-window multi-burn-rate alerting
  rules, and an operator runbook covering every alert.
  (V2-3: PRs [#371](https://github.com/MoranWeissman/sharko/pull/371),
  [#372](https://github.com/MoranWeissman/sharko/pull/372),
  [#373](https://github.com/MoranWeissman/sharko/pull/373))
- **CNCF foundation docs and GitHub config** — `MAINTAINERS`,
  `GOVERNANCE`, `CODE_OF_CONDUCT` (Contributor Covenant 2.1),
  `CONTRIBUTING`, `SECURITY`, and `ADOPTERS` at the repo root; DCO
  `Signed-off-by` enforcement; YAML issue templates (bug / feature /
  docs / security); GitHub Discussions enabled with a Roadmap input
  category.
  (V2-6 subset: PR [#366](https://github.com/MoranWeissman/sharko/pull/366))
- **Operator runbook coverage for the 57 inventoried failure modes** —
  runbook style guide + failure-mode index (57 modes: P0=12, P1=28,
  P2=12) shipped first; 35 new runbooks landed in 3 sequential PRs
  (12 P0 + 11 P1 Providers/Catalog + 14 P1
  API/Orchestrator/Reconciler/Webhook/AI/Adopt); a style-compliance
  refresh closed the gap on 9 existing pages. Every operator-facing
  failure mode in the P0+P1 tiers now has a Symptoms → Diagnosis →
  Mitigation → Root cause → Prevention runbook.
  (V2-4: PRs [#375](https://github.com/MoranWeissman/sharko/pull/375),
  [#376](https://github.com/MoranWeissman/sharko/pull/376),
  [#377](https://github.com/MoranWeissman/sharko/pull/377),
  [#378](https://github.com/MoranWeissman/sharko/pull/378),
  [#379](https://github.com/MoranWeissman/sharko/pull/379))
- **Clean cut from v1.x to v2.0.0** — V125-1-11 compat shim verified
  fully retired in production code (only test-file regression guards
  remain); a v1-to-v2 migration stub documents the reinstall path; the
  v2.0.0 release-notes scaffold replaces ad-hoc placeholders.
  (V2-5: PR [#374](https://github.com/MoranWeissman/sharko/pull/374))
- **Public roadmap + API stability contract** — a community roadmap
  page captures the v3+ trajectory (fine-grained RBAC, SSO,
  multi-ArgoCD, operator mode, rule-based auto-merge); an API
  stability page tiers all 128 endpoints (95 stable / 26 beta / 7
  alpha) with a deprecation policy (1 MINOR version lead time,
  `// Deprecated:` doc-comment + release-notes entry + WARN log +
  removal in subsequent minor).
  (V2-6.3: PR [#380](https://github.com/MoranWeissman/sharko/pull/380))
- **v2.0.0 threat model + 3rd-party security review bundle** — a
  STRIDE-per-trust-boundary threat model covering 6 primary boundaries
  × 6 STRIDE categories (36 cells), 40 mitigations (~95% citing
  V2-shipped artifacts), and 11 residual-risk gaps; a
  security-review-prep bundle ready for an external consultant
  (CNCF-coordinated or directly contracted). Disclosure SLO formalized:
  5 business days acknowledgment, 30-day HIGH fix, 90-day MEDIUM.
  (V2-6.5: PR [#381](https://github.com/MoranWeissman/sharko/pull/381))

### Removed

*No production compat shims to retire. v2.0.0 is the first production
release, so there is no prior production line to drop compat code for.
V2-5.1's compat-shim audit found zero production shims and zero
`// Deprecated:` comments in Go source — only test-file regression
guards from V125-1-11, which stay.*

### Security

- **Bootstrap admin credential no longer in structured logs** — the
  auto-generated bootstrap admin password is now displayed on stdout at
  first start (visible to operators watching `kubectl logs`) but is
  structurally absent from slog emissions. The
  `sharko-initial-admin-secret` Kubernetes Secret remains the
  authoritative retrieval path. Defense-in-depth: a regression test
  asserts the password field cannot appear in the structured-log buffer
  even if the V2-2.4 RedactHandler wrapper is bypassed in a future
  refactor.
  ([#382](https://github.com/MoranWeissman/sharko/pull/382))
- **STRIDE threat model published** — see the V2-6.5 entry under
  "What's new" for the full surface analysis, mitigation inventory,
  and residual-risk gap catalogue.
  ([#381](https://github.com/MoranWeissman/sharko/pull/381))

### Bug fixes

- **`internal/auth/store.go::MaybeLogBootstrapCredential` no longer
  emits the bootstrap admin password as a structured slog attribute.**
  See the entry under "Security" for the full fix shape and
  defense-in-depth regression contract.
  ([#382](https://github.com/MoranWeissman/sharko/pull/382))

---

## v1.25.0-pre.0 — Schema envelope + cluster reconciler + DX bundle (2026-05-21)

This pre-release ships two architectural epics that together make YAML the operational source of truth for cluster management: V125-1-9 introduces a schema envelope + JSON Schema + read-time validation + a `sharko validate-config` CLI; V125-1-8 ships the cluster reconciler that converges ArgoCD cluster secrets from the validated YAML. It also folds in the V125-1-10 / -11 / -13.x / -13.y provider + e2e fixes that landed on `main` between v1.23.0-pre.0 and now, plus the V126-* polish + DX bundle (BUG-033, DESIGN-01, DESIGN-02, e2e QoL, `sharko-dev.sh` upgrade subcommand). Chart bumped 1.19.0 → 1.25.0-pre.0 — the chart was stale relative to the tag stream; last shipped tag was `v1.23.0-pre.0`.

### Architectural epics

- **Schema envelope + JSON Schema + read-time validation + `sharko validate-config` CLI (V125-1-9, PR #346)** — `managed-clusters.yaml` and `addon-catalog.yaml` (renamed from `addons-catalog.yaml`) are now wrapped in an `apiVersion: sharko.io/v1` + `kind:` envelope. JSON Schemas (draft 2020-12) are generated by `go run ./cmd/schema-gen` and dual-written to `docs/schemas/*.v1.json` and `internal/schema/*.v1.json` (embedded for read-time validation). New `sharko validate-config <file|dir>` subcommand validates against the embedded schemas (accepts one positional path arg, `--quiet/-q` flag). New CI gates `schemas-up-to-date` + `validate-sharko-config` reject any PR with stale schemas or schema-violating YAML. Legacy unwrapped YAML still loads transparently (auto-detected by `internal/schema.IsEnveloped`). Migration runbook at `docs/site/operator/yaml-schema-migration.md`.
- **Cluster reconciler + `managed-by: sharko` ownership label (V125-1-8, PR #348, closes #257)** — new `internal/clusterreconciler/` stateless goroutine converges ArgoCD cluster Secrets from `managed-clusters.yaml`. Reconciler runs on a 30s safety-net cadence (`DefaultTickInterval`) plus an immediate post-merge trigger via `prTracker.SetOnMergeFn → recon.Trigger()` for sub-5s convergence. The `app.kubernetes.io/managed-by: sharko` label (helpers in `internal/argosecrets/labels.go` — `IsManagedBySharko`, `ApplyManagedBySharkoLabel`) is now the canonical ownership signal: V125-1-7 orphan-delete tightening and V125-2 Adopt distinction both key off it. Orphan / unlabeled Secrets refuse delete — operators bring pre-existing Secrets under management via the Adopt action. `internal/argosecrets/manager.go` exposes `BuildSecretConfigJSON` + `BuildClusterSecretLabels` as shared wrappers so the reconciler and the orchestrator emit identical Secret payloads. Operator runbook at `docs/site/operator/cluster-reconciler.md`.

### Provider + infra (interim sprints folded in)

- **V125-1-10 — `ArgoCDProvider` in-cluster auto-default + Provider.Namespace cross-contamination fix (PR #327 + #328 hotfix bundle).** ArgoCDProvider now auto-defaults to in-cluster mode when Sharko runs in a Pod, and reads `SHARKO_ARGOCD_NAMESPACE` directly with an `slog.Warn` flagging the overloaded `Namespace` field that drove the V125-1-11 typed split.
- **V125-1-11 — Typed ProviderConfig split into three orthogonal types** (`providers.AddonSecretProviderConfig`, `providers.ClusterTestProviderConfig`, `providers.ClusterRegSourceProviderConfig`). The old monolithic `providers.ProviderConfig` and `providers.New` / `providers.NewSecretProvider` factories are retired. Cross-domain leakage (e.g. argocd-namespace flowing into an addon-secret provider) is now a compile error. `SHARKO_ARGOCD_NAMESPACE` env var is still honoured with a deprecation warning; canonical replacement is `clusterTest.argocdNamespace` in Helm values.
- **V125-1-13.x — In-cluster gitfake Pod + env-gated git-host allowlist** (`SHARKO_GIT_ALLOWLIST_HOSTS`). The e2e harness now ships an in-cluster fake git server so helm-mode tests can run without network egress to a real git host; the allowlist env var is opt-in for tests and never set in production deploys.
- **V125-1-13.y — In-process e2e mock-provider wiring + tiered_git override bypass fix.** `make test-e2e-fast` (~30s, no kind) is now the default fast loop; `make test-e2e` remains the kind-backed full suite. The tiered_git override path no longer bypasses the per-request PAT attribution check.

### Bug fixes + UX

- **BUG-033 (V126-1, PR #338)** — UI cluster status flips to `connected` after registration. Previously the UI surfaced a stale `unknown` until manual refresh.
- **DESIGN-01 (V126-2, PR #340)** — Bootstrap ships with an empty `addon-catalog.yaml` by default. Previously the catalog shipped with cert-manager / external-secrets / metrics-server pre-configured; new installs now start clean and operators install from the Marketplace as needed.
- **DESIGN-02 (V126-3, PR #341)** — Catalog tile shows a **Running on N/M clusters** badge; the "Installed" tab was renamed to **Catalog** to reflect that the tab lists every catalog entry, not just installed ones.

### Developer experience

- **V126-4 (PR #344)** — e2e harness heartbeat + cleanup hardening + Git URL validator explicit-fields override. The harness now emits a heartbeat line during long-running setup so CI logs make it obvious things are progressing; cleanup runs even when a test fails mid-setup. The Git URL validator accepts explicit-field overrides for tests that need to exercise specific host shapes.
- **V126-5 (PR #343)** — `scripts/sharko-dev.sh upgrade <version>` subcommand for testing a specific chart version against a local kind cluster (with no-arg form falling back to `charts/sharko/Chart.yaml`). New `ready` preflight resource check that classifies cluster state (`ready` / `up` / `install`) before running, plus `--force-clean` flag for CI / unconditional wipe.

### Migration / upgrade notes

- **Schema envelope (V125-1-9):** Existing 1.19.x / 1.24.x configs (unwrapped YAML) auto-detect as legacy and continue to load. Recommended pre-upgrade step: run `sharko validate-config configuration/` to surface any latent shape issues before they hit a reconciler. `addons-catalog.yaml` was renamed to `addon-catalog.yaml`; the loader accepts both names during the transition.
- **Cluster reconciler (V125-1-8):** Transparent. No operator action required. Existing ArgoCD cluster Secrets continue to work; the reconciler will start managing them once they carry the `app.kubernetes.io/managed-by: sharko` label (set automatically on next mutation, or via the Adopt action for pre-existing Secrets). Orphan / unlabeled Secrets refuse delete — this is intentional and prevents the reconciler from accidentally pruning Secrets created by another tool.
- **Typed provider configs (V125-1-11):** `SHARKO_ARGOCD_NAMESPACE` env var is still honoured with a deprecation warning. Migrate to `clusterTest.argocdNamespace` in Helm values when convenient; removal is slated for v1.26.
- **No breaking changes** in this release. The chart-version jump (1.19.0 → 1.25.0-pre.0) reflects sprint-stream catch-up, not a breaking surface change.

Refs: `.bmad/output/planning-artifacts/release-v1.25.0-pre.0.md`.

---

## v1.24 — Polish & Hotfix Bundle (merged to main; not formally tagged, 2026-05-13)

v1.24 is a maintainer-discipline release: no new user features, but two months of bugs found in real end-to-end smoke passes (Track A on the published `1.23.0-pre.0` image, Track B against a `kind` + ArgoCD stack) got fixed in one consolidated bundle that landed on `main` via PR #319 and a small follow-up sweep (PR #323). A `v1.24.0-pre.0` changelog entry was prepared (`V124-21`) but the tag was never cut — the bundle ships as part of `main` and is what every release after `v1.23.0-pre.0` is built on.

### Highlights

- **Cluster-list endpoint stops 500-ing on fresh installs** — `GET /api/v1/clusters` now returns `200 {"clusters":[]}` when `configuration/managed-clusters.yaml` is absent, and a class-of-bug sweep across `clusters`, `dashboard`, `addons`, `connections`, and `notifications/providers` handlers replaces raw `err.Error()` leakage in 5xx bodies with sanitized `{"error":"<status text>","op":"<op>"}` shape (V124-2.2, V124-2.10, V124-23). Upstream-error paths are also reclassified into 502 / 504 / 429 instead of a flat 500 (V124-3.2).
- **CLI usable inside the official container** — Dockerfile now sets `ENV HOME=/home/sharko` with a writable `~/.sharko`; the Go fallback to `os.TempDir()` is hardened with a TOCTOU / symlink guard that refuses an existing fallback dir owned by a different uid or pointing through a symlink, with `SHARKO_CONFIG_DIR` as the override (V124-2.5, V124-2.11).
- **`sharko login` no longer wedges the parent shell in raw mode** — `readPasswordSafe` was refactored behind an injectable terminal interface so `term.Restore` is guaranteed on every exit path including `GetState` failure; covered by behavioural tests asserting the restore call order (V124-2.6, V124-2.13).
- **Friendly CLI errors** — connection-refused / no-such-host on `sharko login` now reads "is Sharko reachable at `<url>`?" instead of raw `dial tcp ... connect: connection refused`, and case-insensitive `ECONNREFUSED` detection plus realistic `*url.Error` fixtures keep the helper honest (V124-2.4, V124-3.99 L4 / L5).
- **Auto-merge actually auto-merges** — per-request `auto_merge` flag was being silently dropped across `register` / `adopt` / `init` / `update`; the contract is now honoured and merged branches are auto-deleted (BUG-031, BUG-032). The UI-side bug sweep (BUG-034 / 035 / 036 / 038 / 039) cleans up "ArgoCD Connection Failed" copy when status is `unknown`, the "no credentials provider configured" 503 message in no-Vault dev installs, the false "addon deployed" toast on PR-opened-only, the host (management) cluster appearing as a connected observability target, and the missing `yes:true` confirmation flag on UI "Remove cluster".
- **Login footer shows the live build version** instead of a hardcoded `v1.0.0` string (V124-2.1).
- **Maintainer DX: `sharko-dev.sh` framework** — one entry point with subcommands `dev-rebuild`, `argocd-token`, `ready`, `pf`, `reset-admin` (V124-5, V124-7, V124-8, V124-9, V124-12, V124-13). `reset-admin` now rotates the `sharko-initial-admin-secret` rather than just deleting it (BUG-025); `ready` prints full credential values including token + admin password; `argocd-token` keeps its port-forward running so subsequent ready/UI access doesn't time out; `pf` is a standalone port-forward subcommand. Empty-repo bootstrap detects the GitHub 409 instead of cryptically failing (BUG-029). `git.provider` is auto-derived from the URL host so operators don't need to set it twice (BUG-028).
- **Bootstrap & wizard hardening** — wizard step 4 now surfaces app-name and sync-timeout failures, 401s are surfaced explicitly, idempotent retries are treated as success (BUG-031..036). Escape hatches + back-navigation in resume mode (BUG-035..038). Bootstrap root-app path constant + drift guard (BUG-045). `/repo/status` reports `bootstrap_synced` and the UI opens the wizard when bootstrap is unhealthy (BUG-046). Three handler missing-file 500-leaks closed (BUG-047, 048).
- **Write-endpoint validation hotfix** — `connections` POST/PUT require `name` + `provider`; `addons` validate required fields before the upstream Helm call; `notifications/providers` no longer silent on empty body; `clusters/discover/secrets` returns 503 instead of 501 when a credentials provider is missing (V124-4.1..4.5, BUG-017..020).
- **`isGitFileNotFound` no longer string-matches** — the helper used to treat any error containing `"not found"` or `"404"` as an empty cluster list, silently masking auth / branch / permission failures. Replaced with `errors.Is(fs.ErrNotExist)` + a typed `gitprovider.ErrFileNotFound` sentinel, with regression tests for false-positive strings like `"GitHub repository not found — check the URL and credentials"` and `"got 4040 bytes"` (V124-2.12).
- **Comprehensive Go e2e suite (V2 Epic 7-1)** — 15 stories worth of `kind`-based multi-cluster harness, in-memory git fixture, API + GH-REST mock, and end-to-end coverage for cluster lifecycle, catalog + marketplace, addon admin + secrets, per-cluster addon lifecycle, values editor + AI, init + connections, auth + tokens + RBAC, AI + agent + providers, dashboard + observability, and PRs + notifications. Replaces the prior `smoke.sh` Phase 6 + 7. Ships with coverage HTML + JUnit reports and a GitHub Actions workflow.

### Operator impact / migration

No breaking changes. Two behaviours are worth noting:

- **`SHARKO_CONFIG_DIR`** is now respected as the canonical override when running in a HOME-less environment (containers without `$HOME`, locked-down hosts where `/tmp` is shared). If the fallback dir already exists and is not owned by the running uid, the CLI now refuses to write to it and asks you to set this env var.
- **5xx response bodies changed shape.** The `{"error":"<raw-Go-error>"}` body that leaked filesystem paths and provider internals is replaced with `{"error":"<HTTP status text>","op":"<operation>"}`. Any client that grepped on the raw error string needs to read the operation field instead.

## v1.23.0-pre.0 — Catalog Extensibility (2026-04-30)

v1.23 turns v1.21's embedded curated catalog into an extensible one. Three opt-in extensibility paths ship together: third-party private catalog sources merged under the embedded set, per-entry cosign-keyless signing with a configurable trust policy, and a nightly trusted-source scanner bot that opens PRs against `catalog/addons.yaml` for CNCF Landscape and AWS EKS Blueprints. Four epics, twenty stories, four throwaway `-rc.N` tags absorbed production-only failures (TUF cache path, Sigstore bundle format, trust-regex SAN encoding, GoReleaser dirty-tree check) before the user-visible `v1.23.0-pre.0` cut.

### Highlights

- **Third-party private catalogs** — `SHARKO_CATALOG_URLS` (comma-separated HTTPS) configures additional catalog sources. `SHARKO_CATALOG_REFRESH_INTERVAL` (default 1 h) controls the fetch cadence. Each source is fetched, schema-validated, and merged **under** the embedded catalog; embedded always wins on `name` collision. Fetch failures are non-fatal — the last-successful snapshot is retained and `status: stale|failed` surfaces in the sources API. In-memory only — no disk / ConfigMap persistence (stateless principle preserved). Standard `HTTP_PROXY` / `HTTPS_PROXY` env honoured. Runtime SSRF guard pins outbound fetches away from loopback, RFC1918, link-local, and cloud-metadata IPs (V123-1.1 / 1.2 / 1.3).
- **Source attribution on every entry** — `catalog.Entry.source` is `"embedded"` or the full source URL. The `GET /api/v1/catalog/addons/<name>` response includes it; Browse tiles and the detail page render a source badge (Internal / `<URL-host>`) with the full URL + last-fetched timestamp in the tooltip (V123-1.4, V123-1.7).
- **New catalog-sources API** — `GET /api/v1/catalog/sources` returns `[{url, status, last_fetched, entry_count, verified}]` (embedded source always reports `ok`). `POST /api/v1/catalog/sources/refresh` (Tier 2, audited as `catalog_sources_refreshed`) forces a synchronous re-fetch of every configured source. Both endpoints are swagger-annotated and ship in `docs/swagger/` (V123-1.5, V123-1.6).
- **Admin-only Settings → Catalog Sources** — read-only view from `GET /api/v1/catalog/sources` with status chips and a refresh button calling the Tier 2 endpoint. Visible to admins only; honours the v1.22 a11y bar (V123-1.8).
- **Catalog schema v1.1: per-entry `signature:` field** — each entry can carry an optional `signature: {bundle: <URL>}`. v1.0 catalogs continue to load (backward-compatible); v1.21 / v1.22 binaries reading a v1.1 catalog tolerate the new field as "unknown" (V123-2.1).
- **Cosign-keyless verification on load** — when an entry has a `signature.bundle`, Sharko fetches the Sigstore bundle and verifies it against the entry's canonical YAML serialization using the `sigstore-go` library (no shelling out to the `cosign` CLI). The in-memory entry gets `verified: true|false|"signature-mismatch"|"untrusted-identity"` and `signature_identity: <OIDC issuer/subject>`. Verification runs on fetch, not on every reload (resolves design OQ §7.2). Subsystem A holds only a `SidecarVerifier` interface — Subsystem B owns the implementation and the boundary is enforced (V123-2.2, V123-1.2 sidecar wiring).
- **Configurable trust policy** — `SHARKO_CATALOG_TRUSTED_IDENTITIES` accepts a comma-separated regex list. Secure defaults cover CNCF workflow identities (`https://github.com/cncf/.*/.github/workflows/.*`) and Sharko's own release workflow (`https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/(tags|heads)/.*`). Custom regexes can be appended without losing the defaults (V123-2.3).
- **UI Verified badge + Signed-only filter** — tiles render a green "Verified — `<issuer>`" chip when `verified: true`, a neutral "Unsigned" chip when missing a signature, and a warning chip on mismatch / untrusted-identity with a tooltip linking to the operator trust-policy doc. A new `Signed`-only pseudo-filter lives in the Marketplace filters sidebar. WCAG AA contrast satisfied (V123-2.4).
- **Release pipeline signs every embedded entry** — a new `sign-catalog-entries` job in `release.yml` runs after build, signs each entry in `catalog/addons.yaml` with cosign keyless via GitHub OIDC, and materializes signatures into the same YAML for the release commit; signatures attach as `catalog-signatures/` release assets. A v1.23 binary loading the released catalog shows `verified: true` against the default trust policy (V123-2.5). The release pipeline also auto-flags pre-release tags via `prerelease: auto` so debug `-rc.N` tags do not steal "Latest release" on GitHub.
- **Trusted-source scanning bot** — `scripts/catalog-scan.mjs` is a Node ESM framework with per-source plugins under `scripts/catalog-scan/plugins/`. Two plugins ship: **CNCF Landscape** (filters by category + maturity = graduated|incubating, Helm-only, proposes adds / updates) and **AWS EKS Blueprints** (enumerates `lib/addons/*` in `aws-quickstart/cdk-eks-blueprints`). `.github/workflows/catalog-scan.yml` runs daily at `0 4 * * *` via `GITHUB_TOKEN`; never auto-merges, exponential-backoff on 429 / 5xx, and opens **one** branch `catalog-scan/<YYYY-MM-DD>` with labels `catalog-scan` + `needs-review`. PR body contains a markdown table per proposed change with pre-computed Scorecard, license allow-list, and chart resolvability signals (V123-3.1..3.4).
- **Operator + developer docs** — `operator/catalog-trust-policy.md` documents the env, default regex list, and manual `cosign verify-blob` commands; `operator/supply-chain.md` updated with the new release-pipeline per-entry signing section. `developer-guide/catalog-scan-runbook.md` + `developer-guide/catalog-scan-plugins.md` cover reviewer + plugin-author workflows. User-facing pages document `SHARKO_CATALOG_URLS` and the Verified / Unsigned / mismatch chip semantics (V123-3.5, V123-4.1, V123-4.2, V123-4.3).
- **Security hardening** — runtime SSRF guard on the third-party fetcher with `DialContext` IP pinning + redirect re-check (M6 + L1). The signing path was reviewed adversarially by `security-auditor` covering canonical serialization, bundle fetch error paths, trust-policy regex injection, and the release-pipeline signing step (V123-4.4).

### Operator impact / migration

- **Backward-compatible.** Without `SHARKO_CATALOG_URLS` set, Sharko serves only the embedded catalog exactly as v1.22 did. Without `SHARKO_CATALOG_TRUSTED_IDENTITIES` set, the default trust policy is used and embedded entries verify automatically because the release pipeline signs them.
- **Third-party catalog bytes are NOT persisted.** A pod restart re-fetches every configured URL. Operators are expected to point at stable HTTPS endpoints.
- **`POST /api/v1/catalog/sources/refresh`** is Tier 2 — it requires the user-PAT or per-user attribution model from v1.20.
- **`v1.23.0-pre.0`** is the only v1.23 tag that exists. No `v1.23.0` GA tag was cut — the bundle ships as the production-ready pre-release and main moved on to v1.24.

## v1.22 — Marketplace Polish (merged to main 2026-04-21; not formally tagged)

v1.22 is the marketplace-polish release that closes the four carryover items v1.21's §9 "Out of scope" flagged: WCAG 2.1 AA retrofit on the pre-v1.21 pages, multi-arch Docker images, an audit-log retention banner + operator runbook, and real screenshots embedded into the RTD site. Four epics, ten stories. The bundle landed on `main` via PR #265 (`dev/v1.22 → main`); a `v1.22.0` tag was never cut — the bundle ships as part of `main` and is what `v1.23.0-pre.0` was built from.

### Highlights

- **WCAG 2.1 AA retrofit of v1.20 pages** — new `ui/src/__tests__/a11y-v120-pages.test.tsx` axe-core suite covers AddonDetail, ClusterDetail, Settings, AuditViewer, and Connections under the `wcag2a / wcag2aa / wcag21a / wcag21aa` rule sets. All five pages pass with zero serious-or-critical violations. The AuditViewer filter form picked up `id` + `htmlFor` on every label / input pair to clear the `select-name (critical)` violation; the other four pages were already clean (V122-1).
- **Multi-arch Docker image (`linux/amd64` + `linux/arm64`)** — `release.yml` now invokes `docker/setup-qemu-action@v3` and runs `docker/build-push-action@v6` with `platforms: linux/amd64,linux/arm64`. Operators on Graviton / Ampere / arm64 dev machines can `docker pull ghcr.io/moranweissman/sharko:<tag>` and receive the arm64 variant automatically. The Go binary stays native (`CGO_ENABLED=0`); qemu cost is paid by the Alpine final-stage only (V122-2.1).
- **Single cosign signature covers all arches** — keyless OIDC signing runs once against the manifest digest; per-arch verification works via the existing `cosign verify ghcr.io/moranweissman/sharko:<tag>` command with no change to the operator-facing verify flow. `operator/supply-chain.md` documents the multi-arch publishing + the `docker buildx imagetools inspect` command (V122-2.2).
- **Audit-log retention banner** — the Audit Log page renders a persistent, non-dismissible info banner explaining the stateless two-stream audit architecture: an in-memory ring buffer (last 1000 events, reset on pod restart, backs the UI/SSE) plus structured JSON to stdout for the cluster log pipeline (persistent). The banner links to the new operator retention guide. The AuditViewer page heading was also promoted from `<h2>` to `<h1>` so the route has exactly one h1 (V122-3.1).
- **New `operator/audit-log.md` retention guide** — explains the stateless-by-design choice and provides copy-paste setups for Loki (promtail / Grafana Agent), Splunk (HEC or Splunk OTel Collector), ELK (Filebeat), CloudWatch (aws-for-fluent-bit DaemonSet), and GCP Logging (Cloud Logging agent DaemonSet). Wired into `mkdocs.yml` under Operator Manual (V122-3.2).
- **Real screenshots embedded across the docs site** — a Playwright generator (`scripts/docs-screenshots.mjs`) drives a headless chromium against a `make demo` instance with deterministic fixtures and captures five canonical screenshots: `dashboard.png`, `marketplace-browse.png`, `marketplace-detail.png`, `cluster-detail.png`, `audit-log.png`. The five PNGs are committed under `docs/site/assets/screenshots/` (each under 1 MB) and embedded into the landing page hero, `user-guide/marketplace`, `user-guide/dashboard`, `user-guide/clusters`, and `operator/audit-log` (V122-4).
- **v1.21.8 follow-up fixes shipped on the same bundle** — RBAC grants `nodes` get/list so `/api/v1/cluster/nodes` returns 200; the per-cluster addon-overrides editor no longer remount-storms on the Config tab when an addon is toggled.

### Operator impact / migration

No breaking changes. Two operational notes:

- **Multi-arch publishing** is opt-out only — pulling `ghcr.io/moranweissman/sharko:<tag>` on an amd64 host still gets the amd64 image. Operators on legacy infrastructure (e.g. CI runners pinned to a specific image digest) should verify the new manifest list with `docker buildx imagetools inspect`.
- **Audit history reset on pod restart is by design.** The new retention guide is the canonical reference — if your cluster log pipeline isn't capturing Sharko's stdout, audit history is lost on every rollout. Follow the copy-paste setup matching your stack.

## v1.21.0 (in progress) — Bundle 5: Unwrap global values

Bundle 5 fixes a long-standing correctness bug: Sharko's smart-values writer was wrapping every global values file under an `<addon>:` root key, but the ApplicationSet template passes that file directly to Helm via `valueFiles:` — so Helm looked for chart values at the document root and silently ignored every value the user had set.

### Fixes

- **Smart-values writer (`internal/orchestrator/smart_values.go`)** — global file is now written with the chart's keys at the top level. The smart-values header still lives at the top; the per-cluster template block at the bottom stays wrapped under `<addon>:` (intentional — that block is meant to be copy-pasted into the per-cluster file, which IS namespaced).
- **Bootstrap seed templates** — `templates/bootstrap/configuration/addons-global-values/{cert-manager,external-secrets,metrics-server}.yaml` are now in the new shape.
- **Preview-merge endpoint (`POST /addons/{name}/values/preview-merge`)** — both `current` and `merged` are returned in unwrapped shape; if the user's existing file is still legacy-wrapped, it's transparently unwrapped before the diff so the comparison is apples-to-apples.

### New

- **Migration endpoint** — `POST /api/v1/addons/unwrap-globals` (Tier 2). Walks every file under `configuration/addons-global-values/`, detects legacy wraps (single non-comment top-level key matching the addon or chart name), unwraps them in place, stamps a `# Migrated from legacy wrapped format on <date>` header note, and opens ONE PR. Pass `?addon=<name>` to scope the migration to a single file. Idempotent — running it again with no wrapped files left returns 200 with `{migrated: 0, message: "all files already unwrapped"}` and does not open a PR.
- **Detection on `GET /api/v1/addons/{name}/values-schema`** — new `legacy_wrap_detected: bool` field. The Values tab renders a yellow migration banner with a **Migrate this file** button when this fires.
- **Pure helper** — `orchestrator.UnwrapGlobalValuesFile(yamlContent, addonName, chartName) ([]byte, bool, error)`. Comment- and ordering-preserving; covered by unit tests for wrapped, already-unwrapped, multi-key, wrong-root-key, comment-only, and inline-comment cases.

### Backwards compatibility

- **Read path** — legacy wrapped files still parse correctly; the editor renders them and just flags them for migration.
- **No breaking API changes** — additive: new field on `values-schema`, new endpoint.
- **Migration is opt-in** — existing files keep working as before (Helm still ignores them, exactly as today). Users migrate on their own schedule via the banner.

## v1.21.0 — Catalog & Discovery

v1.21 turns Sharko from "you bring the chart, we wire it up" into a **discoverable platform**: a curated catalog of 45 vetted Helm addons with OpenSSF Scorecard signals, a server-side ArtifactHub search overlay, smart values seeding (heuristic + optional LLM), and a hardening pass that signs every release artifact with cosign and adds an SSRF guard to every URL-fetching endpoint.

Nine epics, 37 stories, all CI green.

### Headline features

#### Curated marketplace (Epics V121-1, V121-2, V121-3, V121-5)

- **45 curated addons** ship in the binary as an embedded YAML catalog (`catalog/addons.yaml`). Sourced from CNCF graduates, AWS EKS Blueprints, Bitnami baseline picks, and a small set of vendor-curated charts.
- **Browse subtab** — filter by category, curator (`cncf-graduated`, `aws-eks-blueprints`, …), license, and OpenSSF tier (Strong / Moderate / Weak). Filters persist in the URL for shareable deep-links.
- **Search ArtifactHub subtab** — server-side proxy to ArtifactHub with a 10-minute LRU cache. Curated and external results render in two stacked sections; an unreachable ArtifactHub falls back gracefully to curated-only with a one-click Retry banner.
- **In-page detail view** — clicking a card swaps the grid for a hero + Add-to-catalog form + upstream README + footer. Replaces the popup-style Configure modal that QA flagged as confusing.
- **In-catalog badge** — cards for addons already in your catalog show a badge with a quick link, so you don't open a no-op PR.
- **Add to catalog** — the Submit button reuses `POST /api/v1/addons` and the v1.20 tiered Git plumbing. PR opens against `addons-catalog.yaml` plus a generated values file (smart-values pipeline below).

See [User Guide → Marketplace](user-guide/marketplace.md).

#### Smart values seeding (Epic V121-6)

When you add an addon, Sharko pre-fetches the chart's upstream `values.yaml` for the version you picked and runs a deterministic split:

- Cluster-specific fields (`*.host`, `*.replicaCount`, `*.resources.*`, `*.nodeSelector`, `*.externalSecret*`, …) are commented out at their original position.
- A fully commented per-cluster template block is appended at the bottom — ready to paste into a cluster's overrides file.
- A self-describing header (`# Generated by Sharko from <chart>@<version> on <date>` + `# sharko: managed=true`) is stamped on the front so Sharko knows when to surface the version-mismatch banner.

When you enable the addon on a new cluster, Sharko reads the template block and seeds the cluster's overrides file with the same fields uncommented — no copy-paste required. See [User Guide → Smart Values Layer](user-guide/smart-values.md).

A **version-mismatch banner** appears on the Values tab when the catalog version moves ahead of the values-file version, with a one-click **Refresh now** that calls `PUT /api/v1/addons/{name}/values?refresh_from_upstream=true`.

#### AI annotate with secret-leak guard (Epic V121-7)

When you have an AI provider configured (Settings → AI), Sharko adds a second pass on top of the heuristic splitter:

- **Inline `# description` comments** above non-trivial scalar fields, generated by the LLM. Existing chart comments are preserved verbatim.
- **Additional cluster-specific paths** detected by the LLM are unioned with the heuristic — never subtracted.
- **Per-addon opt-out** via `# sharko: ai-annotate=off` directive in the values-file header.
- **Hard secret-leak block** — a regex pre-scan over every `values.yaml` looks for AWS keys, GitHub PATs, JWTs, Google API keys, Slack tokens, PEM private keys, and high-entropy 16+ char generic `apiKey` / `password` / `token` assignments. On any match, the LLM call is hard-blocked. There is no override.
- **Token + latency caps** — 50 KB input cap, 30 s latency cap. Both fall through to heuristic-only output without failing the Add Addon call.
- **Metrics** — `sharko_ai_annotate_total{outcome}` and `sharko_ai_annotate_latency_seconds{outcome}` exposed on `/metrics`.

See [Operator Manual → AI Configuration](operator/ai-config.md).

#### Tiered attribution + per-user PAT (continued from v1.20)

Every mutating endpoint is classified as **Tier 1** (operational, service token) or **Tier 2** (configuration, prefer per-user PAT). v1.21 keeps the model and audits attribution mode (`service` / `co_author` / `per_user`) on every entry. New mutating endpoints added in v1.21 (catalog reprobe, AI annotate trigger) are wired to the registry — `TestTierCoverage` and `TestAuditCoverage` continue to gate PRs.

See [User Guide → Git Attribution](user-guide/attribution.md) and [Operator Manual → Security § Tiered Git Attribution](operator/security.md#tiered-git-attribution-v120).

#### Cosign keyless on release artifacts (Story V121-8.1)

Starting with v1.21.0, every release artifact is cosign-signed via GitHub Actions OIDC (keyless):

- Container image (`ghcr.io/moranweissman/sharko:vX.Y.Z`)
- Helm OCI chart (`oci://ghcr.io/moranweissman/sharko/sharko:X.Y.Z`)
- GitHub release archives (`sharko_X.Y.Z_<os>_<arch>.tar.gz` + `checksums.txt`)

A CycloneDX SBOM is published as a release asset.

Verification commands and rationale are in [Operator Manual → Supply Chain](operator/supply-chain.md). The certificate identity is bound to `.github/workflows/release.yml` in this repo so a valid signature proves the artifact came from a real release run.

#### SSRF guard on URL-fetching endpoints (Story V121-8.2)

Every endpoint that fetches from a user-supplied URL — currently `GET /api/v1/catalog/validate` for manual Add Addon repo validation — runs through a built-in SSRF check. The guard rejects URLs resolving to:

- Loopback (`127.0.0.0/8`, `::1`)
- RFC1918 private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`)
- Link-local (`169.254.0.0/16`, `fe80::/10`) — including cloud-provider metadata services
- IPv6 ULA (`fc00::/7`)
- Multicast / unspecified

Optional `SHARKO_URL_ALLOWLIST` env var pins outbound fetches to a fixed hostname set for higher-assurance deployments. Blocks return `error_code: "ssrf_blocked"` so the UI's switch table doesn't need to branch on HTTP status.

See [Operator Manual → Security § SSRF guard](operator/security.md#ssrf-guard-on-url-fetching-endpoints).

#### OpenSSF Scorecard daily refresh (Story V121-8.3)

A background goroutine refreshes Scorecard data at 04:00 UTC against `https://api.scorecard.dev` for every catalog entry whose `source_url` is on GitHub. Failures are non-fatal — entries keep their last-known score. Operators can monitor via:

- `sharko_scorecard_refresh_total{status}` — counter partitioned by `ok` / `error` / `skipped`.
- `sharko_scorecard_last_refresh_timestamp` — epoch seconds of the most recent refresh.

#### Unified addon state model (v1.21 QA bundle)

Every UI surface that shows an addon's status (Dashboard, Cluster Detail, Addon Detail, Marketplace) reads from a single `useAddonStates` cache. Five display states: `healthy`, `progressing-advisory`, `degraded`, `missing`, `unknown`. The Dashboard splits `Progressing` out of the red "Apps with issues" widget into its own blue panel — fixes the over-stated urgency v1.20 carried.

See [User Guide → Dashboard](user-guide/dashboard.md).

#### Pending + Merged PRs view (v1.21 QA bundle)

The Dashboard's PR panel adds a **Merged** tab alongside **Pending**. Merged shows recently-merged PRs from the GitOps repo with author and merged-at timestamp; backed by a 60 s server cache to keep the GitHub API call cost bounded. The current tab is preserved in the URL (`?prs_state=merged`) for shareable deep-links.

#### WCAG 2.1 AA on new UI (Story V121-8.4)

All v1.21-new UI surfaces (Marketplace Browse, Search, in-page detail, Smart Values banners) target **WCAG 2.1 AA** — keyboard navigation, focus rings on every interactive element, semantic landmarks, and contrast ratios that pass `axe-core` with zero violations. The reference test lives at `ui/src/__tests__/a11y.test.tsx`. Existing pages predate the target and are tracked for a v1.22 retrofit.

### Migration / breaking changes

Three behaviors changed in ways an existing operator will notice. None require a config file edit — the breaking surface is API-only or UI-only.

- **Removed: `POST /api/v1/addons/{name}/values/pull-upstream`.** The v1.20.1 endpoint is gone. Replacement: `PUT /api/v1/addons/{name}/values` with `{"refresh_from_upstream": true}` in the body. The Refresh now button on the version-mismatch banner uses the new path. CLI consumers and external automation calling the old path will get 404 — update to the new path.
- **Removed: Marketplace Paste Helm URL tab.** The third Marketplace subtab from early v1.21 builds was retired in QA. Replacement: the manual **Add Addon** button on the Catalog tab now auto-validates the repo URL and lists the available chart names — covering the same use case (chart not in our catalog and not on ArtifactHub) without a separate UI surface.
- **Removed: Sync Wave field in Add Addon form.** The Sync Wave input is no longer on Add Addon (Marketplace or manual). Sync wave is set on the **ArgoCD App Options** tab on the Addon Detail page after the PR merges. This matches how every other ArgoCD application option is edited and avoids the asymmetry of one option being settable at create time.

### Quality signals

- 9 epics, 37 stories — every story passed `go build`, `go vet`, `go test`, `npm run build`, `npm test` before merge into the bundle.
- `TestTierCoverage` and `TestAuditCoverage` green throughout — every mutating endpoint added in v1.21 was registered in the tier registry and audit allowlist before the handler was merged.
- Forbidden-content grep clean across the entire bundle.
- Per-PR Docker images built and tested for every story (`pr-docker.yml`).

### What's not in v1.21

Tracked for a future release; explicitly out of scope per design §9:

- Third-party / private catalog repositories — single curated catalog in main repo for v1.21.
- Automated source scanning of upstream `values.yaml` for CVEs / leaked credentials at catalog-maintenance time.
- Non-Helm addons (raw manifests, Kustomize, OLM operators).
- Per-entry cosign signatures on catalog entries — release-level only.
- WCAG AA retrofit on existing pages (Dashboard, ClusterDetail, AddonDetail, Settings) — v1.22 backlog.
- Bundle / composition flows ("install prometheus + grafana + loki as a stack") — v1.22 candidate.
- Telemetry-backed popularity ranking from real Sharko installs.
- Webhook from ArtifactHub on new chart versions.
- Fine-grained RBAC on "which catalog entries a user can see" — V2.x scoped RBAC roadmap.
- Direct install from catalog (no PR) — explicitly rejected; breaks Sharko's mutation model.

---

## Earlier releases

For older releases, see the [GitHub Releases page](https://github.com/MoranWeissman/sharko/releases). Highlights:

- **v1.20** — In-app Helm values editor (global + per-cluster overrides), tiered Git attribution model with per-user PAT.
- **v1.18** — Comprehensive audit coverage on every mutating endpoint.
- **v1.17** — Smart upgrade recommendation cards (next patch / next minor / latest stable) with ArtifactHub advisory integration.
