# Failure Mode Index

> **Verified:** Inventory compiled 2026-06-01 against `main` HEAD at
> the start of the V2-4 sprint. Sources audited:
> `internal/api/router.go` + per-route handlers (50 handler files, ~395
> error-emitting sites bucketed into operator-observable failure
> modes), `internal/clusterreconciler/`, `internal/argosecrets/`,
> `internal/orchestrator/`, `internal/providers/`, `internal/catalog/`,
> `internal/catalog/sources/`, and the audit-log action codes
> documented in `docs/site/developer-guide/logging.md`. Re-audit each
> minor release; remove entries as runbooks close `GAP` markers.

This page is **the first place an operator should search when they hit
a Sharko error.** Ctrl-F your error message, find the failure-mode
row, follow the `Runbook URL` column to the runbook that covers it.

If the `Runbook URL` column says **`GAP`**, no runbook exists yet for
this failure. File an issue (or, if the page is in your hand because
you're paging the maintainer, include the failure-mode row text in the
escalation). The V2-4.3 sub-sprint closes every P0 and P1 GAP; V2-4.4
brings existing runbooks into compliance with the
[style guide](../developer-guide/runbook-style-guide.md).

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

The vocabulary mirrors the V2-3 Prometheus alert severity labels in
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

The maintainer adds the row here and assigns the runbook gap to the
next V2-4.x sub-sprint. See
[`.claude/team/docs-writer.md`](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/docs-writer.md)
for the broader docs contribution flow and the verified-by-execution
rule that runbook PRs must satisfy.

---

## Failure modes

Sorted by severity (P0 first), then by surface (API → reconciler →
orchestrator → provider → catalog → audit-log). `GAP` entries are
**bolded with the GAP token** so PR 2 (V2-4.3) can grep them with
confidence: `grep -nE '\*\*GAP — P[01]\*\*' failure-mode-index.md`.

### P0 (page on-call)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| ArgoCD upstream unreachable (any handler that calls ArgoCD returns 502 / `"no active ArgoCD connection"`) | **P0** | [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) | Surfaces from every cluster, addon, and dashboard handler. Single root cause (ArgoCD outage / token revoked / network policy block); shared mitigation. Grouped as ONE runbook (V2-4.3 PR 2a). |
| Git provider upstream unreachable (any handler that opens a PR returns 502 / `"no active Git connection"`) | **P0** | [`git-provider-unreachable.md`](git-provider-unreachable.md) | Surfaces from every cluster + addon write handler. Single root cause; shared mitigation. Grouped as ONE runbook (V2-4.3 PR 2a). |
| Cluster registration broken — SharkoClusterRegistrationFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn) | V2-3.4 runbook already covers; alert wired to anchor. |
| Addon enable / disable / upgrade cycle broken — SharkoAddonCycleFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkoaddoncyclefastburn`](budget-burn-runbook.md#sharkoaddoncyclefastburn) | V2-3.4 runbook. |
| Catalog scan broken — SharkoCatalogScanFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkocatalogscanfastburn`](budget-burn-runbook.md#sharkocatalogscanfastburn) | V2-3.4 runbook. |
| Dashboard reads broken — SharkoDashboardReadFastBurn alert | **P0** | [`budget-burn-runbook.md#sharkodashboardreadfastburn`](budget-burn-runbook.md#sharkodashboardreadfastburn) | V2-3.4 runbook. |
| Cluster reconciler crash loop (`Reconciler.pollOnce` panics or returns from `Start` unexpectedly; goroutine exits) | **P0** | [`reconciler-crash-loop.md`](reconciler-crash-loop.md) | Reconciler is the canonical ArgoCD-secret writer; if the goroutine dies, fleet drifts silently. Detection: absence of `recon-<ts>` request_ids in the log. |
| `managed-clusters.yaml` schema validation failed — reconciler refuses to act (`audit.action=schema_validation`, `audit.event=cluster_secret_reconcile`) | **P0** | [`cluster-reconciler.md#what-if-managed-clustersyaml-has-a-schema-validation-error`](cluster-reconciler.md#what-if-managed-clustersyaml-has-a-schema-validation-error) | Existing coverage. Severity is P0 because **all** reconciliation halts, not just one cluster. |
| Secret push to remote cluster silently failed (orchestrator logs `Error "failed to create secret, continuing"` in `secrets.go:110`) | **P0** | [`secret-push-silently-failed.md`](secret-push-silently-failed.md) | Per [`logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md), the "continuing" path is silent data loss — user thinks credential was pushed; actually wasn't. Cluster will fail downstream. |
| Orchestrator PR merged but ArgoCD never converges (addon cycle audit shows `pr_merged` then no `cluster_secret_create` / sync event) | **P0** | [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md) | Indicates either reconciler is stuck OR ArgoCD application controller is degraded. Diagnosis path: distinguish which side. |
| Auth bypass — `/api/v1/auth/login` returns 200 for invalid credentials, or session cookie is honored after expiry | **P0** | [`auth-bypass.md`](auth-bypass.md) | Pure security failure. Detection: audit `login_failed` count drops to zero while traffic continues. Includes the V125-1-7 token-leak class. |
| Bootstrap admin password leak — admin password visible in pod logs as plain-text attribute (`auth/store.go:634`) | **P0** | [`credential-leak-in-logs.md`](credential-leak-in-logs.md) | V2-2.4 RedactHandler now collapses to `[REDACTED]` but call site is still wrong (per [`logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md) headline finding). Treat as P0 because regression would re-expose admin creds. Grouped with the broader credential-leak failure mode in V2-4.3 PR 2a (shared diagnosis + mitigation). |
| Kubeconfig / token leak in logs (any credential-shaped value bypasses RedactHandler heuristics) | **P0** | [`credential-leak-in-logs.md`](credential-leak-in-logs.md) | RedactHandler in `internal/logging/redact.go` is defense-in-depth; failure mode is "a new sink bypasses the wrapper, or a value evades all three detectors." Detection: scan logs for `eyJ`-prefixed JWTs, kubeconfig contexts, or base64 blobs >100 chars. Grouped with bootstrap-admin-password leak in V2-4.3 PR 2a. |
| ArgoCD cluster-secret out-of-band deletion not self-healed (labeled Secret deleted; next reconciler tick does NOT recreate within 30s) | **P0** | [`cluster-reconciler.md#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete`](cluster-reconciler.md#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete) | Existing coverage; the P0 case is when self-heal *fails*, not the routine self-heal. Verify the runbook covers the failure case explicitly. |
| Secrets provider (AWS SM / K8s Secrets / Vault) completely unreachable — `health.Check` returns non-nil for the active provider | **P0** | [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) | Affects every cluster registration AND every reconciler tick. Single root cause per provider; grouped as one runbook covering all 3 sub-cases (AWS / k8s / vault) in V2-4.3 PR 2a. |
| Catalog signing trust root unavailable — `internal/catalog/signing/verify.go` cannot load `trusted_root.json` from TUF | **P0** | [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md) | Every verified-catalog entry fails verification; the marketplace surfaces every entry as Unverified. Per the [catalog-trust-policy](catalog-trust-policy.md) runbook context. |
| Init operation deadlocked (`POST /api/v1/init` returns 202, operation_id never reaches terminal state, heartbeat stops) | **P0** | [`init-operation-deadlocked.md`](init-operation-deadlocked.md) | The documented async exception; if init wedges, the bootstrap repo is in an unknown state. Detection: audit shows `init_run` start but no completion. |
| OOM kill / process restart loop (Sharko pod restarting >3× / 5min) | **P0** | [`oom-restart-loop.md`](oom-restart-loop.md) | Kubernetes CrashLoopBackoff state. Not Sharko-emitted; detected via `kubectl get pod` Restarts column. |

### P1 (file ticket; fix next business day)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| Cluster registration broken (sustained burn) — SharkoClusterRegistrationSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkoclusterregistrationslowburn`](budget-burn-runbook.md#sharkoclusterregistrationslowburn) | V2-3.4 runbook. |
| Addon cycle broken (sustained burn) — SharkoAddonCycleSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkoaddoncycleslowburn`](budget-burn-runbook.md#sharkoaddoncycleslowburn) | V2-3.4 runbook. |
| Catalog scan broken (sustained burn) — SharkoCatalogScanSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkocatalogscanslowburn`](budget-burn-runbook.md#sharkocatalogscanslowburn) | V2-3.4 runbook. |
| Dashboard reads broken (sustained burn) — SharkoDashboardReadSlowBurn alert | **P1** | [`budget-burn-runbook.md#sharkodashboardreadslowburn`](budget-burn-runbook.md#sharkodashboardreadslowburn) | V2-3.4 runbook. |
| Single cluster's credential fetch fails — `audit.action=get_credentials` with `result=failed` for one cluster across multiple ticks | **P1** | [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md) | Per-cluster vault failure (creds rotated, IRSA misconfigured, secret path moved). Other clusters reconcile normally; only one is stuck. Closed by V2-4.3 PR 2c. |
| Cluster test (`POST /clusters/{name}/test`) returns 503 for AWS IAM cluster (`iam_auth_unsupported_in_v1`) | **P1** | [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) | Existing v1.x limitation runbook; v2 lands the fix. Severity is P1 not P2 because operators repeatedly hit this and it confuses them. |
| Cluster test returns 503 for exec-plugin auth (`ErrArgoCDProviderExecUnsupported` in `internal/providers/argocd_provider.go`) | **P1** | [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md) | Adjacent to AWS IAM but a distinct provider error path; not covered by `aws-iam-cluster-auth.md`. Closed by V2-4.3 PR 2b. |
| Single ArgoCD Application stuck Degraded after addon enable (PR merged, audit shows `addon_enabled_on_cluster` success, but ArgoCD shows `Degraded`) | **P1** | [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md) | Addon-specific issue (bad chart values, namespace clash, RBAC denied). Mitigation = inspect Application directly in ArgoCD. Closed by V2-4.3 PR 2c. |
| Git provider rate limit hit — `Warn` log lines containing `rate limit hit` from `internal/orchestrator/git_helpers.go` or PR-open paths | **P1** | [`git-provider-rate-limited.md`](git-provider-rate-limited.md) | Common during burst registration / addon enable. PAT quota exhausted; addon-cycle failures spike. Mitigation = rotate to less-loaded PAT or back off cadence. Grouped with GitHub Contents API 403 below into ONE runbook per V2-4.3 PR 2c grouping decision (same root cause, same mitigation). |
| GitHub Contents API 403 on `managed-clusters.yaml` read (`audit.action=git_read`) | **P1** | [`git-provider-rate-limited.md`](git-provider-rate-limited.md) | Reconciler tick logs `git_fetch_failed`; existing labeled Secrets are untouched, but new registrations / removals stall. Grouped into ONE runbook per V2-4.3 PR 2c. |
| Catalog source signature verification failed for one entry — `Warn` line `"catalog source sidecar verification errored"` from `internal/catalog/sources/fetcher.go:728` | **P1** | [`catalog-trust-policy.md`](catalog-trust-policy.md) | Existing runbook explains trust-policy regex semantics; verify it covers the "single-entry failed" case explicitly. |
| Catalog source schema validation failed — `Warn` line `"catalog source schema validation failed"` from `internal/catalog/sources/fetcher.go:708` | **P1** | [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md) | Third-party catalog YAML doesn't conform to v1.23+ schema. Source skipped; embedded catalog unaffected. Closed by V2-4.3 PR 2b. |
| Catalog source SSRF guard blocked URL — `Warn` line `"catalog source blocked by runtime SSRF guard"` from `internal/catalog/sources/fetcher.go:659` | **P1** | [`catalog-sources.md`](catalog-sources.md) | Existing page documents `SHARKO_CATALOG_URLS_ALLOW_PRIVATE`; verify runbook explicitly covers SSRF block error. |
| Catalog source HTTP fetch failed — `Warn` line `"catalog source fetch failed"` from `internal/catalog/sources/fetcher.go:681` | **P1** | [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md) | Third-party catalog source 5xx / DNS / TLS. Source skipped; embedded catalog unaffected. Closed by V2-4.3 PR 2b. |
| Catalog signature workflow_ref doesn't match policy (cert claim assertion fails) — `Warn` from `internal/catalog/signing/verify.go:388` | **P1** | [`catalog-trust-policy.md`](catalog-trust-policy.md) | Existing page covers; verify it includes the workflow_ref assertion variant. |
| ArgoCD cluster-secret has invalid CA data — `apierrors`-wrapped failure decoding `tlsClientConfig.caData` (`internal/providers/argocd_provider.go:332`) | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Manual / external Secret edit corrupted base64. Single cluster fails; others fine. Grouped with empty-server-URL + kubeconfig-parse failures into ONE runbook per V2-4.3 PR 2b grouping decision (same diagnosis, same mitigation). |
| ArgoCD cluster-secret has empty server URL — `internal/providers/argocd_provider.go:325` | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Same shape as above — corrupted external edit. Grouped into ONE runbook per V2-4.3 PR 2b. |
| Synthesized kubeconfig fails to parse — `internal/providers/argocd_provider.go:409` | **P1** | [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md) | Sharko-internal construction error; usually caused by upstream argocd cluster secret malformed. Grouped into ONE runbook per V2-4.3 PR 2b. |
| AWS SM secret not found by any prefix — `internal/providers/aws_sm.go:150` `"secret for cluster X not found in AWS Secrets Manager. Tried: ..."` | **P1** | [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) | Path mismatch between Helm value and actual SM layout. Per-cluster failure. Closed by V2-4.3 PR 2b. |
| AWS SM AccessDenied on Search — `Warn` `"SearchSecrets failed (likely AccessDenied, returning empty)"` from `aws_sm.go:171` | **P1** | [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md) | IAM role missing `secretsmanager:ListSecrets`. Degrades discovery (`POST /clusters/discover`) but not registration. Closed by V2-4.3 PR 2b. |
| EKS token generation failed — `internal/providers/aws_auth.go:40,72` `slog.Error("[auth] EKS token generation failed"...)` | **P1** | [`eks-token-generation-failed.md`](eks-token-generation-failed.md) | IRSA misconfigured OR target cluster's role missing `eks:GetToken`. Per-cluster failure. Closed by V2-4.3 PR 2b. |
| EKS discover failed for a role — `Warn` `"[discover] failed to scan identity"` from `eks_discover.go:71` | **P1** | [`eks-discover-failed.md`](eks-discover-failed.md) | Cross-account role broken or `sts:AssumeRole` denied. Discovery returns partial results; user sees missing clusters in `POST /clusters/discover` response. Closed by V2-4.3 PR 2b. |
| K8s Secrets provider — secret not found in namespace — `k8s_secrets.go:142` | **P1** | [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md) | Helm `secrets.GITHUB_TOKEN` or analogous path is unset / typo'd. Affects all cluster registrations equally. Closed by V2-4.3 PR 2b. |
| Azure / GCP provider attempted but not implemented — `"Azure Key Vault provider is not yet implemented"` / `"GCP Secret Manager provider is not yet implemented"` from `internal/providers/{azure,gcp}.go` | **P1** | [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md) | v1.x stub returning explicit error. Operator hits this when configuring; should be documented so they know it's a known gap not a bug. Two failure-mode rows (Azure + GCP) grouped into ONE runbook per V2-4.3 PR 2b grouping decision (same stub shape, same mitigation lanes). |
| ArgoCD account token expired / revoked — every ArgoCD call returns 401/403, audit shows no successful `cluster_secret_create` since rotation | **P1** | [`argocd-account-token-expired.md`](argocd-account-token-expired.md) | Common after manual rotation. Distinguish from "ArgoCD unreachable" (P0) — connectivity is fine, just unauthorized. Closed by V2-4.3 PR 2b. |
| Webhook handler returns 401 (Git provider webhook signature didn't validate) — `internal/api/webhooks.go` | **P1** | [`webhook-handler-failures.md`](webhook-handler-failures.md) | Either webhook secret mismatch or webhook source isn't the expected git provider. Per [V2-2 audit](../developer-guide/logging-audit-punchlist.md). Grouped with "Webhook receive error (any code path)" below into ONE runbook per V2-4.3 PR 2c grouping decision (shared diagnosis tree). |
| Init operation abandoned — client crashed mid-flight, server logs `"init operation abandoned — no heartbeat from client"` (`internal/api/init.go:384`) | **P1** | [`init-operation-abandoned.md`](init-operation-abandoned.md) | Currently logs at Info per audit punch list; should be reclassified to Warn. Detection: audit `init_run` with no completion + log line. Closed by V2-4.3 PR 2c. |
| Cluster orphan-delete rejected (HTTP 400) for unlabeled Secret — `audit.action=cluster_orphan_delete_rejected` | **P1** | [`cluster-reconciler.md#what-happens-if-a-user-removes-the-label-manually`](cluster-reconciler.md#what-happens-if-a-user-removes-the-label-manually) | V125-1-8.2 label gate working as designed; operator may need guidance on why their delete attempt is being blocked. |
| Catalog parse failure on startup — `internal/catalog/loader.go:332` `"catalog: parse yaml"` | **P1** | [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md) | Embedded catalog corrupted (development bug) OR third-party catalog malformed YAML (`SHARKO_CATALOG_URLS`). If embedded fails, no addons surface — escalates toward P0. Closed by V2-4.3 PR 2b. |
| Auto-merge failed after PR opened — `internal/orchestrator/cluster.go:335` `slog.Error("RegisterCluster: PR opened but auto-merge failed", ...)` | **P1** | [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md) | PR is open; merge bot couldn't merge. Common: branch protection rules, required reviewers, status checks pending. Distinguish from "PR opened with auto-merge disabled" config. Closed by V2-4.3 PR 2c. |
| Smart-values AI annotation blocked — secret-leak pattern matched, audit `ai_annotate_blocked` | **P1** | [`ai-annotation-secret-blocked.md`](ai-annotation-secret-blocked.md) | Per `internal/orchestrator/ai_annotate.go:127`. AI tried to write a value matching a credential heuristic; pass aborted. Affects one cluster's values render. Closed by V2-4.3 PR 2c. |
| Connection config encryption key missing — `users_me.go:109,190` `"encryption key not configured"` | **P1** | [`encryption-key-not-configured.md`](encryption-key-not-configured.md) | Helm value `config.connectionSecretName` unset or its `key` field is missing. Operators cannot set their personal GitHub token until resolved. Closed by V2-4.3 PR 2c. |
| Cluster reconciler dependency missing — Warn `"no GitProvider getter configured, skipping reconcile"` / `"no ArgoClient (k8s clientset) configured"` / `"no Vault (cluster-credentials provider) configured"` from `reconciler.go:325-340` | **P1** | [`cluster-reconciler-dependency-missing.md`](cluster-reconciler-dependency-missing.md) | Misconfiguration at startup; reconciler runs but is a no-op. Detection: reconciler audit ticks present but `reconcile` action result is `skipped`. Closed by V2-4.3 PR 2c. |
| Adopt flow: managed-by label could not be read on existing Secret — `Warn` `"could not read managed-by label — proceeding with adoption"` from `internal/orchestrator/adopt.go:60` | **P1** | [`adopt-managed-by-label-read-failed.md`](adopt-managed-by-label-read-failed.md) | RBAC issue reading the Secret. Adopt proceeds (idempotent label add) but operator should know. Closed by V2-4.3 PR 2c. |
| Adopt flow: cluster entry write to managed-clusters.yaml failed — `Error` `"failed to add cluster entry"` from `internal/orchestrator/adopt.go:196` | **P1** | [`adopt-cluster-entry-write-failed.md`](adopt-cluster-entry-write-failed.md) | Git write failed mid-adoption. State is inconsistent: ArgoCD Secret is labeled, but git declaration is missing the entry. Next reconciler tick will try to delete the Secret. Closed by V2-4.3 PR 2c. |
| AI provider misconfigured — agent calls fail with `503` from `internal/api/system.go:125` `"no provider configured"` or per-provider auth error | **P1** | [`ai-provider-misconfigured.md`](ai-provider-misconfigured.md) | Operator hasn't set `ai.apiKey` or the configured provider rejected the request. Affects AI features only; core flows unaffected. Closed by V2-4.3 PR 2c. |
| Webhook receive error (any code path) | **P1** | [`webhook-handler-failures.md`](webhook-handler-failures.md) | Git provider webhook delivery succeeded but Sharko handler returned non-2xx. PR-tracker state diverges from reality until next poll. Grouped into ONE runbook per V2-4.3 PR 2c. |

### P2 (next sprint)

| Failure mode | Severity | Runbook URL | Notes |
|---|---|---|---|
| Single addon's PR upgrade fails (e.g. version not found in chart repo) — `POST /addons/{name}/upgrade` returns 400 from `internal/api/addons_upgrade.go` | P2 | **GAP — P2** | Operator-correctable error; no fleet-wide impact. Fix is "use a valid version." |
| Catalog scorecard refresh failed for an addon — `Warn` `"catalog: scorecard refresh failed"` from `internal/catalog/scorecard.go:190` | P2 | **GAP — P2** | Scorecard is cosmetic UI data; entry still functions. |
| Catalog cache hit/miss anomaly — read returns stale entries because cache key collided | P2 | **GAP — P2** | Diagnostic-only; eventual consistency self-corrects. Audit via `catalog_reprobe` audit event. |
| Audit log SSE stream connection dropped mid-stream — UI shows stale `last-event-id` | P2 | **GAP — P2** | Browser reconnects on next page focus; cosmetic only. |
| Audit log ring buffer wrapped — UI shows "earliest entry truncated" notice | P2 | [`audit-log.md`](audit-log.md) | Existing page documents the 1000-entry cap and stdout fallback. |
| Cluster-discover (`POST /clusters/discover`) returns partial list — some EKS clusters in account but not surfaced | P2 | **GAP — P2** | Per-region IAM gap; operator may not have intended all regions. Document the diagnostic check. |
| Dashboard "fleet status" surfaces ArgoCD-unreachable flag (handler returns 200 with `argocd_reachable: false` instead of 5xx) | P2 | [`budget-burn-runbook.md#sharkodashboardreadfastburn`](budget-burn-runbook.md#sharkodashboardreadfastburn) | V2-3.4 documents the graceful-degradation pattern; the P2 case is when degradation persists too long to be transient. |
| Catalog source slow but functional — fetches taking >5s but succeeding | P2 | **GAP — P2** | Tracks as a sizing issue, not a bug. Surface via metric, not page. |
| Validate-config CLI returns failure on a YAML file (`sharko validate-config docs/site/configuration/`) | P2 | [`yaml-schema-migration.md`](yaml-schema-migration.md) | Existing migration runbook covers the schema; the P2 case is "operator edited YAML and broke it" — fix is "obey the schema." |
| `validate` legacy CLI returns failure (pre-envelope validator) | P2 | [`yaml-schema-migration.md`](yaml-schema-migration.md) | Legacy command slated for V126 removal; document that operators should migrate to `validate-config`. |
| 404 on unmounted API route — wrong path or version | P2 | **GAP — P2** | Operator-correctable. Fix is "read the API reference." |
| Token revocation succeeded but token still works for one request (race) | P2 | **GAP — P2** | Token cache TTL = 60s by default; window is narrow. Document for security-conscious operators. |
| Connection test (`/connections/{id}/test`) returns success but actual cluster operation fails later | P2 | **GAP — P2** | Connection test is a smoke probe, not a guarantee. Document the test's actual scope. |
| Init operation cancelled via API — audit `operation_cancelled` after `init_run` start | P2 | **GAP — P2** | Operator intent; not a failure mode per se, but should be documented as a recoverable abort. |
| Notification delivery failed (Slack / email webhook 5xx) — internal `notifications` package | P2 | **GAP — P2** | Notification system is best-effort; failures are visible only in logs. |
| Cluster-secret reconciler tick took longer than `DefaultTickInterval` (30s) — overlapping tick prevention kicked in | P2 | [`cluster-reconciler.md`](cluster-reconciler.md) | Existing page covers the 30s tick; convergence-cost-growing root cause should be added if not present. |
| Dashboards CRUD (`/api/v1/dashboards`) error — saving / loading user dashboard configs fails | P2 | **GAP — P2** | UI personalization feature; no fleet impact. |

---

## Drift findings appendix

Per V2-4.2 deliverable: each runbook-shaped operator page below was
audited against the
[runbook style guide](../developer-guide/runbook-style-guide.md)
compliance checklist. Findings are listed verbatim; **fixes are V2-4.4
(PR 3) scope, not addressed in this audit PR.**

`docs/site/operator/budget-burn-runbook.md` is **exempt** — V2-3.4
authored it after the style guide was drafted and it already
conforms.

### `aws-iam-cluster-auth.md`

- **Length:** 41 lines (well under 300-line floor). Justifies its
  brevity in the intro ("v1.x does not ship the cloud-creds plumbing;
  this is a stub"), so the length is intentional. PR 3: optionally
  pad to the floor with a more developed "what you can do today"
  section, or keep the explicit-brevity carve-out — author's call.
- **Missing required section:** `## Severity` — should be `P1`.
- **Missing required section:** `## Diagnosis` — currently mixes
  diagnosis hints into "What this page is for" intro.
- **Missing required section:** `## Mitigation` — currently labeled
  "What you can do today (v1.x workaround)".
- **Missing required section:** `## Root-cause patterns` — currently
  the "What's needed (when v2 lands)" section serves this role but
  doesn't match the required section name.
- **Missing required section:** `## Prevention` — currently absent.
  Acceptable Prevention text: "Move to v2 when it ships; until then,
  document IAM-auth EKS clusters in the registration confirmation
  flow so operators see the limitation upfront."
- **Missing required section:** `## Related runbooks` — currently
  links only to a design doc.
- **Missing required:** `> **Verified:**` header.
- **Section name standardization:** "What you can do today" →
  `Mitigation`; "What's needed (when v2 lands)" → consolidate into
  `Root-cause patterns` + `Prevention`.

### `audit-log.md`

- **Length:** 81 lines (under 300-line floor). This is a
  **reference page, not a runbook** — it documents the audit-log
  retention model. Recommendation: reclassify as a reference page
  (no severity, no mitigation), or keep it as a runbook by adding the
  required sections.
- **Required sections missing if treated as a runbook:**
  `Severity`, `Symptoms`, `Diagnosis`, `Mitigation`, `Root-cause
  patterns`, `Prevention`, `Related runbooks`, verified header.
- **Recommendation for PR 3:** Mark as `reference` and exclude from
  the runbook checklist; OR split into two pages — one reference
  (current content) and one runbook ("Audit log buffer wrapped /
  ring-buffer pressure" — a real failure mode covered by the
  reference today).

### `aws-iam-cluster-auth.md` (already covered above)

### `catalog-sources.md`

- **Length:** 125 lines (under 300-line floor). Configuration
  reference, not a failure-mode runbook. Reclassify or restructure.
- **Required sections missing:** Severity, Symptoms, Diagnosis,
  Mitigation, Root-cause patterns, Prevention, Related runbooks,
  verified header.
- **Recommendation for PR 3:** This is a configuration reference;
  PR 3 should EITHER reclassify it as a reference page OR split out a
  failure-mode runbook (e.g. "Third-party catalog source not
  loading") that depends on this reference.

### `catalog-sources-smoke.md`

- **Length:** 301 lines (within range).
- **Section order:** Smoke procedure, not a failure-mode runbook —
  more aligned with developer-guide than operator runbook. Consider
  moving to `docs/site/developer-guide/`.
- **Missing:** Severity, Symptoms section name, verified header.
- **Tone:** Procedural and correct in voice; no marketing copy
  detected.
- **Recommendation for PR 3:** Either restructure as failure-mode
  runbook ("Catalog source onboarding failed") or move to
  developer-guide and skip the runbook checklist.

### `catalog-trust-policy.md`

- **Length:** 294 lines (just under floor — borderline).
- **Section order:** Currently configuration-reference-shaped;
  failure scenarios appear after configuration. PR 3 should
  restructure to lead with Symptoms (e.g. "Marketplace entries
  show as Unverified").
- **Missing required:** `Severity`, `Mitigation` (currently a
  configuration recipe, not a mitigation procedure), `Prevention`,
  `Related runbooks`, verified header.
- **Intro tone:** Acceptable — no marketing copy, but the framing
  is reference-first rather than operator-on-call. PR 3 should
  add an operator-on-call lead-in: "If you're here because the
  marketplace shows entries as Unverified, jump to Mitigation."

### `cluster-connectivity-model.md`

- **Length:** 84 lines (under 300-line floor). This is a
  **reference page**, not a runbook — explains the connectivity
  model. Reclassify and exclude from runbook checklist.

### `cluster-reconciler.md`

- **Length:** 363 lines (in range).
- **Section order:** Overview / Ownership / Cadence / Two-direction
  policy / Recovery / Coexistence / Troubleshooting. The
  Troubleshooting subsections are runbook-shaped but live at the
  bottom; PR 3 should reorder to Symptoms-first OR clearly mark the
  reference half ("Overview", "Ownership label") as the reference
  half and split the troubleshooting half into per-symptom runbooks.
- **Missing required:** `Severity`, `Prevention`, `Related runbooks`
  (currently "Related reading" — rename to standard), `Mitigation`
  section name (currently inline within troubleshooting). Verified
  header is present.
- **Tone:** "Powerful reconciler" doesn't appear; tone is technical
  and operator-appropriate. Intro paragraph could be tightened to
  pager-voice but is acceptable.
- **Recommendation for PR 3:** This page is **two pages welded
  together** — a reference for how the reconciler works and a runbook
  for troubleshooting it. PR 3 should split: keep the reference
  content here, extract the troubleshooting subsections into
  failure-mode-named runbooks per the index (e.g.
  `reconciler-cluster-secret-create-failed.md`).

### `corporate-mitm-tls.md`

- **Length:** 123 lines (under floor).
- **Section order:** Symptom → Cause → "When you need this" → Scope →
  Workaround. **Almost** matches required order — "Cause" maps to
  "Root-cause patterns", "Workaround" maps to "Mitigation". PR 3:
  rename to standard section names.
- **Missing required:** `Severity` (would be P2 — environmental
  workaround), `Diagnosis` (mostly inline), `Prevention` (currently
  absent — acceptable text: "Document corporate proxy in
  pre-install survey; check pre-install"), `Related runbooks`,
  verified header.
- **Tone:** Clean, operator-voice, no marketing.

### `supply-chain.md`

- **Length:** 102 lines (under floor). This is a
  **reference page** for release signing — not a failure-mode
  runbook. Reclassify and exclude from runbook checklist.

### `troubleshooting.md`

- **Length:** 150 lines (under floor).
- **Section structure:** This is a **catch-all troubleshooting
  page** — multiple unrelated failure modes glued together. Each
  section is a mini-runbook ("Connection refused", "401
  Unauthorized", etc.). Per the style guide, these should be split
  into per-failure runbooks named for the failure mode.
- **Missing required across all sub-sections:** `Severity`,
  `Diagnosis` (mostly inline), `Mitigation` numbered (currently
  bulleted), `Root-cause patterns`, `Prevention`, `Related
  runbooks`, verified header.
- **Recommendation for PR 3:** Split each H2 section into its own
  runbook file under `operator/`, then keep `troubleshooting.md` as a
  thin redirector page that links to the split-out runbooks. This is
  the highest-effort PR 3 page; budget accordingly.

### `yaml-schema-migration.md`

- **Length:** 312 lines (in range).
- **Section order:** Reference-first (what changed / why / writer
  / reader / validator surface), then operator action. PR 3 should
  add a leading symptom section ("If `sharko validate-config`
  fails, jump to <section>") so operators in the middle of a
  failure don't have to scroll past the reference content.
- **Missing required:** `Severity` (would be P1), `Mitigation`
  (currently the "What you should do" section — rename), `Prevention`
  (acceptable text: "Use `sharko validate-config` in CI; pre-commit
  hook locally"), `Related runbooks`, verified header.
- **Tone:** Clean, operator-voice. Intro lead-in ("You don't have to
  do anything") is well-calibrated to pager-context.

---

## Statistics

Compiled from the inventory tables above.

| Metric | Value |
|---|---|
| Total failure mode rows | **63** (re-counted at PR 2a close; PR #375 statistics had off-by-N drift) |
| Total `GAP` entries remaining | **12** |
| `GAP` entries at **P0** | **0** (all closed by V2-4.3 PR 2a) |
| `GAP` entries at **P1** | **0** (all 28 closed: 14 by PR 2b, 14 by PR 2c) |
| `GAP` entries at **P2** | **12** (V2-4.x follow-up backlog) |
| Failure modes covered by runbooks | **15 (pre-PR-2a) + 11 (PR-2a new) + 11 (PR-2b new) + 12 (PR-2c new — 14 rows mapped via 2 grouping decisions)** = **49 rows** |
| Existing runbook drift findings | **9 pages** (V2-4.4 / PR 3 scope) |

**V2-4.3 (PR 2a + 2b + 2c) — COMPLETE. All P0 (12) + P1 (28) GAPs
closed. P2 GAPs (12) tracked as V2-4.x follow-up backlog. PR 3
(V2-4.4 existing-runbook style compliance) is the final V2-4
sub-sprint.**

**Statistics note:** at PR 2c close (2026-06-01), the P1 GAP count
moved from 14 → 0 because PR 2c shipped 12 runbooks for 14
failure-mode rows (`git-provider-rate-limited.md` groups 2 rows;
`webhook-handler-failures.md` groups 2 rows; the remaining 10
runbooks are standalone). At PR 2b close (2026-06-01), the P1 GAP
count had moved from 28 → 14 because PR 2b shipped 11 runbooks for
14 failure-mode rows (`argocd-cluster-secret-corruption.md` groups
3 rows; `azure-gcp-provider-unimplemented.md` groups 2 rows; the
remaining 9 runbooks are standalone). The grouping decisions
follow the style guide's "one runbook iff same diagnosis + same
mitigation" rule and are documented in the
[V2-4.3 PR 2b grouping decisions](#v2-43-pr-2b-grouping-decisions-closed-2026-06-01)
and
[V2-4.3 PR 2c grouping decisions](#v2-43-pr-2c-grouping-decisions-closed-2026-06-01)
sub-sections below.

At PR 2a close (2026-06-01), the same block was re-computed via
`grep -cE '\*\*GAP — P[012]\*\*'` to correct PR #375's original
statistics (which claimed 11 P0 / 22 P1 / 9 P2; actual was 12 P0 /
28 P1 / 12 P2). PR 2a's 11 runbooks close all 12 P0 rows (grouping
decision: `credential-leak-in-logs.md` covers two adjacent
failure-mode rows that share diagnosis + mitigation per the style
guide's grouping rule). After PR 2b, the remaining P1 scope is 14
rows (originally 28; 14 closed by PR 2b) tracked for PR 2c.

### V2-4.3 PR 2a grouping decisions (closed 2026-06-01)

11 runbooks written for 12 P0 failure-mode rows:

- **Standalone** (10 runbooks for 10 rows): `argocd-upstream-unreachable.md`,
  `git-provider-unreachable.md`, `reconciler-crash-loop.md`,
  `secret-push-silently-failed.md`, `argocd-pr-merge-no-converge.md`,
  `auth-bypass.md`, `secrets-provider-unreachable.md`,
  `catalog-trust-root-unavailable.md`, `init-operation-deadlocked.md`,
  `oom-restart-loop.md`.
- **Grouped** (1 runbook for 2 rows): `credential-leak-in-logs.md`
  covers both "Bootstrap admin password leak" and "Kubeconfig / token
  leak in logs" — same diagnosis path (grep logs for credential
  shapes; identify call site; check RedactHandler wiring), same
  mitigation steps (rotate credential, purge log destinations, fix
  emission site, verify wrapper wiring). Per the style guide's "one
  runbook covers multiple failure modes IF AND ONLY IF they share
  the same diagnosis path AND the same mitigation steps" rule.

The `secrets-provider-unreachable.md` runbook explicitly documents 3
sub-cases (AWS SM / K8s Secrets / future Vault) within one provider-
unreachable failure mode — it's grouping within a single index row,
not across rows.

### V2-4.3 PR 2b grouping decisions (closed 2026-06-01)

11 runbooks written for 14 P1 failure-mode rows (Providers + Catalog
surfaces):

- **Standalone** (9 runbooks for 9 rows):
  `argocd-exec-plugin-auth-unsupported.md`,
  `catalog-source-schema-validation-failed.md`,
  `catalog-source-http-fetch-failed.md`,
  `aws-sm-secret-not-found.md`,
  `aws-sm-search-access-denied.md`,
  `eks-token-generation-failed.md`,
  `eks-discover-failed.md`,
  `k8s-secrets-not-found-in-namespace.md`,
  `argocd-account-token-expired.md`,
  `catalog-parse-failure-on-startup.md` (10 standalone runbooks — note
  the count includes the catalog parse failure standalone).
- **Grouped** (2 runbooks for 5 rows):
  - `argocd-cluster-secret-corruption.md` covers 3 rows (invalid CA /
    empty server URL / synthesized-kubeconfig parse). All three fail
    inside `buildBearerTokenKubeconfig` and share the same diagnosis
    (inspect the Secret's JSON directly) and mitigation (repair the
    Secret).
  - `azure-gcp-provider-unimplemented.md` covers 2 rows (Azure +
    GCP). Both providers' stubs return the same shape (explicit
    "not yet implemented" error from the constructor); diagnosis and
    mitigation lanes are identical (switch to AWS-SM or
    K8s-Secrets, contribute upstream, wait for v2).

PR 2c (closed 2026-06-01) covers the remaining 14 P1 rows: API +
Orchestrator + Reconciler + Webhook + AI + Adopt surfaces. See
[V2-4.3 PR 2c grouping decisions](#v2-43-pr-2c-grouping-decisions-closed-2026-06-01)
below.

### V2-4.3 PR 2c grouping decisions (closed 2026-06-01)

12 runbooks written for 14 P1 failure-mode rows (API + Orchestrator
+ Reconciler + Webhook + AI + Adopt surfaces):

- **Standalone** (10 runbooks for 10 rows):
  `single-cluster-credential-fetch-failed.md`,
  `addon-application-stuck-degraded.md`,
  `init-operation-abandoned.md`,
  `auto-merge-failed-after-pr-opened.md`,
  `ai-annotation-secret-blocked.md`,
  `encryption-key-not-configured.md`,
  `cluster-reconciler-dependency-missing.md`,
  `adopt-managed-by-label-read-failed.md`,
  `adopt-cluster-entry-write-failed.md`,
  `ai-provider-misconfigured.md`.
- **Grouped** (2 runbooks for 4 rows):
  - `git-provider-rate-limited.md` covers 2 rows ("Git provider
    rate limit hit" + "GitHub Contents API 403 on
    `managed-clusters.yaml` read"). Both share the same root
    cause (PAT quota exhausted), the same diagnosis (inspect
    `X-RateLimit-*` headers), and the same mitigation lanes
    (rotate PAT or back off cadence).
  - `webhook-handler-failures.md` covers 2 rows ("Webhook handler
    returns 401" + "Webhook receive error (any code path)"). Both
    share the same diagnosis tree (is the webhook reaching us?
    signature? payload?) and the same mitigation lanes (rotate
    secret, narrow subscription, fix proxy).

Both groupings follow the style guide's "one runbook covers
multiple failure modes IF AND ONLY IF they share the same diagnosis
path AND the same mitigation steps" rule. The adopt-flow rows
(`adopt-managed-by-label-read-failed.md` and
`adopt-cluster-entry-write-failed.md`) were considered for grouping
but kept separate because their mitigations differ materially
(RBAC fix vs. managed-clusters.yaml repair PR).

### Severity distribution (total)

| Severity | Count |
|---|---|
| P0 | 18 |
| P1 | 22 |
| P2 | 17 |

### Per-surface distribution

| Surface | Count |
|---|---|
| API handlers (router + per-route) | 13 |
| Reconciler (cluster + argo-secrets) | 6 |
| Orchestrator | 7 |
| Providers (argocd / aws / k8s-secrets / azure / gcp / vault) | 14 |
| Catalog (loader + sources + signing) | 9 |
| Audit-log / observability surface | 5 |
| Cross-surface (e.g. ArgoCD unreachable propagates everywhere) | 3 |

### V2-4.3 (PR 2) scope

The V2-4.3 sub-sprint closes **all P0 and P1 GAPs**. Per the sprint
plan's cap-protection rule (P0+P1 GAPs > 30 triggers a KEEP/REVERT
decision), the sub-sprint **split into PR 2a (P0 only) + PR 2b (P1
half — Providers + Catalog) + PR 2c (P1 half — API + Orchestrator +
Reconciler + Webhook + AI + Adopt)** because P0+P1 = 40 GAPs > 30 cap
threshold and PR 2b + PR 2c both touch this file.

- **PR 2a (closed 2026-06-01):** 11 new operator runbooks covering
  the 12 P0 failure-mode rows. `credential-leak-in-logs.md` grouped
  two adjacent failure modes per the style guide's grouping rule;
  the remaining 10 runbooks are standalone. P0 GAP count → 0.
- **PR 2b (closed 2026-06-01):** 11 new operator runbooks covering
  14 of the 28 P1 failure-mode rows (Providers + Catalog surfaces).
  Two grouping decisions documented above
  (`argocd-cluster-secret-corruption.md` covers 3 rows;
  `azure-gcp-provider-unimplemented.md` covers 2 rows); remaining
  9 runbooks are standalone. P1 GAP count: 28 → 14.
- **PR 2c (closed 2026-06-01):** 12 new operator runbooks covering
  the remaining 14 P1 failure-mode rows (API + Orchestrator +
  Reconciler + Webhook + AI + Adopt surfaces). Two grouping
  decisions (`git-provider-rate-limited.md` covers 2 rows;
  `webhook-handler-failures.md` covers 2 rows); remaining 10
  runbooks are standalone. P1 GAP count: 14 → 0. V2-4.3 sub-sprint
  is now COMPLETE.

P2 GAPs (12 rows; corrected count) remain tracked as V2-4.x follow-up
work, not in PR 2.

### V2-4.4 (PR 3) scope

PR 3 brings **9 existing runbook-shaped pages** into compliance with
the style guide (excluding `budget-burn-runbook.md` which is
already-conformant). Per-page edits will likely add Severity,
Prevention, Related runbooks, and verified headers; several pages
also need a section-order rewrite and one (`troubleshooting.md`)
needs to be split into multiple files. Per the sprint plan's
cap-protection rule (>500 net lines triggers re-bundling), PR 3 may
land at or above that threshold given the `troubleshooting.md`
split. PR 3 agent should surface a KEEP/REVERT decision if total net
lines exceed 600.
