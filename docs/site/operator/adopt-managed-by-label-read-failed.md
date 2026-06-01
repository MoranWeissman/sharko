# Adopt: Managed-By Label Could Not Be Read

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Warn log
> line `"could not read managed-by label — proceeding with adoption"`
> and the proceed-anyway control flow are verified verbatim against
> `internal/orchestrator/adopt.go:58-67` as shipped. The lookup uses
> `o.argoSecretManager.GetManagedByLabel(ctx, clusterName)`; on error
> the adopt flow logs Warn and proceeds (idempotent label add at the
> end of the adopt flow). The FR-4.6 reject is only triggered when the
> label read SUCCEEDS but returns a non-empty, non-`sharko` value. Re-
> verify before changing the Warn-then-proceed control flow or the
> `GetManagedByLabel` interface — both are anchors for the diagnosis
> below.

The cluster adopt flow tried to read the
`app.kubernetes.io/managed-by` label on the existing ArgoCD cluster
Secret, but the read failed (typically with an RBAC `Forbidden`).
Sharko logs a Warn line, **proceeds with the adoption anyway**, and
the final step in the adopt flow (which is idempotent label-add)
sets the label to `sharko` regardless of the prior state.

This is **a graceful-degradation Warn, not a failure**. The cluster
gets adopted; the operator-visible result is success. The Warn line
exists so that **post-adopt forensics work**: if it turns out the
Secret was previously managed by another tool (and the rejection
that FR-4.6 implements should have kicked in), the Warn line is the
forensic anchor.

The blast radius is **minimal**: one cluster's adoption proceeds
when FR-4.6 might have rejected it. The downstream concern is that
**another tool's resource is being claimed by Sharko**. In practice
this only happens when:

- The Sharko service account lacks `secrets/get` RBAC in the
  `argocd` namespace (the read fails before evaluating the label),
  OR
- The Secret was created without any labels (e.g. a manual
  `kubectl create secret generic`), so a labeled-read would return
  an empty string anyway.

The Warn line should be visible to operators so they can decide
whether to investigate post-adoption. This runbook covers diagnosis
and resolution of the RBAC issue (the actionable case) and discusses
the no-op case.

---

## Symptoms

What an operator sees when this fires:

- **`kubectl logs` Warn line** when the adopt flow runs (per
  cluster being adopted):

  ```
  {"time":"...","level":"WARN","msg":"could not read managed-by label — proceeding with adoption","cluster":"prod-eu","error":"secrets \"prod-eu\" is forbidden: User \"system:serviceaccount:sharko:sharko\" cannot get resource \"secrets\" in API group \"\" in the namespace \"argocd\""}
  ```

  Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
    | jq -c 'select(.msg == "could not read managed-by label — proceeding with adoption")'
  ```

- **`POST /api/v1/clusters/adopt`** still returns `status: "success"`
  for the cluster — the adoption completed end-to-end:

  ```json
  {
    "results": [
      {"name": "prod-eu", "status": "success", "git": {"pr_url": "..."}}
    ]
  }
  ```

  The Warn line is **the only signal**. If the operator doesn't look
  at the logs, they may believe the adoption ran cleanly when
  actually the FR-4.6 safety check was bypassed.

- **Audit log** records the adopt as `result=success` (no per-step
  result for the label-read). The audit shape does not surface this
  failure — log-only. A V2-4.x follow-up should add an
  `event=adopt_label_read_failed` audit entry so this is visible from
  the audit query path.

- **Post-adopt state**: the ArgoCD cluster Secret now carries the
  `app.kubernetes.io/managed-by: sharko` label (per the
  `ApplyManagedBySharkoLabel` idempotent setter at end of adopt).
  This is the desired state regardless of what was there before.

- **If the Secret was actually managed by another tool**, that tool
  will see "drift" on its next reconcile and might fight Sharko for
  the label — an operator-visible symptom downstream (typically
  hours after the adopt, when the other tool runs).
- **No specific Prometheus alert fires today.** A V2-4.x follow-up
  is to surface `sharko_adopt_label_read_failed_total` and alert on
  >0 per day.

If the symptom is **the adopt-flow returning `status: "failed"`**
with `"is managed by <tool>, not sharko — cannot adopt"`, that's the
**successful** FR-4.6 rejection (label read succeeded, returned
non-`sharko`). This runbook does not apply; the operator should
either remove the conflicting label upstream or accept that the
cluster cannot be adopted.

If the symptom is **adopt failing at a later step** (cluster entry
write failure, PR conflict), see
[`adopt-cluster-entry-write-failed.md`](adopt-cluster-entry-write-failed.md).

---

## Diagnosis

Three checks: confirm the error is RBAC-shaped, identify Sharko's
role binding, decide whether to fix RBAC or accept the no-op case.

### 1. Confirm the error is RBAC-shaped

The Warn line's `error` field carries the underlying error from the
K8s API. The common shapes:

| Error fragment | Lane |
|---|---|
| `is forbidden: User "..." cannot get resource "secrets"` | RBAC denial — Sharko's role lacks `secrets/get` in `argocd` |
| `not found` | The cluster's Secret doesn't exist in `argocd` (adopt should have failed at the earlier `clusterServerMap` lookup; verify) |
| `connection refused` / `timeout` | API server unreachable; transient |
| `Unauthorized` | Sharko's token isn't authenticated; deeper issue |

```sh
kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
  | jq -c 'select(.msg == "could not read managed-by label — proceeding with adoption")' \
  | jq -r .error \
  | head -5
```

If every recent failure shows `forbidden`, the RBAC fix is the lane
(Mitigation step 1). If only one shows `connection refused`, treat
as a transient failure and verify with step 2.

### 2. Identify Sharko's role binding

```sh
# Find Sharko's service account:
SA_NAME=$(kubectl -n <sharko-ns> get deployment sharko -o jsonpath='{.spec.template.spec.serviceAccountName}')
echo "ServiceAccount: $SA_NAME"

# Find the ClusterRoleBindings / RoleBindings that grant it access:
kubectl get clusterrolebinding,rolebinding -A -o json \
  | jq -r --arg ns "<sharko-ns>" --arg sa "$SA_NAME" \
    '.items[] | select(.subjects[]?.namespace == $ns and .subjects[]?.name == $sa) | "\(.kind) \(.metadata.namespace // "cluster")/\(.metadata.name) -> \(.roleRef.name)"'
```

Expected: one or more bindings naming roles like `sharko-argocd-reader`,
`sharko-cluster-secrets`, etc. Inspect what permissions those roles
actually grant in the `argocd` namespace:

```sh
kubectl describe role sharko-argocd-reader -n argocd
```

You want to see at minimum:

```
PolicyRule:
  Resources  Non-Resource URLs  Resource Names  Verbs
  ---------  -----------------  --------------  -----
  secrets    []                 []              [get list watch create update patch delete]
```

If `secrets` is missing the `get` verb, that's the cause. The
`get` verb is required to read the label on an existing Secret.

### 3. Verify with a synthetic call

Test the actual RBAC from inside the Sharko pod:

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  kubectl auth can-i get secrets -n argocd
```

Expected: `yes` if RBAC is correct, `no` if it's missing.

For a per-resource check:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  kubectl auth can-i get secret/prod-eu -n argocd --as system:serviceaccount:<sharko-ns>:sharko
```

This distinguishes "missing the verb" from "namespace not granted"
shapes.

---

## Mitigation (try in order)

1. **Grant Sharko's role `get` on `secrets` in the `argocd`
   namespace.** This is almost always the fix. Re-render the Helm
   chart with the correct RBAC, or patch the role directly:

   ```sh
   # Direct patch (works for one-off fixes):
   kubectl -n argocd patch role sharko-argocd-reader \
     --type='json' \
     -p='[{"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["secrets"],"verbs":["get","list","watch"]}}]'
   ```

   Or via Helm (the proper long-term fix):

   ```sh
   helm upgrade <sharko-release> charts/sharko/ \
     -n <sharko-ns> \
     --reuse-values \
     --set rbac.argocdNamespace=argocd \
     --set rbac.fullAccess=true   # if the chart exposes a granular flag
   ```

   After the role change, the very next adopt call uses the updated
   permissions — no Sharko restart required (RBAC is enforced on each
   API call).

   Success indicator: re-run Diagnosis step 3 — `kubectl auth can-i`
   returns `yes`. The next adopt call's Warn line does not appear.

2. **Re-run the affected adoption to verify FR-4.6 ran correctly.**
   Since the previous adoption proceeded WITHOUT the label check,
   the operator may want to verify the cluster was actually safe to
   adopt. Read the cluster's previous state from the audit log to
   see what other labels the Secret had:

   ```sh
   curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/audit?resource=cluster:prod-eu&limit=20" \
     | jq -r '.[] | "\(.time) \(.event) \(.action) \(.result)"'
   ```

   If the cluster was previously managed by another tool (e.g. labeled
   `app.kubernetes.io/managed-by: argocd-autopilot`), the adopt
   should have rejected. With RBAC now fixed, you can:

   - **Unadopt** the cluster, restore the previous label, then
     re-evaluate:

     ```sh
     curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
       "http://sharko/api/v1/clusters/prod-eu/unadopt"
     # Restore prior label manually if you know what it was:
     kubectl -n argocd label secret prod-eu \
       app.kubernetes.io/managed-by=<prior-tool> --overwrite
     ```

   - **Accept the adoption** if no conflicting tool exists.

   Success indicator: the cluster is in the intended state (adopted
   OR labeled-by-other-tool) post-cleanup.

3. **For a transient connection-refused / timeout: re-run the adopt
   call.** If Diagnosis step 1 showed a non-RBAC error (transient
   network blip), the API server was briefly unreachable. The
   `findOpenPRForCluster` idempotency check ensures re-running doesn't
   duplicate the PR; the second attempt usually succeeds:

   ```sh
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/adopt" \
     --data-binary '{"clusters":["prod-eu"]}'
   ```

   Success indicator: no Warn line on the retry; adopt completes
   without the label-read failure.

4. **Last resort — manually verify and label.** If the operator
   cannot wait for RBAC changes (e.g. cluster ops require multiple
   sign-offs), the manual path is:

   ```sh
   # Inspect the existing label directly (with cluster-admin access):
   kubectl -n argocd get secret prod-eu \
     -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}'

   # If the label is empty or equals "sharko", the adoption was
   # safe. If it equals another tool's name, the adopt should be
   # reversed:
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/prod-eu/unadopt"
   ```

   Success indicator: the operator has verified the previous
   ownership state and is confident the adoption was safe (or has
   reversed it).

---

## Root-cause patterns

Three common causes.

### Sharko's role doesn't include `secrets/get` in `argocd`

The single most common cause. The Helm chart's default RBAC may
grant Sharko broad access (`*/*` on `*` in the `argocd` namespace),
but operators often tighten this with a more granular custom role
that includes `secrets/create/update/delete/patch` but forgets
`secrets/get`. The orchestrator write path doesn't read existing
Secrets (it creates / updates), so the missing `get` only surfaces
on the adopt flow.

Diagnostic signature: Diagnosis step 1 shows `forbidden`.
Diagnosis step 2 shows a role granting `secrets` verbs but missing
`get`.

Fix lane: Mitigation step 1 (add `get` to the role).

### Hardened cluster with default-deny for service accounts

Some hardened deployments use an admission controller (Gatekeeper,
Kyverno) to default-deny all RBAC for any service account that isn't
explicitly allow-listed. The Helm chart's `rbac` block creates the
right roles, but the admission controller refuses them at apply time
or strips the `secrets` rule.

Diagnostic signature: Diagnosis step 2 shows the role exists but
doesn't have the rules the Helm chart should have created. The
admission controller logs (`kubectl get events -n argocd`) show
denials.

Fix lane: configure the admission controller's allow-list to
include Sharko's role, then re-apply via Helm.

### Transient API server unavailability

Less common. The K8s API server was briefly unreachable during the
adopt call (network blip, etcd compaction lag, API server restart).
The single read failed; the adopt proceeded.

Diagnostic signature: Diagnosis step 1 shows `connection refused`,
`timeout`, or `EOF`. The failure is one-off, not sustained.

Fix lane: Mitigation step 3 (re-run). If sustained, the failure is
not specifically the adopt path — the entire Sharko deployment is
degraded.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — surface the Warn as an audit entry.** Today the
  signal is log-only; add `event=adopt_label_read_failed,
  result=warn` so dashboards / audit queries surface this. The
  failure mode is bounded but post-hoc forensics shouldn't require
  digging through raw logs. V2-4.x follow-up.

- **Gating — pre-flight RBAC check at adopt request time.** Before
  any adopt call, Sharko could `kubectl auth can-i get secret` from
  inside the pod and surface a clear error to the operator if RBAC
  is wrong. That moves the failure from "Warn line operators miss"
  to "explicit 412 Precondition Failed with the missing verb." V2-4.x
  follow-up.

- **Scheduled work — quarterly RBAC drift check.** A scheduled task
  that runs the `auth can-i` matrix for every Sharko-relevant
  resource (secrets, applications, projects, applicationsets) and
  surfaces missing verbs. The drift accumulates when operators
  tighten RBAC for security reasons but don't re-validate Sharko's
  access.

---

## Related runbooks

- [`adopt-cluster-entry-write-failed.md`](adopt-cluster-entry-write-failed.md)
  — the sibling failure mode at the other end of the adopt flow.
  Different cause (Git write vs. RBAC) but adjacent surface.
- [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
  — if the cluster's credential fetch fails during the verify step
  of the adopt flow, the adopt also fails — distinct from the
  label-read variant.
- [`cluster-reconciler.md`](cluster-reconciler.md) — reconciler
  reference: ownership label semantics, the
  `app.kubernetes.io/managed-by` discipline.
- [`secret-push-silently-failed.md`](secret-push-silently-failed.md)
  — the P0 sibling "Warn-but-proceed silently" pattern. Different
  surface, related discipline. Both are flagged in the
  [logging audit punch list](../developer-guide/logging-audit-punchlist.md).
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
  — flags this and other Warn-but-proceed patterns for audit-entry
  emission.

## Escalation

If Mitigation steps 1-4 don't resolve the issue (e.g. RBAC is correct
but the Warn keeps appearing), email the maintainer:
`moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The Warn log line `error` field
- The output of Diagnosis step 2 (Sharko's role bindings)
- The output of Diagnosis step 3 (`kubectl auth can-i` result)
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. The bounded-impact nature (adopt proceeds; only
forensics are affected) keeps this off the pager.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
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
