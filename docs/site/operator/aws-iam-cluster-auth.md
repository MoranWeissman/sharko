# AWS IAM Cluster Auth — Sharko Couldn't Mint a Token

**Severity:** P1

> **Verified:** Authored 2026-07-09 against `main` HEAD as part of
> V2-cleanup-88.6 (connection-redesign documentation), updating this
> runbook for the reality shipped by V2-cleanup-88.2 (#509): Sharko now
> **parses** the `awsAuthConfig` shape (and the two known AWS
> `execProviderConfig` commands) and mints an EKS token using its own
> AWS identity, instead of refusing the shape outright. The
> `ArgoCDProviderCodeIAMRequired` = `"argocd_provider_iam_required"`
> sentinel and the two failure messages below are verified verbatim
> against `resolveAWSAuthConfig` and `mintTokenKubeconfig` in
> `internal/providers/argocd_provider.go`. Re-verify if the mint
> failure messages or the wire code change.

Operators registering (or adopting) an EKS cluster whose ArgoCD cluster
Secret uses AWS IAM authentication (the `awsAuthConfig` shape, or an
`execProviderConfig` naming `argocd-k8s-auth aws` / `aws-iam-authenticator`)
hit a `503 Service Unavailable` when they click **Test connection** in
the UI. This is **not** the "IAM auth isn't supported" gap it used to
be — Sharko recognizes the shape and actively tries to mint a usable
EKS token with its own AWS identity (IRSA / EKS Pod Identity / the
default credential chain, optionally assuming a named role first). This
runbook covers the two ways that mint attempt can still fail: no
resolvable AWS region, or the mint itself being rejected by AWS.

If your cluster Secret uses this shape and Test connection succeeds,
you don't need this runbook — that's the working path, and it's
documented in [Cluster Connectivity Model](cluster-connectivity-model.md)
and [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md).

---

## Symptoms

What an operator sees when this fires:

- UI: clicking **Test connection** on a Cluster detail page surfaces a
  banner with one of two messages, depending on which failure this is
  (see Diagnosis):

  > "cluster "\<name\>" uses AWS IAM authentication (awsAuthConfig) but
  > no AWS region could be determined (no region field in awsAuthConfig,
  > the cluster Secret has no "region" label, and the server URL
  > "\<url\>" isn't a recognizable EKS endpoint). Set the region label on
  > the ArgoCD cluster Secret, or add a region field to awsAuthConfig."

  or

  > "cluster "\<name\>" needs Sharko's own AWS identity (IRSA/Pod
  > Identity) to use this cluster's IAM-based connection, and minting an
  > EKS token failed: \<AWS error\>"

- API: `POST /api/v1/clusters/{name}/test` returns
  `503 Service Unavailable` with body:

  ```json
  {"error":"cluster \"prod-eks-1\" needs Sharko's own AWS identity (IRSA/Pod Identity) to use this cluster's IAM-based connection, and minting an EKS token failed: ...","error_code":"argocd_provider_iam_required"}
  ```

  `error_code` is the stable field the UI dispatches on — always
  `argocd_provider_iam_required` for this failure, whichever of the two
  root causes produced it.

- `kubectl logs -n sharko deploy/sharko` line at the failed test — the
  missing-region case:

  ```
  {"time":"...","level":"WARN","msg":"[provider] argocd cluster awsAuthConfig has no resolvable AWS region — cannot mint an EKS token","cluster":"prod-eks-1","server":"https://...","eksClusterName":"prod-eks-1"}
  ```

  or the mint-failure case:

  ```
  {"time":"...","level":"ERROR","msg":"[provider] EKS token mint failed for argocd cluster — Sharko has no usable AWS identity for this cluster","cluster":"prod-eks-1","server":"https://...","eksClusterName":"prod-eks-1","region":"us-east-1","error":"..."}
  ```

- No Sharko alert fires — `Test connection` is a synchronous read; the
  burn-rate alerts do not cover it because there is no error budget
  defined for cluster-test calls. The failure is purely user-visible.
- ArgoCD itself may or may not be able to reach the cluster, depending
  on whether **ArgoCD's own** IAM identity (a separate role from
  Sharko's — see [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md))
  is correctly set up. A cluster can be healthy in ArgoCD while Sharko's
  Test connection still fails, if only Sharko's own identity is broken —
  they're two different IAM roles doing two different jobs.

If the error message differs (`argocd_provider_exec_unsupported`,
`no credentials provider configured`, `cluster secret malformed`),
this is **not** the right runbook. See
[`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md),
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md),
or
[`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md).

---

## Diagnosis

Four checks, in order. The first confirms the Secret shape; the second
and third split the two root causes; the fourth checks Sharko's own
identity directly.

### 1. Confirm the cluster Secret uses `awsAuthConfig` or a known AWS exec command

```sh
kubectl -n argocd get secret cluster-<cluster-name> -o json \
  | jq -r '.data.config | @base64d' \
  | jq '{ awsAuthConfig, execProviderConfig, bearerToken: (.bearerToken // null | tostring | .[0:10]) }'
```

Expected on this failure path: either `awsAuthConfig` is non-null, or
`execProviderConfig.command` is `"aws-iam-authenticator"` or
`"argocd-k8s-auth"` with `args[0] == "aws"`. `bearerToken` should be
null — if it's set, your cluster isn't using IAM auth at all.

### 2. Check which failure this is — missing region, or failed mint

The two messages in Symptoms tell you which. If it's the region
message, the Secret's `awsAuthConfig`/`execProviderConfig.env` carries
no region, the Secret has no `region` label, and the server URL isn't a
standard EKS hostname (`<id>.gr7.<region>.eks.amazonaws.com`) — Sharko
has genuinely nothing to go on. If it's the mint-failure message, the
shape parsed fine and Sharko knows the region; the AWS call itself
failed.

### 3. For a mint failure, confirm Sharko's own AWS identity

```sh
curl -s https://sharko.example.com/api/v1/system/capabilities \
  -H "Authorization: Bearer $SHARKO_TOKEN" | jq '.aws'
```

`"detected": false` means the Sharko pod has no AWS identity at all —
no IRSA annotation, no Pod Identity association, nothing in the default
credential chain. That's the fix (see Mitigation). `"detected": true`
with a `method` and `identity_arn` means Sharko has *an* identity but
it either lacks `sts:AssumeRole` on the target role, or the target
role's trust policy doesn't list Sharko's role as a trusted principal.

### 4. Confirm the target role's trust policy actually trusts Sharko

```sh
aws iam get-role --role-name example-spoke-role \
  --query 'Role.AssumeRolePolicyDocument' --output json
```

The `Principal.AWS` list must include the ARN from step 3's
`identity_arn` (or the role it's an assumed-role session of). If it
only lists ArgoCD's own hub role, that's the gap — Sharko needs its own
trust entry, it doesn't inherit ArgoCD's. See
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the full
trust-policy recipe.

---

## Mitigation (try in order)

### 1. No resolvable region — set one explicitly

Add a `region` label to the ArgoCD cluster Secret (the field Sharko
checks first after the Secret's own `awsAuthConfig.region` /
`execProviderConfig.env.AWS_REGION`):

```sh
kubectl -n argocd label secret cluster-<cluster-name> region=us-east-1 --overwrite
```

If the Secret is Sharko-managed (not `connectionManagedBy: user`),
this label is a stopgap until the next reconcile — the durable fix is
setting `region` on the cluster's entry in `managed-clusters.yaml` so
Sharko's own writer stops omitting it.

### 2. Sharko has no AWS identity at all — give it one

If step 3 above showed `"detected": false`, annotate the Sharko
ServiceAccount with an IRSA role, or create an EKS Pod Identity
association — see [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md#c-sharkos-own-role-for-pushing-addon-secrets-and-running-tests)
for the exact role policy Sharko needs (`secretsmanager:GetSecretValue`
scoped to your secret prefix, `sts:AssumeRole` on the spoke roles).

### 3. Sharko has an identity but can't assume the target role — fix the trust policy

Add Sharko's role ARN to the target spoke role's trust policy (step 4
above shows you what's currently there):

```json
{
  "Effect": "Allow",
  "Principal": {
    "AWS": "arn:aws:iam::123456789012:role/sharko-hub-role"
  },
  "Action": "sts:AssumeRole"
}
```

And confirm Sharko's own role policy grants `sts:AssumeRole` on that
target role's ARN — see the recipe page for the exact statement shape.

### 4. Verify connectivity manually outside Sharko (while you fix the above)

The fastest "did my cluster actually register?" check that doesn't
depend on Sharko's own IAM chain:

```sh
# Sanity-check ArgoCD's view (ArgoCD has its own, separate IAM role)
kubectl -n argocd get application -o wide | grep <cluster-name>

# Probe the cluster's API directly using your own AWS identity
aws eks get-token --cluster-name <eks-cluster-name> --region <region> \
  | kubectl --token "$(...)" get nodes
```

If ArgoCD itself reports the cluster Healthy/Synced, registration
succeeded and this is purely Sharko's own Test connection button being
blocked on its own IAM setup — not a sign the cluster is broken.

### 5. Last resort — re-register the cluster as bearer-token

**Not recommended.** EKS clusters can be added to ArgoCD using a static
bearer token (a long-lived ServiceAccount token), bypassing IAM auth
entirely. Long-lived ServiceAccount tokens are a security regression
versus IAM auth — they don't rotate, they survive role revocation, and
they can't be scoped down by IAM policy. Use this lane only if your
security posture explicitly allows it and only as a stop-gap while you
fix the IAM chain.

---

## Root-cause patterns

### Sharko's pod has no AWS identity

The most common cause after a fresh install: the Sharko Helm chart was
deployed without the `serviceAccount.annotations."eks.amazonaws.com/role-arn"`
value set, and no EKS Pod Identity association was created either.
`GET /api/v1/system/capabilities` reports `"detected": false, "method": "none"`.
Fix is Mitigation step 2.

### Target role's trust policy only trusts ArgoCD's role, not Sharko's

A common misconfiguration when a platform team sets up the hub-and-spoke
IAM chain for ArgoCD alone and doesn't realize Sharko needs its own,
separate trust entry on the same target role. ArgoCD syncs fine;
Sharko's Test connection fails. Fix is Mitigation step 3.

### No region signal anywhere on the Secret

Sharko's own writer (`argosecrets.buildSecretConfig`) never persists
region as a JSON field, an env var, or a Secret label for its own
`execProviderConfig` output — the EKS server URL is the only fallback
signal for Sharko-registered clusters, and it only works for
standard-shaped `*.eks.amazonaws.com` hostnames. A cluster on a custom
DNS name, or in a GovCloud/China partition with a different server
suffix, has no automatic region signal at all. Fix is Mitigation step 1.

---

## Prevention

- **Set the IAM chain up once, at install time.** [EKS Hub-and-Spoke
  Identity](eks-hub-and-spoke-identity.md) is the recipe to hand your
  platform team before the first EKS cluster gets registered — it
  covers Sharko's role alongside ArgoCD's, so the "Sharko has no trust
  entry" gap in Root-cause patterns above never happens.
- **Check `/system/capabilities` right after install.** A quick
  `curl .../system/capabilities` confirming `"detected": true` catches
  a missing IRSA annotation before it becomes a per-cluster support
  ticket.
- **Set `region` on every cluster's Git entry explicitly** for
  non-standard EKS endpoints (GovCloud, China partitions, custom DNS) —
  don't rely on the server-URL fallback for anything outside the
  standard AWS partition.

---

## Related runbooks

- [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
  — adjacent failure: the Secret's exec command isn't one of the two
  known AWS authenticators at all.
- [`eks-token-generation-failed.md`](eks-token-generation-failed.md) —
  overlapping failure surface (same `getEKSToken` code path), framed
  around Sharko's own IRSA chain rather than a specific cluster's
  Secret shape. Read this one if step 3/4 above point at Sharko's own
  role rather than the target role.
- [Cluster Connectivity Model](cluster-connectivity-model.md) —
  reference for which kubeconfig auth shapes Sharko handles today.
- [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) — the
  full IAM setup recipe this runbook's mitigations point back to.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If the mitigations above don't restore Test connection and the
limitation is blocking fleet on-boarding, email the maintainer:
`moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The output of `GET /api/v1/system/capabilities`
- A diff of `kubectl get secret cluster-<name> -n argocd -o yaml`
  showing the `awsAuthConfig`/`execProviderConfig` shape (redact the
  role ARN if your policy requires; the failure is not role-specific)
- The full `error` string from the failed `POST /clusters/{name}/test`

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-cleanup-88.6 rewrite):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / API responses
- [x] Diagnosis has 4 concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 5 steps in priority order
- [x] Root-cause patterns: 3 named causes
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] Reflects V2-cleanup-88.2 shipped reality, not the old v1.x-unsupported framing
-->
