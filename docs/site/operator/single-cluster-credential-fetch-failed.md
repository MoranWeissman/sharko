# Single Cluster's Credential Fetch Failed

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The audit
> `action=get_credentials` with `result=failure` and the error log
> `"[clusterreconciler] vault GetCredentials failed — skipping cluster
> (others still reconcile)"` are verified verbatim against
> `internal/clusterreconciler/reconciler.go:555-572` as shipped. The
> per-cluster error-isolation contract (one cluster's vault failure
> does NOT block reconciliation of the others — design section 10)
> is implemented at the same call site by the `return` after the audit
> entry, not `continue` or panic. Re-verify before changing the
> reconciler's per-cluster error-isolation contract or the audit-entry
> shape — both are anchors here and in
> [`cluster-reconciler.md`](cluster-reconciler.md).

One specific cluster's credential fetch from the configured secrets
provider (AWS Secrets Manager, Kubernetes Secrets, or Vault) is failing
on every reconciler tick. The reconciler logs an Error line per failed
cluster, emits an audit entry with `action=get_credentials` and
`result=failure`, then **continues** to the next cluster — so the rest
of the fleet keeps converging. The affected cluster's ArgoCD Secret
either was never created (cluster was just added) or is stale (the
existing Secret carries an old token / CA / kubeconfig that does not
match the current upstream cluster).

The blast radius is **one cluster**. The other clusters' reconciler
ticks proceed normally; the dashboard surfaces the affected cluster
with a stale `last_reconciled` timestamp and (if the addon is enabled)
ArgoCD shows the cluster's Application drifting or failing to sync
because the bearer token has rotated.

This is distinct from
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
— that is a fleet-wide failure (the provider is offline; every
cluster's credential fetch fails). This runbook is for the case where
**most clusters reconcile fine** but **one (or a small handful) keeps
failing**.

---

## Symptoms

What an operator sees when this fires:

- **Per-tick error log** from the reconciler, named per cluster:

  ```
  {"time":"...","level":"ERROR","msg":"[clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)","cluster":"prod-eu","cred_key":"prod-eu","error":"<provider-specific error>"}
  ```

  The `cluster` and `cred_key` fields identify which cluster failed.
  When the cluster's `managed-clusters.yaml` entry sets
  `secret_path: <override>`, `cred_key` is the override path; otherwise
  it equals the cluster name.

- **Per-tick audit entry** correlated by `request_id` (synthetic
  `recon-<ts>` ID per tick):

  ```json
  {
    "time": "...",
    "level": "error",
    "event": "cluster_secret_reconcile",
    "user": "sharko",
    "action": "get_credentials",
    "resource": "cluster:prod-eu",
    "source": "reconciler",
    "result": "failure",
    "error": "<provider-specific error>",
    "request_id": "recon-..."
  }
  ```

- **Other clusters reconcile successfully in the same tick**. Query
  the audit ring for the same `request_id`:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=cluster_secret_reconcile&limit=100" \
    | jq -r '.[] | "\(.time) \(.action) \(.resource) \(.result)"' \
    | head -30
  ```

  Expected pattern: most rows show `action=cluster_secret_create` or
  `cluster_secret_skip` with `result=success`; the affected cluster
  shows `action=get_credentials` with `result=failure`.

- **Dashboard** displays the affected cluster with status `Stale` or
  `Reconcile failed` and (if the column is surfaced) a `last_error`
  tooltip showing the provider-specific error string. The fleet's
  overall health stays Green because the other clusters are fine.

- **ArgoCD-side symptom (downstream)**: if the affected cluster had
  a working Secret previously and the credential rotated, ArgoCD's
  cluster-controller starts seeing 401 on its own API calls to the
  cluster, and the Application(s) for that cluster transition to
  `Unknown` or `Degraded` health. This downstream symptom often
  catches the operator's attention before the reconciler audit
  entry does.

- **No specific Prometheus alert fires for one cluster's credential
  failure today.** Sustained per-cluster failure across multiple
  clusters fans into
  [`SharkoClusterRegistrationSlowBurn`](budget-burn-runbook.md#sharkoclusterregistrationslowburn)
  once the error budget is consumed; a single isolated cluster
  staying stuck is operator-detected, not auto-alerted (V2-4.x
  follow-up).

If the audit log shows `action=get_credentials` with `result=failure`
**for every cluster** in the same tick — not just one — this is
**not** the runbook. The provider itself is down. Jump to
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

---

## Diagnosis

Three checks. Step 1 confirms the failure is per-cluster (not
fleet-wide). Step 2 identifies the failure shape from the error
string. Step 3 inspects the provider-side state.

### 1. Confirm the failure is per-cluster, not fleet-wide

Per the contract in `reconciler.go`, a single cluster's vault failure
returns from the per-cluster path but does NOT abort the tick. Verify
by looking at the same tick's audit entries — other clusters should
have `action=cluster_secret_create` or `cluster_secret_skip` with
`result=success`.

```sh
# Replace <ts> with the timestamp of a known-failing tick.
SHARKO_NS=<sharko-ns>
TS_PREFIX="2026-06-01T12:"
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=10000 \
  | jq -c "select(.time | startswith(\"$TS_PREFIX\")) | select(.msg | test(\"clusterreconciler|cluster_secret\"; \"i\"))" \
  | jq -r '"\(.time) \(.level) \(.cluster // .resource) \(.msg)"'
```

You want to see a mix: most rows `INFO`, one row `ERROR` for the
affected cluster. If all rows are `ERROR`, jump to the fleet-wide
runbook above.

### 2. Identify the failure shape from the error string

The error wrapped in the audit entry's `error` field tells you which
provider step failed. Common shapes:

| Error substring | Provider | Root cause lane |
|---|---|---|
| `not found in AWS Secrets Manager` | AWS-SM | path mismatch — [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) |
| `presigning GetCallerIdentity` | AWS-SM (EKS) | IRSA / role chain — [`eks-token-generation-failed.md`](eks-token-generation-failed.md) |
| `AccessDenied` on `GetSecretValue` | AWS-SM | IAM policy missing `secretsmanager:GetSecretValue` on the cluster's secret ARN |
| `secret not found in namespace` | K8s Secrets | wrong namespace or wrong name — [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md) |
| `Forbidden` on `secrets/<path>` | Vault (future) | Vault policy missing read on the path |
| `connection refused` / `timeout` | Any | sub-case of [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md) — but only for THIS cluster's path, often a network policy carve-out |
| `cannot parse kubeconfig` / `invalid kubeconfig` | AWS-SM (raw kubeconfig) | upstream cluster operator stored a malformed kubeconfig at the cluster's secret path |

If the error shape matches a more-specific runbook above, jump
there for the mitigation. This runbook continues with the
generic per-cluster lane.

### 3. Inspect the provider-side state for this cluster

Once you know which provider is failing, probe the provider directly
from the Sharko pod (bypassing reconciler logic):

**For AWS-SM**:

```sh
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
CLUSTER=prod-eu
SECRET_PREFIX=clusters/   # match values.yaml secrets.prefix

kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- \
  aws secretsmanager describe-secret \
  --secret-id "${SECRET_PREFIX}${CLUSTER}"
```

Expected: a JSON describing the secret. If you get
`ResourceNotFoundException`, the secret was deleted or renamed
upstream. If you get `AccessDeniedException`, IRSA / IAM is the issue.

**For K8s Secrets**:

```sh
# Match values.yaml secrets.namespace (default: sharko-secrets)
SECRETS_NS=sharko-secrets

kubectl -n "$SECRETS_NS" get secret "$CLUSTER" \
  -o jsonpath='{.metadata.labels}' | jq
```

Expected: a JSON map including
`{"app.kubernetes.io/managed-by":"sharko"}` (the label selector the
K8s provider scopes to). If the secret is missing or unlabeled, the
secret was deleted upstream or a manual `kubectl edit` removed the
label.

**For the kubeconfig content itself (any provider)**:

The reconciler stops at the credential fetch step on failure; it
never gets to validate the kubeconfig. If the credential fetch
succeeds and the failure surfaces later (e.g. ArgoCD's cluster
controller reports 401), check the existing ArgoCD Secret:

```sh
kubectl -n argocd get secret "$CLUSTER" -o yaml \
  | yq '.data.config' -r | base64 -d \
  | jq '.bearerToken // .tlsClientConfig.caData' -r \
  | head -c 50
```

A bearer token that's `null` or a CA blob that decodes to an empty
string means the Secret was written incorrectly by an earlier
reconciler tick — see
[`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md).

---

## Mitigation (try in order)

1. **Verify the cluster's entry in `managed-clusters.yaml` is
   accurate.** A common silent failure is an operator renaming a
   cluster upstream (e.g. changing `clusterName` in AWS-SM from
   `prod-eu` to `prod-eu-v2`) without updating Sharko's
   `managed-clusters.yaml`. The reconciler still tries to read the
   old path and fails forever.

   ```sh
   # Read the cluster's entry from the git base branch:
   curl -sS -H "Authorization: token ${GITHUB_PAT}" \
     "https://api.github.com/repos/<owner>/<repo>/contents/configuration/managed-clusters.yaml?ref=main" \
     | jq -r .content | base64 -d | yq '.clusters[] | select(.name == "prod-eu")'
   ```

   Expected output includes either `secret_path: <override>` or the
   default (cluster name). Confirm the path matches the actual
   provider-side secret name from Diagnosis step 3.

   Fix: edit `managed-clusters.yaml` (open a PR) to align the cluster
   entry with the actual provider path. Next reconciler tick (within
   30s of merge) will retry successfully.

   Success indicator: audit entry transitions from
   `action=get_credentials, result=failure` to
   `action=cluster_secret_create, result=success` on the next tick.

2. **Repair the provider-side secret.** If the cluster's entry is
   correct but the provider-side secret is missing, malformed, or has
   the wrong label, fix it at the source. For AWS-SM, this often means
   re-uploading the kubeconfig blob or correcting the structured JSON
   shape. For K8s Secrets, this means re-creating the secret with
   the correct label and `kubeconfig` data key.

   **AWS-SM** (raw kubeconfig):

   ```sh
   kubectl --context <admin> -n kube-system get serviceaccount sharko-reader \
     -o yaml > /tmp/sa.yaml
   # Use the cloud-cred tooling that originally produced the secret
   # to generate a fresh kubeconfig, then:
   aws secretsmanager put-secret-value \
     --secret-id "clusters/prod-eu" \
     --secret-string "$(cat /tmp/new-kubeconfig.yaml)"
   ```

   **K8s Secrets**:

   ```sh
   kubectl --context <admin> -n kube-system create token sharko-reader \
     --duration=8760h > /tmp/token
   # Build a kubeconfig referencing the new token, then:
   kubectl -n "$SECRETS_NS" create secret generic prod-eu \
     --from-file=kubeconfig=/tmp/new-kubeconfig.yaml \
     --dry-run=client -o yaml \
     | kubectl apply -f -
   kubectl -n "$SECRETS_NS" label secret prod-eu \
     app.kubernetes.io/managed-by=sharko --overwrite
   ```

   Success indicator: same as step 1 — next reconciler tick reports
   success for this cluster.

3. **Trigger an immediate reconciler tick.** After repairing the
   underlying state, don't wait for the 30s safety-net tick. Use the
   reconcile trigger to force an immediate pass:

   ```sh
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     http://sharko/api/v1/secrets/reconcile
   ```

   (If the manual trigger endpoint is not exposed in your deployment,
   either restart the Sharko pod to force an immediate boot-time tick
   or wait the 30s.)

   Success indicator: a fresh audit entry for the affected cluster
   appears within 5 seconds with `result=success`.

4. **If the cluster should no longer be managed, remove it.** Sometimes
   the failure is the right answer — the cluster was decommissioned
   upstream but its `managed-clusters.yaml` entry was never removed.
   The reconciler will keep failing forever for an entry pointing at a
   non-existent secret.

   ```sh
   curl -sS -X DELETE -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     http://sharko/api/v1/clusters/prod-eu
   ```

   This opens a PR removing the cluster from `managed-clusters.yaml`
   and (after merge) deletes the corresponding ArgoCD Secret. Once the
   PR merges, the failing audit entry stops.

   Success indicator: audit log no longer surfaces the cluster's
   failure; `managed-clusters.yaml` no longer contains the entry.

---

## Root-cause patterns

Three common causes.

### Provider-side path / name drift

The single most common cause. An operator changed the cluster's name,
the secret's name, or the prefix at the provider, but didn't update
`managed-clusters.yaml`. The reconciler still reads the old path.

Diagnostic signature: Diagnosis step 3 returns
`ResourceNotFoundException` / `NotFound`. The cluster's
`managed-clusters.yaml` entry references a path that doesn't exist
at the provider.

Fix lane: Mitigation step 1 (update `managed-clusters.yaml`).

### Provider-side credential rotated

The cluster's credential (kubeconfig, bearer token, CA) was rotated
at the source — common after a managed K8s service rotates its
service-account tokens, an EKS cluster's CA rotates, or an admin
re-issued the in-cluster ServiceAccount the Sharko-reader RBAC binds
to. The credential is still present at the provider path, but it's
invalid.

Diagnostic signature: Diagnosis step 3 returns the secret successfully
with the same shape as before, but the embedded token / CA doesn't
authenticate against the upstream cluster's API server. The reconciler
fetches the credential, builds the Secret, and writes it; ArgoCD
then fails on its first API call to the cluster (401 from the cluster's
API server, not from Sharko).

In this case the reconciler's `get_credentials` step actually
**succeeds** (the path read worked), but the audit may show a
later-stage failure (e.g. `cluster_secret_create` with `result=success`
but ArgoCD subsequently shows `Unknown`). Distinguish by checking
the ArgoCD-side cluster connectivity:

```sh
kubectl -n argocd exec deploy/argocd-server -- \
  argocd cluster get prod-eu
```

Fix lane: Mitigation step 2 (re-upload the credential at the provider
with a freshly-issued token / CA).

### Stale ArgoCD Secret blocking convergence

The provider-side credential is correct, the reconciler's fetch
succeeds, but the ArgoCD Secret in the `argocd` namespace was
manually edited (or carries stale data from before the credential
rotated). The reconciler treats the labeled Secret as "mine,
already exists, skip" — never re-writing it.

Diagnostic signature: Diagnosis step 3 shows the provider has a
fresh credential; the audit shows
`action=cluster_secret_skip, result=success`; but the Secret in
`argocd` namespace has the old payload. ArgoCD itself surfaces the
401.

Fix lane: delete the labeled ArgoCD Secret manually (the reconciler
will recreate it from the provider's fresh credential on the next
tick). See
[`cluster-reconciler.md`](cluster-reconciler.md#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete)
for the recreate behavior.

```sh
kubectl -n argocd delete secret prod-eu
# Wait 30s for the next reconciler tick.
```

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — surface per-cluster reconciler status in the
  dashboard.** The reconciler already emits an audit entry per
  cluster per tick. Add a fleet-status panel that renders, per
  cluster, the most-recent `cluster_secret_reconcile` audit
  `result` and `error`. A red-row per stuck cluster catches Root
  cause pattern 1 (path drift) at first-fail, not at error-budget
  burn. Wiring this panel into the dashboard view is a V2-4.x
  follow-up.

- **Gating — pre-flight check on `add-cluster`.** When an operator
  runs `sharko add-cluster <name>`, Sharko could probe the
  provider for the credential before opening the PR. If the
  credential isn't present, fail the call early — don't add the
  cluster to `managed-clusters.yaml` and then have it stuck
  forever. The existing connection-test endpoint (`POST /clusters/{name}/test`)
  is the right shape; wiring it into the register flow as a
  pre-flight is a V2-4.x follow-up.

- **Scheduled work — periodic credential-freshness audit.** A
  daily cron that walks `managed-clusters.yaml`, probes each
  provider path, and reports per-cluster freshness catches
  rotation drift before the reconciler does. Output to a Slack
  notification or a Sharko audit entry of
  `event=credential_freshness_audit` keeps the discipline of
  rotation-aware operations visible.

---

## Related runbooks

- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — the fleet-wide variant. If multiple clusters fail at the same
  time with the same error shape, the provider itself is the issue.
- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) —
  specific AWS-SM "secret missing at any prefix" failure mode.
- [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
  — specific K8s Secrets "not found in namespace" failure mode.
- [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
  — when the provider succeeds but the EKS STS step fails (per-cluster
  IRSA / role-chain issue).
- [`cluster-reconciler.md`](cluster-reconciler.md) — reconciler
  reference: 30s tick cadence, error-isolation contract, recovery
  behavior.
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — when the ArgoCD-side Secret is malformed (Root cause pattern 3
  shape).
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern used throughout.

## Escalation

If the mitigations above do not resolve the failure within 4 hours,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The cluster name(s) affected
- The output of Diagnosis step 2 (the error string)
- The output of Diagnosis step 3 (provider-side state)
- The Sharko version (`sharko version` or the Helm chart version)
- 5 minutes of relevant logs filtered by `request_id` per the
  [correlation pattern](../developer-guide/logging.md#correlation-ids)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / audit shapes
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
- [x] (if applicable) Alert reference present (SlowBurn cross-link)
-->
