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

## v2.0.3 — Bootstrap addon-deployment fix

**Status:** in development

v2.0.3 is a patch release that fixes a high-severity bug in the bootstrap
Helm chart: it read the addon catalog at the wrong path, so the chart
generated no ArgoCD ApplicationSets and **no addon ever deployed to any
cluster**. No breaking changes.

### Breaking changes

No breaking changes — v2.0.3 is a patch release.

### Bug fixes

- **Bootstrap chart now actually deploys addons.** The chart that turns the
  addon catalog into ArgoCD ApplicationSets read the addon list from the
  top level (`.Values.applicationsets`), but Sharko writes the catalog under
  the `sharko.io/v1` envelope, so the list lives at
  `.Values.spec.applicationsets`. The mismatch meant the chart rendered zero
  AppProjects and zero ApplicationSets regardless of catalog contents — every
  addon showed "not deployed." The template (and the namespace helper, which
  additionally referenced a non-existent `appName` field instead of `name`)
  now read the catalog at the correct enveloped path. A new render regression
  test renders the bootstrap chart against an enveloped catalog and fails if
  it produces no ApplicationSet, and CI's Helm job now renders the bootstrap
  chart on every PR so this can never silently regress.
  (V2-cleanup-17)

### Upgrading — existing repos need a manual one-time fix

The fix above is in Sharko's source templates, so **newly initialized**
repos get the corrected chart automatically. But repos that were already
initialized by an earlier Sharko version still carry the old, broken
bootstrap chart committed in Git — upgrading Sharko does **not** rewrite
those files. Until you apply the fix below, addons in those repos will keep
showing "not deployed."

Sharko has no "re-bootstrap" or "update the bootstrap chart" command today:
`sharko init` refuses to run on an already-initialized repo, and no
reconciler owns the bootstrap chart files. So apply this 2-line correction
by hand in your config repo (the one Sharko commits to), then commit it:

1. In `bootstrap/templates/addons-appset.yaml`, change both
   `.Values.applicationsets` references to `.Values.spec.applicationsets`.
2. In `bootstrap/templates/_helpers.tpl`, change `$.Values.applicationsets`
   to `$.Values.spec.applicationsets` (and, if your copy of the
   `addon.namespacesCSV` helper references `$a.appName`, change it to
   `$a.name`).

Once committed and synced, ArgoCD will generate the missing ApplicationSets
and your enabled addons will deploy.

> **Backlog:** an automated "refresh the bootstrap chart in an existing
> repo" flow (e.g. a `sharko init --update` / re-bootstrap path, or a
> reconciler that owns the bootstrap chart) is tracked as a follow-up so
> future template fixes reach existing repos without manual edits.

## v2.0.2 — First-run smoke-test fixes

**Status:** released 2026-06-07

v2.0.2 is a patch release that closes the issues the maintainer found
during hands-on first-run testing of v2.0.1 — kubeconfig cluster
registration, cluster removal, the marketplace add-addon flow, the setup
wizard, and dev tooling. No breaking changes.

### Breaking changes

No breaking changes — v2.0.2 is a patch release.

### What's new

- **Setup wizard now probes the Git repo state before offering to
  initialize it** — Step 4 carries honest wording so operators aren't told
  an already-initialized repo is empty.
  (V2-cleanup-9: PR [#392](https://github.com/MoranWeissman/sharko/pull/392))
- **Marketplace "Add addon to catalog" flow reaches parity with the init
  flow** — an auto-merge/manual toggle that actually takes effect, a dry-run
  preview of the catalog files that will be written, step-by-step Git
  progress, a clickable pull-request link, and post-submit navigation (to
  the new addon if it auto-merged, or to the pending-PR list if manual).
  (V2-cleanup-14: PR [#397](https://github.com/MoranWeissman/sharko/pull/397))
- **Curated design-history docs page added; remaining v1.x user-facing
  cruft removed** from the docs.
  (V2-cleanup-7: PR [#388](https://github.com/MoranWeissman/sharko/pull/388))

### Bug fixes

- **Kubeconfig cluster registration now works end-to-end.** Three fixes
  combine: registration writes the ArgoCD cluster Secret directly
  ([#391](https://github.com/MoranWeissman/sharko/pull/391)); that Secret is
  protected from the orphan-sweep reconciler during the registration
  window, and the bootstrap probe lists ArgoCD apps instead of fetching one
  by name ([#394](https://github.com/MoranWeissman/sharko/pull/394)); and the
  reconcilers preserve the bearer token so a kubeconfig cluster's Secret
  keeps its correct shape instead of being rebuilt into a broken AWS
  exec-plugin form ([#395](https://github.com/MoranWeissman/sharko/pull/395)).
  (V2-cleanup-8.2/8.3, 11, 12)
- **Cluster removal now honors the auto-merge choice**, like the init and
  register flows — previously the removal pull request was silently left
  open even when auto-merge was selected.
  (V2-cleanup-13: PR [#396](https://github.com/MoranWeissman/sharko/pull/396))
- **Clearer ArgoCD permission handling in the dev environment** — the dev
  install grants the admin apiKey RBAC, and Sharko gives a clearer message
  when ArgoCD denies its token.
  (V2-cleanup-10: PR [#393](https://github.com/MoranWeissman/sharko/pull/393))

### Maintainer tooling

- **`scripts/sharko-dev.sh` reliability fixes** — corrected preflight
  arithmetic, logging routed to stderr, a next-steps footer, and the
  reconciler enabled in the dev install.
  (V2-cleanup-8.1: PR [#390](https://github.com/MoranWeissman/sharko/pull/390))

## v2.0.1 — Release pipeline fix

**Status:** released 2026-06-04

### Bug fixes

- **Release pipeline** — dropped Windows from GoReleaser so release runs
  cleanly on the GitHub Actions Linux runners. No functional change to
  the Sharko binary; Linux and macOS artifacts are unaffected.

## v2.0.0 — Production launch

**Status:** released 2026-06-03

### Breaking changes

No breaking changes — v2.0.0 is the first production release of Sharko.

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

*Nothing removed. v2.0.0 is the first production release, so there is
no prior production line to drop compat code for.*

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
