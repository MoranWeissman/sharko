# Roadmap

This is Sharko's public roadmap. It describes the **themes** the
maintainer intends to pursue across the v2.x line and into v3, plus
the items that have explicitly been ruled OUT. It does **not** publish
commit dates, and the inclusion of a theme below is not a contract
that it will ship in any specific release.

If you are looking for **what shipped in a specific release**, read
[`release-notes.md`](../release-notes.md). The roadmap describes intent;
release notes describe reality.

## What this roadmap is — and isn't

**Is:**

- A description of the maintainer's working priorities, organised by
  rough timeframe (shipped in v2.0.0 / near-term v2.x / medium-term
  v3.x / off-the-roadmap).
- A signal to adopters and integrators about where the project is
  heading, so they can plan their own work around it.
- A surface for community input — see
  [How to influence the roadmap](#how-to-influence-the-roadmap) below.

**Isn't:**

- A commitment to ship any specific theme in any specific release.
- A schedule. Sharko is solo-maintained pre-CNCF-sandbox; **themes
  reflect intent, commit dates depend on community + maintainer
  capacity**, and the order can shift as adopter feedback arrives.
- A contract with integrators. The API stability contract lives at
  [`api-stability.md`](../developer-guide/api-stability.md) — that
  page is the source of truth for what won't break.

## What shipped in v2.0.0

v2.0.0 is Sharko's first production release. The v1.x line was the
development cycle (see
[`migration-v1-to-v2.md`](../operator/migration-v1-to-v2.md) for the
"there is no migration; reinstall fresh" framing). The v2.0.0
production-launch epic focused on **measurable production-readiness**
rather than new user features. Capabilities landed:

- **Performance baselines + SLO targets per critical path.** p50 / p95
  / p99 measurements per phase across the four V2-1 critical surfaces
  (`cluster_registration`, `addon_cycle`, `catalog_scan`,
  `dashboard_read`), with documented SLO targets, error budgets, and
  multi-burn-rate thresholds. A `workflow_dispatch` baseline-refresh
  workflow plus a comparator binary with `-emit` mode gate every PR
  against the committed baselines.
  → See [`slos.md`](../operator/slos.md),
  [`perf-baselines.md`](../operator/perf-baselines.md), and the V2-1
  release-notes entry.
- **100% slog logging with correlation IDs and sensitive-field
  redaction.** Every internal caller now uses `log/slog`. A
  `request_id` propagates across middleware, the cluster reconciler,
  the PR tracker, the orchestrator, and every API handler. A
  `slog.Handler` wrapper redacts tokens, kubeconfigs, and secret
  bodies before they hit any sink.
  → See [`logging.md`](../developer-guide/logging.md) and the V2-2
  release-notes entry.
- **Prometheus telemetry for SLO surfaces.** Histogram + counter
  exposition with V2-1.2-sized buckets, OpenTelemetry-conventional
  metric naming, exemplars carrying `request_id`, a Helm-shipped
  `PrometheusRule` template with multi-window multi-burn-rate alerts,
  and an operator runbook covering every alert end-to-end.
  → See [`metrics-naming.md`](../operator/metrics-naming.md) and
  [`budget-burn-runbook.md`](../operator/budget-burn-runbook.md).
- **Failure-mode index + P0/P1 runbooks.** Every Sharko error path
  bucketed into operator-observable failure modes (63 rows across the
  API, reconciler, orchestrator, providers, catalog, and audit-log
  surfaces). All P0 and P1 GAPs closed by V2-4.3; the remaining 12
  P2s are tracked as a v2.x follow-up backlog. Style guide governs
  every runbook page going forward.
  → See [`failure-mode-index.md`](../operator/failure-mode-index.md)
  and the [runbook style
  guide](../developer-guide/runbook-style-guide.md).
- **First production release notes + v1.x → v2.0.0 migration
  reference.** v2.0.0 is the first production line — there is nothing
  to downgrade to, and no production compat shims to retire. The
  migration reference documents the "reinstall fresh" path because
  state lives in your git repo, not in Sharko.
  → See [`release-notes.md`](../release-notes.md) and
  [`migration-v1-to-v2.md`](../operator/migration-v1-to-v2.md).
- **CNCF foundation docs + GitHub config.** `MAINTAINERS`,
  `GOVERNANCE`, `CODE_OF_CONDUCT` (Contributor Covenant 2.1),
  `CONTRIBUTING`, `SECURITY`, and `ADOPTERS` at the repo root; DCO
  `Signed-off-by` enforcement; YAML issue templates (bug / feature /
  docs / security); GitHub Discussions enabled with a Roadmap input
  category.
  → See the [GOVERNANCE](https://github.com/MoranWeissman/sharko/blob/main/GOVERNANCE.md)
  and [CONTRIBUTING](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md)
  docs in the repo root.
- **Public roadmap + API stability contract (this page + its
  sibling).** Themes-only roadmap plus a per-endpoint stability tier
  inventory with semver guarantees and a 1-minor-version deprecation
  policy.
  → See [`api-stability.md`](../developer-guide/api-stability.md).

## Near-term themes (v2.x post-launch)

Small, predictable items that fit inside the v2.x line without
needing a major bump. The maintainer's working order is roughly:

- **P2 runbook backlog (12 items).** The 12 P2 failure-mode rows still
  marked `GAP` in
  [`failure-mode-index.md`](../operator/failure-mode-index.md) get
  paired with runbooks as adopters surface real symptoms. Operator-
  correctable cases (e.g. "use a valid chart version") may stay as
  documentation pointers rather than full runbooks.
- **Adopter feedback fixes.** Bug reports and small papercuts surfaced
  by real v2.0.0 installs. Prioritisation favours problems that block
  first-install-to-first-cluster.
- **Per-endpoint stability annotation rollout (V2-6.3 follow-up).**
  The stability tiers documented in
  [`api-stability.md`](../developer-guide/api-stability.md) need a
  mechanical pass adding `// @stability <tier>` annotations to each
  handler's Swagger block so the Swagger UI can render the badge
  inline. Separate mechanical PR; no contract change.
- **Per-phase metric wiring follow-ups.** V2-3.1 instrumented the four
  critical surfaces with phase-grained histograms; a small number of
  internal phases inside `cluster_registration`, `addon_cycle`, and
  `catalog_scan` still need their `request_id`-bearing exemplars
  wired through.
- **ServiceMonitor CR shipping (V2-3 follow-up).** The
  `PrometheusRule` template ships in the Helm chart; a matching
  `ServiceMonitor` CR for the
  [Prometheus Operator](https://prometheus-operator.dev/) is a
  near-term addition for adopters who run that stack.

These items are intentionally undated. The maintainer will pick them
up in whatever order makes sense based on what adopters hit first.

## Medium-term themes (v3.x)

These are larger pieces of work that will likely require a v3 major
bump because each touches the API contract or the data model
materially. Each theme is described as **intent**, not commitment, and
the order can shift.

- **Fine-grained RBAC.** Beyond the current Admin / Operator / Viewer
  roles: resource-scoped permissions (per-cluster, per-environment,
  per-addon), API key scoping (e.g. a CI token that can only read or
  only operate on one cluster). See the
  [attribution + permissions design](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/product-manager.md)
  for the V2.x-shipped baseline that this builds on.
- **SSO / OIDC.** SAML and OIDC providers (Google Workspace, Okta,
  Entra ID, GitHub OAuth), with SSO group → Sharko role mapping. v2.x
  ships with local user accounts and API keys only.
- **Multi-ArgoCD.** Support for multiple ArgoCD instances per Sharko
  install (e.g. one ArgoCD per environment or per business unit),
  with connection multiplexing routing each operation to the right
  ArgoCD. v2.x assumes a single ArgoCD per Sharko.
- **Rule-based auto-merge.** Conditions for the auto-merge decision:
  label-based, environment-based, time-of-day-based, with
  [`CODEOWNERS`](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)
  integration so reviewers can be auto-tagged. v2.x ships the binary
  auto-merge toggle from v1.x.
- **Grafana dashboard ship.** Pre-built Grafana dashboard JSON for
  cluster health, addon status, and version matrix, importable
  alongside the existing Prometheus exposition. v2.x ships the
  metrics + alert rules + runbooks; charts are the missing piece.
- **Cloud-provider full support.** GKE with Workload Identity + GCP
  Secret Manager, AKS with Workload Identity + Azure Key Vault, and a
  HashiCorp Vault provider as a credential source. v2.x covers AWS
  Secrets Manager + K8s Secrets in production-supported form, with
  Azure and GCP returning explicit "not yet implemented" stubs.
- **State-storage evolution.** v2.x uses K8s ConfigMaps for runtime
  state (cluster observations, pending PR tracking). v3 should
  re-evaluate: CRDs for better RBAC granularity, Redis for pub/sub
  real-time UI updates, or PostgreSQL if audit log / metrics history
  grow past ConfigMap scale. Decision criteria: ConfigMap size
  approaching 1MB, read/write latency budget impact, or need for
  cross-replica state in HA deployments.
- **Default addon profiles.** Selectable at registration time:
  `prod-eu` vs `dev-sandbox` vs `compliance-env`, each mapping to a
  curated bundle of addons rather than the current single
  connection-level default list. UI + CLI + API surface.
- **Webhook receiver (full implementation).** v2.x has the webhook
  surface scaffolded (`POST /webhooks/git`); v3 expands it into a
  full GitHub / Azure DevOps push-event receiver that triggers
  immediate reconcile rather than waiting for the safety-net tick.
- **Cluster templates, addon marketplace contributions, upgrade
  planner, rollback UI, cost visibility, cluster
  decommissioning.** Adjacent advanced features grouped here because
  each is a self-contained workstream that requires v3-scale data
  modelling. The maintainer expects to ship at most one or two per
  v3.x minor.

### Operator mode (v3+)

Tracked separately because it is a larger commitment than the items
above. The operator-mode theme is to deliver Sharko as a Kubernetes
operator pattern:

- `SharkoConfig` and `ManagedCluster` CRDs as the declarative
  surface.
- Continuous credential rotation driven by the operator reconcile
  loop.
- A `ValidatingAdmissionWebhook` that blocks direct `kubectl` writes
  to Sharko-managed resources.
- Multi-replica HA via the standard operator leader-election pattern.

This is intentionally NOT incremental: it changes the deployment
model and the data model together. It is most likely to land as a v3
or later major.

## Explicitly OFF the roadmap

These directions are not deferred — they are **rejected**. They will
not be reconsidered unless a fundamental constraint changes.

- **Non-Helm addons (Crossplane operators, raw kustomize, raw
  manifests, OLM operators).** Rejected 2026-04-20. Sharko stays
  Helm-focused. The catalog, the smart-values pipeline, the upgrade
  planner, the ApplicationSet generators, and the ArgoCD wiring all
  assume Helm; supporting other addon shapes would either fork every
  one of those surfaces or weaken the Helm-shaped guarantees that
  make Sharko useful in the first place. If you have a non-Helm
  workload, the right tool is ArgoCD directly.

## How to influence the roadmap

Highest-leverage feedback comes from **real adopters with concrete
use cases**. "We are running Sharko in production at N clusters and
hit X" is far more actionable than "Sharko should also do Y."

In order of preference:

- **GitHub Discussions — Roadmap input category.** Open a discussion
  describing the problem, your current workaround, and what
  capability would unblock you. Discussions stay open longer than
  issues and are the maintainer's preferred surface for shaping
  intent. The category was added by the V2-6 GitHub config:
  [https://github.com/MoranWeissman/sharko/discussions](https://github.com/MoranWeissman/sharko/discussions).
- **Feature issue template.** When the problem is concrete enough to
  formalise as a request, use the
  [Feature Request template](https://github.com/MoranWeissman/sharko/issues/new?template=feature.yml).
  The template asks for the problem statement, target users, and how
  you would verify the feature works — answering those three
  questions thoroughly is what moves an item from "interesting" to
  "scheduled."
- **CONTRIBUTING guide.** For larger themes, the
  [CONTRIBUTING](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md)
  guide describes the issue → discussion → PR flow plus the DCO
  sign-off requirement. Code contributions are welcome, but please
  open a discussion before starting work on anything roadmap-shaped
  so the design can be aligned.

The maintainer reads everything but cannot promise to act on every
input. Themes move up the priority list when multiple adopters hit
the same constraint, when the proposed change is small and bounded,
or when someone shows up with a contribution proposal.
