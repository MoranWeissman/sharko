# Release Notes

<!-- V125-1-9 schema envelope merged to dev/v125-1-9-schema-envelope — release note pending V125 tag with V125-1-8. When cutting v1.25, add a "YAML schema envelope" entry covering: envelope shape, schema header, addon-catalog.yaml rename, sharko validate-config CLI, CI validate-sharko-config job, read-time validation in loaders. Cross-link operator/yaml-schema-migration.md. -->

## v1.26 (in progress)

- **Bootstrap no longer pre-populates 3 foundation addons.** Previously `addons-catalog.yaml` shipped with cert-manager, external-secrets, and metrics-server pre-configured. New installs start with an empty catalog; install these (or any other) addons from the Marketplace as needed.

---

## v1.25 (in progress) — Three-mechanism provider config split

v1.25 splits the previously field-overloaded `providers.Config` struct into **three orthogonal typed configs**. The split closes the V125-1-10.8 cross-contamination smell at the type level: the compiler now enforces that an "ArgoCD namespace" knob cannot accidentally flow into an "addon-secrets namespace" slot. See [Operator → Configuration → Provider Configuration (3-mechanism split)](operator/configuration.md#provider-3mech) for the full operator surface and the [Cluster Connectivity Model](operator/cluster-connectivity-model.md) for the end-to-end story.

### What changed

The single `providers.Config` blob — which carried `Type`, `Region`, `Prefix`, `Namespace`, and `RoleARN` for three different consumers — was split into three sibling types in `internal/providers/config_types.go`:

- `AddonSecretProviderConfig` — backends supplying addon secret material (Vault / AWS-SM / Azure-KV / GCP-SM / Kubernetes Secrets). This is the ESO-replacement layer Sharko was built around.
- `ClusterTestProviderConfig` — cluster connectivity credentials (argocd-only in v1.25).
- `ClusterRegistrationSourceConfig` — pre-wire for the future V125-1-8 cluster reconciler; no consumer in v1.25.

The old `providers.Config` struct and the `providers.New` / `providers.NewSecretProvider` factories were retired in the same sprint.

### Why

V125-1-10.8 (commit `28e5bcda`, PR #327) shipped a one-line workaround: ArgoCDProvider ignored its `cfg.Namespace` and read `SHARKO_ARGOCD_NAMESPACE` directly, with an `slog.Warn` flagging the cross-contamination. The smell could not be cleanly fixed inside the overloaded struct — three consumers reading the same `Namespace` field with three different default values is a structural problem. The typed split makes the cross-contamination impossible at compile time.

### Action items for operators

In priority order:

1. **If you set `SHARKO_ARGOCD_NAMESPACE` env var** — it still works but logs a deprecation warning on startup. Migrate to `clusterTest.argocdNamespace` in Helm values (or `ClusterTestProviderConfig.ArgoCDNamespace` for Go API consumers). **Removed in v1.26.**

2. **If your active connection has `provider.type: aws-sm` / `k8s-secrets` / `gcp-sm` / `azure-kv` AT THE CONNECTION LEVEL for fetching cluster kubeconfigs** — those code paths were retired one cycle earlier than the `provider.go:55` doc comment promised. Migrate to `provider.type: argocd` (the auto-default when Sharko runs in-cluster; reads from the ArgoCD cluster Secret Sharko already creates during `sharko register-cluster`). **This does NOT affect operators using the same backend names for addon secrets** — the ESO-replacement layer is unchanged.

3. **If you only use the ESO-replacement** (Vault / AWS-SM / Azure-KV / GCP-SM / Kubernetes Secrets supplying addon secret material via the secrets reconciler) — no changes needed.

### New operator-facing knobs

```yaml
# charts/sharko/values.yaml
clusterTest:
  # Canonical replacement for SHARKO_ARGOCD_NAMESPACE env var.
  # Empty falls back to env (deprecated) → "argocd" default.
  argocdNamespace: ""

clusterRegSource:
  # Pre-wire for V125-1-8 reconciler. No consumer in v1.25.
  type: ""              # "" → no reconciler; "argocd" → V125-1-8 will write
  argocdNamespace: ""   # "" → defaults to "argocd" when V125-1-8 ships
```

Corresponding env vars (always surfaced in startup logs so values can be verified):

| Env var | Helm value |
|---------|------------|
| `SHARKO_CLUSTER_REG_TYPE` | `clusterRegSource.type` |
| `SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE` | `clusterRegSource.argocdNamespace` |
| `SHARKO_ARGOCD_NAMESPACE` *(DEPRECATED, removal v1.26)* | use `clusterTest.argocdNamespace` instead |

### Internal API change (for codebase consumers)

The canonical Go types now live in `internal/providers/config_types.go`. Construct providers with the new typed factories:

- `providers.NewAddonSecretProvider(AddonSecretProviderConfig)` — for the addon-secrets reconciler.
- `providers.NewClusterTestProvider(ClusterTestProviderConfig)` — for the Test cluster surface and any code path that needs a `ClusterCredentialsProvider`.

The pre-v1.25 `providers.Config` struct and `providers.New` / `providers.NewSecretProvider` factories no longer exist.

### Compatibility window

- **Most operators see zero impact.** New installs since V125-1-10.2 already auto-default to `argocd` for cluster connectivity; ESO-replacement users were never touched.
- **`SHARKO_ARGOCD_NAMESPACE` env var** — still functional with a deprecation `slog.Warn` in v1.25. Removed in **v1.26**.
- **Legacy cluster-credentials backends** (`aws-sm` / `k8s-secrets` / `gcp-sm` / `azure-kv` as `provider.type` for fetching cluster kubeconfigs) — **retired in v1.25**. This is the only hard break in the release. Same backend names remain supported as addon-secret backends.

### Acknowledgements

The refactor shipped after V125-1-13.y closed the in-process e2e gate (required scaffolding before this sprint per `epics-v125-1-13.md`). One regression was caught by the V125-1-10.8 regression guard during Story 11.7 validation and fixed in 11.7-fix (`a9632706`) — `connProv.Namespace` was being copied into `ClusterTestProviderConfig.ArgoCDNamespace`, recreating the cross-contamination via a different code path. The fix narrows the fan-out: only `connProv.Type == "argocd"` lights up the cluster-test config, with `ArgoCDNamespace` left empty so the typed env/default fallback runs.

Refs: `.bmad/output/diagnostics/BUG-OVERLOAD-DIAGNOSIS.md`, `.bmad/output/planning-artifacts/epics-v125-1-11.md`.

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
