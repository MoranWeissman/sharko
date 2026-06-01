# Addon ArgoCD Application Stuck Degraded

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Sharko-side
> audit codes `addon_enabled_on_cluster` and `cluster_secret_create`
> success-shape are verified against the addon-cycle audit emitters in
> `internal/orchestrator/addon.go` and the audit conventions documented
> in `docs/site/developer-guide/logging.md`. The ArgoCD-side checks
> below use the standard ArgoCD CLI (`argocd app get`) and the
> ApplicationSet pattern described in the architecture overview at
> [`../architecture/repo-structure.md`](../architecture/repo-structure.md).
> Re-verify before changing the addon-cycle audit event names or the
> ApplicationSet template's per-cluster values-extraction shape — those
> are anchors for the diagnosis below.

A single addon's ArgoCD Application is sitting in `Degraded` (or
`Unknown` / `OutOfSync`) state on a specific cluster, even though
Sharko's end of the cycle reported success: the PR opening the addon
on the cluster was merged, the audit log shows
`event=addon_enabled_on_cluster` with `result=success`, and
(for addons with secret dependencies) the addon's K8s Secret was
pushed to the remote cluster successfully.

The failure is **downstream of Sharko**: ArgoCD applied the addon's
Helm chart against the cluster, and one or more resources came up
unhealthy. The chart's values were wrong, the namespace clashed with
another addon's resources, a required Secret was missing, RBAC denied
the Helm hook, the upstream container image is broken, the cluster
ran out of resource quota — anything in that lane.

The blast radius is **one addon on one cluster**. Other addons on the
same cluster are healthy; the same addon on other clusters is
healthy. Mitigation is fundamentally **inspect the Application in
ArgoCD directly** — Sharko's logs cannot diagnose a chart-render or
chart-apply failure that happened inside ArgoCD's controller.

---

## Symptoms

What an operator sees when this fires:

- **ArgoCD Application status** for the affected cluster + addon
  combination reports `Health: Degraded` (or `Health: Missing` /
  `Health: Suspended` / `Health: Unknown`). The Application name
  pattern is `<cluster>-<addon>` per the ApplicationSet template
  (e.g. `prod-eu-datadog`).
- **`argocd app get` output**:

  ```sh
  argocd app get prod-eu-datadog
  ```

  Returns something like:

  ```
  Name:               argocd/prod-eu-datadog
  Project:            default
  Server:             https://api.prod-eu.example.com
  Namespace:          datadog
  URL:                https://argocd.example.com/applications/prod-eu-datadog
  Repo:               https://github.com/<org>/<repo>
  Path:               apps/datadog
  SyncWindow:         Sync Allowed
  Sync Policy:        Automated (Prune)
  Sync Status:        OutOfSync from main (abc123)
  Health Status:      Degraded (1 of 2 components failed)
  ```

- **Sharko's audit log** shows the enable-on-cluster cycle completed
  successfully:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=addon_enabled_on_cluster&limit=20" \
    | jq -r '.[] | "\(.time) \(.resource) \(.result)"'
  ```

  Expected: a row matching the cluster + addon with `result=success`,
  timestamped before the operator noticed the Degraded state.

- **No Sharko-side error logs** for the cluster + addon combination
  after the cycle completion. Grep for the cluster name:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
    | jq -c "select(.cluster == \"prod-eu\" and .level == \"ERROR\")"
  ```

  Empty output = Sharko's view of the operation is "done, success." The
  failure is entirely downstream.

- **`kubectl get application` on the ArgoCD control plane** confirms
  the Application exists with the expected source path:

  ```sh
  kubectl -n argocd get application prod-eu-datadog -o yaml \
    | yq '.status.health.status, .status.sync.status'
  ```

- **No specific Prometheus alert fires for one addon-on-one-cluster
  degradation today.** Sustained per-addon failure across multiple
  clusters does fan into
  [`SharkoAddonCycleSlowBurn`](budget-burn-runbook.md#sharkoaddoncycleslowburn).
  A single Application stuck Degraded is detected by operators (via
  the Fleet Dashboard's per-cluster health column) or by ArgoCD's
  own alerting (out-of-scope for this runbook).

If the symptom is **every addon on a specific cluster** stuck
Degraded, this is **not** the runbook — the cluster itself is
broken (network, RBAC, or registration). See
[`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
or
[`cluster-reconciler.md`](cluster-reconciler.md). If the symptom is
**the same addon on every cluster** stuck Degraded, the chart itself
or the addon's catalog entry is broken — distinct failure mode (often
visible at the catalog-source level; see
[`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)).

---

## Diagnosis

Four checks, narrowing from "what is the Application's actual health"
to "what failed inside the chart."

### 1. Get the Application's structured health status

```sh
argocd app get prod-eu-datadog -o json \
  | jq '{name: .metadata.name, sync: .status.sync.status, health: .status.health.status, resources: .status.resources}'
```

Key fields:

- `sync.status` — `Synced` means ArgoCD applied what it saw in Git;
  `OutOfSync` means the chart rendered to something different from
  what's running on the cluster.
- `health.status` — `Healthy`, `Degraded`, `Suspended`,
  `Missing`, `Unknown`, `Progressing`.
- `resources` — per-resource breakdown. Find the offending resource
  here.

### 2. Identify the failing resource

```sh
argocd app get prod-eu-datadog -o json \
  | jq '.status.resources[] | select(.health.status != "Healthy" and .health.status != null) | {kind, name, status, health}'
```

Expected output is a list of resources whose health is not Healthy.
Common signatures:

- `Deployment` with `health.status = Degraded` and
  `health.message = "Deployment has 0 replicas available"` — pod
  scheduling failure (image pull, resource quota, node selector).
- `Pod` with `health.status = Degraded` and
  `health.message = "Back-off pulling image ..."` — image-pull failure
  (chart version pointing to a nonexistent tag).
- `Pod` with `health.status = Degraded` and
  `health.message = "CreateContainerConfigError: secret \"datadog-keys\" not found"`
  — addon's secret dependency was not pushed to the remote cluster (this
  is the [`secret-push-silently-failed.md`](secret-push-silently-failed.md)
  surface; cross-link).
- `Job` with `health.status = Degraded` and
  `health.message = "Job has reached the specified backoff limit"` —
  Helm hook failed (commonly a migration job that needs a missing
  Secret or ConfigMap).
- `RoleBinding` / `ClusterRoleBinding` with sync errors — RBAC for
  the addon's ServiceAccount conflicts with another addon (namespace
  clash).

### 3. Read the actual chart-render output

```sh
argocd app manifests prod-eu-datadog | head -100
```

Shows the post-render Kubernetes manifests that ArgoCD applied. Cross-
reference with the chart's `values.yaml` rendered using the
per-cluster values file:

```sh
# Fetch the per-cluster values file Sharko opened the PR for:
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/contents/configuration/addons-clusters-values/prod-eu.yaml?ref=main" \
  | jq -r .content | base64 -d
```

Look for the addon-specific section (e.g. `datadog:` block). Common
problem shapes:

- The `datadog.apiKey` is empty (the AddonSecretRef wasn't expanded;
  the addon will fail to authenticate).
- `datadog.namespace` clashes with another addon already deployed in
  the same namespace.
- A required-by-the-chart top-level key is missing entirely.

### 4. Inspect the pod / event log on the remote cluster

If the failing resource is a Pod, get the actual container event
trail:

```sh
# Get the addon's namespace from the chart's values.
ADDON_NS=datadog

# Use the ArgoCD CLI to proxy through to the remote cluster:
argocd app resources prod-eu-datadog \
  --resource :v1:Pod \
  | head -20

# Or if you have direct kubeconfig access to the remote cluster:
kubectl --kubeconfig <remote-kubeconfig> -n "$ADDON_NS" describe pod -l app.kubernetes.io/name=datadog | head -40
kubectl --kubeconfig <remote-kubeconfig> -n "$ADDON_NS" get events --sort-by='.lastTimestamp' | tail -20
```

The event log is usually the **single most useful diagnostic**: image
pull errors, FailedScheduling, ConfigMap-not-found, Secret-not-found,
PVC-pending — all show up here with the exact resource name and the
specific reason.

---

## Mitigation (try in order)

The first step is always to **read the Application's own error
output** before making changes. ArgoCD's `OperationState.Message`
contains the chart's apply error; that's the canonical "what
happened" surface.

1. **Read the Application's `OperationState` message.** ArgoCD records
   the last sync operation's outcome in
   `.status.operationState.message`. This is the chart-apply error,
   verbatim:

   ```sh
   argocd app get prod-eu-datadog -o json \
     | jq -r '.status.operationState.message'
   ```

   The message tells you whether the failure is a chart render error
   (Helm rendering failed), a kube-apply error (kubectl apply failed
   on a specific resource), or a sync-window block (ArgoCD's sync
   window is closed). Read it before guessing.

   Common messages and routing:

   - `error validating data: ValidationError(...)` — the chart
     rendered to invalid Kubernetes YAML. Fix the values file
     (Mitigation step 2).
   - `Error from server (Forbidden): ...` — RBAC denied on a
     resource. Often namespace clash. Fix the namespace in the
     values file or the chart-side RBAC.
   - `Error from server (AlreadyExists): ...` — another addon
     (or a non-Sharko-managed resource) already owns the resource.
     Choose: rename the addon's namespace, change `ownerReferences`,
     or remove the conflicting resource.
   - `connection refused` to the cluster — the cluster Secret is
     stale (token rotated). Cross-link to
     [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
     or
     [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md).

2. **Fix the per-cluster values file.** If Diagnosis step 3 surfaced
   a missing or wrong value, open a PR fixing the
   `configuration/addons-clusters-values/<cluster>.yaml` file. Then
   either auto-merge (default) or merge manually; ArgoCD picks the
   change up on the next sync.

   ```sh
   # Example: bump the datadog namespace to avoid a clash.
   cat > /tmp/values-patch.yaml <<EOF
   datadog:
     namespace: datadog-prod-eu
     apiKey: <ref-to-secret>
   EOF

   # Open the PR via the Sharko PATCH endpoint:
   curl -sS -X PATCH -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/prod-eu" \
     --data-binary @/tmp/values-patch.yaml \
     -H "Content-Type: application/yaml"
   ```

   Success indicator: after the PR merges and ArgoCD syncs, the
   Application's `health.status` transitions to `Healthy`.

3. **Force a sync from ArgoCD.** If the values file is already
   correct but ArgoCD's last sync was a stale render (e.g. cache
   issue, or you fixed the upstream chart and bumped the version),
   force a fresh sync:

   ```sh
   argocd app sync prod-eu-datadog --force
   ```

   Or via Sharko's `POST /api/v1/clusters/{name}/refresh` endpoint
   if you want the sync to flow through Sharko's audit path:

   ```sh
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/prod-eu/refresh"
   ```

   Success indicator: `argocd app get` shows `sync.status: Synced`
   and `health.status: Healthy` within ~60s.

4. **Roll back the addon to the previous working version.** If the
   degradation started immediately after an addon upgrade and the
   error is in the chart itself (Diagnosis step 3 shows a values
   shape that the previous version accepted but this one rejects),
   roll back via the addon-upgrade endpoint with the old version:

   ```sh
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/addons/datadog/upgrade" \
     --data-binary '{"version":"3.74.0", "cluster":"prod-eu"}'
   ```

   This opens a PR pinning the per-cluster override to the previous
   version. Other clusters running the new version are unaffected.

   Success indicator: after the PR merges, ArgoCD syncs to the
   previous version and `health.status` returns to `Healthy`.

5. **Last resort — disable the addon on this cluster.** If Mitigation
   steps 1-4 don't resolve the failure within ~30 minutes and the
   addon is blocking other operations on the cluster, disable it
   temporarily via `PATCH /api/v1/clusters/{name}`:

   ```sh
   curl -sS -X PATCH -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/clusters/prod-eu" \
     --data-binary '{"remove_addon":["datadog"]}'
   ```

   This opens a PR removing the addon's label from the cluster.
   ArgoCD's ApplicationSet deletes the Application; the broken state
   clears. Re-enable when the underlying issue is fixed.

   Success indicator: Application disappears from `argocd app list`;
   audit log records `event=addon_disabled_on_cluster, result=success`.

---

## Root-cause patterns

Four common causes.

### Wrong values in the per-cluster values file

The single most common cause. The per-cluster values file lacks a
required key, has a typo'd key, or carries a value the chart's
schema rejects. ArgoCD's sync fails with a validation or apply error
from kube-apiserver.

Diagnostic signature: Diagnosis step 1 shows `sync.status: OutOfSync`
with the failure message identifying a YAML / validation problem.
Diagnosis step 3 shows the missing or wrong key in the rendered
values file.

Fix lane: Mitigation step 2 (fix the values file via PR). The fix
is operator-side; Sharko's role is opening the PR, not authoring
the values.

### Addon's secret dependency was not pushed

The addon declares a secret in the catalog (`secrets:` block; see
[`../user-guide/addons.md`](../user-guide/addons.md))
and the secret-push step on cluster registration failed silently.
The chart deploys; the pod tries to mount the Secret; CreateContainer
fails with `secret \"datadog-keys\" not found`.

Diagnostic signature: Diagnosis step 4 (pod event log) shows
`Failed to mount volume datadog-keys` or
`CreateContainerConfigError: secret \"datadog-keys\" not found`.

Fix lane: this is a cross-link to
[`secret-push-silently-failed.md`](secret-push-silently-failed.md).
The fix is in that runbook (re-push the addon secret to the cluster),
not in this one.

### Namespace clash with another addon

Two addons configured for the same namespace claim conflicting
resources (e.g. both want to own the `default` ServiceAccount, both
want to install a `ClusterRoleBinding` with the same name). The
second one to sync fails with `Forbidden` or `AlreadyExists`.

Diagnostic signature: Diagnosis step 1's `OperationState.message`
contains `AlreadyExists` or `Forbidden` referencing a resource owned
by another addon. The two addons share `namespace` in the per-cluster
values file.

Fix lane: Mitigation step 2 (rename the namespace in the values file
for one of the addons; rare cases require fixing the chart's
hard-coded resource names).

### ArgoCD sync window or self-heal disabled

ArgoCD's Project sync window for the affected cluster is closed
(maintenance window, deployment freeze) or the Application's
`syncPolicy.automated` was manually unset.

Diagnostic signature: Diagnosis step 1 shows `sync.status: OutOfSync`
but `Sync Window: Sync Denied` or `Sync Policy: Manual` instead of
`Automated`. The Application has been OutOfSync for hours without
attempts.

Fix lane: re-enable automated sync (one-off ArgoCD CLI command) or
wait for the sync window to open. Not a Sharko issue — operator's
sync policy.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — alert on per-Application health degradation.**
  ArgoCD already exposes per-Application health metrics
  (`argocd_app_info{health_status="Degraded"}`). Operators should
  alert on Application-level degradation independently of Sharko —
  ArgoCD is the source of truth for "did the chart apply." Sharko's
  audit-log surface complements this but isn't sufficient.

- **Gating — schema-validate per-cluster values at PR time.** Sharko
  could template-render the addon's chart with the proposed values
  during PR review (Helm's `helm template`) and surface render
  errors as PR comments. That would catch Root cause pattern 1
  (wrong values) before the PR merges and ArgoCD attempts a sync.
  Wiring this into the PR-open flow is a V2-4.x follow-up.

- **Scheduled work — quarterly addon health audit.** Walk the Fleet
  Dashboard and flag any Application that's been Degraded for
  >24 hours. These accumulate and become operational tech debt —
  treating them as routine work (not just incidents) keeps the
  fleet honest. The audit lives next to the credential-freshness
  audit from
  [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md).

---

## Related runbooks

- [`secret-push-silently-failed.md`](secret-push-silently-failed.md)
  — when the addon's secret dependency wasn't pushed; very common
  cause of Degraded status (Root cause pattern 2).
- [`single-cluster-credential-fetch-failed.md`](single-cluster-credential-fetch-failed.md)
  — when all addons on the cluster are failing; the cluster's
  credential is wrong, not the addon.
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — when ArgoCD can't reach the cluster at all (one symptom of which
  is `Health: Unknown` rather than `Degraded`).
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md)
  — the P0 escalation: PR merged but ArgoCD never sees the change
  at all. Distinct from "ArgoCD sees the change and fails to apply
  it" which is this runbook.
- [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md)
  — when the PR didn't actually merge in the first place.
- [`budget-burn-runbook.md`](budget-burn-runbook.md) — when
  Application degradation is widespread enough to consume the
  addon-cycle error budget.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If the mitigations above do not resolve the failure within 4 hours,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The cluster + addon combination
- The output of Diagnosis step 1 (Application's sync + health
  status)
- The output of Diagnosis step 2 (failing resource list)
- The output of Mitigation step 1 (`OperationState.message`)
- The Sharko version and ArgoCD version

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA, not a paged response. For ArgoCD-internal issues
(chart render, kube-apply), the ArgoCD project is the upstream owner
— Sharko's escalation is for Sharko-side mis-routing only.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks (4)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (5 steps)
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve (mkdocs build verifies)
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference present (SlowBurn cross-link)
-->
