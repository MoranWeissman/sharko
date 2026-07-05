# Adopt: Cluster Entry Write to managed-clusters.yaml Failed

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Error log
> line `"failed to add cluster entry"` is verified verbatim against
> `internal/orchestrator/adopt.go:191-197` as shipped — `addEntryErr`
> from `gitops.AddClusterEntry(clusterAddonsData, ...)` is logged but
> the flow CONTINUES (the values file is committed without the
> managed-clusters.yaml entry being updated). This is by design for the
> "freshly-bootstrapped repo" carve-out (empty managed-clusters.yaml ->
> bootstrap to `clusters:\n`), but for non-bootstrap cases it produces
> a **partial-state outcome**: ArgoCD Secret is labeled (post-merge),
> the cluster's values file exists, but `managed-clusters.yaml` does
> NOT list the cluster. Re-verify before changing the error-isolation
> contract in adopt.go — the partial-state outcome is the symptom
> below.

The adopt flow tried to add a new cluster entry to
`configuration/managed-clusters.yaml` (the canonical "Sharko-managed
clusters" list) but the YAML manipulation failed. Sharko logs Error,
proceeds to open the PR **without** the managed-clusters.yaml update,
and the operator sees an adoption that looks successful — the values
file commits, the PR merges, the ArgoCD Secret gets labeled `sharko`.

The downstream consequence is **silent state inconsistency**:

1. The ArgoCD cluster Secret carries
   `app.kubernetes.io/managed-by: sharko` (set by the post-merge
   `argoSecretManager.SetAnnotation` step).
2. The cluster's values file exists at
   `configuration/addons-clusters-values/<cluster>.yaml`.
3. `managed-clusters.yaml` does NOT contain a row for this cluster.

The next reconciler tick reads `managed-clusters.yaml`, finds no
entry for `<cluster>`, but finds a labeled-as-sharko Secret in
ArgoCD with that name. **The reconciler then deletes the Secret**
(the two-direction policy: "in ArgoCD but not in git -> delete"
per `cluster-reconciler.md`).

So the adoption appears to succeed, then 30 seconds later the
cluster's ArgoCD Secret is gone and the operator is left wondering
what happened. The Error log line is the only forensic anchor.

This runbook covers diagnosis (catch the partial state before
the reconciler deletes the Secret) and repair (add the cluster
entry to `managed-clusters.yaml` so reconciliation converges).

---

## Symptoms

What an operator sees when this fires:

- **`kubectl logs` Error line** during the adopt flow:

  ```
  {"time":"...","level":"ERROR","msg":"failed to add cluster entry","cluster":"prod-eu","error":"<gitops error>"}
  ```

  Common error shapes:

  ```
  yaml: line 14: did not find expected key
  yaml: did not find expected node content
  cluster entry already exists: prod-eu
  ```

  Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
    | jq -c 'select(.msg == "failed to add cluster entry")'
  ```

- **`POST /api/v1/clusters/adopt`** returns `status: "success"` for
  the affected cluster:

  ```json
  {
    "results": [
      {
        "name": "prod-eu",
        "status": "success",
        "git": {"pr_url": "...", "merged": true}
      }
    ]
  }
  ```

  The Error log line is the **only signal of partial state**. The
  HTTP response is misleading.

- **30 seconds later, the cluster's ArgoCD Secret is deleted**:

  ```sh
  kubectl -n argocd get secret prod-eu
  # Error from server (NotFound): secrets "prod-eu" not found
  ```

  And the subsequent reconciler tick's audit log:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=cluster_secret_reconcile&resource=cluster:prod-eu&limit=20" \
    | jq -r '.[] | "\(.time) \(.action) \(.result) \(.detail)"'
  ```

  Expected: an entry showing `action=cluster_secret_delete` with
  `result=success` and `detail=in argocd but not in git`. That's the
  "orphan delete" path firing on the labeled-but-not-listed Secret.

- **PR shows the values file commit only** — there's no
  `managed-clusters.yaml` change in the diff:

  ```sh
  curl -sS -H "Authorization: token ${GITHUB_PAT}" \
    "https://api.github.com/repos/<org>/<repo>/pulls/<id>/files" \
    | jq -r '.[] | .filename'
  ```

  Expected after the failure: only
  `configuration/addons-clusters-values/prod-eu.yaml`. Expected on a
  healthy adopt: that file PLUS
  `configuration/managed-clusters.yaml`.

- **`managed-clusters.yaml`** does not contain the cluster:

  ```sh
  curl -sS -H "Authorization: token ${GITHUB_PAT}" \
    "https://api.github.com/repos/<org>/<repo>/contents/configuration/managed-clusters.yaml?ref=main" \
    | jq -r .content | base64 -d | yq '.clusters[] | select(.name == "prod-eu")'
  ```

  Empty output = the cluster is not in the canonical list.

- **No specific Prometheus alert fires today.** A V2-4.x follow-up
  is to surface `sharko_adopt_partial_state_total` and alert on >0.

If the symptom is **adopt failing with HTTP 502** during the PR
creation (e.g. `creating pull request: ...`), the failure is
upstream Git-side, not the YAML manipulation. See
[`git-provider-unreachable.md`](git-provider-unreachable.md) or
[`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md).

If the symptom is **the Warn-level label-read failure**, see
[`adopt-managed-by-label-read-failed.md`](adopt-managed-by-label-read-failed.md)
— the sibling adopt failure mode.

---

## Diagnosis

Three checks: detect the partial state, identify the
`AddClusterEntry` failure reason, decide whether to repair via PR or
back out the entire adoption.

### 1. Detect partial state on the cluster

The fastest detection is to compare three sources for the same
cluster:

```sh
CLUSTER=prod-eu

# 1. Does the ArgoCD Secret exist?
kubectl -n argocd get secret "$CLUSTER" \
  -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' || echo "no-secret"

# 2. Does the values file exist?
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/contents/configuration/addons-clusters-values/${CLUSTER}.yaml?ref=main" \
  | jq -r '.name // "no-values-file"'

# 3. Is the cluster in managed-clusters.yaml?
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/contents/configuration/managed-clusters.yaml?ref=main" \
  | jq -r .content | base64 -d \
  | yq ".clusters[] | select(.name == \"${CLUSTER}\") | .name"
```

The three outputs together tell you the state:

| Secret | Values file | managed-clusters.yaml | State |
|---|---|---|---|
| `sharko` | exists | exists | Healthy — adoption succeeded fully |
| `sharko` | exists | missing | **This runbook's failure mode** |
| `no-secret` | exists | missing | Reconciler already deleted the Secret |
| `no-secret` | exists | exists | Reconciler hasn't run yet; will recreate Secret on next tick |
| `no-secret` | missing | missing | Adopt failed earlier; safe to retry |

The "Secret labeled / values file exists / managed-clusters.yaml
missing" pattern is the smoking gun.

### 2. Identify the AddClusterEntry failure reason

The Error log line's `error` field tells you why the YAML
manipulation failed:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
  | jq -c "select(.msg == \"failed to add cluster entry\" and .cluster == \"$CLUSTER\")" \
  | jq -r .error
```

Common shapes:

- `yaml: line N: did not find expected key` — the existing
  `managed-clusters.yaml` has a syntax error (manual edit corrupted
  it). The parser bails before adding the new entry.
- `cluster entry already exists: <name>` — the cluster is already in
  `managed-clusters.yaml`. This is the **dry-run-twice or retry-
  after-partial-success** path. The previous adoption did add the
  entry; this attempt is idempotent-conflicting. Usually safe to
  re-run as a no-op.
- `unmarshal errors: ...` — incompatible schema (older
  `managed-clusters.yaml` shape vs. the V125-1-9 envelope).
- `failed to lookup clusters key in document` — the file exists but
  doesn't have the expected top-level structure.

### 3. Decide repair vs. roll-back

Two paths:

- **Repair**: open a follow-up PR adding the cluster to
  `managed-clusters.yaml`. The ArgoCD Secret was deleted by the
  reconciler 30s after the original adopt; the next tick after the
  repair PR merges will recreate it from the provider. This is
  usually the right path.
- **Roll-back**: unadopt the cluster entirely, fix the
  `managed-clusters.yaml` syntax error upstream, then re-adopt.
  Cleaner state but more steps.

Choose repair if the operator wants the cluster managed and the
underlying `managed-clusters.yaml` is otherwise healthy. Choose
roll-back if the underlying file is broken (syntax errors that
will affect future adoptions too).

---

## Mitigation (try in order)

1. **If the reconciler hasn't deleted the Secret yet, add the
   cluster entry manually via a fresh PR.** When you catch the
   partial state within ~30 seconds of the adopt completing, the
   ArgoCD Secret is still in place. Opening a quick PR adding the
   cluster to `managed-clusters.yaml` and merging it stops the
   reconciler from deleting the Secret.

   ```sh
   # Clone the repo locally:
   git clone https://github.com/<org>/<repo>.git
   cd <repo>

   # Edit managed-clusters.yaml. Append the cluster under clusters:
   cat >> configuration/managed-clusters.yaml <<EOF
     - name: ${CLUSTER}
       labels:
         monitoring: "true"   # match the addons enabled at adopt time
         logging: "true"
   EOF

   # Validate YAML before commit:
   sharko validate-config configuration/managed-clusters.yaml

   git checkout -b fix/adopt-${CLUSTER}-add-entry
   git commit -am "fix(adopt): add ${CLUSTER} to managed-clusters.yaml"
   git push origin fix/adopt-${CLUSTER}-add-entry
   gh pr create --title "fix(adopt): add ${CLUSTER}" --body "Recovery for partial-adopt state. See [adopt-cluster-entry-write-failed.md](https://github.com/MoranWeissman/sharko/blob/main/docs/site/operator/adopt-cluster-entry-write-failed.md)." --base main
   gh pr merge --squash --auto
   ```

   Success indicator: after the PR merges, the next reconciler tick
   (within 30s) emits `cluster_secret_skip` for this cluster (Secret
   exists, label OK, cluster listed). No further intervention needed.

2. **If the reconciler has already deleted the Secret, add the
   entry AND wait for the reconciler to recreate the Secret.** Same
   PR as step 1, but recovery is a two-step process:

   - PR merges -> reconciler reads `managed-clusters.yaml`, sees
     the new entry, fetches the credential from the provider, creates
     the ArgoCD Secret with the `sharko` label.
   - Total recovery time: <60 seconds from PR merge.

   ```sh
   # Verify the Secret recreated:
   sleep 30
   kubectl -n argocd get secret "$CLUSTER" \
     -o jsonpath='{.metadata.labels}'
   ```

   Success indicator: Secret exists in ArgoCD with the
   `app.kubernetes.io/managed-by: sharko` label.

3. **Fix the underlying YAML syntax error in managed-clusters.yaml.**
   If Diagnosis step 2 showed
   `yaml: line N: did not find expected key`, the file is corrupted
   and any future adopt call will hit the same error. Repair the
   syntax before re-adopting:

   ```sh
   # Pull the current managed-clusters.yaml content:
   curl -sS -H "Authorization: token ${GITHUB_PAT}" \
     "https://api.github.com/repos/<org>/<repo>/contents/configuration/managed-clusters.yaml?ref=main" \
     | jq -r .content | base64 -d > /tmp/managed-clusters.yaml

   # Inspect line N (from the error message):
   sed -n "${N}p" /tmp/managed-clusters.yaml

   # Fix the syntax (commonly: trailing comma, unquoted special char,
   # tab mixed with spaces). Then validate:
   sharko validate-config /tmp/managed-clusters.yaml
   ```

   Then open a PR to commit the fixed file.

   Success indicator: `sharko validate-config` reports the file is
   valid; subsequent adoption attempts no longer hit the
   `AddClusterEntry` failure.

4. **Last resort — unadopt and re-adopt.** If steps 1-3 are too
   involved for the operator's situation (e.g. they want a clean
   slate), unadopt the cluster, fix any underlying YAML issues,
   then re-adopt:

   ```sh
   # Unadopt removes the cluster from managed-clusters.yaml (if
   # present), deletes the values file, and clears the labeled
   # Secret. Idempotent:
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/${CLUSTER}/unadopt"

   # Fix managed-clusters.yaml underlying syntax if needed.

   # Re-adopt:
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/adopt" \
     --data-binary "{\"clusters\":[\"${CLUSTER}\"]}"
   ```

   Success indicator: re-adopt completes without the Error log line;
   `managed-clusters.yaml` contains the cluster; ArgoCD Secret is
   labeled.

---

## Root-cause patterns

Three common causes.

### Manual edit corrupted managed-clusters.yaml

The single most common cause. An operator manually edited
`managed-clusters.yaml` in the GitHub UI or via a side PR and the
edit introduced a YAML syntax error. The next Sharko adopt call hits
the parser failure during `AddClusterEntry`.

Diagnostic signature: Diagnosis step 2 shows
`yaml: line N: did not find expected key`. The git log shows a
recent non-Sharko commit touching the file.

Fix lane: Mitigation step 3 (fix the syntax) + step 1 (add the new
entry after the fix lands).

### Schema drift between V125-1-9 envelope and bare-YAML

The schema envelope (`apiVersion: sharko.dev/v1`) is the canonical
shape for `managed-clusters.yaml`. If the existing file is a legacy
bare-YAML shape, the AddClusterEntry path may fail to round-trip
cleanly.

Diagnostic signature: Diagnosis step 2 shows `unmarshal errors`. The
file lacks the envelope (`apiVersion: sharko.dev/v1`).

Fix lane: edit `managed-clusters.yaml` to wrap content in the
envelope shape and re-run `sharko validate-config configuration/`
to confirm the schema passes, then re-attempt the adopt.

### Idempotent re-adoption of an already-listed cluster

Less common, but seen during operator-side retry. The first adopt
succeeded fully (entry added to managed-clusters.yaml); the operator
re-ran the adopt thinking the first failed; the second attempt's
AddClusterEntry returns "already exists."

Diagnostic signature: Diagnosis step 2 shows
`cluster entry already exists: <name>`. The cluster IS in
managed-clusters.yaml from a prior successful adoption.

Fix lane: no action needed — the prior adoption was complete. The
Error log line is a benign "already done" signal. Consider it
diagnostic-only.

---

## Rollback plan

If Mitigation step 4 (unadopt and re-adopt) was a mistake (e.g. the
cluster was working fine and the unadopt deleted the Secret
unnecessarily), the recovery path is:

1. Re-run the adopt. It's idempotent — if the values file was
   restored by the unadopt's PR-revert and the Secret was deleted,
   the adopt re-creates everything from scratch.

2. Verify the three-state matrix from Diagnosis step 1 shows all
   three sources match.

Mitigation steps 1, 2, 3 are non-destructive (PR-based fixes).

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — surface the failure as an Error-level audit
  entry.** Today the failure is log-only; surfacing
  `event=adopt_managed_clusters_yaml_write_failed,
  result=partial` in the audit ring lets dashboards catch this
  the moment it happens, not 30 seconds later when the reconciler
  deletes the Secret. V2-4.x follow-up.

- **Gating — bail the adopt early on `AddClusterEntry` failure.**
  The current control flow logs Error then proceeds. The right
  behavior is to **fail the per-cluster adopt** with HTTP 5xx so
  the operator sees the failure as a failure. The reconciler's
  destructive cleanup that happens 30s later is too late to be
  user-actionable. Code fix in `adopt.go:196` — return `cr.Status =
  "failed"` instead of continuing. V2-4.x follow-up; this runbook
  is the operator-side recovery until that fix lands.

- **Scheduled work — quarterly managed-clusters.yaml schema audit.**
  Run `sharko validate-config configuration/managed-clusters.yaml`
  in CI on every PR (already in place per V125-1-9) and also
  schedule it as a periodic check that runs against the deployed
  main branch. Catches corruption from non-Sharko commits.

---

## Related runbooks

- [`adopt-managed-by-label-read-failed.md`](adopt-managed-by-label-read-failed.md)
  — the sibling adopt failure mode at the start of the flow.
- [`cluster-reconciler.md`](cluster-reconciler.md) — the two-direction
  policy that turns the partial state into Secret deletion 30s after
  the adopt.
- [`secret-push-silently-failed.md`](secret-push-silently-failed.md)
  — the P0 sibling "Warn-but-proceed silently" pattern in the
  orchestrator. Both flagged in the logging audit punch list.
- [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md)
  — if the adopt PR doesn't merge at all, this isn't the runbook.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
  — flags this Error-but-proceed pattern for proper fail-stop
  control flow.

## Escalation

If the mitigations above don't restore the cluster to a healthy
state, email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The Error log line `error` field
- The output of Diagnosis step 1 (three-state matrix)
- The output of Diagnosis step 2 (parsing error)
- The PR URL from the original adopt attempt
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the recovery PR (Mitigation step 1) takes
~5 minutes and the system is self-healing once the
`managed-clusters.yaml` is consistent, the urgency is bounded.

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
