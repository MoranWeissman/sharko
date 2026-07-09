# Troubleshooting

> **Redirector page.** This page used to be a catch-all troubleshooting
> grab bag with multiple unrelated failure modes glued together. After
> V2-4, each failure mode has its own runbook in `docs/site/operator/`
> following the
> [runbook style guide](../developer-guide/runbook-style-guide.md). The
> [`failure-mode-index.md`](failure-mode-index.md) is the master
> inventory — start there, Ctrl-F your error message, and follow the
> Runbook URL column to the specific page.

This page now exists only to keep the old `/operator/troubleshooting/`
URL alive (existing inbound links from external blog posts, prior
Sharko releases, and the user-guide pages still resolve here) and to
route operators to the right runbook based on their symptom.

## Where to start

1. **Ctrl-F your symptom** in
   [`failure-mode-index.md`](failure-mode-index.md). The index maps
   every operator-facing failure mode (HTTP status, log line, alert
   name) to the runbook that covers it.
2. **If your error is a P0 burn-rate alert**, jump to
   [`budget-burn-runbook.md`](budget-burn-runbook.md) — the alert's
   `runbook_url` annotation lands you on the right anchor directly.
3. **If your error is a 502 from any handler that touches ArgoCD**,
   jump to
   [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md).
4. **If your error is a 502 from any handler that opens a PR**, jump
   to [`git-provider-unreachable.md`](git-provider-unreachable.md).

## Common symptoms → runbook map

The H2 sections this page used to contain are now standalone runbooks
or covered by adjacent runbooks. The mapping:

| Symptom (legacy section title)                          | Runbook today                                                                                                            |
| ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| "Connection refused" reaching the Sharko UI             | Not a Sharko-emitted failure (port-forward / Ingress setup) — see [Installation](installation.md)                        |
| "401 Unauthorized" on API or CLI                        | If credentials are valid but sessions are rejected, jump to [`auth-bypass.md`](auth-bypass.md). For routine "session expired" cases, log in again. |
| "502 Bad Gateway" from any handler                      | [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) (for ArgoCD-touching paths) or [`git-provider-unreachable.md`](git-provider-unreachable.md) (for PR-opening paths). |
| `sharko init` fails on empty repository                 | [`init-operation-deadlocked.md`](init-operation-deadlocked.md) (for stuck operations); for repository-not-found errors, create the repo and push at least one commit before re-running `sharko init`. |
| "gitopsCfg not initialized" on write operations         | [`encryption-key-not-configured.md`](encryption-key-not-configured.md) or [`cluster-reconciler-dependency-missing.md`](cluster-reconciler-dependency-missing.md) depending on which dependency is missing. |
| Addon drift detection shows every cluster as drifted    | [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md) for per-cluster cases; for fleet-wide drift, the root cause is usually [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) or a misconfigured ArgoCD ApplicationSet. |
| Pod crashes with "read-only filesystem" error           | Not a Sharko bug — Sharko's `securityContext` sets `readOnlyRootFilesystem: true`. Any component writing to local FS needs a `tmpfs` mount or PVC. See [Configuration](configuration.md). |
| Pod restart loop / CrashLoopBackoff                     | [`oom-restart-loop.md`](oom-restart-loop.md).                                                                            |
| ArgoCD Application stays Degraded after addon enable    | [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md).                                            |
| Audit log "earliest entry truncated" warning            | [`audit-log.md`](audit-log.md) (reference for retention model + ring buffer cap).                                        |
| Catalog entries show as Unverified                      | [`catalog-trust-policy.md`](catalog-trust-policy.md).                                                                    |
| Third-party catalog source not loading                  | [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md) / [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md). |
| Corporate-proxy TLS interception breaks `argocd-repo-server` | [`corporate-mitm-tls.md`](corporate-mitm-tls.md).                                                                  |
| Test connection returns 503 on EKS                         | [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) (Sharko couldn't mint a token with its own identity) or [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md) (exec command Sharko doesn't recognize as AWS). |
| AWS Secrets Manager: secret not found / access denied   | [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) / [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md). |
| K8s Secrets provider — token not found                  | [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md).                                        |
| Auto-merge failed after PR opened                       | [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md).                                          |
| Git provider rate limited                               | [`git-provider-rate-limited.md`](git-provider-rate-limited.md).                                                          |
| Webhook handler errors                                  | [`webhook-handler-failures.md`](webhook-handler-failures.md).                                                            |
| Reconciler stopped / not converging                     | [`reconciler-crash-loop.md`](reconciler-crash-loop.md) / [`cluster-reconciler.md`](cluster-reconciler.md) (architecture reference + embedded troubleshooting). |
| Credential leak suspected in pod logs                   | [`credential-leak-in-logs.md`](credential-leak-in-logs.md).                                                              |
| Init operation deadlocked or abandoned                  | [`init-operation-deadlocked.md`](init-operation-deadlocked.md) / [`init-operation-abandoned.md`](init-operation-abandoned.md). |

## General-purpose commands

Operators who used to land here for the "show me the logs" copy-paste
block:

```sh
# Live logs
kubectl logs -f -n sharko -l app.kubernetes.io/name=sharko

# Previous container logs (after crash)
kubectl logs -n sharko -l app.kubernetes.io/name=sharko --previous

# Events
kubectl get events -n sharko --sort-by='.lastTimestamp'

# Filter by request_id (V2-2.2 correlation pattern — every Sharko log
# line carries one; a single request_id joins lines across middleware,
# service, orchestrator, reconciler, and audit)
kubectl logs -n sharko deploy/sharko --tail=2000 \
  | jq 'select(.request_id == "req-<id>")'
```

The full correlation pattern lives in
[`../developer-guide/logging.md`](../developer-guide/logging.md).

## Why this page is a redirector now

Pre-V2-4, this page was a catch-all troubleshooting bag glued together
from multiple unrelated failure modes. Each mini-section was a tiny
runbook ("Connection refused", "401 Unauthorized", "502 Bad Gateway",
etc.) without symptoms / diagnosis / mitigation / root-cause /
prevention separation. After V2-4 the failure-mode index and the
per-failure runbooks shipped, and each of those mini-sections has a
proper runbook now. Keeping this page as a catch-all would duplicate
content and let it drift; keeping it as a redirector preserves the
inbound URL without the duplication.

## Related runbooks

- [`failure-mode-index.md`](failure-mode-index.md) — master inventory
  of every operator-facing failure mode in Sharko.
- [`../developer-guide/runbook-style-guide.md`](../developer-guide/runbook-style-guide.md)
  — the rubric every runbook in
  `docs/site/operator/` follows.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  the V2-2.2 `request_id` correlation pattern used in diagnosis steps
  across every runbook.
