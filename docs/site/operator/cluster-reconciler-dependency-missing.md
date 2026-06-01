# Cluster Reconciler Dependency Missing

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The three
> Warn-level skip messages are verified verbatim against
> `internal/clusterreconciler/reconciler.go:324-342` as shipped:
>
> - `"[clusterreconciler] no GitProvider getter configured, skipping reconcile"` (line 325)
> - `"[clusterreconciler] no ArgoClient (k8s clientset) configured, skipping reconcile"` (line 336)
> - `"[clusterreconciler] no Vault (cluster-credentials provider) configured, skipping reconcile"` (line 340)
>
> Each is a precondition guard in `pollOnce`; the reconciler returns
> without doing work but does NOT panic — the goroutine stays alive
> for the next tick. This distinguishes the failure from
> [`reconciler-crash-loop.md`](reconciler-crash-loop.md) (P0). Re-verify
> before changing the dependency-injection shape in `Deps` or the
> Warn-level skip-log strings — both are anchors here.

The cluster reconciler is running but is a no-op. Each 30s tick logs
a Warn line saying which dependency is missing and returns without
doing any work. The reconciler goroutine stays alive (no crash, no
restart) — but no ArgoCD cluster Secrets are being created, updated,
or deleted in response to `managed-clusters.yaml` changes.

The blast radius is **the entire fleet's GitOps convergence**:

- New cluster registrations: PR merges, but the ArgoCD cluster
  Secret is never created -> ArgoCD treats the cluster as
  unregistered -> Applications fail to sync.
- Cluster removals: PR merges, but the labeled ArgoCD Secret is
  never deleted -> ArgoCD continues to sync to the removed cluster
  (until manual intervention).
- Out-of-band Secret edits: not self-healed -> drift accumulates.

The orchestrator-side write path (the synchronous flow that runs
during a `POST /clusters` call) still creates Secrets directly via
`argosecrets.Manager` — so brand-new operations may appear to work,
but the safety-net reconciler that catches missed creations and
post-merge convergence is not running. Operators usually catch this
when something subtle goes wrong (a Secret stays around after a
cluster removal, or a label change isn't reflected).

This is **not** the same as
[`reconciler-crash-loop.md`](reconciler-crash-loop.md) (P0). Crash
loop = goroutine panics + restarts forever. Dependency missing =
goroutine quietly does nothing. The fix is different: crash loop
needs the panic root-caused; dependency missing needs the wiring
restored.

---

## Symptoms

What an operator sees when this fires:

- **`kubectl logs` Warn line every 30s** (or whatever
  `DefaultTickInterval` is set to):

  ```
  {"time":"...","level":"WARN","msg":"[clusterreconciler] no GitProvider getter configured, skipping reconcile","request_id":"recon-..."}
  ```

  Or one of the other two skip lines depending on which
  dependency is missing. Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
    | jq -c 'select(.msg | test("\\[clusterreconciler\\].*skipping reconcile"; "i"))' \
    | head -10
  ```

  Expected: a steady cadence of one Warn line per tick interval. If
  the cadence is irregular or stops entirely, the goroutine isn't
  even running — that's a different failure (see
  [`reconciler-crash-loop.md`](reconciler-crash-loop.md)).

- **Audit log shows reconciler ticks but no `cluster_secret_create`
  or `cluster_secret_skip` action**:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?source=reconciler&limit=50" \
    | jq -r '.[] | "\(.time) \(.event) \(.action) \(.result)"'
  ```

  Expected on a healthy reconciler: rows showing per-cluster
  `action=cluster_secret_create` / `cluster_secret_skip` with
  `result=success`. Expected on broken reconciler: empty output —
  or only tick-start entries with no per-cluster activity.

- **`audit.action=reconcile` with `result=skipped`** (when the
  reconciler audit emitter records the skip):

  Today the skip path returns from `pollOnce` without emitting an
  audit entry; the only signal is the Warn log line. A V2-4.x
  follow-up should add `event=cluster_secret_reconcile, action=reconcile,
  result=skipped, error=<which dep was nil>` so the failure mode is
  visible from the audit-log surface, not just from logs.

- **Fleet-wide downstream impact**: clusters that were registered
  AFTER the reconciler stopped working don't appear in ArgoCD; their
  Applications fail to sync; the dashboard surfaces them as
  `Registration pending`. Clusters that were registered BEFORE the
  break work normally — Secrets are present, ArgoCD syncs fine —
  until they're modified or deleted.
- **`kubectl get pods -n argocd`** shows healthy ArgoCD; the failure
  is Sharko-internal. ArgoCD doesn't surface a "Sharko isn't
  reconciling" signal of its own.
- **No specific Prometheus alert fires today.** A V2-4.x follow-up
  is to expose `sharko_reconciler_ticks_skipped_total{reason="..."}`
  and alert on >0 over 5 minutes.

If the symptom is **per-cluster errors** in the reconciler log
(some clusters succeed, others fail), this isn't the runbook — the
reconciler is running and the failure is per-cluster. Jump to
[`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
or
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

If the symptom is **no logs at all from the reconciler** (silent),
the goroutine is dead. Jump to
[`reconciler-crash-loop.md`](reconciler-crash-loop.md).

---

## Diagnosis

Three checks: identify which dependency is missing, trace the
wiring back to the deployment surface, decide whether the fix is
environment-side or code-side.

### 1. Identify which dependency is missing

The Warn log line names the specific dependency. Map to the
deployment surface:

| Warn message | Dependency | Comes from |
|---|---|---|
| `no GitProvider getter configured` | Active git provider | Connection config: a configured Git connection ("active" connection) |
| `no ArgoClient (k8s clientset) configured` | Kubernetes clientset for the `argocd` namespace | Server initialization at `cmd/sharko/serve.go`; `rest.InClusterConfig()` |
| `no Vault (cluster-credentials provider) configured` | Cluster-credentials provider (AWS-SM, K8s Secrets, etc.) | `internal/providers/` initialization from Helm values |

```sh
kubectl -n <sharko-ns> logs -l app=sharko --tail=1000 \
  | jq -c 'select(.msg | test("\\[clusterreconciler\\]"; "i"))' \
  | tail -3
```

The last 3 lines tell you which precondition is firing.

### 2. Verify the corresponding surface in the running pod

**For "no GitProvider getter configured":**

The reconciler reads the active git provider from `connSvc`. If no
Git connection is configured (or all configured connections are
disabled), this trips. Check:

```sh
curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/connections" \
  | jq '.[] | {name, type, active, healthy}'
```

Expected: at least one `type: github` (or `gitlab`, `bitbucket`,
`azuredevops`) connection with `active: true` and `healthy: true`.
If no connection has `active: true`, the operator never finished
the initial connection setup; the reconciler will skip forever
until one is configured.

**For "no ArgoClient (k8s clientset) configured":**

The clientset is built at server start from
`rest.InClusterConfig()`. If Sharko is running outside K8s (e.g.
local dev mode) or the in-cluster config can't be loaded, the
reconciler skips.

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  ls -la /var/run/secrets/kubernetes.io/serviceaccount/
```

Expected: `ca.crt`, `namespace`, `token` files. If missing,
`automountServiceAccountToken: false` was set on the pod or the
service account isn't projected. Fix the pod spec.

**For "no Vault (cluster-credentials provider) configured":**

The cluster-credentials provider is configured via Helm values
(`clusterRegSourceProvider`). If unset, the reconciler skips.

```sh
helm get values <sharko-release> -n <sharko-ns> \
  | yq '.clusterRegSourceProvider // "unset"'
```

Expected output: a `type:` field plus provider-specific config.
If `unset`, the provider was never configured. The Helm chart
notes / values should call this out as a required field for
fleet-management mode.

### 3. Decide environment-fix vs code-fix

| Missing dependency | Fix surface |
|---|---|
| GitProvider | Operator configures a Git connection via UI/CLI (`sharko connect`) |
| ArgoClient | Operator fixes pod spec (service account projection) |
| Vault | Operator sets `clusterRegSourceProvider` in Helm values |

All three are operator-side fixes. There is no code-side reason
for the dependency to be missing — these are deployment-time
wiring problems.

---

## Mitigation (try in order)

The exact mitigation depends on which dependency is missing.

1. **For missing GitProvider — configure a Git connection.** If
   Diagnosis step 2 (Git lane) showed no `active: true` connection,
   the operator must complete the initial connection setup:

   - **UI path**: Settings -> Connections -> Add Git Connection ->
     pick provider (GitHub / GitLab / Bitbucket / Azure DevOps) ->
     fill in repo + PAT -> Test -> Save -> mark active.
   - **CLI path**:

     ```sh
     sharko connect github \
       --repo <org>/<repo> \
       --token <PAT> \
       --base-branch main
     # Then activate it:
     sharko connect list
     # (note the connection ID)
     curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
       "http://sharko/api/v1/connections/<id>/activate"
     ```

   After the connection is active, the reconciler's next tick (within
   30s) picks it up automatically — no restart required (the
   `GitProvider` accessor is resolved on each tick).

   Success indicator: the Warn line stops appearing; the next tick
   logs `cluster_secret_reconcile` audit entries.

2. **For missing ArgoClient — fix the pod's service account
   projection.** The K8s clientset depends on the pod having an
   in-cluster service-account token. If the deployment spec sets
   `automountServiceAccountToken: false` or no service account is
   bound, the clientset can't initialize.

   ```sh
   kubectl -n <sharko-ns> get deployment sharko -o yaml \
     | yq '.spec.template.spec.automountServiceAccountToken, .spec.template.spec.serviceAccountName'
   ```

   Expected: `null` or `true` for the mount flag; the
   `serviceAccountName` should be `sharko` (or whatever the Helm
   chart configures). If wrong, re-render the deployment:

   ```sh
   helm upgrade <sharko-release> charts/sharko/ \
     -n <sharko-ns> \
     --reuse-values \
     --set serviceAccount.create=true \
     --set serviceAccount.name=sharko
   ```

   Then restart Sharko so the corrected pod spec takes effect:

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: the Warn line stops appearing; the reconciler
   audit log shows successful ticks.

3. **For missing Vault — set `clusterRegSourceProvider` in Helm
   values.** The cluster-credentials provider must be configured for
   the reconciler to fetch kubeconfigs for new clusters. Pick the
   provider that matches your fleet's credential storage:

   ```yaml
   # charts/sharko/values.yaml or your override file:
   clusterRegSourceProvider:
     type: aws-sm                # or k8s-secrets
     prefix: clusters/           # AWS-SM only
     namespace: sharko-secrets   # K8s Secrets only
   ```

   Then `helm upgrade <sharko-release> charts/sharko/ --reuse-values
   --set-file ...` and restart Sharko (Helm upgrade triggers a
   rollout automatically for env-var changes).

   Success indicator: same as steps 1 and 2 — Warn line stops; audit
   log shows successful ticks.

4. **Last resort — manual ArgoCD Secret writes via the orchestrator
   write path.** Even with the reconciler skipping, the orchestrator
   write path during `POST /clusters` still creates ArgoCD Secrets
   directly (via `argosecrets.Manager.Ensure`). New cluster
   registrations should still appear in ArgoCD.

   If the operator is blocked waiting for one of mitigations 1-3 to
   complete (e.g. Helm chart changes are queued for a deployment
   window) and a brand-new cluster needs to register NOW, the
   synchronous orchestrator path works:

   ```sh
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters" \
     --data-binary '{"name":"prod-emergency", "secret_path":"clusters/prod-emergency"}'
   ```

   The ArgoCD Secret is created by the synchronous path; the
   reconciler isn't needed for the first creation. The downside:
   no self-heal for out-of-band edits, no convergence after PR
   merges from non-Sharko callers. Use sparingly until the
   reconciler is restored.

   Success indicator: the cluster appears in ArgoCD (`argocd cluster
   list` shows it); ArgoCD Applications for the cluster start
   syncing.

---

## Root-cause patterns

Three common causes.

### Sharko deployed without completing the connections setup

The single most common cause for the GitProvider variant. The
operator helm-installed Sharko but didn't open the UI / use the
CLI to configure the Git connection. The reconciler runs as soon
as the server starts but has no active connection to read from.

Diagnostic signature: brand-new Sharko deployment; Diagnosis step 2
(Git lane) shows no active connections; the Warn line started
firing right after the first pod came up.

Fix lane: Mitigation step 1 (configure a Git connection).

### Helm values omit `clusterRegSourceProvider`

The Vault-variant cause. The operator helm-installed Sharko using
default values, which don't include a credentials-provider
configuration. The reconciler runs, sees a configured Git
connection, but skips because there's no way to fetch
kubeconfigs for new clusters.

Diagnostic signature: Diagnosis step 2 (Vault lane) shows
`clusterRegSourceProvider: unset`. The Helm values override file
doesn't include this section. The deployment is missing a
deliberate provider choice.

Fix lane: Mitigation step 3 (set the provider config).

### Pod spec strips service-account token

Less common, but seen in hardened deployments. A security policy
sets `automountServiceAccountToken: false` on all pods by default;
Sharko's pod spec doesn't override it. The K8s clientset can't
initialize.

Diagnostic signature: Diagnosis step 2 (ArgoClient lane) shows the
mount flag is `false`. The pod was deployed against a hardened
PodSecurityPolicy / PodSecurity admission controller that defaults
to no-mount.

Fix lane: Mitigation step 2 (re-render the deployment with the
service-account mount enabled). The Helm chart already includes
this; the override re-renders correctly.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — emit an audit entry per skip.** Today the skip
  path is log-only; add `event=cluster_secret_reconcile,
  action=reconcile, result=skipped, error=<dep>` so the failure
  surfaces on the audit-log query path (which dashboards already
  consume). V2-4.x follow-up.

- **Gating — Helm chart `required` guards for the credentials
  provider.** The chart could require `clusterRegSourceProvider`
  to be set explicitly (no default) so `helm install` fails at
  install time instead of at first reconciler tick. The same
  pattern catches the missing-encryption-key failure mode
  documented in
  [`encryption-key-not-configured.md`](encryption-key-not-configured.md).

- **Scheduled work — installation runbook callout for the connections
  setup.** The installation runbook should walk operators through the
  GitProvider + credentials-provider setup as a hard prerequisite,
  not as an optional follow-up. Audit the runbook quarterly.

---

## Related runbooks

- [`reconciler-crash-loop.md`](reconciler-crash-loop.md) — the P0
  sibling. If the reconciler goroutine is dying repeatedly, that's
  a panic, not a dependency miss.
- [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
  — if the reconciler is running but failing per-cluster, that's
  this runbook.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — if the provider is configured but unreachable, that's the
  fleet-wide variant.
- [`cluster-reconciler.md`](cluster-reconciler.md) — reconciler
  reference: 30s tick cadence, ownership label, Trigger() shape.
- [`encryption-key-not-configured.md`](encryption-key-not-configured.md)
  — adjacent install-time misconfiguration. Both are
  Helm-values-shaped failures.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` / `recon-<ts>` correlation pattern.

## Escalation

If the mitigations above do not resolve the failure within 1 hour,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The Warn log line (which dependency is missing)
- The output of Diagnosis step 2 (connection / pod-spec / Helm
  values for the affected lane)
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the orchestrator write path still
creates Secrets for fresh registrations (Mitigation step 4), the
fleet isn't completely broken — convergence drift is the
operator's concern, not data loss.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (4 steps)
- [x] Root-cause patterns: 2+ named causes (3 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference noted as V2-4.x follow-up
-->
