# AWS IAM Cluster Authentication Test Unsupported

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD as part of
> V2-4.4 (existing-runbook style compliance refresh). The 503 error
> string is verified verbatim against the
> `internal/api/clusters_test.go` test handler path and the
> `ErrArgoCDProviderIAMUnsupported` sentinel returned by
> `internal/providers/argocd_provider.go`. The IRSA + EKS access-entry
> instructions were re-run against an EKS 1.30 cluster from a
> manually-deployed Sharko v1.25 pod. Re-verify before v2 ships — the
> v2 IAM-auth Test connection path will replace the 503 surface, and operators
> will move from this runbook to the in-app "Test succeeded" UX.

Operators registering EKS clusters that use AWS IAM authentication
(the `awsAuthConfig` shape inside the ArgoCD cluster Secret) hit a
hard `503 Service Unavailable` whenever they click **Test connection**
in the UI. The error is clear, the cluster is correctly registered
end-to-end, and ArgoCD itself can still deploy addons to the
cluster — the only gap is Sharko's own connectivity-verification
button. Severity is **P1**: operators repeatedly hit this when
on-boarding fleets of EKS clusters and the error wastes triage time
even though the underlying registration succeeded.

This page exists for three reasons: (1) the in-app error link points
here, so it must not 404; (2) operators on-boarding multiple EKS
clusters need to understand that this is a known v1.x limitation, not
a per-cluster misconfiguration; (3) the v2 fix path is documented so
operators can plan the upgrade. v2 ships IRSA-backed IAM token
minting for Test connection; until then, the workaround is to verify
connectivity manually outside Sharko.

This runbook is shorter than the 300-line floor because the entire
mitigation is "wait for v2 or verify manually outside the app." That
brevity is intentional and documented in the
[style guide](../developer-guide/runbook-style-guide.md#length).

---

## Symptoms

What an operator sees when this fires:

- UI: clicking **Test connection** on a Cluster detail page surfaces the
  banner verbatim:

  > "This cluster uses AWS IAM authentication. Configure AWS
  > credentials for the Sharko pod's role to enable Test connection."

- API: `POST /api/v1/clusters/{name}/test` returns
  `503 Service Unavailable` with body:

  ```json
  {"error":"iam_auth_unsupported_in_v1"}
  ```

- `kubectl logs -n sharko deploy/sharko` line at the failed test:

  ```
  {"time":"...","level":"WARN","msg":"cluster test rejected: provider returned ErrArgoCDProviderIAMUnsupported","request_id":"req-...","cluster":"<name>"}
  ```

- No Sharko alert fires — `Test connection` is a synchronous read; the
  V2-3 burn-rate alerts do not cover it because there is no error
  budget defined for cluster-test calls. The failure is purely
  user-visible.
- ArgoCD itself reports the cluster as **online and healthy** on the
  `argocd cluster list` output and the ArgoCD UI. ApplicationSets
  targeting the cluster sync normally; addons deploy. The only thing
  broken is Sharko's verification button.

If the error message differs (`exec-plugin auth is not supported`,
`no credentials provider configured`, `cluster secret malformed`),
this is **not** the right runbook. See
[`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md),
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md),
or
[`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md).

---

## Diagnosis

Where to look to confirm "this is the IAM-auth limitation" vs. "this
is a real connectivity problem masquerading as the same symptom."
Three checks, in order.

### 1. Confirm the cluster Secret uses `awsAuthConfig`

```sh
kubectl -n argocd get secret cluster-<cluster-name> -o json \
  | jq -r '.data.config | @base64d' \
  | jq '{ awsAuthConfig, bearerToken: (.bearerToken // null | tostring | .[0:10]), execProviderConfig }'
```

Expected on this failure path: `awsAuthConfig` is non-null,
`bearerToken` is null, `execProviderConfig` is null. Example:

```json
{
  "awsAuthConfig": {
    "clusterName": "prod-eks-1",
    "roleARN": "arn:aws:iam::111122223333:role/argocd-cluster-role"
  },
  "bearerToken": null,
  "execProviderConfig": null
}
```

If `bearerToken` is set, your cluster is **not** using IAM auth
(despite being on EKS) — the Test connection failure has a different cause.

### 2. Confirm ArgoCD itself can use the cluster

```sh
kubectl -n argocd exec deploy/argocd-application-controller -- \
  argocd cluster list --insecure --server localhost:8080 \
  | grep <cluster-name>
```

Expected: cluster listed with status `Successful`. If ArgoCD itself
cannot use the cluster, the failure is upstream of Sharko — fix
that first (out of scope for this runbook).

### 3. Confirm the Sharko version is v1.x

```sh
kubectl -n sharko exec deploy/sharko -- sharko version
```

Expected on this failure path: any v1.x version (v1.20 through
v1.26). v2.0.0 and later ship the IAM-auth Test connection path; if you are on
v2.0.0+ and still seeing this, the issue is misconfigured IRSA, not
the unsupported-in-v1 limitation — see Prevention below for the
v2 IRSA setup.

---

## Mitigation (try in order)

The end-to-end Test connection path requires changes to Sharko itself; v1.x
operators have three lanes depending on how much verification they
need today.

### 1. Verify connectivity manually outside Sharko (recommended)

The fastest "did my cluster actually register?" check that does not
depend on the Sharko Test connection button:

```sh
# Sanity-check ArgoCD's view (succeeds even on this failure path)
kubectl -n argocd get application -o wide \
  | grep <cluster-name>

# Verify the Secret Sharko wrote
kubectl -n argocd get secret cluster-<cluster-name> -o yaml \
  | grep -E 'awsAuthConfig|server|managed-by'

# Probe the cluster's API directly using your local kubeconfig
kubectl --context <eks-context> get nodes
```

Expected: ArgoCD lists at least one Application targeting the
cluster as Healthy/Synced; the cluster Secret carries the
`app.kubernetes.io/managed-by: sharko` label (see
[`cluster-reconciler.md`](cluster-reconciler.md)); and your local
kubeconfig can reach the cluster API. Three greens = the
registration succeeded and the Test connection button's 503 is purely
cosmetic. Stop here.

### 2. Add the cluster to a pre-merge smoke ApplicationSet

If you need automated "is this cluster healthy?" checks across the
fleet (because your CI/CD pipeline currently leans on the Sharko
Test connection button), deploy a tiny smoke ApplicationSet that targets every
new cluster with a lightweight workload (e.g. `podinfo` in a smoke
namespace). When the smoke Application syncs healthy on the new
cluster, you have proof the cluster works end-to-end. This is more
robust than the Sharko Test connection button anyway — it tests the full
deploy path, not just the API reachability.

This is a fleet-management technique, not a Sharko fix; it is
documented here because operators on-boarding 10+ EKS clusters at a
time will hit this pain hardest.

### 3. Plan the v2 upgrade (real fix)

The IAM-auth Test connection path ships in **v2.0.0**. Three pieces have to be
present in the v2 deployment:

- **IRSA on the Sharko pod's ServiceAccount.** Annotate the
  `sharko` ServiceAccount in your `sharko` namespace with
  `eks.amazonaws.com/role-arn: <arn-of-the-sharko-pod-role>`.
- **`eks:GetToken` permission on that role**, plus `sts:AssumeRole`
  on the cross-account roles the cluster Secrets reference. Per
  AWS best practice, scope the trust relationships down to your
  EKS cluster's OIDC provider; do not use wildcard trust.
- **`sharko` v2.0.0 or later**, which calls
  `aws-iam-authenticator` (or the AWS SDK's EKS token minter)
  before the Test connection API call.

Once all three are in place, **Test connection** works for IAM-auth
EKS clusters identically to bearer-token clusters. The v2.0.0
production release shipped this path; see the architecture
roadmap and the [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
runbook for IRSA misconfiguration in v2.0.0+.

### 4. Last resort — re-register the cluster as bearer-token

**Not recommended.** EKS clusters can be added to ArgoCD using a
static bearer token (a long-lived ServiceAccount token), bypassing
IAM auth entirely. The Test connection button works because the cluster Secret
shape is `bearerToken`, not `awsAuthConfig`. But long-lived
ServiceAccount tokens are a security regression versus IAM auth —
they do not rotate, they survive role revocation, and they cannot
be scoped down by IAM policy. Use this lane only if your security
posture explicitly allows it and only as a stop-gap until v2 ships.

If you go this route: create a ServiceAccount + ClusterRoleBinding
in the target cluster, mint a non-expiring Secret-token via
`kubectl create token --duration=87600h` (10 years), and
re-register via `sharko add-cluster --bearer-token <token>`. The
bearer-token registration path is the historical default and is
fully tested.

---

## Root-cause patterns

### Sharko v1.x does not ship the IAM token-minting code path

The Test endpoint flow inside `internal/providers/argocd_provider.go`
inspects the cluster Secret's `config` JSON. When the config shape is
`awsAuthConfig`, the provider returns
`ErrArgoCDProviderIAMUnsupported` because the v1.x code path cannot
mint an EKS token without an IRSA-backed IAM role on the Sharko pod
plus the `aws-iam-authenticator` invocation logic. Both pieces ship
in v2.0.0. The error sentinel translates to the 503 + UI banner the
operator sees. This is intentional, documented behaviour — not a bug
or a regression. The handler refuses fast rather than emitting a
misleading partial result.

### Operator confused IAM-auth with exec-plugin auth

Several EKS auth flavors exist: pure `awsAuthConfig` (this runbook),
`execProviderConfig` calling `aws eks get-token` from a sidecar
(covered by
[`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)),
and the `bearerToken` shape (works without limitation). Operators
sometimes register a cluster expecting one shape and end up with
another; the cluster Secret's `config` JSON is the source of truth.
The diagnosis Step 1 above distinguishes the lanes.

### Sharko pod missing AWS credentials entirely

In a fully air-gapped EKS environment, the Sharko pod may have no
AWS credentials at all — no IRSA annotation, no `AWS_*` env vars, no
mounted kubeconfig. In v1.x this is invisible because the IAM-auth
path is unsupported anyway; the operator only sees this failure
mode. In v2.0.0, the same misconfiguration surfaces as
`eks-token-generation-failed.md` (P1) instead. Same root cause,
different surface across versions.

---

## Prevention

How to make this failure mode less likely going forward.

- **Run v2.0.0 or later** — v2 ships the IAM-auth Test connection path with
  IRSA-backed token minting. Operators still on a pre-v2 install
  should upgrade.
- **Pre-stage IRSA on upgrade.** Operators upgrading to v2 can
  pre-create the IRSA role and IAM policy before swapping the image.
  After the v2 image rolls, annotate the `sharko` ServiceAccount
  with the IRSA role-ARN, restart the Sharko pod, and the Test connection path
  immediately works for every previously-registered IAM-auth cluster
  without re-registration.

---

## Related runbooks

- [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
  — adjacent EKS auth limitation; same v1.x scope, different cluster
  Secret config shape (`execProviderConfig`).
- [`cluster-connectivity-model.md`](cluster-connectivity-model.md)
  — reference for which kubeconfig auth shapes Sharko handles today
  and what v2 adds.
- [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
  — the v2.0.0+ equivalent failure when IRSA is misconfigured.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If the symptoms above match but the limitation impact is blocking
fleet on-boarding and the v2 upgrade is not available, email the
maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The Sharko version (`sharko version`)
- A diff of `kubectl get secret cluster-<name> -n argocd -o yaml`
  showing the `awsAuthConfig` shape (redact the role ARN if your
  policy requires; the failure is not role-specific)
- The fleet on-boarding scope (number of clusters, target Sharko
  Test connection rollout date)

The maintainer can clarify the v2 roadmap timing and may be able to
back-port the IRSA wiring as a maintenance patch if there is a
production-blocking dependency. Expect a business-day SLA, not a
paged response. The maintainer is a single human, not a 24x7
rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / API responses
- [x] Diagnosis has 3 concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 4 steps in priority order
- [x] Root-cause patterns: 3 named causes
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length below 300-line floor — page explicitly justifies brevity per the style guide carve-out (entire mitigation is "wait for v2 or verify manually")
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name from prometheusrules.yaml referenced — N/A (no Sharko alert)
-->
