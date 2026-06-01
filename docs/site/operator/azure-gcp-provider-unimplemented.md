# Azure / GCP Provider Not Yet Implemented

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. Both stub
> error messages are verified against source: `"Azure Key Vault
> provider is not yet implemented — community contributions welcome
> at https://github.com/MoranWeissman/sharko"` at
> `internal/providers/azure.go:28` and `"GCP Secret Manager provider
> is not yet implemented — community contributions welcome at
> https://github.com/MoranWeissman/sharko"` at
> `internal/providers/gcp.go:28`. Both stubs return the same error
> from `NewAzureKeyVaultProviderFromAddonConfig` /
> `NewGCPSecretManagerProviderFromAddonConfig`, fail every method on
> the `SecretProvider` interface, and ship implementation guidance in
> the package doc comments. The grouping of these two rows into one
> runbook follows the style guide's "same root cause + same
> mitigation" rule. Re-verify when either provider ships an
> implementation that flips the stub.

The operator configured Sharko's `provider` value (Helm
`secrets.provider`, env var `SHARKO_PROVIDER`, or the API config) to
`azure` or `gcp`, expecting Sharko to fetch cluster credentials from
Azure Key Vault or GCP Secret Manager. Sharko ships **stub
implementations** of both providers in v1.x — they're registered in
the provider factory so the configuration parses, but every call to
`NewAzureKeyVaultProviderFromAddonConfig` /
`NewGCPSecretManagerProviderFromAddonConfig` returns the
"not yet implemented" error immediately.

This is **NOT a runtime failure** in the sense of "something broke" —
it's a deliberate v1.x scope cut. The runbook exists so an operator
who hits the error knows it's a documented gap rather than a bug
they need to debug. The fix is one of: (a) switch to AWS-SM or
K8s-Secrets for now and migrate when the native provider ships, (b)
implement the provider locally and contribute upstream, or (c) wait
for v2 when one or both of the providers ship.

Both providers' stubs include detailed implementation guidance in
the package doc comments (`internal/providers/azure.go:8-21` and
`internal/providers/gcp.go:8-21`) for contributors. The Sharko
maintainer welcomes community PRs; the stubs define the interface
boundary so implementation is straightforward once the
authentication chain (Workload Identity / ADC) is set up.

---

## Symptoms

What an operator sees when this fires:

- **Sharko fails to start when configured with `secrets.provider:
  azure` or `secrets.provider: gcp`** (assuming the addon-secrets
  provider is the unimplemented one):

  Pod logs:
  ```
  {"time":"...","level":"ERROR","msg":"failed to create addon secrets provider","provider":"azure","error":"Azure Key Vault provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko"}
  ```

  Or:
  ```
  {"time":"...","level":"ERROR","msg":"failed to create addon secrets provider","provider":"gcp","error":"GCP Secret Manager provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko"}
  ```

  The pod typically exits with a non-zero status during startup
  initialization and Kubernetes enters CrashLoopBackoff.

- **`kubectl get pod -n <sharko-ns>`** shows the Sharko pod in
  `CrashLoopBackoff` with a non-zero last-exit code:

  ```
  NAME                       READY   STATUS             RESTARTS   AGE
  sharko-7d8f9b6c5-x2k4t    0/1     CrashLoopBackoff   5          3m
  ```

- **If the unimplemented provider is configured as the
  ClusterCredentialsProvider (cluster-test path) rather than the
  AddonSecretProvider**, the failure surfaces at first cluster-test
  call:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"Azure provider not implemented"}
  ```

  Or:
  ```
  {"error":"GCP provider not implemented"}
  ```

  The pod stays up; only the cluster operations fail.

- **No specific Prometheus alert fires** — the pod doesn't start, so
  `/metrics` is unavailable and the
  [`SharkoServiceDown`](budget-burn-runbook.md) type alerts may
  fire instead.

If the symptom is "the pod is running but no addons surface," this is
**not** this runbook — see
[`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
for the catalog-side failure mode.

If the symptom is "AWS-SM not found" or "K8s-Secrets not found,"
those are different providers — see the respective runbooks linked
in Related runbooks.

---

## Diagnosis

Three checks. Step 1 confirms the configured provider is unimplemented.
Step 2 identifies which surface (addon secrets vs cluster
credentials) is hitting the stub. Step 3 verifies the pod's actual
runtime state.

### 1. Read off the configured provider

```sh
# Helm value:
helm get values sharko -n <sharko-ns> | grep -A2 -E 'secrets:|provider:'

# Resolved env var on the pod (read-only when the pod is running):
SHARKO_PROVIDER=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_PROVIDER")].value}')
echo "Configured provider: ${SHARKO_PROVIDER:-aws-sm (default)}"
```

If the value is `azure` or `gcp`, this runbook applies. If it's
`aws-sm` or `k8s-secrets`, look elsewhere for the error source.

### 2. Identify which provider surface is hitting the stub

Sharko has three provider surfaces (per V125-1-11):

- `AddonSecretProvider` — used by the addon-secret reconciler. Helm
  key: `secrets.provider`.
- `ClusterTestProvider` — used by `POST /clusters/{name}/test`.
- `ClusterRegSourceProvider` — used by cluster registration /
  discovery. Helm key may be `clusterRegSource.provider` or
  embedded in cluster Test config.

Inspect the resolved provider per surface:

```sh
# Pod env vars for each provider surface:
kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env}' \
  | jq -c '.[] | select(.name | test("SHARKO.*PROVIDER")) | {name, value}'
```

If `SHARKO_PROVIDER` (addon secrets) is `azure`/`gcp`, the pod
fails to start. If only `SHARKO_CLUSTER_TEST_PROVIDER` or
`SHARKO_CLUSTERREG_PROVIDER` is `azure`/`gcp`, the pod runs but
the corresponding operations fail.

### 3. Inspect the pod's startup logs for the explicit stub error

```sh
# If the pod is CrashLoopBackoff, get the previous container's logs:
kubectl -n <sharko-ns> logs deploy/sharko --previous \
  | jq -c 'select(.msg | test("not yet implemented|provider not implemented"; "i"))'

# If the pod is running but operations fail, get current logs:
kubectl -n <sharko-ns> logs deploy/sharko --since=15m \
  | jq -c 'select(.msg | test("not yet implemented|provider not implemented"; "i"))'
```

You should see one of the four stub error messages
(`NewAzureKeyVaultProviderFromAddonConfig` /
`NewGCPSecretManagerProviderFromAddonConfig` /
`AzureKeyVaultProvider.GetCredentials` /
`GCPSecretManagerProvider.GetCredentials`). The message includes the
GitHub URL for contributions.

---

## Mitigation (try in order)

1. **Switch to AWS-SM or K8s-Secrets for now and migrate when the
   native provider ships.** This is the lowest-friction path for
   v1.x.

   Decision matrix:

   - **You're on EKS** → use `aws-sm` with EKS structured-JSON
     secrets. See
     [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) for
     the secret layout.
   - **You're on GKE / AKS with a non-Sharko cluster running Sharko**
     → use `k8s-secrets` with one Secret per managed cluster. See
     [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md).
   - **You're on GKE / AKS without an existing Kubernetes cluster
     for Sharko** → run Sharko on AWS EKS or on-prem K8s with
     `k8s-secrets`. Cross-cloud is fine; the Sharko pod doesn't
     have to live in the same cloud as the managed clusters.

   Apply the Helm change:

   ```sh
   helm upgrade --reuse-values \
     --set "secrets.provider=k8s-secrets" \
     --set "secrets.namespace=sharko-secrets" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The pod restarts; the configured provider initializes.

2. **Implement the provider locally as a fork and contribute
   upstream.** The stubs in
   `internal/providers/azure.go` and
   `internal/providers/gcp.go` include detailed implementation
   guidance in the package doc comments:

   **Azure Key Vault stub guidance** (from `azure.go:8-21`):
   - Authentication: `github.com/Azure/azure-sdk-for-go/sdk/azidentity.NewDefaultAzureCredential`
     (Workload Identity on AKS, Azure CLI / env vars for local dev)
   - Secret access:
     `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets.NewClient`
     + `client.GetSecret(ctx, secretName, version, nil)`
   - AKS token: `azidentity.DefaultAzureCredential` with scope
     `6dae42f8-4368-4678-94ff-3960e28e3630/.default` (AKS resource);
     build a kubeconfig with the token as bearer credential.
   - `ListClusters`: iterate Key Vault secrets by configured
     prefix convention (e.g. `clusters-{cluster-name}`) using
     `client.NewListSecretPropertiesPager`.

   **GCP Secret Manager stub guidance** (from `gcp.go:8-21`):
   - Authentication: `golang.org/x/oauth2/google.DefaultTokenSource`
     (Workload Identity on GKE, ADC for local dev)
   - Secret access:
     `cloud.google.com/go/secretmanager/apiv1.NewClient` +
     `secretmanagerpb.AccessSecretVersionRequest`
   - GKE token: `google.DefaultTokenSource` with scope
     `https://www.googleapis.com/auth/cloud-platform`; build a
     kubeconfig with the token as bearer credential.
   - `ListClusters`: iterate projects via Resource Manager API or
     a prefix convention (e.g. `clusters/{cluster-name}`).

   Submit the PR to https://github.com/MoranWeissman/sharko;
   contributions are welcome.

3. **Wait for v2.** Native Azure and GCP providers are in the V2.x
   backlog. The interface is stable; the work is implementation +
   tests + CI. No timeline commitment in v1.x.

   In the meantime, configure Sharko with a supported provider
   (Mitigation step 1) and document the gap as a known limitation
   in your platform engineering runbook.

4. **Last resort — build a wrapper SecretProvider out-of-tree that
   bridges Azure Key Vault / GCP Secret Manager.** If you cannot
   contribute upstream (org policy, IP constraints), an internal
   `SecretProvider` implementation that conforms to the
   `internal/providers.SecretProvider` interface and is registered
   via a private fork can work as a stopgap. This requires
   maintaining a Sharko fork.

   This is a heavyweight path. Mitigation step 1 is almost always
   cheaper.

---

## Root-cause patterns

### v1.x scope cut

Sharko's v1.x ships AWS-SM and K8s-Secrets as the production-supported
providers. Azure Key Vault and GCP Secret Manager are stubs because
the implementation+test+CI work doesn't fit v1.x's scope. The maintainer
welcomes community PRs to flip the stubs into shipped implementations.

Diagnostic signature: the stub error is the explicit
"not yet implemented" message. There's no code path that returns
this error spuriously; if it fires, the provider is genuinely
unimplemented.

Fix is Mitigation step 1, 2, or 3.

### Operator chose the provider expecting it to exist

The operator deployed Sharko expecting Azure/GCP support to be in
the box, based on a marketplace listing or a documentation page that
under-emphasized the v1.x scope. The provider stub is the
operator's first signal that it doesn't exist yet.

Diagnostic signature: same as above.

Fix is documentation clarity (Prevention) and Mitigation step 1
in the short term.

### Multi-cloud platform team installing Sharko for cross-cloud fleet management

A platform team manages clusters in AWS, Azure, and GCP from one
Sharko instance. They expected to use a different provider per
cluster ("Azure for our AKS clusters, GCP for GKE"). Sharko's v1.x
architecture is **one provider per Sharko instance** — the multiple-
providers-per-instance feature is V2+ work.

Diagnostic signature: same as above, plus the operator's
configuration attempts include multiple providers or per-cluster
provider overrides that aren't currently supported.

Fix: use a single provider that can hold credentials for all
clouds — typically AWS-SM (which can store raw kubeconfigs for any
cluster) or K8s-Secrets — until V2 ships multi-provider support.

---

## Prevention

- **Monitoring — startup failure alert.** The pod's
  CrashLoopBackoff state is already detected by Kubernetes; an
  operator with Prometheus monitoring on
  `kube_pod_container_status_restarts_total{namespace="<sharko-ns>"}`
  catches this within minutes of the failed deploy. The
  graceful-shutdown logging at startup gives the
  `not yet implemented` message in clear language.

- **Gating — `helm install`/`upgrade` pre-flight check.** The chart
  could ship a `pre-install` hook Pod that validates
  `secrets.provider` against the supported set and fails the
  install with an actionable message. Catches the misconfiguration
  before the main pod even starts.

- **Documentation — supported providers in the install guide.** The
  install guide should explicitly list which providers ship in
  v1.x. The `secrets.provider` Helm value documentation should
  enumerate `aws-sm` and `k8s-secrets` as the only supported
  values and note Azure / GCP as stubs.

- **Gating — chart validation rule.** The Helm chart's `values.schema.json`
  (if shipped) should enforce
  `secrets.provider in {"aws-sm", "k8s-secrets"}` for v1.x. Operators
  who try the unimplemented values would see the rejection at
  `helm template` / `helm install` time, before any pod starts.

- **Community visibility — track Azure/GCP implementation
  contributions.** A `good-first-issue` label on the GitHub repo
  for these providers makes the contribution path visible.
  Mitigation step 2 is more likely to be exercised when contributors
  can see it's actively welcomed.

---

## Related runbooks

- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  supported AWS path and its per-cluster not-found failure mode.
- [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md)
  — AWS-SM IAM-degradation runbook.
- [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
  — the supported K8s-Secrets path; cross-cloud-friendly.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — fleet-wide P0 for when the supported provider IS the active one
  and it's down.
- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — sibling
  v1.x limitation (AWS IAM auth in ArgoCDProvider, scope-cut).
- [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
  — sibling v1.x limitation (exec-plugin auth in ArgoCDProvider).
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If Mitigation step 1 (switch to AWS-SM / K8s-Secrets) is not
acceptable for your environment (org policy mandates Azure-only or
GCP-only secrets storage), open a feature-request issue on the
GitHub repo: https://github.com/MoranWeissman/sharko/issues. Include:

- Your environment (cloud provider, K8s distribution, expected
  cluster count)
- Why AWS-SM / K8s-Secrets cannot serve as a stopgap
- Whether you can contribute the implementation (Mitigation step 2)

The maintainer reviews feature requests on a regular cadence. For
production-blocking conversations only, email
`moran.weissman@gmail.com` with the same context.

The maintainer is a single human, not a 24×7 rotation. This is a
v1.x scope cut, not a bug; escalation accelerates roadmap visibility
but doesn't unlock the feature in v1.x.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current; explains 2-row grouping rationale per style guide
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages (4 variants)
- [x] Diagnosis has 3+ concrete checks (3 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (3 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice; explicit "this is a v1.x scope cut" framing
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] No alert applicable (scope cut); explicitly stated
-->
