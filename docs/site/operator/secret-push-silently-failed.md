# Secret Push to Remote Cluster Silently Failed

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The error log
> line `"[secrets] failed to create secret, continuing"` and the
> `result.Failed` accumulator are verified verbatim against
> `internal/orchestrator/secrets.go:110` as shipped. The "continuing"
> path is the canonical silent-data-loss surface flagged in
> [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md).
> Re-verify before changing the `"continuing"` log string or the
> `result.Failed` accumulator shape — both are anchors for log greps
> and audit-log correlation.

Sharko's API returned `201 Created` for a cluster registration. The
operator received the success response, the PR was opened, the PR was
merged, and the ArgoCD cluster Secret was created. But one or more of
the addon secrets (Datadog API key, Grafana token, ESO credentials,
etc.) that were supposed to be pushed to the remote cluster's namespace
**were not actually pushed**. The user thinks the credential is in
place; the addon will deploy and fail with `MissingSecret` /
`Forbidden` / `401` once ArgoCD's sync wave reaches it.

This is a P0 because the failure is **silent data loss from the
operator's perspective**. The HTTP response is success, no exception
propagates to the caller, and the audit log shows the cluster operation
as `result=success`. The addon's failure shows up minutes-to-hours later
when ArgoCD syncs, and the operator has to trace the failure backwards
to the secret push — a slow, error-prone diagnosis.

The canonical reference for the underlying bug shape is
[`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
"continuing on error" — this runbook is the operator-facing companion.

---

## Symptoms

What an operator sees when this fires:

- **`POST /api/v1/clusters` returns 201** with the cluster object as
  expected, but the response body's `addon_secrets` field (if surfaced)
  shows partial population: `{"created": ["datadog-keys"], "failed":
  ["external-secrets-creds"]}`. If the response shape doesn't surface
  failures, operators only notice via downstream symptoms below.
- **Addon ArgoCD Application is in `Degraded` state** after the next
  sync — with the underlying error indicating a missing or invalid
  Kubernetes Secret:

  ```
  Resource Health Check:
    Pod some-addon-xyz: CreateContainerError
    Reason: Secret "datadog-keys" not found in namespace "datadog"
  ```

- **Sharko logs show a per-addon ERROR line at the moment of cluster
  registration**, but the overall registration is reported as
  success:

  ```
  {"time":"...","level":"ERROR","msg":"[secrets] failed to create secret, continuing","request_id":"req-...","addon":"datadog","error":"unauthorized: kubeconfig token expired"}
  ```

- **Audit-log entry**: `event=cluster_register` with `result=success`,
  but a per-addon secondary entry shows `event=secret_push`
  `result=failed` for the same `request_id` — and the operator is
  expected to correlate by request_id manually (today).
- **The remote cluster's namespace does NOT contain the expected
  secret**:

  ```sh
  kubectl --kubeconfig <remote-kubeconfig> -n <addon-ns> get secret <secret-name>
  # Error from server (NotFound): secrets "<secret-name>" not found
  ```

  …while Sharko's view of "secret was pushed" is green.

- **Alert** — there is currently no Prometheus alert specifically for
  "addon secret push failed." This is part of the failure mode's
  P0-ness: silent failure with no automated detection.

---

## Diagnosis

The single most important diagnostic: **don't trust the cluster
registration's HTTP response. Look at the per-addon log line.**

### 1. Grep the per-registration log for the failure line

For a specific cluster registration where you suspect this failure:

```sh
SHARKO_NS=<sharko-ns>
REQUEST_ID=req-<id-from-audit-log>

kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=10000 \
  | jq -c "select(.request_id == \"$REQUEST_ID\")" \
  | grep -E "failed to create secret|continuing"
```

If you don't know the `request_id`, find it from the audit log:

```sh
# Audit entries for the cluster registration:
curl -sS http://sharko/api/v1/audit?event=cluster_register \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.entries[] | select(.cluster == "<cluster-name>")'
```

Pull the `request_id` from the audit entry and run the grep above.

A hit means: at least one addon secret push failed. Read the `addon`
and `error` attributes for the specific failure.

### 2. Enumerate the failures per addon

If Sharko's log retention covers the full window, count failed pushes
per cluster registration:

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --since=24h \
  | jq -c 'select(.msg | test("failed to create secret, continuing"))' \
  | jq -c '{request_id, addon, error}' \
  | sort | uniq -c | sort -rn
```

Output groups by `(request_id, addon, error)` triple. Patterns:

- **Multiple `request_id`s with the same `addon` + `error`** — a
  consistent bug for that addon's secret push (kubeconfig path issue,
  vault path stale, RBAC missing). Fleet-wide impact.
- **One `request_id` with many `addon`s failing the same way** — one
  cluster's kubeconfig is broken; the cluster's connectivity is the
  issue.

### 3. Verify the actual remote-cluster state

For each failed push, verify the remote cluster:

```sh
# Get the remote cluster's kubeconfig from the secrets provider
# (assuming AWS Secrets Manager backend; substitute your provider):
aws secretsmanager get-secret-value --secret-id clusters/<cluster-name> \
  --query SecretString --output text > /tmp/remote.kubeconfig

# Now probe the namespace:
NAMESPACE=<addon-ns>
SECRET_NAME=<addon-secret-name>
kubectl --kubeconfig /tmp/remote.kubeconfig -n "$NAMESPACE" \
  get secret "$SECRET_NAME" -o yaml | head -10
```

Three outcomes:

- **Secret exists with correct keys** — push actually succeeded; the
  log is stale or the addon is failing for a different reason (chart
  bug, RBAC). Stop here; this runbook does not apply.
- **Secret does not exist** — push failed; the log is correct. Proceed
  to Mitigation.
- **Secret exists but is empty / wrong keys** — partial push; the
  failure is in the `EnsureSecret` body. File a P0 bug; the
  remote-write path corrupted state.

### 4. Verify the upstream secret value (vault / AWS SM) is fetchable

If the failure was `"fetching key X from path Y"`, the secrets provider
was unreachable for that specific key, not the remote-cluster write.
Verify the upstream:

```sh
# For AWS SM:
aws secretsmanager get-secret-value --secret-id secrets/datadog/api-key \
  --query SecretString --output text | head -c 20

# For Kubernetes-Secrets provider:
kubectl -n <provider-ns> get secret <vault-secret> -o yaml | grep <key>
```

If this fails, the failure is at the provider read, not the remote
write — see
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

---

## Mitigation (try in order)

The goal is to (a) get the secret onto the remote cluster, (b)
re-trigger the addon's deployment, and (c) prevent the silent-failure
mode from striking again on the next registration.

1. **Re-push the secret manually for the affected cluster.** Sharko
   exposes the `POST /api/v1/clusters/{name}/secrets/refresh` endpoint
   which re-runs the secret push logic for all addons enabled on the
   cluster — without re-running cluster registration:

   ```sh
   curl -sS -X POST \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/<cluster-name>/secrets/refresh"
   ```

   Read the response carefully — this endpoint returns `{"created":
   [...], "failed": [...]}` so you can verify each push succeeded.
   If `failed` is still non-empty, the per-addon failure is consistent
   (not a transient blip); jump to step 2.

   Success indicator: `failed: []` and the
   `kubectl --kubeconfig ... get secret` probe from Diagnosis step 3
   returns the secret.

2. **Push the secret directly via `kubectl` as a temporary unblock.**
   If the API path is also broken (e.g. the remote kubeconfig is
   genuinely invalid), bypass Sharko and push manually:

   ```sh
   # Fetch the secret value from the provider:
   SECRET_VALUE=$(aws secretsmanager get-secret-value \
     --secret-id secrets/<addon>/<key> \
     --query SecretString --output text)

   # Create the secret on the remote cluster:
   kubectl --kubeconfig /tmp/remote.kubeconfig -n <addon-ns> \
     create secret generic <secret-name> \
     --from-literal=<key>="$SECRET_VALUE" \
     --dry-run=client -o yaml \
     | kubectl --kubeconfig /tmp/remote.kubeconfig apply -f -
   ```

   Add the Sharko ownership label so the reconciler doesn't fight you:

   ```sh
   kubectl --kubeconfig /tmp/remote.kubeconfig -n <addon-ns> \
     label secret <secret-name> \
     app.kubernetes.io/managed-by=sharko --overwrite
   ```

   Success indicator: addon ArgoCD Application transitions from
   `Degraded` to `Healthy` on the next sync.

3. **Re-sync the affected ArgoCD Application** so it picks up the
   newly-pushed secret:

   ```sh
   argocd app sync <addon-app-name> --prune --refresh
   ```

   Or via the ArgoCD UI: Application detail → Sync → Sync.

   Success indicator: the Application's resource tree shows the
   addon's Pods entering `Running` state.

4. **If the failure is fleet-wide (multiple clusters' registrations are
   silently failing)** — disable the affected addon globally to stop
   bleeding into more clusters. From the addon catalog:

   ```sh
   # Disable the addon by removing it from cluster addons:
   curl -sS -X PATCH \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{"removeAddons":["<addon-name>"]}' \
     "http://sharko/api/v1/clusters/<cluster-name>"
   ```

   Then file a bug against the addon's secret definition. Common
   root causes: the secret definition path moved in the vault, the
   addon's expected secret key name was renamed, or the addon's
   target namespace was renamed in the chart.

5. **Last resort — roll back the addon registration entirely.** If
   the addon's secret push is fundamentally broken and ArgoCD has
   already partially synced, deregister the addon for all clusters:

   ```sh
   # Mark addon as unavailable in the catalog (administrative action):
   # Edit catalog YAML, set addon as PLANNED or remove the entry.
   # Restart Sharko to pick up the change.
   ```

   This is destructive — the addon's existing deployments will be
   pruned by ArgoCD on next sync. Coordinate with the addon owner
   before proceeding.

---

## Root-cause patterns

### Remote cluster kubeconfig token expired

The Sharko-side path to the remote cluster (kubeconfig fetched from the
provider) has a token that expired between cluster registration and
secret push. For EKS clusters with IRSA, the STS token is valid for
15 minutes — if cluster registration takes longer than that or if
Sharko caches the token across requests, the push fails with
"unauthorized."

Diagnostic signature: log shows `"error":"unauthorized"` or
`"error":"401"` in the addon-specific failure line. The cluster
itself is healthy and reachable from outside Sharko (e.g. you can
`kubectl get pods` from your laptop with a fresh token).

Why it happens: the cached kubeconfig in Sharko's in-memory provider
cache is stale. Sharko does not currently re-mint the token on each
request — a P1 bug worth tracking.

Fix is Mitigation step 1 (`/clusters/{name}/secrets/refresh`), which
forces a fresh token mint. Prevention is to set the secrets-refresh
cadence to less than 10 minutes, or to switch to a long-lived bearer
token if security constraints allow.

### Vault / Secrets-Manager path moved

The secret definition in the addon catalog points to a vault path that
no longer resolves (the secrets-store admin moved it, the prefix
changed, the key was renamed). Sharko's GetSecretValue fails, and
because the failure happens in the per-addon inner loop, the cluster
registration's outer try/catch swallows it and logs "continuing."

Diagnostic signature: log shows `"error":"fetching key X from path Y:
...not found"`. The vault path itself, when probed manually, indeed
returns NotFound.

Why it happens: out-of-band secrets-store admin changes (path moves,
prefix changes, key renames) without coordinating with the Sharko
catalog. Common after secrets-store migrations or RBAC restructures.

Fix:
- Short-term: update the catalog entry (the `secrets.keys` map) to point
  at the new path, restart Sharko, re-trigger the cluster's
  `/secrets/refresh`.
- Long-term: add a CI check that validates every catalog entry's secret
  paths against the actual vault tree before publishing. The path
  validation is in scope for a follow-up V2-4.x ticket.

### Remote-cluster namespace doesn't exist

The addon catalog specifies a target namespace (e.g. `datadog`,
`external-secrets`), but the remote cluster doesn't have that
namespace. `EnsureSecret` calls `Create` against a non-existent
namespace and gets a NotFound from the API server.

Diagnostic signature: log shows
`"error":"namespaces \"<ns>\" not found"`. The remote cluster, when
probed manually, indeed doesn't have the namespace.

Why it happens: the cluster was registered fresh, the addon chart
would normally create the namespace via Helm, but the secret push runs
BEFORE the chart deploys. Race condition: Sharko expects the namespace
to exist already because it's writing a secret into it.

Fix:
- Short-term: create the namespace manually:
  ```sh
  kubectl --kubeconfig /tmp/remote.kubeconfig create namespace <ns>
  ```
  Then re-trigger Mitigation step 1.
- Long-term: change Sharko's secret-push logic to create the namespace
  if missing, with a label noting Sharko created it. The logic is in
  `EnsureSecret` in `internal/remoteclient/`.

### Per-addon RBAC missing on the remote kubeconfig

The kubeconfig Sharko uses for the remote cluster has limited RBAC —
enough to read cluster info, but not enough to write secrets in the
addon's namespace. This happens when the cluster was registered with a
read-only kubeconfig and then upgraded to "writable" out of band.

Diagnostic signature: log shows `"error":"secrets is forbidden: User
... cannot create resource secrets"`. The cluster is reachable; the
RBAC is the gate.

Why it happens: the kubeconfig stored in the secrets provider has
limited RBAC, but the addon catalog assumed full cluster-admin. Common
when the operator migrated from a read-only fleet to a fully-managed
fleet without updating the per-cluster kubeconfig.

Fix:
- Short-term: replace the kubeconfig in the secrets provider with one
  that has the required RBAC. Re-trigger `/secrets/refresh`.
- Long-term: document the minimum RBAC required by Sharko, and add a
  startup probe that verifies the kubeconfig can write to the
  expected namespaces.

---

## Rollback plan

Mitigation steps 1-3 are non-destructive (they push or re-sync
existing intent).

For Mitigation step 4 (disable addon for one cluster):

1. Re-enable the addon once the root cause is fixed:
   ```sh
   curl -sS -X PATCH \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     -d '{"addAddons":["<addon-name>"]}' \
     "http://sharko/api/v1/clusters/<cluster-name>"
   ```

For Mitigation step 5 (catalog-level addon removal):

1. Restore the catalog entry.
2. Restart Sharko.
3. Re-register the affected clusters' addons one by one, verifying
   the secret push succeeds each time.

---

## Prevention

- **Code change — turn "continuing" into a hard failure**, or surface
  it explicitly in the HTTP response. Per the
  [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md),
  the `"continuing"` path in
  `internal/orchestrator/secrets.go:110` is the canonical
  silent-data-loss surface. The fix is one of:
  - Return the failed secret push as part of the
    `POST /api/v1/clusters` response body so the caller knows.
  - Mark the cluster registration as partial success (HTTP 207
    Multi-Status or 202 Accepted with a follow-up endpoint).
  - Hard-fail the cluster registration and require the operator to
    re-run after fixing the secret config.
  This is the V2-4.x code-level prevention. Tracking required.

- **Monitoring — alert on per-addon secret-push failures.** Add a
  Prometheus counter `sharko_addon_secret_push_failures_total` and
  alert when its rate exceeds 0 for any 5-minute window. Wiring is a
  small change in `internal/orchestrator/secrets.go`.

- **Scheduled work — daily reconciliation of secret state.** Sharko
  already runs the cluster-secret reconciler at 30s cadence; add an
  equivalent secrets-reconciler that probes each managed cluster's
  expected addon secrets and re-pushes any missing/changed ones. This
  brings eventual consistency to addon secrets, matching the existing
  guarantee for cluster Secrets.

---

## Related runbooks

- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) —
  the related failure mode where the provider (AWS SM / vault / k8s
  Secrets) is unreachable. Often the upstream cause of the
  fetching-side failures here.
- [`cluster-reconciler.md`](cluster-reconciler.md) — V125-1-8
  reconciler for ArgoCD cluster Secrets. The addon-secret reconciler
  pattern modeled in Prevention would mirror this.
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md) —
  related symptom (operator thinks the cluster is set up; ArgoCD
  doesn't agree). When the symptom is "addon Application is Degraded"
  rather than "ArgoCD didn't see the merge."
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md) —
  the engineering punchlist that flagged this failure mode.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-4 don't restore the addon to Healthy within 30
minutes, or if the same failure recurs across multiple cluster
registrations (suggesting a fleet-wide bug), email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The cluster name(s) affected
- The addon name(s) affected
- The exact log line from Diagnosis step 1 (the
  `"failed to create secret, continuing"` entry with `addon` and
  `error` attributes)
- The audit-log entry for the cluster registration
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Silent
data-loss bugs are prioritized — expect a same-business-day
investigation.

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
- [x] (if applicable) No alert defined yet (per Symptoms)
-->
