# Failure Mode Index

> **Verified:** Inventory compiled 2026-06-01 against Sharko as shipped.
> Every route handler, the cluster reconciler, the ArgoCD cluster-Secret
> writer, the orchestrator, the credentials providers and the catalog
> loader and source fetcher were walked for operator-observable failures,
> together with the audit-log action codes documented in
> [`../developer-guide/logging.md`](../developer-guide/logging.md).
> Re-audit each minor release; remove entries as runbooks close `GAP`
> markers.
>
> **Updated 2026-08-11:** every credentials-backend failure row below was
> re-checked. Sharko no longer puts a credentials backend's own error text
> in a log line, an API response, an audit entry, a reconcile record or a
> Kubernetes event — that text can carry credential material. Rows that
> used to tell you to Ctrl-F an AWS message now tell you which `step`
> field to read instead. **Ctrl-F still works, but search for the `step`
> value or the log `msg`, not for an AWS error code** — an AWS error code
> will not appear in Sharko's output at all.
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

This page is **the first place an operator should search when they hit
a Sharko error.** Ctrl-F your error message, find the failure-mode
row, follow the `Runbook URL` column to the runbook that covers it.

If the `Runbook URL` column says **`GAP`**, no runbook exists yet for
this failure. File an issue (or, if the page is in your hand because
you're paging the maintainer, include the failure-mode row text in the
escalation). Every P0 and P1 row has a runbook already; the remaining
gaps are all P2.

---

## How to use this page

1. **Identify the symptom** — what error message did you see? What
   alert fired? What's the HTTP status? Use Ctrl-F on the failure-mode
   text below.
2. **Read the severity** — P0 means page; P1 means file a ticket for
   business hours; P2 means track and fix next sprint. See the legend
   below.
3. **Open the runbook URL** — the column links directly to the
   runbook that owns this failure mode. If it says `GAP`, no runbook
   exists; the failure is tracked here, but mitigation is "page the
   maintainer."
4. **Follow the runbook** — symptoms → diagnosis → mitigation →
   root-cause → prevention. Every runbook follows the same shape
   ([style guide](../developer-guide/runbook-style-guide.md)).

## Severity legend

The vocabulary mirrors the Prometheus alert severity labels in
`charts/sharko/templates/prometheusrules.yaml` so paging and ticketing
align.

| Tier | Meaning | Pager? | SLA |
|------|---------|--------|-----|
| **P0** | Page. Cluster registration broken, secrets store offline, reconciler crash loop, ArgoCD unreachable, auth bypass, silent data loss. The fleet is getting worse the longer it sits. | Yes | Immediate response (minutes) |
| **P1** | Ticket within 24h. Single-cluster sync failure, specific addon failing, rate limit hit, signature verification failure on one source. The working population can usually retry through it. | No (file ticket) | Next business day |
| **P2** | Next sprint. Transient diagnostic-only failure, edge case for one operator workflow, cosmetic UI issue, noisy log that doesn't reflect a real problem. | No | Plan into next sprint |

Severity is **about user impact**, not technical depth. See
[the style guide](../developer-guide/runbook-style-guide.md#2-severity)
for the full discussion and the calling rules.

## How to file a new failure mode

Open an issue against the Sharko repo with:

- The exact error message / log line / alert name
- The HTTP method + path (if API-driven) or the background task
  (reconciler tick, PR tracker poll, catalog refresh)
- Your proposed severity tier (P0/P1/P2) with rationale
- Whether you believe a runbook exists already (Ctrl-F this page)

The maintainer adds the row here and tracks the missing runbook. Every
runbook carries a verified-by-execution header, so a new one needs its
steps actually run, not just written.

---

## Failure modes

Sorted by severity (P0 first), then by surface (API → reconciler →
orchestrator → provider → catalog → audit-log). `GAP` entries are
**bolded with the GAP token**, so you can list every gap at a glance:
`grep -nE '\*\*GAP — P[012]\*\*' failure-mode-index.md`.

### P0 (page on-call)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| ArgoCD upstream unreachable (any handler that calls ArgoCD returns 502 or 503 with Sharko's fixed no-usable-ArgoCD-connection sentence) | **P0** | [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) | Surfaces from every cluster, addon, and dashboard handler. Single root cause (ArgoCD outage / token revoked / network policy block); shared mitigation. Grouped as ONE runbook. |
| Git provider upstream unreachable (any handler that opens a PR returns 502 or 503 with Sharko's fixed no-usable-Git-connection sentence) | **P0** | [`git-provider-unreachable.md`](git-provider-unreachable.md) | Surfaces from every cluster + addon write handler. Single root cause; shared mitigation. Grouped as ONE runbook. |
| Cluster registration broken — SharkoClusterRegistrationFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn) | The burn-rate runbook covers it, and the alert links straight to the anchor. |
| Addon enable / disable / upgrade cycle broken — SharkoAddonCycleFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkoaddoncyclefastburn`](budget-burn-runbook.md#sharkoaddoncyclefastburn) | Covered by the burn-rate runbook. |
| Catalog scan broken — SharkoCatalogScanFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkocatalogscanfastburn`](budget-burn-runbook.md#sharkocatalogscanfastburn) | Covered by the burn-rate runbook. |
| Dashboard reads broken — SharkoDashboardReadFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkodashboardreadfastburn`](budget-burn-runbook.md#sharkodashboardreadfastburn) | Covered by the burn-rate runbook. |
| Cluster reconciler crash loop (the reconcile loop panics or exits and never ticks again) | **P0** | [`reconciler-crash-loop.md`](reconciler-crash-loop.md) | Reconciler is the canonical ArgoCD-secret writer; if the goroutine dies, fleet drifts silently. Detection: absence of `recon-<ts>` request_ids in the log. |
| `managed-clusters.yaml` schema validation failed — reconciler refuses to act (`audit.action=schema_validation`, `audit.event=cluster_secret_reconcile`) | **P0** | [`cluster-reconciler.md#what-if-managed-clustersyaml-has-a-schema-validation-error`](cluster-reconciler.md#what-if-managed-clustersyaml-has-a-schema-validation-error) | Existing coverage. Severity is P0 because **all** reconciliation halts, not just one cluster. |
| Secret push to remote cluster silently failed (Sharko logs `Error "failed to create secret, continuing"`) | **P0** | [`secret-push-silently-failed.md`](secret-push-silently-failed.md) | The "continuing" path is silent data loss — user thinks credential was pushed; actually wasn't. Cluster will fail downstream. |
| Orchestrator PR merged but ArgoCD never converges (addon cycle audit shows `pr_merged` then no `cluster_secret_create` / sync event) | **P0** | [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md) | Indicates either reconciler is stuck OR ArgoCD application controller is degraded. Diagnosis path: distinguish which side. |
| Auth bypass — `/api/v1/auth/login` returns 200 for invalid credentials, or session cookie is honored after expiry | **P0** | [`auth-bypass.md`](auth-bypass.md) | Pure security failure. Detection: audit `login_failed` count drops to zero while traffic continues. Includes the token-hash collision class. |
| Bootstrap admin password leak — admin password visible in pod logs as a plain-text attribute | **P0** | [`credential-leak-in-logs.md`](credential-leak-in-logs.md) | Redaction now collapses the value to `[REDACTED]`, but the password is still handed to the logger, so a regression in redaction would re-expose it. Grouped with the broader credential-leak failure mode — shared diagnosis and mitigation. |
| Kubeconfig / token leak in logs (any credential-shaped value bypasses RedactHandler heuristics) | **P0** | [`credential-leak-in-logs.md`](credential-leak-in-logs.md) | Redaction is defense-in-depth; failure mode is "a new sink bypasses the wrapper, or a value evades all three detectors." Detection: scan logs for `eyJ`-prefixed JWTs, kubeconfig contexts, or base64 blobs >100 chars. Grouped with the bootstrap-admin-password leak. |
| ArgoCD cluster-secret out-of-band deletion not self-healed (labeled Secret deleted; next reconciler tick does NOT recreate within 30s) | **P0** | [`cluster-reconciler.md#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete`](cluster-reconciler.md#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete) | Existing coverage; the P0 case is when self-heal *fails*, not the routine self-heal. Verify the runbook covers the failure case explicitly. |
| Secrets provider (AWS SM / K8s Secrets / Vault) completely unreachable — the health check on the active provider fails | **P0** | [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) | Affects every cluster registration AND every reconciler tick. Single root cause per provider; one runbook covers all three sub-cases (AWS / k8s / vault). |
| Orphan sweep held — desired state parsed to zero clusters while sharko-labeled Secrets exist (`audit.event=orphan_sweep_held`, Error log `"orphan sweep HELD"`) | **P0** | [`upgrading-and-rollback.md#orphan-sweep-held`](upgrading-and-rollback.md#orphan-sweep-held) | The orphan-sweep guard working as designed: `managed-clusters.yaml` exists non-empty in Git but reads as zero clusters — signature of a version/format mismatch (e.g. mixed Sharko versions on one repo). NO Secret is deleted while held; resolve the mismatch, guard disarms on the next tick. P0 because the underlying cause (a wrong-version writer on the repo) endangers the whole fleet. |
| Unrecognized `sharko.*` apiVersion — every reader hard-errors with `"unrecognized Sharko apiVersion ... refusing to guess"` (reconciler tick aborts, `audit.action=schema_validation`) | **P0** | [`upgrading-and-rollback.md#unrecognized-sharko-apiversion`](upgrading-and-rollback.md#unrecognized-sharko-apiversion) | The forward guard: a config file written by a newer/unknown Sharko is never silently read as empty (the pre-guard behavior that let a stale binary orphan-sweep the fleet). Mitigation: upgrade the instance that logged the error. |
| Catalog signing trust root unavailable — Sharko cannot load `trusted_root.json` from TUF | **P0** | [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md) | Every verified-catalog entry fails verification; the marketplace surfaces every entry as Unverified. Per the [catalog-trust-policy](catalog-trust-policy.md) runbook context. |
| Init operation deadlocked (`POST /api/v1/init` returns 202, operation_id never reaches terminal state, heartbeat stops) | **P0** | [`init-operation-deadlocked.md`](init-operation-deadlocked.md) | The documented async exception; if init wedges, the bootstrap repo is in an unknown state. Detection: audit shows `init_run` start but no completion. |
| OOM kill / process restart loop (Sharko pod restarting >3× / 5min) | **P0** | [`oom-restart-loop.md`](oom-restart-loop.md) | Kubernetes CrashLoopBackoff state. Not Sharko-emitted; detected via `kubectl get pod` Restarts column. |

### P1 (file ticket; fix next business day)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| Cluster registration broken (sustained burn) — SharkoClusterRegistrationSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkoclusterregistrationslowburn`](budget-burn-runbook.md#sharkoclusterregistrationslowburn) | Covered by the burn-rate runbook. |
| Addon cycle broken (sustained burn) — SharkoAddonCycleSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkoaddoncycleslowburn`](budget-burn-runbook.md#sharkoaddoncycleslowburn) | Covered by the burn-rate runbook. |
| Catalog scan broken (sustained burn) — SharkoCatalogScanSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkocatalogscanslowburn`](budget-burn-runbook.md#sharkocatalogscanslowburn) | Covered by the burn-rate runbook. |
| Dashboard reads broken (sustained burn) — SharkoDashboardReadSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkodashboardreadslowburn`](budget-burn-runbook.md#sharkodashboardreadslowburn) | Covered by the burn-rate runbook. |
| Single cluster's credential fetch fails — `audit.action=get_credentials` with `result=failure` for one cluster across multiple ticks; log line `"[clusterreconciler] vault GetCredentials failed"` with `step=get-credentials` | **P1** | [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md) | Per-cluster credentials failure (creds rotated, IRSA misconfigured, secret path moved). Other clusters reconcile normally; only one is stuck. The audit entry's `error` field is a fixed safe sentence, not the backend's text — `GET /audit` is open to the viewer role (hotfix 2026-08-11). Read `step` and `cred_key` off the log line, then probe the provider directly. |
| Zero-addon clusters show "Unknown" connectivity after upgrading to ≥ v2.2.0 (pre-rename bootstrap templates: the deployed connectivity-check ApplicationSet still selects the old `sharko.io/...` label) | **P1** | [`upgrading-and-rollback.md#connectivity-check-after-upgrading-pre-v220-templates`](upgrading-and-rollback.md#connectivity-check-after-upgrading-pre-v220-templates) | Cosmetic but confusing: the first reconcile after upgrade migrates secret labels to `sharko.dev/...` and the old-label ApplicationSet stops matching. Clusters running ≥1 addon are unaffected. Fix = re-render/refresh bootstrap templates from the upgraded Sharko. |
| Cluster test (`POST /clusters/{name}/test`) returns 503 for AWS IAM cluster because Sharko couldn't mint a token with its own identity (`argocd_provider_iam_required`) | **P1** | [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) | Sharko parses `awsAuthConfig` / known AWS exec commands and mints with its own AWS identity — this fires only when the mint attempt itself fails (no region, no Sharko AWS identity, wrong trust policy). Severity is P1 not P2 because it blocks on-boarding until the IAM chain is fixed. |
| Cluster test returns 503 for an unrecognized exec-plugin command (wire code `argocd_provider_exec_unsupported`) | **P1** | [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md) | Only fires for exec commands OTHER than the two known AWS authenticators (`argocd-k8s-auth aws`, `aws-iam-authenticator`), which are parsed and minted successfully. Distinct provider error path from `aws-iam-cluster-auth.md`. |
| Single ArgoCD Application stuck Degraded after addon enable (PR merged, audit shows `addon_enabled_on_cluster` success, but ArgoCD shows `Degraded`) | **P1** | [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md) | Addon-specific issue (bad chart values, namespace clash, RBAC denied). Mitigation = inspect the Application directly in ArgoCD. |
| Git provider rate limit hit — `Warn` log lines containing `rate limit hit` from any Git operation | **P1** | [`git-provider-rate-limited.md`](git-provider-rate-limited.md) | Common during burst registration / addon enable. PAT quota exhausted; addon-cycle failures spike. Mitigation = rotate to less-loaded PAT or back off cadence. Grouped with the GitHub Contents API 403 below into ONE runbook — same root cause, same mitigation. |
| GitHub Contents API 403 on `managed-clusters.yaml` read (`audit.action=git_read`) | **P1** | [`git-provider-rate-limited.md`](git-provider-rate-limited.md) | Reconciler tick logs `git_fetch_failed`; existing labeled Secrets are untouched, but new registrations / removals stall. Grouped into ONE runbook. |
| Catalog source signature verification failed for one entry — `Warn` line `"catalog source sidecar verification errored"` | **P1** | [`catalog-trust-policy.md`](catalog-trust-policy.md) | Existing runbook explains trust-policy regex semantics; verify it covers the "single-entry failed" case explicitly. |
| Catalog source schema validation failed — `Warn` line `"catalog source schema validation failed"` | **P1** | [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md) | Third-party catalog YAML doesn't conform to v1.23+ schema. Source skipped; embedded catalog unaffected. |
| Catalog source SSRF guard blocked URL — `Warn` line `"catalog source blocked by runtime SSRF guard"` | **P1** | [`catalog-sources.md`](catalog-sources.md) | Existing page documents `SHARKO_CATALOG_URLS_ALLOW_PRIVATE`; verify runbook explicitly covers SSRF block error. |
| Catalog source HTTP fetch failed — `Warn` line `"catalog source fetch failed"` | **P1** | [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md) | Third-party catalog source 5xx / DNS / TLS. Source skipped; embedded catalog unaffected. |
| Catalog signature workflow_ref doesn't match policy (cert claim assertion fails) — a `Warn` line naming the claim mismatch | **P1** | [`catalog-trust-policy.md`](catalog-trust-policy.md) | Existing page covers; verify it includes the workflow_ref assertion variant. |
| ArgoCD cluster-secret has invalid CA data — the base64 in `tlsClientConfig.caData` will not decode | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Manual / external Secret edit corrupted base64. Single cluster fails; others fine. Grouped with empty-server-URL + kubeconfig-parse failures into ONE runbook — same diagnosis, same mitigation. |
| ArgoCD cluster-secret has empty server URL — `data["server"]` is missing or empty | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Same shape as above — corrupted external edit. Grouped into ONE runbook. |
| Synthesized kubeconfig fails to parse — the kubeconfig Sharko builds from the Secret will not parse back | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Sharko-internal construction error; usually caused by a malformed upstream ArgoCD cluster secret. Grouped into ONE runbook. |
| AWS SM secret not found by any prefix — log line `"[provider] GetCredentials failed"` with `step=all-lookups` and a `tried` list | **P1** | [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) | Path mismatch between Helm value and actual SM layout. Per-cluster failure. The "Tried: ..." text is no longer in the API response (hotfix 2026-08-11) — the `tried` list is on the log line, which is where the runbook reads it. The `suggestions` list in the response still works and is often the whole answer. |
| AWS SM AccessDenied on Search — `Warn` `"SearchSecrets failed (likely AccessDenied, returning empty)"` with `step=list-secrets` | **P1** | [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md) | IAM role missing `secretsmanager:ListSecrets`. Degrades the "similar secret" suggestions shown after a failed cluster-test (`POST /clusters/{name}/test`) but not registration. The line carries `query`, `prefix` and `step` — no AWS error text (fixed 2026-08-11 — this line used to carry the AWS error's own words). Get the role ARN from `sts get-caller-identity` in the pod, not from a log line. |
| EKS token generation failed — Error log `"[auth] EKS token generation failed"`, told apart by `step` (`load-aws-config` / `presign-get-caller-identity`); the caller logs `step=sts` on the addon-secrets path or `step=mint-eks-token` on the cluster-test path | **P1** | [`eks-token-generation-failed.md`](eks-token-generation-failed.md) | IRSA misconfigured OR target cluster's role missing `eks:GetToken`. Per-cluster failure. No AWS SDK error text anywhere — not in the log AND not in the API response (that changed in the 2026-08-11 hotfix; the response carries a fixed safe sentence). Work from request id + cluster + region + `step`, then run `sts get-caller-identity` / `sts assume-role` from the pod for AWS's own reason. |
| K8s Secrets provider — secret not found in namespace — `"secret for cluster %q not found in namespace %q"` | **P1** | [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md) | Helm `secrets.GITHUB_TOKEN` or analogous path is unset / typo'd. Affects all cluster registrations equally. |
| Azure / GCP provider attempted but not implemented — `"Azure Key Vault provider is not yet implemented"` / `"GCP Secret Manager provider is not yet implemented"` | **P1** | [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md) | v1.x stub returning explicit error. Operator hits this when configuring; should be documented so they know it's a known gap not a bug. The two rows (Azure + GCP) are grouped into ONE runbook — same stub shape, same mitigation lanes. |
| ArgoCD account token expired / revoked — every ArgoCD call returns 401/403, audit shows no successful `cluster_secret_create` since rotation | **P1** | [`argocd-account-token-expired.md`](argocd-account-token-expired.md) | Common after manual rotation. Distinguish from "ArgoCD unreachable" (P0) — connectivity is fine, just unauthorized. |
| Webhook handler returns 401 (Git provider webhook signature didn't validate) | **P1** | [`webhook-handler-failures.md`](webhook-handler-failures.md) | Either the webhook secret does not match, or the webhook source is not the expected Git provider. Grouped with "Webhook receive error (any code path)" below into ONE runbook — shared diagnosis tree. |
| Init operation abandoned — client crashed mid-flight, server logs `"init operation abandoned — no heartbeat from client"` | **P1** | [`init-operation-abandoned.md`](init-operation-abandoned.md) | Currently logs at Info; should be reclassified to Warn. Detection: audit `init_run` with no completion, plus the log line. |
| Cluster orphan-delete rejected (HTTP 400) for unlabeled Secret — `audit.action=cluster_orphan_delete_rejected` | **P1** | [`cluster-reconciler.md#what-happens-if-a-user-removes-the-label-manually`](cluster-reconciler.md#what-happens-if-a-user-removes-the-label-manually) | The label gate working as designed; operator may need guidance on why their delete attempt is being blocked. |
| Catalog parse failure on startup — `"catalog: parse yaml"` | **P1** | [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md) | Embedded catalog corrupted (development bug) OR third-party catalog malformed YAML (`SHARKO_CATALOG_URLS`). If embedded fails, no addons surface — escalates toward P0. |
| Auto-merge failed after PR opened — Error log `"RegisterCluster: PR opened but auto-merge failed"` | **P1** | [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md) | PR is open; merge bot couldn't merge. Common: branch protection rules, required reviewers, status checks pending. Distinguish from "PR opened with auto-merge disabled" config. |
| Smart-values AI annotation blocked — secret-leak pattern matched, audit `ai_annotate_blocked` | **P1** | [`ai-annotation-secret-blocked.md`](ai-annotation-secret-blocked.md) | The AI tried to write a value matching a credential heuristic, so the pass was aborted. Affects one cluster's values render. |
| Connection config encryption key missing — `"encryption key not configured"` | **P1** | [`encryption-key-not-configured.md`](encryption-key-not-configured.md) | Helm value `config.connectionSecretName` unset or its `key` field is missing. Operators cannot set their personal GitHub token until resolved. |
| Cluster reconciler dependency missing — Warn `"no GitProvider getter configured, skipping reconcile"` / `"no ArgoClient (k8s clientset) configured"` / `"no Vault (cluster-credentials provider) configured"` | **P1** | [`cluster-reconciler-dependency-missing.md`](cluster-reconciler-dependency-missing.md) | Misconfiguration at startup; reconciler runs but is a no-op. Detection: reconciler audit ticks present but the `reconcile` action result is `skipped`. |
| Adopt flow: managed-by label could not be read on existing Secret — `Warn` `"could not read managed-by label — proceeding with adoption"` | **P1** | [`adopt-managed-by-label-read-failed.md`](adopt-managed-by-label-read-failed.md) | RBAC issue reading the Secret. Adopt proceeds (the label add is idempotent) but the operator should know. |
| Adopt flow: cluster entry write to managed-clusters.yaml failed — `Error` `"failed to add cluster entry"` | **P1** | [`adopt-cluster-entry-write-failed.md`](adopt-cluster-entry-write-failed.md) | Git write failed mid-adoption. State is inconsistent: ArgoCD Secret is labeled, but Git declaration is missing the entry. The next reconciler tick will try to delete the Secret. |
| AI provider misconfigured — agent calls fail with `503` and `"no provider configured"`, or with a per-provider auth error | **P1** | [`ai-provider-misconfigured.md`](ai-provider-misconfigured.md) | Operator hasn't set `ai.apiKey` or the configured provider rejected the request. Affects AI features only; core flows unaffected. |
| Webhook receive error (any code path) | **P1** | [`webhook-handler-failures.md`](webhook-handler-failures.md) | Git provider webhook delivery succeeded but Sharko handler returned non-2xx. PR-tracker state diverges from reality until the next poll. Grouped into ONE runbook. |
| Self-managed ArgoCD cluster secret deleted by the orphan sweep after a mode flip + entry removal in ONE Git PR (cluster connection vanishes; ArgoCD shows the cluster gone) | **P1** | [`self-managed-connections.md#switching-to-self-managed-and-then-removing-flip-wait-for-a-sync-then-remove`](self-managed-connections.md#switching-to-self-managed-and-then-removing-flip-wait-for-a-sync-then-remove) | The label handover needs one reconcile tick between the flip and the removal; a single PR that does both bypasses every strip. Removals through Sharko's API/UI strip eagerly; direct Git edits do not. Mitigation: recreate the secret from your own source of truth. |

### P2 (next sprint)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| Single addon's PR upgrade fails (e.g. version not found in chart repo) — `POST /addons/{name}/upgrade` returns 400 | P2 | **GAP — P2** | Operator-correctable error; no fleet-wide impact. Fix is "use a valid version." |
| Catalog scorecard refresh failed for an addon — `Warn` `"catalog: scorecard refresh failed"` | P2 | **GAP — P2** | Scorecard is cosmetic UI data; entry still functions. |
| Catalog cache hit/miss anomaly — read returns stale entries because cache key collided | P2 | **GAP — P2** | Diagnostic-only; eventual consistency self-corrects. Audit via `catalog_reprobe` audit event. |
| Audit log SSE stream connection dropped mid-stream — UI shows stale `last-event-id` | P2 | **GAP — P2** | Browser reconnects on next page focus; cosmetic only. |
| Audit log ring buffer wrapped — UI shows "earliest entry truncated" notice | P2 | [`audit-log.md`](audit-log.md) | Existing page documents the 1000-entry cap and that there is no fallback. |
| Dashboard "fleet status" surfaces ArgoCD-unreachable flag (handler returns 200 with `argocd_reachable: false` instead of 5xx) | P2 | [`budget-burn-runbook.md#sharkodashboardreadfastburn`](budget-burn-runbook.md#sharkodashboardreadfastburn) | The burn-rate runbook documents the graceful-degradation pattern; the P2 case is when degradation persists too long to be transient. |
| Catalog source slow but functional — fetches taking >5s but succeeding | P2 | **GAP — P2** | Tracks as a sizing issue, not a bug. Surface via metric, not page. |
| Validate-config CLI returns failure on a YAML file (`sharko validate-config docs/site/configuration/`) | P2 | **GAP — P2** | Operator-correctable: edit the YAML so it matches the embedded JSON Schema (`apiVersion: sharko.dev/v1` envelope). |
| `validate` legacy CLI returns failure (pre-envelope validator) | P2 | **GAP — P2** | Legacy command slated for removal; document that operators should migrate to `validate-config`. |
| 404 on unmounted API route — wrong path or version | P2 | **GAP — P2** | Operator-correctable. Fix is "read the API reference." |
| Token revocation succeeded but token still works for one request (race) | P2 | **GAP — P2** | Token cache TTL = 60s by default; window is narrow. Document for security-conscious operators. |
| Connection test (`/connections/{id}/test`) returns success but actual cluster operation fails later | P2 | **GAP — P2** | Connection test is a smoke probe, not a guarantee. Document the test's actual scope. |
| Init operation cancelled via API — audit `operation_cancelled` after `init_run` start | P2 | **GAP — P2** | Operator intent; not a failure mode per se, but should be documented as a recoverable abort. |
| Notification delivery failed (chat or email webhook returns 5xx) | P2 | **GAP — P2** | Notification system is best-effort; failures are visible only in logs. |
| Cluster-secret reconciler tick took longer than `DefaultTickInterval` (30s) — overlapping tick prevention kicked in | P2 | [`cluster-reconciler.md`](cluster-reconciler.md) | Existing page covers the 30s tick; convergence-cost-growing root cause should be added if not present. |
| Dashboards CRUD (`/api/v1/dashboards`) error — saving / loading user dashboard configs fails | P2 | **GAP — P2** | UI personalization feature; no fleet impact. |
| Cluster removal leaves the ArgoCD cluster secret in place — removal response contains `skip_argocd_secret_not_sharko_labeled` | P2 | [`self-managed-connections.md#switching-to-self-managed-and-then-removing-flip-wait-for-a-sync-then-remove`](self-managed-connections.md#switching-to-self-managed-and-then-removing-flip-wait-for-a-sync-then-remove) | The ownership gate working as designed: the secret does not carry `app.kubernetes.io/managed-by: sharko`, so Sharko refuses the delete (fail-safe). Delete the secret by hand if it really should go. |
