# Cluster Reconciler Crash Loop

**Severity:** P0

> **Verified:** Authored 2026-06-01 against Sharko as shipped. The
> reconcile tick body, the `recon-<unix_ts>` and
> `recon-fanout-<unix_ts>` correlation-id shapes, and the 30-second tick
> cadence are verified against Sharko as shipped. The reconciler's
> behaviour and its ownership-label semantics are described on
> [`cluster-reconciler.md`](cluster-reconciler.md), which is the
> reference page for it.
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

The Sharko pod is alive — the API still answers, the UI still loads —
but the cluster-secret reconciler goroutine is gone. Every 30 seconds
the fleet should converge ArgoCD cluster Secrets to match
`managed-clusters.yaml`; instead, the reconciler ticks have stopped. New
registrations land in Git and never reach ArgoCD; deregistrations leave
ArgoCD Secrets behind; the fleet silently drifts further from declared
state with every minute.

This is a P0 because the failure is **silent**. The HTTP API reports
success, audit-log entries show `pr_merged` events as expected, but the
downstream ArgoCD state never converges. Operators only notice when a
cluster they registered never shows up in ArgoCD or a cluster they
deregistered is still receiving addon sync. Page on-call.

The reconciler is the canonical ArgoCD-secret writer in V1.25+ (see
[`cluster-reconciler.md`](cluster-reconciler.md) for the architectural
overview). This runbook is for the failure mode where that goroutine
exits or panics, not for the failure mode where it ticks normally but
the underlying providers fail (that's
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
and adjacent runbooks).

---

## Symptoms

What an operator sees when this fires:

- **Absence of `recon-<ts>` request_ids in the log.** This is the
  primary signal. Healthy operation shows one `recon-<unix_ts>` ID per
  30s tick plus zero-or-more `recon-fanout-<unix_ts>` IDs per
  post-merge `Trigger()`. When ticks stop, the log stream shows no
  `recon-` prefixed lines for >2 minutes.

  ```
  # Healthy stream (one per 30s):
  {"time":"...","level":"INFO","msg":"reconciler tick start","request_id":"recon-1717200000"}
  {"time":"...","level":"INFO","msg":"reconciler tick complete","request_id":"recon-1717200000",...}
  ```

- **Panic stack trace at the moment of failure** (if the reconciler
  goroutine panicked):

  ```
  {"time":"...","level":"ERROR","msg":"reconciler panic recovered","request_id":"recon-<ts>","panic":"runtime error: ...","stack":"..."}
  ```

  If the panic-recovery wrapper itself fails, the panic appears in pod
  stderr without the structured JSON:

  ```
  panic: runtime error: invalid memory address or nil pointer dereference
  [signal SIGSEGV: segmentation violation ...]
  goroutine 47 [running]:
  github.com/MoranWeissman/sharko/internal/clusterreconciler.(*Reconciler).pollOnce(...)
  ```

- **UI/API show cluster operations as succeeding while ArgoCD remains
  unchanged.** `GET /api/v1/clusters` returns the new cluster as
  `managed: true`; `kubectl -n argocd get secrets -l
  app.kubernetes.io/managed-by=sharko` does not include it.
- **Alert** `SharkoReconcilerStalled` (if shipped — currently a
  Prevention follow-up; today the operator detects via log absence).
- **Audit-log gap**: events with `event=cluster_secret_reconcile` stop
  appearing. The audit log is the canonical visible artefact of a
  successful tick; absence is the signal.

If the symptom is "the reconciler ticks but reports errors per cluster,"
this is not the runbook — see
[`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md).
This runbook is for the case where the reconcile loop itself exits or
panics.

---

## Diagnosis

Four checks. Each narrows whether the goroutine is dead, deadlocked, or
crashing in a recoverable way.

### 1. Confirm reconciler ticks have stopped

```sh
SHARKO_NS=<sharko-ns>
kubectl -n "$SHARKO_NS" logs -l app=sharko --since=5m \
  | jq -c 'select(.request_id // "" | startswith("recon-"))' \
  | tail -20
```

Expected on a healthy system: at least 10 lines (5 minutes × 2
ticks/minute × ≥1 log line per tick).

If the output is empty for >2 minutes, the reconciler is not ticking.
Confirm against the pod's start time — a recently-restarted pod may not
have ticked yet (first tick happens after `DefaultTickInterval = 30s`).

```sh
kubectl -n "$SHARKO_NS" get pod -l app=sharko \
  -o jsonpath='{.items[0].status.startTime}'
```

### 2. Look for a panic in the pod logs

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=5000 \
  | grep -E "panic|reconciler panic recovered|goroutine .* \[running\]" \
  | head -50
```

Three possible outcomes:

- **Recovered panic line** (`"reconciler panic recovered"`) — the
  panic-recovery wrapper caught the panic, logged it, but the
  goroutine continued. If you see this and ticks subsequently
  resumed, the runbook is informational only. If you see it and ticks
  stopped after, the recovery wrapper itself failed (rare; see root
  cause "recovery wrapper bug").
- **Bare panic stack trace** — the goroutine crashed without being
  caught. The pod itself is still alive (HTTP API still answers) but
  the reconciler thread is gone. Mitigation: restart the pod.
- **No panic** — the goroutine is deadlocked (a lock not released, a
  channel never closed, a context never cancelled). Mitigation:
  restart the pod, capture goroutine dump first.

### 3. Capture a goroutine dump (if pod is still alive)

For deadlock diagnosis, capture the full goroutine state before
restarting. Sharko exposes `/debug/pprof/goroutine?debug=2` (if pprof
is enabled — verify with the operator team):

```sh
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
kubectl -n "$SHARKO_NS" port-forward "$SHARKO_POD" 6060:6060 &
curl -sS "http://localhost:6060/debug/pprof/goroutine?debug=2" \
  > /tmp/sharko-goroutines.txt
```

Search for the reconciler goroutine:

```sh
grep -A 30 "clusterreconciler.*pollOnce\|clusterreconciler.*Start" \
  /tmp/sharko-goroutines.txt
```

Telltale signs of deadlock:

- `goroutine ... [chan send, 28 minutes]` — a send blocked on a
  channel with no receiver.
- `goroutine ... [semacquire, 28 minutes]` — a mutex held by another
  goroutine that itself is blocked.
- `goroutine ... [select, 28 minutes]` — a select with no ready case
  for >2 minutes.

Save the dump file with the post-mortem ticket — it pinpoints the bug
even after pod restart.

### 4. Check pod resource pressure

```sh
kubectl -n "$SHARKO_NS" top pod -l app=sharko
kubectl -n "$SHARKO_NS" describe pod -l app=sharko \
  | grep -A 5 "Last State\|OOMKilled\|Restart"
```

If `Last State: Terminated: OOMKilled`, the reconciler goroutine was
killed not because of a Sharko bug but because the pod ran out of
memory. The fix is to raise the memory limit and capture a heap dump
for post-mortem — see
[`oom-restart-loop.md`](oom-restart-loop.md) for the OOM-specific
runbook.

---

## Mitigation (try in order)

1. **Restart the pod.** This restores the reconciler — it starts fresh
   in `Start()` and the first `pollOnce` runs after
   `DefaultTickInterval = 30s`. Cheap, fast, and resolves every panic
   and deadlock case.

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: within ~60 seconds of the new pod becoming
   ready, you see `"reconciler tick start"` lines in the log:

   ```sh
   kubectl -n "$SHARKO_NS" logs -l app=sharko --since=2m \
     | jq -c 'select(.request_id // "" | startswith("recon-"))' \
     | head -5
   ```

2. **Verify reconciler ticks are running and converging.** After the
   restart, force a `Trigger()` by opening a no-op PR (or by waiting
   for the next merged PR's `prTracker.SetOnMergeFn` to fire) and
   confirm a `recon-fanout-<ts>` line follows:

   ```sh
   kubectl -n "$SHARKO_NS" logs -l app=sharko --since=2m \
     | jq -c 'select(.request_id // "" | startswith("recon-fanout-"))'
   ```

3. **Capture the goroutine dump and panic stack trace BEFORE
   restarting** if you want post-mortem data. The dump and the
   `"reconciler panic recovered"` log line (if present) are the
   evidence needed to fix the bug. Save them to ticket attachments.

   ```sh
   # Goroutine dump (if pprof enabled — see Diagnosis step 3):
   curl -sS "http://localhost:6060/debug/pprof/goroutine?debug=2" \
     > /tmp/sharko-goroutines-pre-restart.txt

   # Panic line:
   kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=5000 \
     | jq -c 'select(.msg | test("panic"; "i"))' \
     > /tmp/sharko-panic.json
   ```

4. **If the panic re-occurs after restart**, the underlying bug is
   reproducible from `managed-clusters.yaml` state. The reconciler
   panics every time it parses the same input. Two paths forward:

   a. **Roll back the recent change to `managed-clusters.yaml`**: if a
      recent commit added a malformed entry that triggers a panic in
      the parser or the ArgoCD-secret builder, revert it via a PR:

      ```sh
      cd <bootstrap-repo>
      git log --oneline configuration/managed-clusters.yaml | head -5
      # Identify the last-known-good commit
      git revert <bad-commit-sha>
      gh pr create --title "revert: managed-clusters.yaml" \
        --body "Reverting commit that triggered reconciler panic"
      # auto-merge once CI green
      ```

   b. **Stop the reconciler temporarily** to give the operator time
      to debug. **There is no kill-switch environment variable.** Sharko
      reads no `SHARKO_RECONCILER_ENABLED` or equivalent — the cluster
      reconciler starts whenever Sharko has an in-cluster Kubernetes
      client and a ConfigMap store, and there is no way to ask it not to.

      Earlier versions of this page told you to set
      `SHARKO_RECONCILER_ENABLED=false`. **Do not do that.** Sharko has
      never read that name, and from this release a `SHARKO_` name Sharko
      does not recognise stops the server at startup — so the command that
      was supposed to buy you time now takes the whole server down.

      The only ways to stop it are to stop the process or to take away
      what it needs:

      ```sh
      # Blunt but certain — no Sharko, no reconciler (and no API either):
      kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=0
      ```

      With the reconciler off, ArgoCD's existing Secrets continue to
      work, but new registrations/deregistrations require manual
      `kubectl` patching of ArgoCD Secrets until the bug is fixed.

      If you need Sharko's API up while the reconciler is down, that is
      not something the product supports today — say so in the incident
      channel rather than reaching for an env var that would stop the
      server. Document this as the temporary state in the audit log.

5. **Last resort — scale Sharko to zero and back.** A clean restart
   with no in-flight retry storm:

   ```sh
   kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=0
   sleep 30
   kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=1
   ```

   If the pod immediately enters CrashLoopBackoff after restart, the
   panic is happening during startup (init code path, not the
   reconciler tick). See
   [`oom-restart-loop.md`](oom-restart-loop.md) for the
   pod-CrashLoopBackoff runbook.

---

## Root-cause patterns

### Unhandled panic in `pollOnce`

A code path in the reconciler dereferences a nil pointer, indexes out of
bounds, or hits a `runtime.panic`. The panic-recovery wrapper catches
it on the first occurrence (logging
`"reconciler panic recovered"`) but the underlying cause stays — every
subsequent tick re-panics on the same input.

Diagnostic signature: repeated `"reconciler panic recovered"` lines at
the 30s cadence, all with the same panic string. The
`managed-clusters.yaml` content has not been changed in days — meaning
the panic is in code, not data.

Why it happens: a recent Sharko upgrade introduced the bug. The
reconciler builds each ArgoCD cluster Secret through shared code, so a
regression in how that Secret is built shows up here.

Fix: file a P0 bug with the goroutine dump and panic log lines. The
maintainer needs the panic stack frame and the `managed-clusters.yaml`
that reproduces it.

### Deadlock on the reconcile mutex

The reconciler holds an internal mutex while iterating clusters. If a
sub-call (`vault.Get`, `argocd.RegisterCluster`, an ArgoCD client
operation) hangs indefinitely while the mutex is held, subsequent
ticks block waiting for the lock and the goroutine appears stalled.

Diagnostic signature: no panic in logs; goroutine dump shows
`pollOnce` in `[semacquire, N minutes]` state with another goroutine
holding the same lock and itself blocked on a network call (e.g.
`net/http.(*Transport).RoundTrip`).

Why it happens: a downstream provider call (vault, ArgoCD) does not
respect its context cancellation, so the goroutine waits forever on a
nil response. The reconciler holds the lock for the duration of the
hang.

Fix: restart the pod. Capture the goroutine dump first. The root cause
fix is to ensure every provider call respects `ctx.Done()` — usually
adding `http.Client.Timeout` or wiring `WithTimeout` into the call
chain. File a P1 bug with the dump attached.

### Reconciler dependency missing at startup

The reconciler needs three things at startup: a Git provider, Kubernetes
API access to the ArgoCD namespace, and a cluster-credentials provider.
If any is missing, the reconciler runs but every tick is a no-op, and it
says so — `"no GitProvider getter configured, skipping reconcile"`.

Diagnostic signature: ticks ARE running (you see `recon-<ts>` IDs) but
every tick logs at WARN: `"no GitProvider getter configured, skipping
reconcile"` or `"no ArgoClient (k8s clientset) configured"` or
`"no Vault (cluster-credentials provider) configured"`.
The `audit.action=reconcile`
`audit.result=skipped` lines appear.

This is not a crash loop — it's a misconfiguration. The reconciler
loop is alive; it's just intentionally no-op-ing.

Fix: review the Helm values for `secrets.GITHUB_TOKEN`,
`config.connectionSecretName`, and provider credentials. Restart
Sharko after correcting. The per-dependency runbook is
[`cluster-reconciler-dependency-missing.md`](cluster-reconciler-dependency-missing.md).

### OOMKill on the pod

The Sharko pod was killed by the kernel for exceeding its memory limit.
The reconciler goroutine dies with the pod. On restart it's healthy,
but the cycle repeats every few hours / days.

Diagnostic signature: `kubectl describe pod` shows `Last State:
Terminated`, `Reason: OOMKilled`. The reconciler tick absence aligns
exactly with the pod restart time.

Why it happens: managed-clusters.yaml grew larger than expected, or the
in-memory cluster cache leaks. Common at fleet sizes >200 clusters
without bumping the Helm value `resources.limits.memory`.

Fix is the [`oom-restart-loop.md`](oom-restart-loop.md) runbook. The
short version: raise the limit and capture a heap dump for post-mortem.

---

## Rollback plan

Mitigation step 1 (restart) is non-destructive — the worst case is the
crash repeats and you escalate to step 4.

For Mitigation step 4a (revert a bad commit in `managed-clusters.yaml`):

1. If the revert PR itself causes problems (it removes a cluster that's
   in production), revert the revert:

   ```sh
   git revert <revert-commit-sha>
   ```

2. Open a hotfix PR that fixes the underlying parse / build issue
   instead of reverting the cluster entry.

For Mitigation step 4b: there is nothing to roll back. Nothing was
turned off, because Sharko has no switch for it. If you scaled the
deployment to zero, scale it back up:

```sh
kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=1
kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
```

Then verify ticks resume per Mitigation step 2.

If someone set `SHARKO_RECONCILER_ENABLED` on the deployment before
reading this, remove it — otherwise the server will refuse to start:

```sh
kubectl -n "$SHARKO_NS" set env deployment/sharko SHARKO_RECONCILER_ENABLED-
kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
```

---

## Prevention

- **Monitoring — alert on absence of recent `recon-<ts>` ticks.** Sharko
  does not export this metric today. The alert below is a design sketch
  for a future release, not something you can deploy now. The sketch: a
  recording rule that counts reconciler ticks in the last 5 minutes and
  pages when the count drops below threshold —

  ```
  rate(sharko_reconciler_ticks_total[5m]) == 0
  ```

  — paging when that stays true for more than 2 minutes. It would catch
  the silent failure mode before any user-visible cluster operation
  misbehaves. Wiring requires Sharko to emit
  `sharko_reconciler_ticks_total`, which it does not export today.

- **Gating — pprof endpoint in non-prod, opt-in in prod.** The
  goroutine dump in Diagnosis step 3 depends on
  `/debug/pprof/goroutine?debug=2` being reachable. Ship Sharko with
  pprof enabled by default in `dev` mode and gated behind a Helm value
  `debug.pprofEnabled` in prod. The cost of having pprof in prod is
  negligible; the cost of not having it during a crash-loop diagnosis
  is "we restarted before capturing the dump."

- **Scheduled work — chaos drill once per quarter.** There is no
  panic-injection hook in the shipped binary — no environment variable
  turns one on. Run the drill against a staging build with a deliberate
  panic compiled into `pollOnce`, or exercise the same recovery path by
  deleting the Sharko pod mid-tick. Verify the panic-recovery wrapper
  catches and logs cleanly. Tests the runbook end-to-end and trains the
  operator on Mitigation step 2's "verify ticks resumed" check.

---

## Related runbooks

- [`cluster-reconciler.md`](cluster-reconciler.md) — the reference page
  for the reconciler. Read this for the
  ownership-label semantics and the two-direction policy.
- [`oom-restart-loop.md`](oom-restart-loop.md) — when the pod itself
  is CrashLoopBackoff'ing. Often the cause when the reconciler tick
  absence aligns with pod restart events.
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md) —
  related symptom (PR merged, ArgoCD never converged). When the
  reconciler is healthy but the convergence doesn't land, the cause
  is downstream of the reconciler.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) —
  adjacent failure where the reconciler ticks but the credential
  fetch fails.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `recon-<ts>` and `recon-fanout-<ts>` correlation-ID shapes.

## Escalation

If Mitigation steps 1-4 do not resolve within 30 minutes, or the panic
reproduces on every pod restart, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The panic stack trace (full, not truncated)
- The goroutine dump from Diagnosis step 3 (if captured)
- The current `managed-clusters.yaml` content (or a redacted version
  with cluster names replaced)
- The Sharko version
- A 5-minute window of logs filtered by `request_id` starting with
  `recon-`

The maintainer is a single human, not a 24×7 rotation. Reconciler
crash bugs are P0 — expect a same-business-day investigation, but not
a paged response.
