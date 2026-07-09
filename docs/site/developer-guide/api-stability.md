# API Stability Contract

This page is the **source of truth** for what won't break in the
Sharko HTTP API across the v2.x line. It is the contract integrators
(CI/CD pipelines, plugin authors, Backstage/Port/Terraform adapters,
future v2.x adopters) can rely on when wiring Sharko into their
systems.

The roadmap at [`community/roadmap.md`](../community/roadmap.md)
describes the maintainer's **intent**. This page describes the
**guarantees**.

## Why this contract exists

v2.0.0 is the first production release of Sharko and the first time
the API has stability commitments behind it. Before this page,
integrators had no formal way to know which endpoints they could
build against safely; the answer was "ask the maintainer." That does
not scale.

This contract documents:

- The semantic-versioning policy Sharko follows when the API surface
  changes.
- The deprecation policy that gives integrators lead time before a
  removal.
- Per-endpoint stability tiers so integrators can distinguish
  endpoints they can depend on long-term from endpoints that may
  still evolve.
- The explicit list of what this contract does NOT cover, so
  integrators know where to look for the answer when they need it.

If you are an integrator and an endpoint you depend on breaks in a
way this contract did not warn you about, that is a bug and a
[GitHub issue](https://github.com/MoranWeissman/sharko/issues) is the
right place to report it.

## Versioning policy

Sharko follows
[Semantic Versioning 2.0.0](https://semver.org/) for the HTTP API
covered by this contract. Sharko version numbers map to the API in
the following way:

- **MAJOR** — a breaking change to any endpoint at the `stable` tier.
  The integrator MUST review the release notes and may need to update
  their integration before upgrading. v3.0.0 will be the next major
  bump.
- **MINOR** — additive changes (new endpoints, new optional request
  fields, new response fields). Breakage permitted at the `beta`
  tier with notice in release notes. `stable` endpoints may be
  formally deprecated in a MINOR; the actual removal happens in a
  later release per the deprecation policy below.
- **PATCH** — bug fixes and security fixes. No new endpoints. No new
  optional fields. Breakage permitted at the `alpha` tier without
  notice.

The version that ships in `sharko version` and in the `User-Agent`
header is the binary version; the API contract follows the same
number.

### What counts as a "breaking change" at the `stable` tier

Any of the following on a `stable` endpoint:

- Removing the endpoint.
- Changing the HTTP method or path.
- Removing or renaming a response field that previously existed.
- Changing the type of a response field (e.g. string → integer).
- Removing or renaming a request field that previously was accepted.
- Tightening the schema for a request field (e.g. accepting a
  superset of values, then dropping some).
- Changing the documented HTTP status code for a documented success
  or failure case.

The following are **not** breaking changes:

- Adding a new endpoint.
- Adding a new optional request field with a backwards-compatible
  default.
- Adding a new response field.
- Adding a new value to an existing string-enum response field
  (integrators MUST handle unknown values gracefully — this is part
  of the contract).
- Performance improvements, bug fixes, error-message wording changes.

## Deprecation policy

Sharko's deprecation policy gives integrators **one MINOR version of
lead time** before a `stable` endpoint or `stable` field is removed.
Concretely:

- An endpoint deprecated in v2.1.0 will not be removed before v2.2.0.
- An endpoint deprecated in v2.5.0 will not be removed before v2.6.0.
- An endpoint that is renamed counts as a deprecation of the old
  name + an addition of the new name, with both present for one
  MINOR.

A deprecation is only valid if **all of the following** are in place
in the release that announces it:

1. **`// Deprecated:` comment on the Go declaration.** The handler
   (and any exported request/response struct) gets the standard Go
   `Deprecated:` doc-comment per
   [Go's deprecation convention](https://go.dev/wiki/Deprecated).
   The V2-5.3 compat-shim audit confirmed zero `// Deprecated:`
   comments currently exist in the codebase — v2.0.0 starts with a
   clean slate.
2. **Release-notes "Deprecated" section entry.** The release that
   announces the deprecation has a `### Deprecated` section in
   [`release-notes.md`](../release-notes.md) listing the endpoint
   or field with the slated removal version and the recommended
   replacement.
3. **Runtime WARN-level log line on invocation.** When the deprecated
   surface is invoked, the handler emits a `slog.Warn` entry with the
   endpoint path, the integrator-supplied `request_id` (from the V2-2.2
   correlation work — see
   [`logging.md`](logging.md)), and the slated removal version. This
   lets adopters grep their logs for `level=WARN msg="deprecated API
   invoked"` and find every caller before the removal.
4. **Removal in the next MINOR.** Deprecated in v2.1 → removed in
   v2.2. The release notes for the removing MINOR have a `### Removed`
   section listing the surface and pointing back to the announcing
   release.

### Rollback path

If a removal turns out to be premature (an adopter surfaces a real
dependency we did not know about), the maintainer reverts the
removal in a PATCH release. The endpoint comes back with the same
shape; the deprecation may stay in place pending a new plan.
Reverting a removal is not a breaking change at the contract level —
it is a bug fix.

## Stability tiers

Sharko mirrors the Kubernetes API tier convention:

- **`stable`** — full semver guarantee. The endpoint contract will
  not break in any v2.x release. Default tier for all endpoints in
  the core cluster-registration and addon-cycle flows. The
  integrator can depend on these long-term.
- **`beta`** — surface is stable enough for general use but may break
  in a MINOR release with notice in release notes (no full
  one-MINOR-lead deprecation). Default tier for newer or evolving
  shapes that the maintainer is not yet ready to commit to
  long-term. The integrator should expect to update their integration
  at most once per MINOR.
- **`alpha`** — experimental; opt-in. May break in any release
  (including PATCH) without notice. Use only if the integrator is
  willing to follow upstream changes closely. The integrator should
  NOT assume backwards compatibility across releases.

**Default-to-`beta` rule.** When the maintainer is uncertain about
committing an endpoint to `stable`, the endpoint is marked `beta`
until a v2.x minor establishes that the shape is settled. A `beta`
endpoint can be promoted to `stable` in a MINOR with no contract
implication; a `stable` endpoint can only be downgraded by going
through the full deprecation policy above.

## API surface inventory

Endpoints below are grouped by surface. The "Tier" column is the
default for the surface; per-endpoint exceptions get their own row.

All endpoints are at `/api/v1/<path>` unless otherwise noted. The
`/metrics` endpoint is at the root.

### Cluster registration + management

Surface default: **`stable`**. The core production flow.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/clusters` | `stable` | Surface default. |
| POST | `/clusters` | `stable` | Surface default. |
| GET | `/clusters/{name}` | `stable` | Surface default. |
| DELETE | `/clusters/{name}` | `stable` | Surface default. |
| PATCH | `/clusters/{name}` | `stable` | Surface default. |
| POST | `/clusters/{name}/refresh` | `stable` | Surface default. |
| GET | `/clusters/{name}/values` | `stable` | Surface default. |
| GET | `/clusters/{name}/config-diff` | `stable` | Surface default. |
| GET | `/clusters/{name}/comparison` | `stable` | Surface default. |
| GET | `/clusters/{name}/history` | `stable` | Surface default. |
| POST | `/clusters/{name}/test` | `stable` | Surface default. |
| POST | `/clusters/{name}/diagnose` | `stable` | Surface default. |
| POST | `/clusters/batch` | `stable` | Surface default. |
| GET | `/clusters/available` | `stable` | Surface default. |
| POST | `/clusters/adopt` | `stable` | V125-1-8 label-gate flow; shape settled. |
| POST | `/clusters/{name}/unadopt` | `stable` | Mirrors adopt. |
| DELETE | `/clusters/{name}/orphan` | `stable` | V125-1-7/8 ownership-label gate; settled. |
| GET | `/clusters/{name}/secrets` | `stable` | Surface default. |
| POST | `/clusters/{name}/secrets/refresh` | `stable` | Surface default. |
| POST | `/clusters/{name}/addons/{addon}` | `stable` | Per-cluster addon enable. |
| DELETE | `/clusters/{name}/addons/{addon}` | `stable` | Per-cluster addon disable. |
| POST | `/clusters/{name}/doctor` | `beta` | New in V2-cleanup-88.4/89.5 (5 checks); check-ID vocabulary may still grow. |
| POST | `/clusters/{name}/reconcile` | `beta` | New in V2-cleanup-89.4; `last_reconcile` outcome/message shape may still evolve. |
| GET | `/clusters/{cluster}/addons/{name}/values` | `stable` | Per-cluster values read. |
| PUT | `/clusters/{cluster}/addons/{name}/values` | `stable` | Per-cluster values write. |
| GET | `/clusters/{cluster}/addons/{name}/values/recent-prs` | `stable` | Read-only PR list. |

### Addon operations

Surface default: **`stable`**. The other half of the core production
flow.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| POST | `/addons` | `stable` | Add addon to catalog. |
| GET | `/addons/list` | `stable` | Surface default. |
| GET | `/addons/catalog` | `stable` | Surface default. |
| GET | `/addons/{name}` | `stable` | Surface default. |
| DELETE | `/addons/{name}` | `stable` | Surface default. |
| PATCH | `/addons/{name}` | `stable` | Configure addon. |
| GET | `/addons/{name}/values` | `stable` | Read global values. |
| PUT | `/addons/{name}/values` | `stable` | Write global values. |
| GET | `/addons/{name}/values-schema` | `stable` | Surface default. |
| POST | `/addons/{name}/values/preview-merge` | `stable` | Surface default. |
| GET | `/addons/{name}/values/recent-prs` | `stable` | Read-only PR list. |
| GET | `/addons/{name}/changelog` | `stable` | Read-only. |
| POST | `/addons/{name}/upgrade` | `stable` | Surface default. |
| POST | `/addons/upgrade-batch` | `stable` | Surface default. |
| GET | `/addons/version-matrix` | `beta` | Newer aggregated read; shape may evolve as fleets grow. |
| POST | `/addons/unwrap-globals` | `beta` | Legacy migration endpoint; intended for sunset once unused. |
| POST | `/addons/{name}/values/annotate` | `beta` | AI annotate; shape evolves with AI providers. |
| PUT | `/addons/{name}/values/ai-opt-out` | `beta` | AI opt-out directive; shape evolves with AI. |

### Dashboard, health, fleet, and observability

Surface default: **`stable`** for the core read APIs.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/dashboard/stats` | `stable` | Surface default. |
| GET | `/dashboard/attention` | `stable` | Surface default. |
| GET | `/dashboard/pull-requests` | `stable` | Surface default. |
| GET | `/health` | `stable` | Liveness probe. |
| GET | `/fleet/status` | `stable` | Surface default. |
| GET | `/observability/overview` | `beta` | Newer aggregator; shape may evolve. |
| GET | `/embedded-dashboards` | `alpha` | UI personalisation; not intended for integrator use. |
| POST | `/embedded-dashboards` | `alpha` | UI personalisation; not intended for integrator use. |

### Connection management

Surface default: **`stable`**.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/connections/` | `stable` | Surface default. |
| POST | `/connections/` | `stable` | Surface default. |
| PUT | `/connections/{name}` | `stable` | Surface default. |
| DELETE | `/connections/{name}` | `stable` | Surface default. |
| POST | `/connections/active` | `stable` | Surface default. |
| POST | `/connections/test-credentials` | `stable` | Surface default. |
| POST | `/connections/test` | `stable` | Surface default. |
| GET | `/connections/discover-argocd` | `stable` | Surface default. |

### AI features

Surface default: **`beta`**. AI surfaces are deliberately tiered
lower than the core flow because the underlying provider shapes
(OpenAI, Claude, Gemini, Ollama, custom) evolve, the smart-values
pipeline is still maturing, and the maintainer wants the freedom to
adjust contracts as adopter feedback arrives.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| POST | `/agent/chat` | `beta` | Surface default. |
| POST | `/agent/reset` | `beta` | Surface default. |
| GET | `/ai/config` | `beta` | Surface default. |
| POST | `/ai/config` | `beta` | Surface default. |
| POST | `/ai/test-config` | `beta` | Surface default. |
| POST | `/ai/provider` | `beta` | Surface default. |
| POST | `/ai/test` | `beta` | Surface default. |
| POST | `/upgrade/ai-summary` | `beta` | AI shape; not in core flow. |
| GET | `/upgrade/ai-status` | `beta` | AI shape; not in core flow. |

### PR tracking

Surface default: **`stable`**.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/prs` | `stable` | Surface default. |
| GET | `/prs/{id}` | `stable` | Surface default. |
| POST | `/prs/{id}/refresh` | `stable` | Surface default. |
| DELETE | `/prs/{id}` | `stable` | Surface default. |
| GET | `/prs/merged` | `stable` | Surface default. |

### Catalog (read + maintenance)

Surface default: **`stable`** for the read APIs.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/catalog/addons` | `stable` | Surface default. |
| GET | `/catalog/addons/{name}` | `stable` | Surface default. |
| GET | `/catalog/addons/{name}/versions` | `stable` | Surface default. |
| GET | `/catalog/addons/{name}/readme` | `stable` | Surface default. |
| GET | `/catalog/addons/{name}/project-readme` | `stable` | Surface default. |
| GET | `/catalog/remote/{repo}/{name}` | `stable` | Surface default. |
| GET | `/catalog/remote/{repo}/{name}/project-readme` | `stable` | Surface default. |
| GET | `/catalog/search` | `stable` | Surface default. |
| GET | `/catalog/repo-charts` | `stable` | Surface default. |
| GET | `/catalog/sources` | `stable` | V123 sources API; settled. |
| POST | `/catalog/sources/refresh` | `stable` | V123 sources API; settled. |
| GET | `/catalog/validate` | `stable` | Surface default. |
| POST | `/catalog/reprobe` | `beta` | Cache control; shape may evolve. |

### Addon secrets

Surface default: **`stable`**.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/addon-secrets` | `stable` | Surface default. |
| POST | `/addon-secrets` | `stable` | Surface default. |
| DELETE | `/addon-secrets/{addon}` | `stable` | Surface default. |

### Init + async operations

Surface default: **`beta`**. Init is the documented async exception
(`POST /init` returns 202 with an `operation_id` plus a heartbeat
contract), and the operations API is new enough that the maintainer
wants room to adjust the heartbeat / cancel / poll shape if real
usage exposes a missing capability.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| POST | `/init` | `beta` | Async + heartbeat shape may evolve. |
| GET | `/operations/{id}` | `beta` | New surface. |
| POST | `/operations/{id}/heartbeat` | `beta` | New surface. |
| POST | `/operations/{id}/cancel` | `beta` | New surface. |

### Auth, tokens, and users

Surface default: **`stable`** for core auth.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| POST | `/auth/login` | `stable` | Surface default. |
| POST | `/auth/logout` | `stable` | Surface default. |
| POST | `/auth/update-password` | `stable` | Surface default. |
| POST | `/auth/hash` | `alpha` | Internal password-hashing helper; not intended for integrators. |
| POST | `/tokens` | `stable` | API key create. |
| GET | `/tokens` | `stable` | API key list. |
| DELETE | `/tokens/{name}` | `stable` | API key revoke. |
| GET | `/users` | `stable` | Surface default. |
| POST | `/users` | `stable` | Surface default. |
| PUT | `/users/{username}` | `stable` | Surface default. |
| DELETE | `/users/{username}` | `stable` | Surface default. |
| POST | `/users/{username}/reset-password` | `stable` | Surface default. |
| GET | `/users/me` | `stable` | V1.20 surface; settled. |
| PUT | `/users/me/github-token` | `stable` | V1.20 tiered-attribution surface; settled. |
| DELETE | `/users/me/github-token` | `stable` | V1.20 surface; settled. |
| POST | `/users/me/github-token/test` | `stable` | V1.20 surface; settled. |

### Secrets reconcile + cluster secrets

Surface default: **`stable`** (V125-1-8 reconciler API is settled).

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| POST | `/secrets/reconcile` | `stable` | V125-1-8 reconciler trigger. |
| GET | `/secrets/status` | `stable` | V125-1-8 reconciler status. |

### Audit log

Surface default: **`stable`** for the polled API; `beta` for SSE.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/audit` | `stable` | Surface default. |
| GET | `/audit/stream` | `beta` | SSE shape may evolve as the audit retention model matures. |

### Providers + system config

Surface default: **`stable`**.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/providers` | `stable` | Surface default. |
| POST | `/providers/test` | `stable` | Surface default. |
| POST | `/providers/test-config` | `stable` | Surface default. |
| GET | `/config` | `stable` | Surface default. |

### Cluster nodes + notifications + ArgoCD reads

Surface default: **`stable`**.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/cluster/nodes` | `stable` | Surface default. |
| GET | `/notifications` | `stable` | Surface default. |
| POST | `/notifications/read-all` | `stable` | Surface default. |
| GET | `/argocd/resource-exclusions` | `stable` | Surface default. |

### Upgrade planner

Surface default: **`stable`** (AI sub-endpoints already covered in
the AI section above).

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/upgrade/{addonName}/versions` | `stable` | Surface default. |
| POST | `/upgrade/check` | `stable` | Surface default. |
| GET | `/upgrade/{addonName}/recommendations` | `stable` | Surface default. |

### Repo / docs / webhooks

Surface default: mixed.

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/repo/status` | `stable` | Bootstrap-state read; settled. |
| GET | `/docs/list` | `alpha` | In-app docs viewer; UI-specific. |
| GET | `/docs/{slug}` | `alpha` | In-app docs viewer; UI-specific. |
| POST | `/webhooks/git` | `beta` | Webhook receiver scaffolded in v2; full implementation is a v3 theme (see [roadmap](../community/roadmap.md)). |

### Metrics

| Method | Endpoint | Tier | Rationale |
|---|---|---|---|
| GET | `/metrics` | `stable` | Prometheus exposition format is the contract. Sharko-specific metric names follow the OpenTelemetry naming conventions documented in [`metrics-naming.md`](../operator/metrics-naming.md). |

### Summary

| Tier | Count |
|---|---|
| `stable` | 95 |
| `beta` | 26 |
| `alpha` | 7 |
| **Total** | **128** |

## How tiers are annotated in code

The convention for Swagger-block tier annotation is:

```go
// @stability stable
// @Router /clusters [post]
func RegisterCluster(...) {
    ...
}
```

The annotation rollout to every handler is a deliberate follow-up
PR. The first V2-6.3 PR ships this contract page (the source of
truth for tiers) and the policy framework; a subsequent mechanical
PR walks every handler and adds the comment, then regenerates
`docs/swagger/` so the Swagger UI renders a tier badge inline next
to each endpoint. Until that follow-up lands, **this page is the
authority** — if the table here and the Swagger UI disagree, this
page wins.

## What this contract does NOT cover

The following surfaces are deliberately out of scope and are
governed by their own lanes (or have no semver guarantee at all):

- **Internal Go packages (`internal/...`).** Sharko is a server, not
  a library. The internal packages have no API stability commitment;
  refactoring them across releases is expected. Integrators MUST NOT
  import `internal/...` into their own code (Go's package visibility
  enforces this; the contract reiterates it).
- **CLI command output formats.** `sharko` CLI command output
  (`list-clusters`, `pr list`, etc.) is intended to remain
  human-readable, but the precise format is not covered by this
  contract. If a future need arises for stable CLI output, it will
  get its own semver lane (likely via a `--output json` flag with a
  documented schema).
- **Helm chart values schema.** The `charts/sharko/values.yaml`
  schema follows its own semver — the chart version, not the binary
  version. Helm-values breaking changes are flagged in
  [`release-notes.md`](../release-notes.md) under the relevant
  release's "Operator impact / migration" section.
- **UI HTML structure and CSS class names.** The Sharko web UI is a
  thin client over this API. The UI's internal markup, CSS classes,
  and component shapes are not part of the API contract; only the
  API endpoints the UI calls are.
- **The `/swagger/` UI itself.** The Swagger UI at `/swagger/` and
  the generated `docs/swagger/swagger.json` / `swagger.yaml` are
  documentation surfaces. They are kept in sync with the API by CI,
  but the precise format swaggo emits (e.g. OpenAPI 2.0 vs 3.0) is
  not part of this contract. Integrators that want a stable spec
  format should consume the API directly per this page.

## How integrators verify an endpoint's tier

Until the per-endpoint Swagger annotation rollout lands (see "How
tiers are annotated in code" above), the verification flow is:

1. Look up the endpoint in the [surface inventory
   table](#api-surface-inventory) on this page.
2. Read the tier in the rightmost column.
3. For `beta` and `alpha` endpoints, plan to test against each
   release before upgrading; for `stable` endpoints, the semver
   policy above is the guarantee.

Once the follow-up PR lands, the Swagger UI will render the tier
badge inline next to each endpoint and this manual lookup will only
be needed to cross-check the policy itself.

## Related

- [Roadmap](../community/roadmap.md) — what is shipping in which
  rough timeframe; describes intent.
- [Release notes](../release-notes.md) — what actually shipped in
  each release; the authoritative changelog.
- [Logging](logging.md) — the slog-based logging that the
  deprecation-warning runtime log lines hook into; explains
  `request_id` correlation.
