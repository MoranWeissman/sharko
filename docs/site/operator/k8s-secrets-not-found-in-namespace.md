# K8s Secrets Provider — Secret Not Found in Namespace

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The error
> message `"secret for cluster %q not found in namespace %q. Set
> --secret-path to specify the exact secret name"` is verified
> against `internal/providers/k8s_secrets.go:142`. The slog.Error at
> the same site emits with `step="fetch"` and an `error` value
> mentioning the namespace. The provider tries the exact cluster
> name as the Secret name (`fetchK8sSecret`) and if that fails,
> searches for similar names via `searchSimilarK8s` (line 149) to
> surface suggestions. Re-verify when the `GetCredentials` two-step
> lookup is refactored or `searchSimilarK8s` filters change.

A single cluster's K8s-Secrets-provider credential fetch failed
because no Secret with the cluster's name (and a `kubeconfig` data
key) exists in the configured Sharko-secrets namespace. The provider
tried the exact name first, then searched for similarly-named
Secrets to surface as suggestions; if there were similar names, the
error includes them.

The failure is per-cluster. Other clusters whose Secrets are properly
named and contain the `kubeconfig` key continue to reconcile
normally. The fix is one of: (a) create the missing Secret at the
expected name, (b) rename an existing similarly-named Secret to
match, or (c) override the cluster's `secret_path` (per K8s-Secrets
provider semantics, this maps to a different Secret name).

Operators commonly hit this in two scenarios. First: registering a
cluster before the kubeconfig Secret exists (the registration step
doesn't pre-flight the secret). Second: deploying Sharko into an
existing K8s-Secrets layout where Secrets follow a different naming
convention (e.g. `cluster-prod-eu-kubeconfig` instead of `prod-eu`).
This runbook walks operators through both lanes.

This is the K8s-Secrets sibling of the AWS-SM not-found runbook (see
[`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md)). The
RBAC story is different (namespace-scoped K8s RBAC instead of
account-wide IAM), but the operator-visible UX is similar.

---

## Symptoms

What an operator sees when this fires:

- **API: `POST /api/v1/clusters/{name}/test`** (or any
  cluster-credential-needing operation) returns 502 / 500 with the
  exact error from `internal/providers/k8s_secrets.go:142`:

  Without suggestions:
  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"secret for cluster \"prod-eu\" not found in namespace \"sharko-secrets\". Set --secret-path to specify the exact secret name"}
  ```

  With suggestions (when `searchSimilarK8s` finds substring matches):
  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"secret for cluster \"prod-eu\" not found in namespace \"sharko-secrets\". Similar secrets: cluster-prod-eu-kubeconfig, prod-eu-staging. Set --secret-path to specify the exact secret name"}
  ```

- **Sharko logs the failure at error level** (k8s_secrets.go:142):

  ```
  {"time":"...","level":"ERROR","msg":"[provider] GetCredentials failed (k8s)","request_id":"req-...","cluster":"prod-eu","step":"fetch","error":"secret not found in namespace sharko-secrets"}
  ```

  And if suggestions were found:

  ```
  {"time":"...","level":"INFO","msg":"[provider] found similar secrets","query":"prod-eu","found":2}
  ```

- **The cluster row** in the dashboard shows **Test failed** with the
  not-found error in the tooltip. Other clusters show **Healthy** —
  this is per-cluster.

- **No specific Prometheus alert fires** for a single missing Secret.
  Fleet-wide misconfiguration (every cluster's Secret naming is
  wrong) fans up into
  [`SharkoClusterRegistrationFastBurn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn).

- **If the error says "no 'kubeconfig' key"** (different shape, from
  `k8s_secrets.go:104`), the Secret exists at the expected name but
  doesn't have the required data key. That's a Secret-shape problem,
  not a not-found — see Mitigation step 3.

If the symptom is "every cluster fails" with this error, the issue
is likely a Helm misconfiguration (wrong `secrets.namespace` value)
or RBAC (the Sharko SA can't list Secrets in the configured
namespace). Single-cluster failure stays in this runbook.

If the error includes "is forbidden: User ... cannot list secrets,"
this is RBAC not not-found — see Mitigation step 4 / escalate to
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

---

## Diagnosis

Four checks. Step 1 confirms it's per-cluster. Step 2 captures the
configured namespace. Step 3 inspects the actual Secrets in that
namespace. Step 4 verifies the cluster name and the convention.

### 1. Confirm the failure is per-cluster

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | select(.test_error | test("not found in namespace"; "i")) | {name, test_status, test_error}'
```

One cluster failing = per-cluster mitigation. Many clusters failing
in the same namespace = Helm config issue (Mitigation step 5).

### 2. Read off the configured Sharko-secrets namespace

```sh
# From Helm values:
helm get values sharko -n <sharko-ns> | grep -A1 -E 'secrets:|namespace:'

# From the deployment env (resolved value, includes any defaults):
SHARKO_SECRETS_NS=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_SECRETS_NAMESPACE")].value}')
echo "Configured Sharko-secrets namespace: ${SHARKO_SECRETS_NS:-sharko (default)}"
```

The default per `internal/providers/k8s_secrets.go:33` is `sharko`.
If the Helm value sets a different namespace, that's the one the
provider looks in.

### 3. List actual Secrets in the configured namespace

The K8s-Secrets provider looks for Secrets matching the cluster name
(exact match) that have a `kubeconfig` data key. List candidates:

```sh
CLUSTER=<failing-cluster-name>
NS=${SHARKO_SECRETS_NS:-sharko}

# All Secrets in the namespace:
kubectl -n "$NS" get secrets \
  -l app.kubernetes.io/managed-by=sharko \
  -o json \
  | jq '.items[] | {name: .metadata.name, hasKubeconfig: (.data.kubeconfig != null)}'

# Specifically the cluster's expected Secret:
kubectl -n "$NS" get secret "$CLUSTER" -o json 2>&1 \
  | head -20
```

Three outcomes:

- **Secret exists at expected name AND has `kubeconfig` key** — the
  test was racing the create (Mitigation step 1).
- **Secret exists at expected name BUT NO `kubeconfig` key** — the
  Secret's data structure is wrong (Mitigation step 3).
- **No Secret at expected name; similar names exist** — naming
  convention mismatch (Mitigation step 2 or 5).

### 4. Confirm RBAC permits Sharko to read this namespace

If the Secret might exist but Sharko can't see it:

```sh
SA=$(kubectl -n <sharko-ns> get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}')

# Can Sharko list Secrets in the target namespace?
kubectl auth can-i list secrets -n "$NS" \
  --as=system:serviceaccount:<sharko-ns>:"$SA"

# Can Sharko get a specific Secret?
kubectl auth can-i get secret "$CLUSTER" -n "$NS" \
  --as=system:serviceaccount:<sharko-ns>:"$SA"
```

Both should return `yes`. If `no`, RBAC is the gap (Mitigation step
4); a Sharko Helm reinstall typically restores the ClusterRole +
RoleBinding.

---

## Mitigation (try in order)

1. **If Diagnosis step 3 shows the Secret IS at the expected name
   and has the `kubeconfig` key — the test was racing the create.**
   Sharko has no negative-cache; re-run the operation:

   ```sh
   curl -sS -X POST "http://sharko/api/v1/clusters/$CLUSTER/test" \
     -H "Authorization: Bearer ${SHARKO_TOKEN}"
   ```

   Success indicator: 200 with `{"reachable": true, "version":
   "v1.x.y"}`.

2. **If the Secret exists at a different name (Diagnosis step 3
   suggestion list), use `secret_path` to override.** This is a
   per-cluster fix that doesn't disturb other clusters:

   ```sh
   CLUSTER=<failing-cluster-name>
   ACTUAL_NAME=<from-similar-secrets-suggestion>
   curl -sS -X PATCH "http://sharko/api/v1/clusters/$CLUSTER" \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     -H "Content-Type: application/json" \
     -d "{\"secret_path\":\"$ACTUAL_NAME\"}"
   ```

   For the K8s-Secrets provider, `secret_path` is just the Secret
   name (not a slash-path; that's only for the AWS-SM provider).
   The provider's `fetchK8sSecret` looks for an exact match on this
   field.

   Success indicator: re-run cluster test, see 200.

3. **If the Secret exists at expected name but lacks the
   `kubeconfig` key, add it.** The K8s-Secrets provider requires
   the data key to be exactly `kubeconfig` (per
   `k8s_secrets.go:102-104`):

   ```sh
   # Extract current kubeconfig from your local context:
   KUBECONFIG_B64=$(kubectl --context "$CLUSTER" config view --raw \
     --minify --flatten | base64 -w0)

   kubectl -n "$NS" patch secret "$CLUSTER" \
     --type='json' \
     -p="[{\"op\":\"add\",\"path\":\"/data/kubeconfig\",\"value\":\"$KUBECONFIG_B64\"}]"
   ```

   Add the ownership label too (V125-1-8 convention):

   ```sh
   kubectl -n "$NS" label secret "$CLUSTER" \
     "app.kubernetes.io/managed-by=sharko" --overwrite
   ```

   Re-run the cluster test.

4. **If RBAC is the gap (Diagnosis step 4 returns "no"), re-apply
   the Helm chart to restore the ClusterRole + RoleBinding.**
   Sharko's chart ships a ClusterRole that grants
   `list/get/watch secrets` in the configured namespace; if it was
   removed (cleanup, audit) restoring is the cleanest fix:

   ```sh
   helm upgrade --reuse-values sharko sharko/sharko -n <sharko-ns>
   ```

   For a manual repair (when re-applying is not desirable):

   ```sh
   kubectl create clusterrole sharko-secrets-reader \
     --verb=get,list,watch \
     --resource=secrets

   kubectl create rolebinding sharko-secrets-reader \
     -n "$NS" \
     --clusterrole=sharko-secrets-reader \
     --serviceaccount=<sharko-ns>:"$SA"
   ```

   Verify with `kubectl auth can-i` (Diagnosis step 4) returns `yes`.

5. **If multiple clusters fail because the configured namespace is
   wrong (or the convention mismatch is fleet-wide), update the
   Helm value to match the existing layout.** This is the
   fleet-wide fix when an operator deploys Sharko into an existing
   K8s-Secrets layout:

   ```sh
   helm upgrade --reuse-values \
     --set "secrets.namespace=existing-kubeconfigs" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The deployment rolls out; the new namespace is in effect on the
   next `GetCredentials` per cluster. Re-test all affected clusters.

   If the convention difference is the Secret naming (e.g.
   `cluster-prod-eu-kubeconfig` instead of `prod-eu`), no Helm fix
   exists — the Sharko convention is "Secret name = cluster name."
   Either rename the Secrets to match (use `kubectl get secret -o
   yaml | sed | kubectl apply -f -`) or override each cluster's
   `secret_path` (Mitigation step 2).

---

## Root-cause patterns

### Cluster registered before the Secret was created

The most common cause in greenfield deployments. An operator runs
`sharko add-cluster prod-eu` before creating the
`sharko-secrets/prod-eu` Secret. The registration succeeds (it
doesn't pre-flight the secret); the first test fails. After the
Secret is created, the failure self-heals on the next test.

Diagnostic signature: Diagnosis step 3 shows the Secret now exists
at the expected name; the Secret's creation timestamp postdates
the failure-start time.

Fix is Mitigation step 1 — re-run.

### Convention mismatch — Sharko expects bare cluster name

A platform team adopted Sharko into a namespace where existing
Secrets follow a different convention
(`cluster-<name>-kubeconfig`, `<name>-kc`, etc.). The Sharko convention
is `Secret name = cluster name` exactly; there's no prefix/suffix
config knob for K8s-Secrets.

Diagnostic signature: Diagnosis step 3's suggestion list shows
similarly-named Secrets at a different convention; every cluster
fails the same way.

Fix is Mitigation step 2 (per-cluster override) or rename Secrets
to match Sharko's convention.

### Secret exists but `kubeconfig` data key is missing

The Secret was created with a different data key (e.g. `kc`,
`config`, `kubeconfig.yaml`). The provider's
`secret.Data["kubeconfig"]` lookup returns the absent-key error
(distinct from the not-found shape).

Diagnostic signature: Diagnosis step 3's specific-secret lookup
returns the Secret, but the error message says "has no 'kubeconfig'
key" instead of "not found in namespace".

Fix is Mitigation step 3 — patch the Secret's data with a properly
keyed entry.

### RBAC was tightened by a security review

The Sharko SA had `list/get secrets` in the target namespace;
a security audit narrowed it. Every `GetCredentials` call fails not
with "not found" but with a 403 from the kube-apiserver. The
provider wraps the 403 as a fetch error, which surfaces here
with a slightly different shape.

Diagnostic signature: Diagnosis step 4 returns `no`; the error
string contains `"is forbidden"` rather than `"not found"`.

Fix is Mitigation step 4 — restore RBAC.

### Wrong namespace in Helm values

A platform team deployed Sharko expecting Secrets in
`my-team/secrets` but the Helm value resolved to default `sharko`.
Every cluster fails because the lookup is happening in an empty
namespace.

Diagnostic signature: Diagnosis step 2 shows the resolved
namespace doesn't match what the operator expected; the actual
Secrets live in a different namespace.

Fix is Mitigation step 5 — set `secrets.namespace` to the correct
value.

---

## Prevention

- **Monitoring — per-cluster credential-fetch failure counter.**
  Same as the AWS-SM not-found runbook: a V2-3.x follow-up metric
  `sharko_provider_get_credentials_errors_total{cluster, provider,
  reason}` with reasons including `not_found`, `missing_data_key`,
  `rbac_denied` would let operators alert on patterns. Today, the
  only signal is the per-cluster `test_status` in
  `/api/v1/fleet/status`.

- **Gating — `sharko add-cluster` should pre-flight the secret.**
  Before committing the registration, call `provider.GetCredentials`
  to confirm the Secret exists with the right shape. Catches
  greenfield-race and convention-mismatch at registration time.

- **Documentation — naming convention in the install guide.**
  The install guide should explicitly call out the K8s-Secrets
  provider's convention: **Secret name = cluster name; data key =
  `kubeconfig`**. Many operators trip on the data-key requirement
  (they assume any key works) or the bare-name expectation (they
  assume a prefix is supported).

- **Documentation — sample Secret YAML.** Ship a copy-paste
  Secret YAML in the install guide so operators can create the
  expected shape without trial-and-error:

  ```yaml
  apiVersion: v1
  kind: Secret
  metadata:
    name: <cluster-name>
    namespace: <sharko-secrets-namespace>
    labels:
      app.kubernetes.io/managed-by: sharko
  type: Opaque
  data:
    kubeconfig: <base64-encoded-kubeconfig>
  ```

- **Scheduled work — quarterly Secret-shape audit.** A periodic job
  that calls `GetCredentials` for every managed cluster and reports
  any shape failure catches drift before the first user-visible
  failure (orphaned cluster references, Secrets accidentally
  modified, RBAC drift).

---

## Related runbooks

- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  sibling provider's not-found failure mode; identical operator UX,
  different backend.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — P0 escalation: every K8s-Secret read fails (RBAC tightened
  fleet-wide, namespace unreachable).
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — adjacent failure: Secret found, fetch succeeded, but parse
  failed.
- [`cluster-reconciler.md`](cluster-reconciler.md) — V125-1-8
  reconciler context for `app.kubernetes.io/managed-by` label
  ownership.
- [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  — fleet-wide registration alert.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-4 don't restore the cluster's credential fetch
AND the cluster is critical, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The cluster name and the configured namespace
- The output of Diagnosis steps 3 + 4 (Secret listing + RBAC check)
- Whether the issue is single-cluster or fleet-wide
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Most
not-found failures are operator-correctable; escalation is rare.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (4 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (5 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] Alert names referenced (FastBurn)
-->
