# Cluster Reconciler Crash Loop

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The reconciler
> tick body (`pollOnce`), the `recon-<unix_ts>` and `recon-fanout-<unix_ts>`
> correlation-ID shapes, and the `DefaultTickInterval = 30s` cadence are
> verified against `internal/clusterreconciler/reconciler.go` as shipped.
> The cross-link to the V125-1-8 design and ownership-label semantics
> points at [`cluster-reconciler.md`](cluster-reconciler.md), which is
> the established reference page. Re-verify when the reconciler's
> tick cadence, correlation-ID shape, or panic-recovery wrapper changes
> — all three are load-bearing in Diagnosis.

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
[`reconciler-per-cluster-failure.md`](failure-mode-index.md) (P1 GAP,
PR 2b scope). This runbook is for the case where the goroutine itself
exits or panics.

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

   b. **Disable the reconciler temporarily** to give the operator time
      to debug. Patch the deployment to set
      `SHARKO_RECONCILER_ENABLED=false` (or whatever the env-var
      kill-switch is; verify with the engineering team):

      ```sh
      kubectl -n "$SHARKO_NS" set env deployment/sharko \
        SHARKO_RECONCILER_ENABLED=false
      kubectl -n "$SHARKO_NS" rollout status deployment/sharko
      ```

      With the reconciler off, ArgoCD's existing Secrets continue to
      work, but new registrations/deregistrations require manual
      `kubectl` patching of ArgoCD Secrets until the bug is fixed.
      Document this as the temporary state in the audit log.

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
`internal/clusterreconciler/` package depends on
`internal/argosecrets/manager.go`'s `BuildSecretConfigJSON` /
`BuildClusterSecretLabels` wrappers — a regression there propagates
into the reconciler.

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

The reconciler is constructed in `serve.go` with dependencies
(GitProvider, ArgoClient, Vault). If any is nil at startup, the
reconciler runs but every tick is a no-op (`"no GitProvider getter
configured, skipping reconcile"` warning per V2-2.3 audit).

Diagnostic signature: ticks ARE running (you see `recon-<ts>` IDs) but
every tick logs at WARN: `"no GitProvider getter configured, skipping
reconcile"` or `"no ArgoClient (k8s clientset) configured"` or
`"no Vault (cluster-credentials provider) configured"` from
`reconciler.go:325-340`. The `audit.action=reconcile`
`audit.result=skipped` lines appear.

This is not a crash loop — it's a misconfiguration. The reconciler
loop is alive; it's just intentionally no-op-ing.

Fix: review the Helm values for `secrets.GITHUB_TOKEN`,
`config.connectionSecretName`, and provider credentials. Restart
Sharko after correcting. The full per-dependency runbook is a P1 GAP
(PR 2b scope).

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

For Mitigation step 4b (disable reconciler):

1. Re-enable the reconciler by removing the env var:

   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     SHARKO_RECONCILER_ENABLED-
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko
   ```

2. Verify ticks resume per Mitigation step 2.

---

## Prevention

- **Monitoring — alert on absence of recent `recon-<ts>` ticks.** Add
  a Sharko recording rule that counts reconciler ticks in the last
  5 minutes and pages when the count drops below threshold:

  ```promql
  rate(sharko_reconciler_ticks_total[5m]) == 0
  ```

  When this is true for > 2 minutes, page. Catches the silent failure
  mode before any user-visible cluster operation misbehaves. Wiring
  requires Sharko to emit `sharko_reconciler_ticks_total` — a P1
  follow-up in the V2-3.x metric backlog.

- **Gating — pprof endpoint in non-prod, opt-in in prod.** The
  goroutine dump in Diagnosis step 3 depends on
  `/debug/pprof/goroutine?debug=2` being reachable. Ship Sharko with
  pprof enabled by default in `dev` mode and gated behind a Helm value
  `debug.pprofEnabled` in prod. The cost of having pprof in prod is
  negligible; the cost of not having it during a crash-loop diagnosis
  is "we restarted before capturing the dump."

- **Scheduled work — chaos drill once per quarter.** Inject a panic
  into the reconciler in staging (set
  `SHARKO_RECONCILER_PANIC_TEST=true` if such a hook exists, or
  `kubectl exec` a `kill -9 <reconciler-goroutine>` if pprof allows).
  Verify the panic-recovery wrapper catches and logs cleanly. Tests
  the runbook end-to-end and trains the operator on Mitigation
  step 2's "verify ticks resumed" check.

---

## Related runbooks

- [`cluster-reconciler.md`](cluster-reconciler.md) — V125-1-8
  architectural overview of the reconciler. Read this for the
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
- [x] (if applicable) Alert name from prometheusrules.yaml referenced
-->
