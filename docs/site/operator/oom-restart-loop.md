# OOM Kill / Process Restart Loop

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The
> CrashLoopBackoff detection patterns (kubectl Restarts column, pod
> `Last State: Terminated, Reason: OOMKilled`, pod `lastTerminationReason`)
> are standard Kubernetes signals. Sharko-specific Helm chart memory
> defaults live in `charts/sharko/values.yaml` under `resources.limits`.
> Re-verify when Helm chart memory defaults change or when pprof / heap
> endpoint surface changes.

The Sharko pod is restarting in a tight loop. `kubectl get pod` shows
`STATUS: CrashLoopBackOff` with rising `RESTARTS` count over the last
5 minutes (typically > 3). Every restart either consumes more memory
than the kubelet limits allow and triggers OOMKill, or panics during
startup, or fails its liveness probe. The fleet cannot operate — every
API call hits a pod that's not ready, the reconciler ticks not at all
(see [`reconciler-crash-loop.md`](reconciler-crash-loop.md) for the
in-pod variant where the pod is alive but the reconciler thread is
dead), and the operator sees user-visible HTTP errors. Page on-call.

This runbook is for the **whole-pod restart loop** failure mode, not
the in-pod reconciler crash. The detection signal is the kubelet's
`Restarts` counter ticking up — that means the pod itself is dying.

---

## Symptoms

What an operator sees when this fires:

- **`kubectl get pod` shows CrashLoopBackOff and Restarts > 3:**

  ```sh
  kubectl -n <sharko-ns> get pod -l app=sharko
  # NAME                       READY   STATUS             RESTARTS      AGE
  # sharko-748d6f9bc8-xyz      0/1     CrashLoopBackOff   5 (32s ago)   12m
  ```

- **`kubectl describe pod` shows `Last State: Terminated`** with a
  reason — most commonly `OOMKilled`:

  ```
  Last State:     Terminated
    Reason:       OOMKilled
    Exit Code:    137
    Started:      Sat, 01 Jun 2026 12:00:00 +0000
    Finished:     Sat, 01 Jun 2026 12:03:45 +0000
  ```

  Other terminal reasons:
  - `Error` with Exit Code 1 — panic during startup
  - `Error` with Exit Code 2 — config error during startup
  - `Completed` (rare for a server) — process exited cleanly,
    typically a bug

- **`kubectl get event`** shows kubelet recording the OOMKill or
  liveness-probe failure:

  ```sh
  kubectl -n <sharko-ns> get events --sort-by='.lastTimestamp' --field-selector involvedObject.name=<pod-name>
  # ...
  # Warning   Unhealthy           Liveness probe failed: HTTP probe failed with statuscode: 500
  # Normal    Killing             Container sharko failed liveness probe, will be restarted
  # Warning   BackOff             Back-off restarting failed container
  ```

- **Sharko logs (from the PREVIOUS pod) may show**:
  - Startup panic stack trace if the failure is startup-time
  - Slowly-rising memory usage if the failure is OOMKill
  - Healthy startup followed by liveness-probe failure if it's
    long-running degradation

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --previous --tail=200
  ```

- **HTTP API is unavailable** — `curl http://sharko/api/v1/health`
  returns connection refused or a Service-Unavailable from the kube
  service IP because no endpoint is Ready.
- **Alerts that fire** when this is sustained:
  - Kubernetes-native alerts (kube-state-metrics):
    `KubePodCrashLooping` — fires after 15m of CrashLoopBackOff.
  - `SharkoClusterRegistrationFastBurn` and others — fire because all
    Sharko API paths return errors.

---

## Diagnosis

Three checks. Each narrows whether the failure is OOM (raise the limit),
startup panic (read the panic line), or liveness-probe regression
(check the probe config).

### 1. Read the previous pod's last state

```sh
SHARKO_NS=<sharko-ns>

kubectl -n "$SHARKO_NS" describe pod -l app=sharko \
  | grep -A 10 "Last State\|Reason\|Exit Code"
```

Three terminal-state buckets:

- **`Reason: OOMKilled`, `Exit Code: 137`** — kubelet killed the
  container for exceeding its memory limit. Jump to Diagnosis step 2.
- **`Reason: Error`, `Exit Code: 1` or `2`** — process exited with
  panic or config error. Jump to Diagnosis step 3.
- **`Reason: Completed`, `Exit Code: 0`** — process exited cleanly
  (rare for a server). Jump to Diagnosis step 4.

### 2. OOM diagnosis — how much memory and why

Get the configured limit:

```sh
kubectl -n "$SHARKO_NS" get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.containers[0].resources.limits.memory}'
# Expected: e.g. "512Mi" or "1Gi"
```

Compare to actual usage over time (if Prometheus + kube-state-metrics
is installed):

```promql
max_over_time(container_memory_working_set_bytes{
  namespace="<sharko-ns>", pod=~"sharko-.*"
}[1h])
```

If actual usage is approaching or exceeding the limit at a normal
operational time (e.g. just from holding the cluster cache for
managed-clusters.yaml), the limit is too low.

Inspect the heap usage trend if `/debug/pprof/heap` is enabled:

```sh
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
kubectl -n "$SHARKO_NS" port-forward "$SHARKO_POD" 6060:6060 &
go tool pprof -top -unit mb "http://localhost:6060/debug/pprof/heap"
```

The top-allocators report shows which packages own the heap. Common
hot spots:

- `internal/clusterreconciler` — managed-clusters.yaml is large
- `internal/catalog` — third-party catalog source is huge
- `internal/auth/store` — session table grew unbounded
- A goroutine leak — `goroutine` profile shows hundreds of leaked
  goroutines

### 3. Startup panic diagnosis

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --previous --tail=500 \
  | grep -E "panic|fatal|FATAL|error during startup" \
  | head -30
```

Common startup panic patterns:

- **Config error** (`Exit Code: 2`):
  ```
  fatal: SHARKO_ARGOCD_NAMESPACE not set
  ```
  → Helm value missing. Fix the values.yaml.

- **Provider init failure** (`Exit Code: 1`):
  ```
  panic: failed to initialize aws-sm provider: ...
  ```
  → See
  [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  for the provider-side diagnosis.

- **Bootstrap state corrupt** (`Exit Code: 1`):
  ```
  fatal: managed-clusters.yaml schema validation failed: ...
  ```
  → See
  [`cluster-reconciler.md`](cluster-reconciler.md#what-if-managed-clustersyaml-has-a-schema-validation-error).

- **TUF / catalog signing failure** (`Exit Code: 1`):
  ```
  fatal: catalog signing: trusted_root.json load failed
  ```
  → See
  [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md).

### 4. Liveness-probe diagnosis

If `Last State: Reason: Completed` or the events show liveness-probe
failures, inspect the probe config:

```sh
kubectl -n "$SHARKO_NS" get deployment sharko -o yaml \
  | grep -A 20 "livenessProbe:\|readinessProbe:"
```

Common regressions:

- The probe path changed (`/health` → `/api/v1/health` or vice
  versa); the deployment still references the old path.
- The probe timeout is shorter than Sharko's actual startup time
  (Sharko takes ~5-10s to initialize providers; a 2s timeout fails).
- The probe expects a specific response shape that Sharko changed.

### 5. Cluster-level resource pressure

```sh
kubectl describe node <node-where-sharko-runs> \
  | grep -A 5 "Allocated resources"
```

If the node is fully allocated and the Sharko pod is being evicted by
kube-scheduler, the failure is node-level capacity. Increase node count
or reschedule Sharko to a less-pressured node via affinity.

---

## Mitigation (try in order)

The order: raise the memory ceiling if OOM, fix startup error if
panic, fix probe if probe, then capture state for post-mortem.

1. **For OOMKill — raise the memory limit.** This is the cheapest and
   fastest mitigation. Bump the limit by 2x and restart:

   ```sh
   # Patch the limit in place:
   kubectl -n <sharko-ns> set resources deployment/sharko \
     --limits=memory=1Gi --requests=memory=512Mi
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=300s
   ```

   If you use Helm-managed values, the proper path is:

   ```sh
   helm upgrade --reuse-values \
     --set resources.limits.memory=1Gi \
     --set resources.requests.memory=512Mi \
     sharko sharko/sharko -n <sharko-ns>
   ```

   Success indicator: pod stays Ready, no further OOMKill events in
   the next 30 minutes.

   If 1Gi isn't enough, raise further. Production-scale fleets
   (200+ clusters, full catalog with third-party sources) commonly
   need 2-4Gi. This is not a leak; the working set genuinely scales.

2. **For startup panic — fix the underlying config/state and
   restart.** Per Diagnosis step 3, the panic line points to the
   cause:

   - Missing config → patch Helm values, restart.
   - Provider unreachable → fix the upstream per the linked runbook,
     restart Sharko.
   - Bootstrap state corrupt → see the linked runbook.
   - TUF / catalog signing → see the linked runbook.

   In each case, restart Sharko after the fix:

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   ```

3. **For liveness-probe regression — fix the probe config.** Per
   Diagnosis step 4:

   ```sh
   # Example: probe path was /health, Sharko serves /api/v1/health
   kubectl -n <sharko-ns> patch deployment sharko --type='json' -p='[
     {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/httpGet/path","value":"/api/v1/health"}
   ]'
   ```

   Or via Helm:

   ```sh
   helm upgrade --reuse-values \
     --set livenessProbe.httpGet.path=/api/v1/health \
     --set livenessProbe.timeoutSeconds=10 \
     sharko sharko/sharko -n <sharko-ns>
   ```

4. **Capture a heap dump or goroutine dump BEFORE restarting** if the
   OOM is suspicious (memory growth is faster than fleet size justifies).
   The dump is the evidence needed to find the leak:

   ```sh
   SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
   kubectl -n <sharko-ns> port-forward "$SHARKO_POD" 6060:6060 &

   # Heap dump:
   curl -sS "http://localhost:6060/debug/pprof/heap" \
     > /tmp/sharko-heap-pre-restart.pb.gz

   # Goroutine dump:
   curl -sS "http://localhost:6060/debug/pprof/goroutine?debug=2" \
     > /tmp/sharko-goroutines-pre-restart.txt

   # Allocations profile:
   curl -sS "http://localhost:6060/debug/pprof/allocs" \
     > /tmp/sharko-allocs-pre-restart.pb.gz
   ```

   Attach to the post-mortem ticket. If pprof isn't enabled, you lose
   this evidence on restart — prioritize capture before mitigation.

5. **Last resort — scale Sharko to zero and back as a clean state
   reset.** If the pod restarts loop because of corrupted state
   that's not in Git (in-memory cache poisoning, file-descriptor
   leak), scale-down + scale-up clears it:

   ```sh
   kubectl -n <sharko-ns> scale deployment/sharko --replicas=0
   sleep 30
   kubectl -n <sharko-ns> scale deployment/sharko --replicas=1
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=180s
   ```

   If the pod still CrashLoopBackOffs after scale-up, the failure is
   reproducible from config/state — see the relevant linked runbook
   per Diagnosis step 3.

---

## Root-cause patterns

### Memory limit too low for fleet size

The most common cause. The chart's default `resources.limits.memory`
(typically 256Mi or 512Mi) was sized for small deployments. As the
fleet grows past ~50 clusters or the catalog grows past ~200 entries
or a third-party catalog source ships a large bundle, the working set
exceeds the limit.

Diagnostic signature: `Reason: OOMKilled`. The heap top-allocators
show legitimate package usage (catalog data, managed-clusters
in-memory representation, session table). No goroutine leak in the
goroutine dump (count ~50-100, not 1000s).

Fix is Mitigation step 1 (raise the limit). Long-term: ship a
fleet-size-aware default in the chart, and document the expected
memory headroom per N clusters in `operator/installation.md`.

### Goroutine leak

Sharko leaks goroutines, eventually consuming memory and triggering
OOMKill. Common when a context cancellation isn't propagated, or a
channel never gets closed.

Diagnostic signature: `Reason: OOMKilled`. The goroutine dump shows
hundreds-to-thousands of goroutines, many in the same state (e.g.
`[chan send, 30 minutes]`). The heap top-allocator is `runtime` or
goroutine stack overhead.

Why it happens: a code path spawns goroutines without bounding their
lifetime. Common offenders: long-running HTTP requests with no
context cancellation, background polls without a stop channel.

Fix: heap and goroutine dump → file a P0 bug → upgrade Sharko when
fixed. Short-term mitigation: scheduled restart (CronJob restarting
the pod every 6h) bounds the leak's accumulation. Crude but bounds the
blast radius.

### Startup panic from missing config

Required environment variable / Helm value is unset. Sharko's startup
checks panic rather than proceeding with a bad config.

Diagnostic signature: `Reason: Error`, `Exit Code: 2`. Startup logs
show a `fatal:` line naming the missing config.

Fix: patch the Helm values, restart. Long-term: improve startup error
messages to point at the specific Helm value path.

### Startup panic from upstream dependency

Sharko's startup probes ArgoCD, the secrets provider, the Git
provider. If any is unreachable, Sharko refuses to start.

Diagnostic signature: `Reason: Error`, `Exit Code: 1`. Startup logs
show a `fatal: failed to initialize <provider>` line.

This is **intentional** — the startup is fail-fast on dependency
issues so the operator can't run Sharko in a half-functional state.
Fix the upstream per the linked runbook, restart Sharko.

### Liveness-probe regression after chart upgrade

A Helm chart upgrade changed the liveness probe's path or timeout, but
Sharko's HTTP surface changed independently. The probe now fails
consistently; kubelet restarts the pod every probe-failure interval.

Diagnostic signature: `Reason: Completed` or the events show
`Liveness probe failed`. The pod starts cleanly, runs for ~30s, then
gets killed by the probe.

Fix is Mitigation step 3 (correct the probe config to match Sharko's
current API).

---

## Rollback plan

For Mitigation step 1 (raise memory limit) — non-destructive; can be
reduced later if needed.

For Mitigation step 2 (config fix):

1. If the config patch broke other functionality, revert via Helm:
   ```sh
   helm rollback sharko <previous-revision> -n <sharko-ns>
   ```

For Mitigation step 3 (probe fix):

1. If the new probe config is wrong (too aggressive, wrong path),
   revert via Helm rollback.

For Mitigation step 5 (scale-to-zero recovery):

1. Non-destructive; doesn't need rollback.

---

## Prevention

- **Monitoring — alert on Restarts increase.** kube-state-metrics
  exposes `kube_pod_container_status_restarts_total`. Alert when this
  rises by > 3 in 15 minutes:

  ```promql
  rate(kube_pod_container_status_restarts_total{
    namespace="<sharko-ns>", pod=~"sharko-.*"
  }[15m]) > 0.2
  ```

  Pages before the CrashLoopBackOff state cascades into burn-rate
  alerts.

- **Monitoring — memory headroom alert.** Alert when the working set
  exceeds 80% of the limit:

  ```promql
  (
    container_memory_working_set_bytes{namespace="<sharko-ns>", pod=~"sharko-.*"}
    /
    container_spec_memory_limit_bytes{namespace="<sharko-ns>", pod=~"sharko-.*"}
  ) > 0.80
  ```

  Pages BEFORE OOMKill, giving the operator time to bump the limit.

- **Gating — pprof endpoint enabled by default.** Mitigation step 4
  depends on pprof. Ship the chart with pprof enabled, port 6060,
  in-cluster only (no Service). The cost is negligible; the value
  during incidents is high.

- **Gating — startup config validation.** Sharko's startup validates
  every required config value and prints actionable error messages
  before any subsystem initializes. Catches the config-error class
  before any pod runs in a confused state.

- **Capacity — fleet-size-aware memory defaults.** The chart's default
  `resources.limits.memory` should scale with the documented expected
  fleet size. Provide values overlays for "small" (< 50 clusters),
  "medium" (50-200 clusters), and "large" (200+ clusters) presets.

- **Scheduled work — quarterly chaos drill: kill the Sharko pod.**
  Verify the restart is clean, the readiness probe correctly
  withholds traffic during startup, and the reconciler picks up from
  the last-committed Git state. Validates Sharko's restart-resilience
  before a real incident.

---

## Related runbooks

- [`reconciler-crash-loop.md`](reconciler-crash-loop.md) — the in-pod
  variant where the pod is alive but the reconciler thread is dead.
  Check that runbook FIRST if the pod itself is up but reconciler
  ticks have stopped.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) —
  underlying cause of startup-panic-on-provider-init.
- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) —
  underlying cause of startup-panic-on-argocd-init.
- [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md) —
  underlying cause of startup-panic-on-trusted-root-load.
- [`cluster-reconciler.md`](cluster-reconciler.md) — underlying cause
  of startup-panic-on-managed-clusters-yaml-corrupt.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`installation.md`](installation.md) — chart defaults and the
  recommended `resources` settings per fleet size.

## Escalation

If the pod continues CrashLoopBackOff after Mitigation steps 1-3 — or
if you cannot identify the cause from Diagnosis steps 1-4 — email the
maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The output of `kubectl describe pod` for the failing pod
- The previous pod's last 500 lines of logs
  (`kubectl logs --previous --tail=500`)
- The heap dump and goroutine dump (Mitigation step 4) if captured
- The current `resources` settings (Helm values)
- The Sharko version and chart version
- The expected fleet size (cluster count + addon count)

The maintainer is a single human, not a 24×7 rotation. Pod-loop
incidents are P0 because they make Sharko unreachable; expect a
same-business-day investigation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (5 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (5 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert names referenced (KubePodCrashLooping + FastBurn)
-->
