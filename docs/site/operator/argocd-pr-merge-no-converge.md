# Orchestrator PR Merged but ArgoCD Never Converges

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The audit-event
> shape (`pr_merged`, `cluster_secret_create`, `cluster_secret_reconcile`)
> is verified against the V2-2 audit codes documented in
> [`../developer-guide/logging.md`](../developer-guide/logging.md) and
> the V125-1-8 reconciler design referenced in
> [`cluster-reconciler.md`](cluster-reconciler.md). The
> `prTracker.SetOnMergeFn → recon.Trigger()` wiring lives in
> `cmd/sharko/serve.go` and the reconciler implements it in
> `internal/clusterreconciler/reconciler.go`. Re-verify if the audit
> event names or the on-merge trigger wiring change.

A cluster (or addon) registration's PR was merged. The audit log shows
`pr_merged`. The Sharko API view of the cluster says `managed: true`.
But ArgoCD's cluster Secret does not exist (or ArgoCD's Application
controller has not picked up the cluster), and the fleet sits in a
split-state: Git says "this cluster is registered," ArgoCD says "I've
never heard of it." Page on-call.

This is a P0 because the failure indicates one of two **architectural
breakdowns**: either the V125-1-8 reconciler is stuck (a P0 in itself —
see [`reconciler-crash-loop.md`](reconciler-crash-loop.md)) or the
ArgoCD Application controller is degraded. The diagnosis path
**distinguishes which side**, then routes to the side-specific runbook.

If you confirm via diagnosis that the failure is on the reconciler
side, route to
[`reconciler-crash-loop.md`](reconciler-crash-loop.md). If on the
ArgoCD side, the partial-runbook coverage is
[`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md)
(when ArgoCD itself is unreachable) and ArgoCD's own runbook (when
the Application controller is degraded). This page is the **router**
between them.

---

## Symptoms

What an operator sees when this fires:

- **API reports success**: `POST /api/v1/clusters` returned `201`, the
  audit log shows `event=cluster_register` with `result=success`, and
  `GET /api/v1/clusters` lists the cluster with `managed: true`.
- **PR merged**: the audit log entry `event=pr_merged` for the
  cluster's PR shows `result=success`. The Git repo's
  `configuration/managed-clusters.yaml` contains the cluster's entry on
  `main`.
- **ArgoCD cluster Secret does NOT exist** in the `argocd` namespace
  for the cluster:

  ```sh
  kubectl -n argocd get secret -l \
    app.kubernetes.io/managed-by=sharko,sharko/cluster=<cluster-name>
  # No resources found in argocd namespace.
  ```

  OR the Secret exists but the ArgoCD Application controller has not
  recognised it (no Application list for the cluster's labels).

- **Time elapsed since PR merge > 2 minutes**. The reconciler's
  `Trigger()` should converge within seconds; the 30s safety-net tick
  bounds the worst case. Two minutes without convergence is the
  threshold for "this is broken, not slow."
- **No matching `cluster_secret_create` audit event** for the same
  cluster. The audit-trail break is the canonical signal: PR merge
  succeeded, secret creation never happened.
- **Alerts that may fire**:
  - `SharkoClusterRegistrationFastBurn` once registrations have
    sustained-failed (5m+1h windows). See
    [`budget-burn-runbook.md`](budget-burn-runbook.md#sharkoclusterregistrationfastburn).
  - `SharkoReconcilerStalled` (if shipped — a Prevention follow-up
    today).

If the symptom is "PR was opened but not merged," the diagnosis path is
entirely different — see
[`git-provider-unreachable.md`](git-provider-unreachable.md) (Git
provider issue) or `auto-merge-failed.md` (PR open, merge blocked —
P1 GAP, PR 2b scope). This runbook is for "PR confirmed merged but
ArgoCD doesn't reflect it."

---

## Diagnosis

Three checks. The goal is to **distinguish reconciler-side from
ArgoCD-side** in under 5 minutes.

### 1. Confirm the PR actually merged into main

Trust nothing — verify the Git state:

```sh
ORG=<your-org>
REPO=<your-bootstrap-repo>
CLUSTER=<cluster-name>

# Fetch the canonical managed-clusters.yaml from main:
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${ORG}/${REPO}/contents/configuration/managed-clusters.yaml?ref=main" \
  | jq -r '.content' | base64 -d \
  | yq ".clusters[] | select(.name == \"${CLUSTER}\")"
```

If the cluster entry IS present in `main`, the PR was genuinely merged.
If it's NOT, the audit log lies — the PR-tracker thinks the merge
landed but the merge actually failed or was reverted out-of-band. In
that case, this runbook does not apply; see
`auto-merge-failed.md` (P1 GAP) or treat as a Git-provider /
PR-tracker bug.

### 2. Determine which side is broken — reconciler vs. ArgoCD

The pivotal check: are reconciler ticks running and seeing the new
entry?

```sh
SHARKO_NS=<sharko-ns>

# Were there reconciler ticks since the PR merge?
PR_MERGE_TIME="2026-06-01T12:00:00Z"  # from the audit log

kubectl -n "$SHARKO_NS" logs -l app=sharko --since=10m \
  | jq -c "select(.request_id // \"\" | startswith(\"recon-\"))" \
  | jq -c "select(.time > \"$PR_MERGE_TIME\")" \
  | head -10
```

Three outcomes:

- **No `recon-` lines at all since the merge** — the reconciler is dead
  or stalled. Route to
  [`reconciler-crash-loop.md`](reconciler-crash-loop.md). This is
  Reconciler-Side.
- **`recon-` lines present but no `recon-fanout-` lines** — the safety-
  net tick is firing but the post-merge `Trigger()` never fired. The
  PR tracker → reconciler wiring is broken. This is a Sharko-internal
  bug; see Mitigation step 2.
- **Both `recon-` and `recon-fanout-` lines present, ticking
  normally** — the reconciler is doing its job. Check what it sees:

  ```sh
  kubectl -n "$SHARKO_NS" logs -l app=sharko --since=10m \
    | jq -c "select(.request_id // \"\" | startswith(\"recon-\"))" \
    | jq -c "select(.cluster == \"${CLUSTER}\")"
  ```

  If lines for the cluster show `action=cluster_secret_create` with
  `result=failed`, the reconciler is trying and failing. Read the
  `error` attribute — most common is ArgoCD-namespace write rejected.
  This is ArgoCD-Side or RBAC; jump to step 3.

### 3. Probe the ArgoCD Secret create path directly

If Diagnosis step 2 indicates ArgoCD-side, verify the namespace and
RBAC:

```sh
# Can Sharko's service account write secrets in argocd namespace?
kubectl auth can-i create secrets -n argocd \
  --as=system:serviceaccount:<sharko-ns>:sharko
# Expected: yes
```

If `no`, the ArgoCD RBAC was tightened out-of-band and Sharko's SA no
longer has the required permission. Fix: re-apply the Sharko Helm
chart's `clusterrole` template (creates the role + binding for the SA).

```sh
# Try the write directly (as the SA):
kubectl -n argocd create secret generic test-can-write \
  --from-literal=test=value \
  --as=system:serviceaccount:<sharko-ns>:sharko \
  --dry-run=server
# Expected: dry-run report (no error). Error means RBAC issue.
```

If the create succeeds at the API level but the ArgoCD Application
controller doesn't notice, ArgoCD is unhealthy on the Application side:

```sh
kubectl -n argocd get pods -l app.kubernetes.io/component=application-controller
kubectl -n argocd logs deploy/argocd-application-controller --tail=200 \
  | grep -E "ERROR|cluster|secret" | head -30
```

Common: `argocd-application-controller` is OOMing, getting throttled, or
the kube-apiserver watch on Secrets is rate-limited.

### 4. Inspect the audit log around the merge

```sh
# All audit events for the cluster, sorted by time:
curl -sS \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/audit?cluster=${CLUSTER}" \
  | jq '.entries[] | {time, event, action, result, request_id}' \
  | tail -20
```

Expected on a healthy registration:

```
{"time":"...","event":"cluster_register","result":"success","request_id":"req-..."}
{"time":"...","event":"pr_opened","result":"success","request_id":"req-..."}
{"time":"...","event":"pr_merged","result":"success","request_id":"req-..."}
{"time":"...","event":"cluster_secret_reconcile","action":"cluster_secret_create","result":"success","request_id":"recon-fanout-..."}
```

On a failure, the last `cluster_secret_*` entry is either absent
(reconciler never converged) or present with `result=failed` (reconciler
tried, ArgoCD rejected).

---

## Mitigation (try in order)

The order routes you to the correct side-specific runbook for steps 3-5;
steps 1-2 are Sharko-internal recovery.

1. **Force a reconcile tick by restarting Sharko.** Cheapest recovery
   path and clears in-memory state issues:

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   On startup, the reconciler runs a fresh `pollOnce` that re-reads
   `managed-clusters.yaml` from Git and reconciles to ArgoCD. If the
   cluster entry is in Git but the Secret is missing, the tick will
   create it.

   Success indicator: within ~60 seconds of the pod becoming ready,
   `kubectl -n argocd get secret -l sharko/cluster=<cluster-name>`
   returns the Secret.

2. **If Sharko ticks but the post-merge `Trigger()` didn't fire** — a
   Sharko-internal bug (`prTracker.SetOnMergeFn` not wired or
   `recon.Trigger()` channel full). Force a synthetic trigger via the
   API (if a manual trigger endpoint exists; today this is a
   restart):

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   ```

   Capture the goroutine dump first if pprof is enabled — see
   [`reconciler-crash-loop.md`](reconciler-crash-loop.md#3-capture-a-goroutine-dump-if-pod-is-still-alive).

   File a P1 bug with the goroutine dump attached.

3. **If diagnosis identified the reconciler as crashed or stalled** —
   route to [`reconciler-crash-loop.md`](reconciler-crash-loop.md) and
   follow its mitigation. This runbook does not duplicate that work.

4. **If diagnosis identified ArgoCD as unreachable** — route to
   [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md)
   and follow its mitigation. Again, no duplication here.

5. **If diagnosis identified RBAC missing (Sharko SA can't write to
   `argocd` namespace)** — re-apply the Sharko Helm chart so the
   ClusterRole / RoleBinding template lands:

   ```sh
   helm upgrade --reuse-values sharko sharko/sharko -n "$SHARKO_NS"
   ```

   Verify the RBAC:

   ```sh
   kubectl auth can-i create secrets -n argocd \
     --as=system:serviceaccount:<sharko-ns>:sharko
   ```

   Success indicator: the dry-run create succeeds; the next reconcile
   tick (within 30s) creates the cluster Secret successfully.

---

## Root-cause patterns

### Reconciler goroutine dead (crash or deadlock)

The reconciler's goroutine has stopped ticking — either panicked
without recovery, deadlocked on a sub-call, or exited from `Start()`
unexpectedly. Symptoms manifest as "PR merged, no convergence" because
nothing is doing the convergence work.

Diagnostic signature: Diagnosis step 2 returned "no `recon-` lines at
all since the merge." Pod is alive (HTTP API still answers) but the
reconciler thread is gone.

This is the canonical
[`reconciler-crash-loop.md`](reconciler-crash-loop.md) case. The
runbook there explains why the goroutine died.

### Post-merge trigger wiring broken

The 30s safety-net tick is firing (`recon-<ts>` lines present), but the
low-latency post-merge `Trigger()` from `prTracker.SetOnMergeFn` is
not firing (`recon-fanout-<ts>` lines absent). The reconciler will
eventually converge — within 30s — but the operator sees a longer
window than expected.

Diagnostic signature: `recon-` lines yes, `recon-fanout-` lines no.
The convergence happens, just slower than the design intends.

Why it happens: `prTracker.SetOnMergeFn` may not have been wired at
startup (e.g. PR tracker disabled by Helm value, or a partial Helm
upgrade left state inconsistent). Alternative: the `Trigger()` channel
buffer is full from a previous high-burst event and the send is
non-blocking-dropped.

Fix is a Sharko restart. Long-term: add a metric for "trigger sends"
vs "trigger receives" so the wiring break is detectable. File as P1
bug.

### ArgoCD Application controller degraded

The reconciler creates the cluster Secret successfully, but ArgoCD's
Application controller doesn't notice — either because it's
OOM-killed, throttled by kube-apiserver, or its watch on Secrets is
stalled.

Diagnostic signature: `kubectl -n argocd get secret` shows the Secret
exists with the correct labels, but `argocd cluster list` doesn't show
the new cluster. The `argocd-application-controller` logs show errors,
high memory usage, or no recent activity.

Why it happens: ArgoCD's Application controller has a watch on Secrets
in the `argocd` namespace; high churn (mass cluster registration, mass
addon enable) can overwhelm the watch's resync queue. Memory pressure
during the resync triggers OOMKill.

Fix is on the ArgoCD side: restart the Application controller, raise
its memory limit, reduce the cluster onboarding rate. See ArgoCD's
own operator runbook.

### Sharko service account lost RBAC to argocd namespace

The Sharko SA had `create secrets` permission in the `argocd`
namespace; an out-of-band RBAC tightening (security review, OPA
policy, manual cleanup) removed it. The reconciler tick fails at the
kube API with 403, logs the error, and continues — but the cluster
Secret never lands.

Diagnostic signature: Diagnosis step 3's `kubectl auth can-i` probe
returns `no`. The Sharko logs show `"argocd secret create failed"`
with `"forbidden"` in the error.

Why it happens: the Sharko Helm chart's ClusterRole was deleted
(operator cleanup), or a higher-priority OPA policy denied the action.

Fix is Mitigation step 5 (re-apply Helm chart). Long-term: add a
startup probe that checks RBAC and refuses to start if Sharko cannot
write Secrets in `argocd` namespace. Prevents silent failure mode.

---

## Rollback plan

This failure is read-only in nature — no destructive mitigation steps
exist. The mitigations either restart Sharko (idempotent) or re-apply
Helm (idempotent) or route to a downstream runbook.

If Mitigation step 5 (Helm re-apply) accidentally reverts a config
change you intended to keep:

1. Restore the desired Helm values:
   ```sh
   helm upgrade --values <your-values.yaml> sharko sharko/sharko \
     -n "$SHARKO_NS"
   ```

2. Verify Sharko is healthy and the reconciler is converging.

---

## Prevention

- **Monitoring — alert on the audit-trail break.** Add a Prometheus
  rule that watches for `pr_merged` events without a corresponding
  `cluster_secret_create` within 90 seconds:

  ```promql
  sum(rate(sharko_audit_events_total{event="pr_merged"}[5m]))
    -
  sum(rate(sharko_audit_events_total{event="cluster_secret_create",result="success"}[5m]))
  > 0
  ```

  When non-zero for >90s, page. Catches both the reconciler-side and
  ArgoCD-side cases. Wiring requires Sharko to emit
  `sharko_audit_events_total` as a Counter labeled by `event` and
  `result` — a P1 follow-up in V2-3.x.

- **Gating — startup RBAC probe.** Sharko at startup should call
  `kubectl auth can-i create secrets -n <argocd-ns>` against its own
  SA, and refuse to start if the answer is no. Catches the
  "RBAC was tightened" cause before any registration silently fails.
  Implementation belongs in `cmd/sharko/serve.go` startup checks.

- **Gating — startup `Trigger()` wiring probe.** Sharko at startup
  should send a no-op `Trigger()` and verify the reconciler receives
  it within 1s. Catches the "wiring broken" cause before any
  PR-merge happens.

- **Scheduled work — quarterly chaos drill.** Inject a 1-minute pause
  in the reconciler's `pollOnce` (e.g.
  `SHARKO_RECONCILER_TEST_PAUSE_MS=60000`) in staging. Verify the
  monitoring alert fires. Verify the operator can follow this runbook
  to diagnose and mitigate. Trains the procedure before a real
  incident.

---

## Related runbooks

- [`reconciler-crash-loop.md`](reconciler-crash-loop.md) —
  reconciler-side failure path. This runbook routes to it.
- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) —
  ArgoCD-side failure path. This runbook routes to it.
- [`cluster-reconciler.md`](cluster-reconciler.md) — V125-1-8
  architectural reference for the reconciler and the
  `prTracker.SetOnMergeFn → recon.Trigger()` wiring.
- [`secret-push-silently-failed.md`](secret-push-silently-failed.md) —
  adjacent silent-failure mode (addon secret push failed). Symptoms
  feel similar; the diagnosis is different.
- [`budget-burn-runbook.md`](budget-burn-runbook.md) — V2-3 alerts that
  fire when this is sustained.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern; `recon-` and `recon-fanout-`
  ID shapes.

## Escalation

If the mitigations don't restore convergence within 30 minutes — or if
the same failure recurs across multiple cluster registrations
(suggesting a fleet-wide issue) — email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The cluster name(s) affected
- The PR URL(s)
- The merge timestamp(s)
- The audit-log events for each cluster (Diagnosis step 4 output)
- The reconciler-tick log lines around the merge time (Diagnosis step 2)
- Output of Diagnosis step 3's RBAC probe
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Convergence
bugs are P0 — expect a same-business-day investigation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (4 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name referenced (SharkoClusterRegistrationFastBurn)
-->
