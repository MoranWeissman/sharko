# Architecture & Design History

This page is a curated summary of the architectural decisions that shaped Sharko v2.0. It's written for contributors and integrators who want enough context to read the code with intent — not as a changelog, and not as a marketing tour. Each section names the codename of the internal epic that owned the decision so you can trace the full design conversation in `docs/design/` if you need the deeper rationale, alternatives considered, and trade-offs.

## GitOps as the API for state change

Every change Sharko makes to your fleet lands as a Git commit and a pull request. The HTTP API is a façade over that Git workflow: when you call `POST /api/v1/clusters`, Sharko opens a PR that adds the cluster to `managed-clusters.yaml`; ArgoCD then converges the actual ArgoCD `Secret` from the merged YAML. We made this choice early because it gives you two artifacts for every change — an audit-quality Git history and a state-quality YAML file — without making the server stateful. ArgoCD is the deploy engine; Sharko is the configuration owner. The two never share a write path to the cluster, which keeps Sharko safe to restart at any moment.

## Catalog as cosign-signed bundles with a configurable trust policy (V123)

The curated catalog ships as YAML bundled into the Sharko binary, with optional third-party catalogs fetched at runtime via `SHARKO_CATALOG_URLS`. Every entry can carry a Sigstore-keyless signature, and Sharko verifies that signature against the operator's trust policy (`SHARKO_CATALOG_TRUSTED_IDENTITIES`) before surfacing a green **Verified** badge in the UI. Verification runs once on fetch — not on every reload — and the verifier lives behind an interface so the trust-policy code can't leak into the fetch path. The release pipeline signs every embedded entry on every tag, so a fresh install with the default trust policy verifies out of the box.

## Tiered Git attribution: service identity vs personal PAT

Every Sharko mutation is classified as Tier 1 (operational — cluster register, addon enable, upgrade) or Tier 2 (configuration — values edits, catalog metadata, sync wave). Tier 1 always commits as the Sharko service account with the user as a `Co-authored-by:` trailer. Tier 2 prefers the user's personal GitHub PAT when one is configured, falling back to service + co-author when it isn't. This lets `git blame` answer "who set replicaCount to 100?" the natural way for configuration changes while keeping the platform's identity on operational ones. The two-tier model is also the seam where scoped RBAC will plug in — that work is on the post-v2 roadmap.

## Schema envelope + cluster reconciler ownership model (V125-1-8 / V125-1-9)

Sharko-owned YAML files (`managed-clusters.yaml`, `addon-catalog.yaml`) ship in an `apiVersion: sharko.io/v1` / `kind` / `metadata` / `spec` envelope and validate against a committed JSON Schema at read time. The schema is generated from Go types and dual-written to `docs/schemas/` and `internal/schema/`, so CI rejects any YAML that drifts from the model. On the runtime side, a stateless reconciler converges ArgoCD cluster Secrets from the validated YAML on a 30-second cadence (plus an immediate post-merge trigger), keyed off an ownership label (`app.kubernetes.io/managed-by: sharko`) that distinguishes Sharko-managed Secrets from anything else in the namespace. Unlabeled Secrets refuse delete — operators bring pre-existing Secrets under management via an explicit Adopt action.

## Smart values AI pipeline with secret-leak detection (V124)

When you add an addon, Sharko pre-fetches the chart's upstream `values.yaml`, runs a heuristic split (cluster-specific fields like `host`, `replicaCount`, `resources.*` are commented out at their original position; a per-cluster template block is appended at the bottom), and stamps a self-describing header. With an AI provider configured, Sharko optionally adds inline descriptive comments via the LLM — but a regex pre-scan blocks the call hard if the values file contains anything matching an AWS key, GitHub PAT, JWT, Google API key, Slack token, PEM private key, or high-entropy generic credential pattern. There is no override. The latency and token caps make Add Addon non-blocking on slow LLMs, and the audit trail distinguishes heuristic-only from LLM-annotated output.

## Defense-in-depth logging redaction (V2-2.4)

All internal callers were migrated from stdlib `log` to `log/slog`, with `request_id` propagated across middleware, the reconciler, the PR tracker, the orchestrator, and API handlers so every emission can be correlated end-to-end. A `slog.Handler` wrapper sits between every caller and every sink, redacting tokens, kubeconfigs, and secret bodies before they hit the structured-log buffer. The redactor is defense-in-depth — even a future caller that accidentally passes a sensitive field as a `slog.Attr` is structurally prevented from leaking it. A regression test asserts the bootstrap admin password cannot reach the structured-log buffer even if the redactor wrapper is bypassed in a refactor.

## SLO + error-budget telemetry (V2-3)

Sharko exposes Prometheus histograms and counters on every critical path (cluster registration, addon cycle, catalog scan, dashboard read) using OpenTelemetry-conventional metric naming, with histogram buckets sized from real p50/p95/p99 baselines captured under the perf-harness. Exemplars carry the `request_id` so a slow request in a histogram bucket can be traced back to its log lines. A Helm-shipped `PrometheusRule` template emits multi-window multi-burn-rate alerts against documented SLO targets, and every alert has a matching operator runbook explaining what fires, what to check, and what to do about it.

## Comprehensive runbook coverage (V2-4)

Sharko's 57 inventoried failure modes (12 P0, 28 P1, 12 P2) each have a Symptoms → Diagnosis → Mitigation → Root cause → Prevention runbook. The runbook style guide enforces structure (named sections, ordered steps, concrete strings, no marketing voice, no "should be obvious") and verified-by-execution headers (`> **Verified:** <date> against <image>`) — reviewers reject runbooks that were authored without execution. The runbook bundle exists because the maintainer's first-K8s smoke pass hit a runbook that had been authored without execution and was structurally broken; the style guide is the response, and execution is now the gate.

## Design archive

For the raw design proposals showing how each subsystem was decided — including alternatives considered, trade-offs, deltas applied after first contact with real workloads, and trace-back to the commits and PRs that implemented each decision — see [`docs/design/` on GitHub](https://github.com/MoranWeissman/sharko/tree/main/docs/design). Filenames are date-prefixed (`YYYY-MM-DD-<topic>.md`) so the archive reads in chronological order; epic codenames inline reference the sprint that owned the decision.

## Development methodology

Sharko is built by a single maintainer, but the workflow is structured so multiple contributors can join without losing the design rationale that's already baked into the codebase. Every feature passes through the same four-phase loop — discovery, planning, dispatch, and quality gates — with concrete artifacts at each gate (a planning doc names the locked decisions and risks; per-story dispatches embed the relevant role context; code-review and security-auditor passes run before any merge to a sprint branch). Design documents are dated and epic-numbered (`V123`, `V124`, `V125-1-8`, `V2-2.4`) so contributors can trace what was decided when and which commits implemented it. That's why `docs/design/` reads like a project journal rather than a static reference — the codenames in this page line up with the filenames in that directory.
