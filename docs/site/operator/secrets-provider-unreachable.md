# Secrets Provider Unreachable

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The provider
> interface (`ClusterCredentialsProvider`) and the AWS / K8s-Secrets /
> stub provider implementations live in `internal/providers/`. The
> EKS-STS token mint failure path at `internal/providers/aws_auth.go`
> and the AWS-SM secret-not-found path at
> `internal/providers/aws_sm.go:150` are verified against the source as
> shipped. Per-cluster (P1) failure modes are tracked in
> [`failure-mode-index.md`](failure-mode-index.md); this P0 runbook
> covers the **fleet-wide** failure where the entire provider is
> unreachable. Re-verify when provider constructors or the
> `health.Check` interface change.

The active secrets provider — AWS Secrets Manager, Kubernetes Secrets,
or a future Vault backend — is completely unreachable. Every cluster
registration that needs cluster credentials fails; every reconciler tick
that needs to mint a fresh kubeconfig fails; every addon-secret refresh
fails. The fleet is in a state where no new cluster operations can
proceed and ongoing ones stall. Page on-call.

This is distinct from **per-cluster** secret failures (one cluster's
vault path moved, one cluster's RBAC is broken) — those are P1 GAPs
tracked in
[`failure-mode-index.md`](failure-mode-index.md) (PR 2b scope). This
runbook is for the case where **every** provider call fails — typically
because the provider itself is down (AWS SM regional outage), the
provider's auth is broken (IRSA misconfigured, IAM role deleted), or
the network path is blocked (NetworkPolicy, VPC endpoint).

The runbook handles all three providers in a single page because the
diagnosis and mitigation patterns are structurally identical: probe the
upstream, probe the auth, probe the network. The provider-specific
details differ; the runbook calls them out per section.

---

## Symptoms

What an operator sees when this fires:

- **Every `POST /api/v1/clusters` returns 502** because cluster
  registration cannot fetch credentials. The response body cites the
  provider:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"failed to fetch cluster credentials: <provider-specific error>"}
  ```

- **Health probe response surfaces the failure**:

  ```sh
  curl -sS http://sharko/api/v1/health | jq '.providers'
  ```

  Expected on healthy: `{"aws-sm": {"reachable": true, ...}}` or
  equivalent per provider. Bypass signal: `reachable: false` with a
  specific `error` value (e.g. `"InvalidUserID.NotFound"`,
  `"connection timeout"`, `"403 Forbidden"`).

- **Sharko logs show a burst of provider-fetch failures**, one per
  fetch attempt:

  ```
  {"time":"...","level":"ERROR","msg":"secrets provider fetch failed","request_id":"req-...","provider":"aws-sm","error":"..."}
  ```

  For AWS-SM specifically:
  ```
  {"time":"...","level":"ERROR","msg":"GetSecretValue failed","cluster":"...","error":"..."}
  ```

  For EKS STS (AWS auth chain):
  ```
  {"time":"...","level":"ERROR","msg":"[auth] EKS token generation failed","cluster":"...","error":"..."}
  ```

- **Reconciler tick failures** — the reconciler attempts to mint
  per-cluster kubeconfigs for state validation and the mint fails:

  ```
  {"time":"...","level":"WARN","msg":"reconcile skipped: provider unreachable","request_id":"recon-...","provider":"aws-sm"}
  ```

- **Alerts that fire** when this is sustained:
  - `SharkoClusterRegistrationFastBurn` — registration depends on
    provider; sustained failure pages.
  - `SharkoAddonCycleFastBurn` — addon-secret refresh depends on
    provider.

If the symptom is "one cluster's credential fetch fails," this is the
**per-cluster** failure mode (P1, runbook in PR 2b). This runbook
applies when **every** call to the provider fails.

---

## Diagnosis

Four checks, in order. Each narrows whether the failure is upstream
(provider outage), auth (IRSA / IAM / RBAC), or network.

### 1. Which provider is active?

Read Sharko's config:

```sh
curl -sS http://sharko/api/v1/config \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.providers'
```

Expected: a list with the active provider. Three shipped providers
today:

- `aws-sm` — AWS Secrets Manager
- `k8s-secrets` — Kubernetes Secrets in a configured namespace
- (Future: `vault` for HashiCorp Vault — stub until v2.x)

The diagnosis path differs per provider; jump to the relevant
sub-section in step 2.

### 2a. AWS-SM provider diagnosis

Verify, in order:

**Is AWS Secrets Manager itself up?**

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
AWS_REGION=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AWS_REGION")].value}')

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws --region "$AWS_REGION" secretsmanager list-secrets --max-results 1 \
  --query 'SecretList[0].Name' --output text
```

Expected: a secret name (or `None` if there are no secrets — also OK,
means the API responded). Failure signals:

- `Unable to locate credentials` — IRSA / instance profile isn't
  resolving. See "Is IRSA correctly set up?" below.
- `AccessDenied` — IAM role lacks `secretsmanager:ListSecrets`. The
  IAM policy is the gate.
- `ConnectTimeoutError` or `EndpointConnectionError` — VPC endpoint
  is broken, NetworkPolicy is blocking egress, or AWS-SM regional
  service is degraded.
- `RequestLimitExceeded` — throttling. Sharko is calling SM too
  aggressively; this is rate-limit-driven failure.

**Is IRSA correctly set up?**

```sh
# What service account is Sharko using?
kubectl -n <sharko-ns> get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}'

# Is the SA annotated with the IAM role ARN?
SA=$(kubectl -n <sharko-ns> get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}')
kubectl -n <sharko-ns> get sa "$SA" \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'
```

Expected: ARN like `arn:aws:iam::<account-id>:role/SharkoIRSARole`.

**Is the IAM role assumable?**

From the AWS account, verify the role's trust policy permits Sharko's
service account. If you have AWS CLI access:

```sh
ROLE_ARN=$(kubectl -n <sharko-ns> get sa "$SA" \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}')
aws iam get-role --role-name "$(basename $ROLE_ARN)" \
  --query 'Role.AssumeRolePolicyDocument'
```

The trust policy should include a statement allowing the cluster's
OIDC provider to assume the role for Sharko's SA. Mismatched
OIDC provider URLs (typo'd account-id, wrong region) are common.

### 2b. K8s-Secrets provider diagnosis

Verify, in order:

**Can Sharko read Secrets in the configured namespace?**

```sh
NS=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_SECRETS_NAMESPACE")].value}')
SA=$(kubectl -n <sharko-ns> get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}')

kubectl auth can-i list secrets -n "$NS" \
  --as=system:serviceaccount:<sharko-ns>:"$SA"
# Expected: yes

kubectl auth can-i get secrets -n "$NS" \
  --as=system:serviceaccount:<sharko-ns>:"$SA"
# Expected: yes
```

If `no`, the ClusterRole / RoleBinding for the Sharko SA was deleted or
narrowed. Re-apply the Helm chart to restore.

**Are the expected Secrets present?**

```sh
# Sharko expects one Secret per cluster, with label
# app.kubernetes.io/managed-by=sharko (or similar by config).
kubectl -n "$NS" get secret -l app.kubernetes.io/managed-by=sharko
```

If empty and you expect entries to exist, the Secrets were deleted
out-of-band or the namespace was wrong.

### 2c. Network path probe (both providers)

For AWS-SM, probe the regional endpoint from inside the pod:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget -q -O /dev/null --no-check-certificate \
  "https://secretsmanager.$AWS_REGION.amazonaws.com" \
  && echo "TCP to AWS SM endpoint: OK" \
  || echo "TCP to AWS SM endpoint: FAILED"
```

For K8s-Secrets, probe the kube-apiserver:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget -q -O /dev/null --no-check-certificate \
  "https://kubernetes.default.svc:443" \
  && echo "TCP to kube-apiserver: OK" \
  || echo "TCP to kube-apiserver: FAILED"
```

If TCP succeeds but the API call fails, the issue is auth (jump to
Mitigation step 2). If TCP fails, the issue is network (jump to
Mitigation step 3).

### 3. Audit-log correlation

```sh
SHARKO_NS=<sharko-ns>
kubectl -n "$SHARKO_NS" logs -l app=sharko --since=30m \
  | jq -c 'select(.msg | test("provider|GetSecretValue|EKS token|secrets fetch"; "i"))' \
  | jq -c '{time, level, msg, provider, cluster, error}' \
  | head -30
```

Patterns:

- Burst starts at a specific timestamp → cross-reference with provider
  status page (AWS Service Health Dashboard) or recent IAM changes
  (CloudTrail).
- All failures have `error: "Unable to locate credentials"` → IRSA
  isn't resolving (Pod missing AWS env injection).
- All failures have `error: "AccessDenied"` → IAM policy was tightened
  out of band.

---

## Mitigation (try in order)

1. **If Diagnosis step 2c shows TCP failure to the provider endpoint**
   — repair the network path. Most common: a NetworkPolicy in the
   Sharko namespace blocks egress, OR a VPC endpoint is missing/broken.

   For NetworkPolicy:

   ```sh
   kubectl get networkpolicy -n <sharko-ns>
   # Inspect any default-deny policy; add an explicit allow for
   # egress to the provider endpoint.
   ```

   For AWS-SM regional endpoint, the egress NetworkPolicy must
   allow port 443 to the AWS service IP range OR to the VPC
   endpoint's CIDR.

   Restart the pod after the policy change so any cached failures
   clear:

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   ```

   Success indicator: Diagnosis step 2c's wget succeeds. Health
   endpoint shows `providers.aws-sm.reachable: true`.

2. **If Diagnosis step 2a/2b shows auth failure** — repair the
   credentials.

   **For AWS IRSA**:

   ```sh
   # Re-apply the SA annotation (cleanest fix if the annotation was
   # removed):
   ROLE_ARN="arn:aws:iam::<account-id>:role/SharkoIRSARole"
   kubectl -n <sharko-ns> annotate sa <sa-name> \
     "eks.amazonaws.com/role-arn=$ROLE_ARN" --overwrite

   # Restart so the pod gets a fresh AWS credential mount:
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   ```

   If the IAM role itself was deleted, re-create it (the chart's
   `values.yaml` documents the required policy):

   ```json
   {
     "Effect": "Allow",
     "Action": [
       "secretsmanager:GetSecretValue",
       "secretsmanager:ListSecrets",
       "eks:DescribeCluster"
     ],
     "Resource": "*"
   }
   ```

   For cross-account EKS, add `sts:AssumeRole` for each target role.

   **For K8s-Secrets RBAC**:

   ```sh
   # Re-apply the Helm chart to restore the ClusterRole/RoleBinding:
   helm upgrade --reuse-values sharko sharko/sharko -n <sharko-ns>
   ```

   Success indicator: Diagnosis step 2a's `aws secretsmanager
   list-secrets` returns a secret name; Diagnosis step 2b's `kubectl
   auth can-i list secrets` returns `yes`.

3. **If Diagnosis step 2a shows AWS-SM regional outage** — wait it
   out, or fail over to a secondary region.

   Sharko's current architecture does not support multi-region
   secrets-provider failover automatically. Manual failover:

   ```sh
   # Patch the deployment to point at the secondary region:
   kubectl -n <sharko-ns> set env deployment/sharko \
     AWS_REGION=<secondary-region>
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

   For this to work, the secrets must already be replicated to the
   secondary region (AWS SM replication). If they aren't, the
   provider will succeed but every fetch will return NotFound — see
   per-cluster failures in
   [`failure-mode-index.md`](failure-mode-index.md).

   Cross-reference the AWS Service Health Dashboard for the regional
   incident before paging downstream consumers.

4. **If Diagnosis shows throttling** (RequestLimitExceeded) — back off
   the cadence. The AWS-SM `GetSecretValue` rate limit defaults to
   5000/sec across the account; if Sharko is sharing the budget with
   other consumers, bursts can throttle.

   ```sh
   # Pause CI/cron driving high-volume cluster operations:
   kubectl -n <ci-ns> patch cronjob/cluster-onboarder \
     -p '{"spec":{"suspend":true}}'
   ```

   Sharko has no internal rate-limit knob today. A P1 follow-up is to
   add a token-bucket on the provider client. Document in the
   incident retro.

5. **Last resort — temporarily switch provider type.** If the active
   provider is the failure and a fallback exists (e.g. K8s-Secrets
   was a working provider before migration to AWS-SM), revert the
   Helm value:

   ```sh
   helm upgrade --reuse-values \
     --set secrets.provider=k8s-secrets \
     sharko sharko/sharko -n <sharko-ns>
   ```

   This requires the secrets to actually exist in the fallback
   provider's namespace. Without that, registration will fail with
   per-cluster NotFound errors. The fallback is meaningful only if
   you maintain dual storage during normal operation.

---

## Root-cause patterns

### AWS regional outage

AWS Secrets Manager in the deployed region is degraded. The AWS
Service Health Dashboard lists an active incident; Sharko's failure
aligns in time.

Diagnostic signature: Diagnosis step 2a's `list-secrets` returns
`ConnectTimeoutError` or `5xx` from AWS SM. Other AWS services in
the same region also degraded (cross-check with CloudWatch, CloudTrail
write-failure metrics).

Fix is upstream — wait or fail over (Mitigation step 3). Sharko
self-recovers when AWS-SM does.

### IRSA misconfigured / IAM role deleted

The Sharko pod's IRSA chain is broken. The Pod has no AWS env
injection, or the SA annotation points at a non-existent role, or the
role's trust policy doesn't trust the cluster's OIDC provider.

Diagnostic signature: Diagnosis step 2a returns `Unable to locate
credentials` or `AccessDenied: User is not authorized to perform
sts:AssumeRoleWithWebIdentity`.

Why it happens: an IAM cleanup deleted the role; a Helm upgrade
overrode the SA annotation; a security review tightened the trust
policy.

Fix is Mitigation step 2 (re-annotate SA, restart pod). For the
IAM-side fix, the role's trust policy is documented in the Sharko
chart's values.yaml comments.

### NetworkPolicy or VPC endpoint blocks egress

Sharko's pod can't reach the AWS-SM regional endpoint. Either a
NetworkPolicy in the Sharko namespace blocks egress, OR a VPC endpoint
for AWS SM is missing/misconfigured, OR a corporate proxy intercepts
the TLS handshake without a valid cert (see
[`corporate-mitm-tls.md`](corporate-mitm-tls.md)).

Diagnostic signature: Diagnosis step 2c's wget fails with timeout or
connection refused; the regional endpoint resolves correctly via
`nslookup` from the pod.

Fix is Mitigation step 1 (NetworkPolicy update) and the VPC endpoint
configuration in your AWS account.

### K8s-Secrets RBAC tightened

The Sharko SA had `list/get secrets` permission in the configured
namespace; an OPA policy, RBAC audit, or manual cleanup removed it.
Every `GetSecret` call fails with 403.

Diagnostic signature: Diagnosis step 2b's `kubectl auth can-i list
secrets` returns `no`. The Sharko logs show
`"secrets is forbidden: User ..."` errors.

Fix is Mitigation step 2 (re-apply Helm chart). Prevention: a startup
RBAC probe (same pattern as
[`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md)'s
Prevention) refuses to start if Sharko cannot list Secrets in the
configured namespace.

---

## Rollback plan

Mitigation steps 1, 2 are non-destructive (re-applying NetworkPolicy or
SA annotation is idempotent).

For Mitigation step 3 (manual region failover):

1. Restore the original region once the outage clears:
   ```sh
   kubectl -n <sharko-ns> set env deployment/sharko \
     AWS_REGION=<primary-region>
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

For Mitigation step 5 (provider type switch):

1. Revert to the original provider:
   ```sh
   helm upgrade --reuse-values \
     --set secrets.provider=aws-sm \
     sharko sharko/sharko -n <sharko-ns>
   ```

2. Verify reachability via `/api/v1/health`.

---

## Prevention

- **Monitoring — health-probe-based alert.** Add a Prometheus rule
  that scrapes `/api/v1/health` periodically and alerts when
  `providers.<active>.reachable == false` for > 60s:

  ```promql
  sharko_provider_reachable{provider="aws-sm"} == 0
  ```

  Wiring requires Sharko to emit a per-provider reachability metric
  — a V2-3.x follow-up.

- **Gating — startup IRSA probe.** Sharko at startup should call
  `aws sts get-caller-identity` once and refuse to start if it fails.
  Catches the "IRSA misconfigured" cause before any cluster
  registration silently fails. Implementation in
  `cmd/sharko/serve.go` startup checks.

- **Gating — startup RBAC probe for K8s-Secrets.** Similar to above
  — `kubectl auth can-i list secrets -n <provider-ns>` against the
  Sharko SA at startup; refuse to start on failure.

- **Scheduled work — quarterly IAM trust-policy review.** The IRSA
  trust policy is the most-bit-rot-prone surface. Once per quarter,
  verify the role still trusts the cluster's OIDC provider, the
  policy still grants the required actions, and the cluster's OIDC
  URL hasn't changed (it can change on cluster upgrades).

- **Capacity — secrets replication to a secondary region.** Configure
  AWS-SM cross-region replication for all cluster credential
  secrets. Sharko can then fail over by env-var change (Mitigation
  step 3) without losing data. AWS SM replication is feature-flag
  per-secret; a documented operator procedure formalizes it.

---

## Related runbooks

- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) —
  adjacent upstream-unreachable failure (ArgoCD). If both fail at
  once, the root cause is likely cross-cutting (network, IAM, OPA).
- [`git-provider-unreachable.md`](git-provider-unreachable.md) —
  same pattern for the Git provider.
- [`corporate-mitm-tls.md`](corporate-mitm-tls.md) — corporate proxy
  TLS interception, a common cause of NetworkPolicy-shaped failures
  in restricted environments.
- [`reconciler-crash-loop.md`](reconciler-crash-loop.md) — if the
  reconciler is also stalled (because every tick fails on provider
  unreachable).
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
  Per-cluster (P1) credential failures are tracked there.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-3 do not restore reachability within 30 minutes,
email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The active provider name and configuration
- The output of Diagnosis steps 2a/2b/2c
- The AWS Service Health Dashboard snapshot (if AWS-SM)
- A 5-minute window of logs filtered by `request_id` from a failed
  registration attempt
- The Sharko version

The maintainer is a single human, not a 24×7 rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (4 named: 1, 2a, 2b, 2c, 3)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert names referenced (FastBurn)
-->
