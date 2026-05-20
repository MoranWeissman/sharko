# Release Notes

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
