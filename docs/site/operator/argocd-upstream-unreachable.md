# ArgoCD Upstream Unreachable

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The 502 error
> string `"no active ArgoCD connection"` is verified verbatim against
> `internal/api/addon_ops.go:73,190`, `internal/api/addons_upgrade.go:58,132`,
> `internal/api/addons_write.go:67,241,331`, and
> `internal/api/ai_annotate.go:97` as shipped. Diagnosis queries reference
> the V2-3 metric family in `internal/metrics/` and the
> `request_id` correlation pattern documented in
> [`../developer-guide/logging.md`](../developer-guide/logging.md). Re-verify
> before changing the `writeError` body string or the V2-3 burn-rate
> recording rules — both anchors are load-bearing here.

Every API path that touches ArgoCD is failing. Every cluster write, every
addon enable, every addon upgrade, every adopt, every dashboard read that
fans out into ArgoCD is returning 502. The fleet is in a state that gets
worse the longer it sits: PRs queue up, reconciler ticks no-op, and
inflight registrations are blocked. Page on-call.

This is **the single most-fanned-out P0** in Sharko — almost every API
write path catches the "no active ArgoCD connection" sentinel and bails
with 502. One root cause (ArgoCD outage, token revoked, network policy
block) surfaces from a dozen handlers. Diagnose the upstream first, not
the symptom site.

---

## Symptoms

What an operator sees when this fires:

- HTTP `502 Bad Gateway` response from `POST /api/v1/clusters`,
  `POST /api/v1/clusters/batch`, `POST /api/v1/clusters/{name}/adopt`,
  `DELETE /api/v1/clusters/{name}`, `PATCH /api/v1/clusters/{name}`,
  `POST /api/v1/clusters/{name}/test`, `POST /api/v1/addons`,
  `PATCH /api/v1/addons/{name}`, `POST /api/v1/addons/{name}/upgrade`,
  `POST /api/v1/addons/upgrade-batch`, and any `/ai/annotate-values` call.
- Response body verbatim: `{"error":"no active ArgoCD connection: <err>"}`
  where `<err>` is the underlying transport error (DNS resolution failure,
  TCP refused, TLS handshake failure, 401/403 from ArgoCD, or context
  timeout).
- UI: every cluster card and every addon control surfaces a red banner
  "ArgoCD connection lost" on the Fleet Dashboard and the Cluster Detail
  pages.
- `kubectl logs` line (one per failed handler invocation):

  ```
  {"time":"...","level":"WARN","msg":"argocd reachability probe failed","request_id":"req-...","error":"..."}
  ```

- Alert `SharkoClusterRegistrationFastBurn` (and any other FastBurn alert
  that depends on ArgoCD) is firing from
  `charts/sharko/templates/prometheusrules.yaml`. The full burn-rate
  surface is documented in
  [`budget-burn-runbook.md`](budget-burn-runbook.md).
- Burn-rate query confirms cross-surface failure:

  ```promql
  sum(rate(sharko_cluster_registration_errors_total[5m]))
  /
  clamp_min(sum(rate(sharko_cluster_registration_total[5m])), 1e-9)
  ```

  When this exceeds 1.44% (14.4× the 99.9% target) on both the 5m and 1h
  window, the FastBurn alert pages.

If the symptom is *one* handler returning 502 and others succeeding, this
is **not** the runbook. Either the request path doesn't reach ArgoCD (the
caller never set up an ArgoCD connection — see Configuration), or the
specific addon's chart is broken (per-addon Degraded Application is a
P1 failure mode tracked in
[`failure-mode-index.md`](failure-mode-index.md) — runbook in V2-4.3
PR 2b scope). This runbook is for the **fleet-wide** "every ArgoCD
path fails" case.

---

## Diagnosis

Where to look to confirm "ArgoCD upstream is down" vs. "Sharko's view of
ArgoCD is broken." Three checks, in this order.

### 1. Is ArgoCD itself healthy?

Get to the ArgoCD pods directly, bypassing Sharko entirely.

```sh
kubectl -n argocd get pods
kubectl -n argocd get pods -o wide  # which nodes, restart counts
```

Expected: every `argocd-*` pod `Running` with `0` restarts in the last
hour. If `argocd-server` or `argocd-application-controller` is
`CrashLoopBackOff`, `Pending`, or showing high restart counts, the
upstream is the failure — not Sharko. Stop here and follow ArgoCD's own
runbook (out-of-scope for Sharko).

```sh
kubectl -n argocd logs deploy/argocd-server --tail=200
kubectl -n argocd logs deploy/argocd-application-controller --tail=200
```

If the logs show admission webhook denials, network refused, or repeated
OOMKill, ArgoCD itself is the failure. Page the ArgoCD owner.

### 2. Can Sharko's pod reach ArgoCD's API?

Exec into the Sharko pod and probe ArgoCD's `/api/v1/version` directly:

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget -q -O - "https://argocd-server.argocd.svc:443/api/v1/version" \
  --no-check-certificate
```

Three possible outcomes:

- **JSON response** (e.g. `{"Version":"v2.10.3",...}`) — ArgoCD is
  reachable and authorised. Sharko's token works. The failure is
  intermittent or rate-related; jump to root cause "intermittent
  reachability".
- **Connection refused / DNS error / TLS handshake error** — network path
  from Sharko to ArgoCD is broken. Check NetworkPolicy and Service.
- **HTTP 401 / 403** — network is fine but the token is rejected. Jump
  to root cause "ArgoCD account token expired or revoked".

### 3. What does the Sharko health endpoint say?

```sh
kubectl -n <sharko-ns> port-forward "$SHARKO_POD" 8080:8080 &
curl -s http://localhost:8080/api/v1/health | jq .
```

Health response surfaces `argocd_reachable: bool` and the last reachability
probe timestamp without leaking the token. If `argocd_reachable: false`
matches the Diagnosis step 2 outcome, the Sharko-side detection is correct
and you can trust the symptom triage.

For log-driven diagnosis, the V2-2.2 `request_id` pattern joins every
handler's per-request lines into one stream:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
  | jq -c 'select(.msg | test("argocd|no active ArgoCD"; "i"))' \
  | head -50
```

For Loki-equipped stacks, the same in LogQL:

```logql
{app="sharko"} | json | msg =~ "(?i)argocd|no active ArgoCD"
```

A burst of `"argocd reachability probe failed"` lines clustered in a
window pinpoints when reachability broke — match against ArgoCD pod
restart times to confirm causation.

---

## Mitigation (try in order)

The order matches the most-likely-to-work-fastest first. Stop at the
first step that restores reachability — don't keep walking the list.

1. **Restart Sharko's pod to re-discover ArgoCD's Service endpoint.**
   The `autoDiscoverArgoCD()` path in `internal/argocd/client.go` probes
   every Service in `SHARKO_ARGOCD_NAMESPACE` at startup; if ArgoCD was
   redeployed (new ClusterIP, renamed Service), Sharko's cached endpoint
   is stale and a pod restart picks the new one up. This is the cheapest
   step and it fixes the most-common cause.

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: `/api/v1/health` returns `argocd_reachable: true`
   within 30 seconds of the new pod becoming ready. If not, proceed to
   step 2.

2. **Verify the ArgoCD account token Sharko is configured with is still
   valid.** Tokens can be rotated out-of-band; Sharko has no way to
   detect this until the next call returns 401/403. Read the token from
   the secret Sharko was deployed with and probe ArgoCD directly:

   ```sh
   ARGOCD_TOKEN=$(kubectl -n <sharko-ns> get secret <sharko-release>-argocd \
     -o jsonpath='{.data.token}' | base64 -d)
   kubectl -n argocd exec deploy/argocd-server -- \
     curl -sS -H "Authorization: Bearer ${ARGOCD_TOKEN}" \
     http://localhost:8080/api/v1/account
   ```

   If the response is `401` or `404 account not found`, the token was
   revoked. Generate a new token via the ArgoCD CLI, update the Sharko
   secret, restart Sharko:

   ```sh
   argocd account generate-token --account sharko
   # paste the new token into the Sharko secret
   kubectl -n <sharko-ns> patch secret <sharko-release>-argocd \
     --type='json' \
     -p='[{"op":"replace","path":"/data/token","value":"<base64-of-new-token>"}]'
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   ```

   Success indicator: same as step 1 — `/api/v1/health` flips to
   `argocd_reachable: true`.

3. **Check for a NetworkPolicy that blocks Sharko → ArgoCD.** A recently
   applied NetworkPolicy in the `argocd` namespace or the Sharko
   namespace is a common trigger after a security review or a CNCF
   policy rollout. List policies in both namespaces and inspect the
   `from`/`to` selectors:

   ```sh
   kubectl get networkpolicy -n argocd
   kubectl get networkpolicy -n <sharko-ns>
   kubectl describe networkpolicy -n argocd <policy-name>
   ```

   If a policy was applied today and blocks Sharko's namespace, the fix
   is to add Sharko's pod selector to the policy's `from` clause or
   add a dedicated NetworkPolicy that explicitly permits Sharko's
   service account to talk to `argocd-server:443`.

   Reference policy snippet that permits Sharko:

   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: allow-sharko-to-argocd
     namespace: argocd
   spec:
     podSelector:
       matchLabels:
         app.kubernetes.io/name: argocd-server
     ingress:
     - from:
       - namespaceSelector:
           matchLabels:
             kubernetes.io/metadata.name: <sharko-ns>
         podSelector:
           matchLabels:
             app: sharko
       ports:
       - protocol: TCP
         port: 8080
   ```

   Success indicator: from the Sharko pod, `wget` against
   `argocd-server.argocd.svc:443` returns a response instead of timing
   out.

4. **Confirm ArgoCD's Service still resolves.** A renamed Service (e.g.
   ArgoCD upgrade changed the chart's Service name template) breaks
   Sharko's hardcoded fallback. Verify:

   ```sh
   kubectl -n argocd get svc
   kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
     nslookup argocd-server.argocd.svc.cluster.local
   ```

   If the Service was renamed, set `SHARKO_ARGOCD_SERVER` in the Sharko
   deployment to the new name (or wait for the next pod restart — see
   step 1 — to let `autoDiscoverArgoCD()` find it).

5. **Last resort — scale Sharko to zero and back.** If steps 1-4 don't
   surface a root cause, a clean restart with no in-flight retry storm
   isolates whether the failure is Sharko-side (clears on restart) or
   genuinely upstream (persists across restart).

   ```sh
   kubectl -n <sharko-ns> scale deployment/sharko --replicas=0
   sleep 30
   kubectl -n <sharko-ns> scale deployment/sharko --replicas=1
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

   If reachability comes back on the new pod, the previous pod's
   in-memory state was corrupted (cached bad DNS, stuck HTTP/2 connection,
   exhausted file descriptors). Capture pod logs from the killed pod
   before they age out for post-mortem.

---

## Root-cause patterns

The four most common causes of fleet-wide ArgoCD-unreachable in Sharko's
shipped surface. Each is described in terms of what you see in the logs
and what makes it distinctive from the others.

### ArgoCD pod outage

The most common cause and the easiest to confirm. `argocd-server` or
`argocd-application-controller` is in `CrashLoopBackOff`, OOMKilled, or
scaled to zero. Sharko correctly reports the upstream as unreachable;
the failure is genuinely in ArgoCD's stack, not Sharko's.

Diagnostic signature: `kubectl -n argocd get pods` shows non-Running
pods; the Sharko log burst of `"argocd reachability probe failed"`
aligns in time with the ArgoCD pod's last restart. Sharko's
`SharkoClusterRegistrationFastBurn` alert fires within ~2 minutes of the
ArgoCD outage.

Fix in ArgoCD, not in Sharko. Sharko self-recovers on the next
reachability probe once ArgoCD returns. See ArgoCD's own operator
documentation for ArgoCD-side root causes (admission webhook timeout,
Redis pressure, repo-server OOM).

### ArgoCD account token expired or revoked

Sharko's `Authorization: Bearer <token>` header is rejected with 401 or
403. Network path is fine — ArgoCD's API is reachable from Sharko's
pod, but the token Sharko was deployed with no longer authorises any
operation.

Diagnostic signature: the wget probe in Diagnosis step 2 succeeds with
401/403, not a transport error. The audit log shows `cluster_secret_create`
attempts with `result=failed` and an `error` attribute containing
`"401 Unauthorized"` or `"403 Forbidden"`.

Why it happens: someone rotated the token via `argocd account
delete-token` (operator hardening, security review, or a credential
audit) without updating the secret Sharko reads. The Helm-installed
default token has no expiry, so this only surfaces post-rotation.

Fix is mechanical: generate a new token, update the secret, restart the
pod (Mitigation step 2). Sharko picks up the new token on startup —
there is no hot-reload of the ArgoCD token in the current code.

### NetworkPolicy block

A NetworkPolicy applied to either the `argocd` namespace or the
`<sharko-ns>` namespace blocks the Sharko → ArgoCD path. Common after a
security team's "default deny ingress" rollout, after a CNCF policy
template lands, or after an ArgoCD upgrade that ships new policies.

Diagnostic signature: the wget probe in Diagnosis step 2 fails with
"connection refused" or "connection timed out" while `nslookup` against
`argocd-server.argocd.svc` succeeds. DNS works, TCP doesn't. From the
node hosting Sharko, `kubectl get networkpolicy -A` shows a policy
applied in the last day.

Fix is the explicit-permit NetworkPolicy snippet in Mitigation step 3,
or relaxation of the existing deny-all policy to permit Sharko's pod
selector.

### Stale Service endpoint after ArgoCD redeploy

ArgoCD was redeployed in-place (Helm upgrade, manifest reapply,
namespace migration) and the Service got a new ClusterIP. Sharko cached
the old endpoint at startup via `autoDiscoverArgoCD()` and is hitting an
endpoint that no longer routes.

Diagnostic signature: the wget probe in Diagnosis step 2 fails with
"connection refused" or hangs; from the Sharko pod, `nslookup
argocd-server.argocd.svc` returns a different ClusterIP than the one
Sharko's logs reference on startup. The `"argocd reachability probe
failed"` lines started exactly at the ArgoCD redeploy time.

Fix is a Sharko pod restart (Mitigation step 1). The next
`autoDiscoverArgoCD()` pass picks up the new endpoint. To prevent: pin
ArgoCD's Service name in `charts/sharko/values.yaml` via
`config.argocdServer`, so Sharko reads from env not from discovery.

---

## Rollback plan

If Mitigation step 2 (token rotation) made things worse — for example,
the new token has narrower scope than the old one and other operations
break — rollback path:

1. Restore the old secret from the previous secret version (if the K8s
   API server has secret revision history enabled):

   ```sh
   kubectl -n <sharko-ns> get secret <sharko-release>-argocd \
     --show-managed-fields -o yaml > /tmp/sharko-argocd-current.yaml
   # Restore previous via your secrets-store backup tool, or:
   kubectl -n <sharko-ns> rollout undo deployment/sharko
   ```

2. Generate a new token with the full set of permissions Sharko needs
   (cluster CRUD + application CRUD + project CRUD). The ArgoCD account
   used by Sharko must have these claims in `argocd-rbac-cm`:

   ```
   p, sharko, clusters, *, *, allow
   p, sharko, applications, *, *, allow
   p, sharko, projects, *, *, allow
   ```

3. Confirm rollback success: `/api/v1/health` shows `argocd_reachable:
   true` and a synthetic write (e.g. `POST /api/v1/clusters/{name}/test`)
   returns 200.

Mitigation steps 1, 3, 4, 5 are non-destructive — no rollback needed.

---

## Prevention

How to make this failure mode less likely going forward. Three concrete
levers — one monitoring, one gating, one scheduled work item.

- **Monitoring — pre-page on the reachability probe directly.** The
  current alert (`SharkoClusterRegistrationFastBurn`) fires on
  error-rate burn after the failure is already user-visible. Add a
  Sharko-internal recording rule that exposes the latest reachability
  probe result and alert on it BEFORE the burn-rate alert:

  ```promql
  sharko_argocd_reachable_probe == 0
  ```

  When this is true for > 60s, page; this catches the failure before
  any user-visible API returns 502. (Wiring the recording rule into
  `internal/metrics/` is a P1 follow-up; the index entry stays a P0
  GAP until then because the *user-impact* surface is page-grade.)

- **Gating — pre-flight the ArgoCD account in CI.** Add a CI check to
  the Helm chart that verifies the configured ArgoCD account has the
  three required RBAC claims (`clusters`, `applications`, `projects`).
  If the account is misconfigured, the install fails fast at
  `helm install` time instead of at the first cluster registration. The
  check belongs in `charts/sharko/templates/_helpers.tpl` as a `required`
  guard or as a `kubectl auth can-i` probe in a startup Job.

- **Scheduled work — quarterly token rotation drill.** Sharko's ArgoCD
  account token has no expiry by default; tokens last until manually
  revoked. Schedule a 90-day rotation: generate a new token, update
  the secret, restart Sharko, verify reachability. The drill catches
  the rotation runbook drift (Mitigation step 2) and trains the
  operator on the procedure before a real incident.

---

## Related runbooks

- [`budget-burn-runbook.md`](budget-burn-runbook.md) — V2-3.3 burn-rate
  alerts. The `SharkoClusterRegistrationFastBurn` /
  `SharkoAddonCycleFastBurn` / `SharkoDashboardReadFastBurn` alerts all
  cross-link here when ArgoCD-unreachable is the root cause.
- [`cluster-reconciler.md`](cluster-reconciler.md) — when ArgoCD is
  reachable but Sharko's reconciler isn't converging the ArgoCD cluster
  Secret. Adjacent failure mode; same surface, different layer.
- [`git-provider-unreachable.md`](git-provider-unreachable.md) — the
  exact mirror failure for Git provider connectivity. If both fail at
  once, you have a cross-namespace network policy issue, not a
  per-provider one.
- [`failure-mode-index.md`](failure-mode-index.md) — the master
  inventory of every operator-facing failure mode in Sharko.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  V2-2.2 `request_id` correlation pattern, used in every diagnosis
  step above.

## Escalation

If the mitigations above do not resolve the failure within 30 minutes,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The exact alert name (`SharkoClusterRegistrationFastBurn` or other)
- The Sharko version (`sharko version` or the Helm chart version)
- A 5-minute window of relevant logs filtered by `request_id` per the
  [correlation pattern](../developer-guide/logging.md#correlation-ids)
- The output of the wget probe in Diagnosis step 2

The maintainer is a single human, not a 24×7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks
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
