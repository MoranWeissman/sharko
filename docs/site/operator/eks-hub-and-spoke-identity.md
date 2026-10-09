# Connecting EKS Clusters with IAM (Hub-and-Spoke Identity)

> **Reference page, not a runbook.** This page is the recipe for wiring
> IAM-based access from your ArgoCD hub to your EKS spoke clusters, and
> for giving Sharko the same access so it can push addon secrets and run
> connectivity tests. It does not cover a specific failure — if
> something in this recipe is broken, see
> [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) (Sharko couldn't
> mint a usable token) or
> [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
> (Sharko's own identity is broken). For the bigger picture of where
> connection truth lives, see
> [Cluster Connectivity Model](cluster-connectivity-model.md).

Every EKS cluster that ArgoCD deploys to needs one thing settled before
anything else works: **who is allowed to authenticate to the cluster's
Kubernetes API, and as what identity?** On EKS, that's an IAM question,
not a Kubernetes-RBAC-only question — the cluster's `aws-auth`
ConfigMap or its EKS **access entries** decide which IAM principals get
in.

This page walks through the three IAM identities involved in a
hub-and-spoke EKS setup, in plain language, with copy-paste JSON and
CLI you can adapt. **Setting this up is the ArgoCD installer's job, not
Sharko's** — Sharko never creates IAM roles, policies, or EKS access
entries on your behalf. What Sharko does is *use* whatever identity it's
been given, and tell you what it detected via
`GET /api/v1/system/capabilities`.

## The three identities

```
┌─────────────────────┐        assumes         ┌─────────────────────┐
│   ArgoCD's role      │ ────────────────────►  │  Spoke account role  │
│  (on the hub cluster) │                        │  (in each spoke acct) │
└─────────────────────┘                        └─────────────────────┘
          ▲                                                │
          │ also assumable by                              │ mapped via
          │                                                 │ EKS access entry
┌─────────────────────┐                                     ▼
│   Sharko's role       │                        ┌─────────────────────┐
│  (on the hub cluster) │ ─── assumes (same) ──►  │   Spoke EKS cluster   │
└─────────────────────┘                        └─────────────────────┘
```

### (a) ArgoCD's role — the one that actually talks to the spoke API

ArgoCD's `argocd-application-controller` and `argocd-server` components
are the ones that open a Kubernetes client against each spoke cluster to
sync Applications. On EKS they need an AWS identity to mint an EKS
authentication token (the same `k8s-aws-v1.` presigned-STS-URL token
Sharko's own minting code produces — see
[Cluster Connectivity Model](cluster-connectivity-model.md)).

Give them that identity one of two ways:

- **IRSA (IAM Roles for Service Accounts)** — annotate the
  `argocd-application-controller` and `argocd-server` ServiceAccounts
  with a role ARN:

  ```yaml
  # values.yaml for the argo-cd Helm chart
  controller:
    serviceAccount:
      annotations:
        eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/argocd-hub-role"
  server:
    serviceAccount:
      annotations:
        eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/argocd-hub-role"
  ```

- **EKS Pod Identity** — create a Pod Identity association instead of an
  annotation (newer EKS clusters, no OIDC provider trust needed):

  ```bash
  aws eks create-pod-identity-association \
    --cluster-name hub-cluster \
    --namespace argocd \
    --service-account argocd-application-controller \
    --role-arn arn:aws:iam::123456789012:role/argocd-hub-role
  ```

Either way, `argocd-hub-role`'s policy needs `sts:AssumeRole` on every
spoke role it needs to reach. **Scope it down** — a wildcard resource
works, but it means a compromised hub role can assume *any* spoke role
in *any* account that trusts it, which is a much bigger blast radius
than the hub actually needs:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": [
        "arn:aws:iam::123456789012:role/example-spoke-role",
        "arn:aws:iam::234567890123:role/example-spoke-role"
      ]
    }
  ]
}
```

`"Resource": "*"` here does work — AssumeRole doesn't fail on a
wildcard — but it trades convenience for a much wider blast radius. List
the spoke role ARNs explicitly, and add a new statement (or a new line
in the list) each time you onboard a spoke account.

### (b) The spoke account's role — one per spoke account, trusting the hub

In each spoke AWS account, create (or reuse) a role that:

1. **Trusts the hub role** — its trust policy allows `sts:AssumeRole`
   from `argocd-hub-role`'s ARN:

   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Principal": {
           "AWS": "arn:aws:iam::123456789012:role/argocd-hub-role"
         },
         "Action": "sts:AssumeRole"
       }
     ]
   }
   ```

2. **Is mapped into the spoke cluster** via an EKS access entry — this
   is the step that actually grants Kubernetes-level permissions once
   the IAM assume-role succeeds. Without this, a successfully-assumed
   role still gets a Kubernetes `403` at the API server:

   ```bash
   aws eks create-access-entry \
     --cluster-name spoke-cluster \
     --principal-arn arn:aws:iam::234567890123:role/example-spoke-role \
     --region us-east-1

   aws eks associate-access-policy \
     --cluster-name spoke-cluster \
     --principal-arn arn:aws:iam::234567890123:role/example-spoke-role \
     --policy-arn arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy \
     --access-scope type=cluster \
     --region us-east-1
   ```

   Pick a narrower access policy than `AmazonEKSClusterAdminPolicy` if
   ArgoCD's job on that cluster doesn't need cluster-admin — EKS ships
   several built-in access policies at different privilege levels.
   (Clusters still on the legacy `aws-auth` ConfigMap instead of access
   entries need the equivalent `mapRoles` entry there instead.)

This is the role ARN that ends up in the ArgoCD cluster Secret's
`awsAuthConfig.roleARN` or `execProviderConfig` `--role-arn` argument —
see [Managing Cluster Connections Yourself](self-managed-connections.md#the-secret-you-create)
for the exact Secret shape.

### (c) Sharko's own role — for pushing addon secrets and running tests

Sharko needs its own AWS identity for two things, both of which are
**separate from ArgoCD's sync path** — Sharko never syncs addons to
spoke clusters itself:

- **`secretsmanager:GetSecretValue`** — to read cluster credentials and
  addon secret values out of AWS Secrets Manager (if that's your
  configured secrets backend).
- **`sts:AssumeRole`** on the same spoke roles from (b) — for two
  narrower jobs than ArgoCD's: minting an EKS token to push addon
  secrets directly onto a spoke cluster (`internal/remoteclient`), and
  minting one to answer **Test connection** for an existing ArgoCD
  cluster Secret that uses `awsAuthConfig` or `execProviderConfig` (see
  [Cluster Connectivity Model](cluster-connectivity-model.md)).

```yaml
# charts/sharko/values.yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/sharko-hub-role"
```

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadClusterCredentialSecrets",
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:us-east-1:123456789012:secret:clusters/*"
    },
    {
      "Sid": "ReadAddonSecretValues",
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:us-east-1:123456789012:secret:addon-secrets/*"
    },
    {
      "Sid": "AssumeSpokeRolesForTestAndSecretPush",
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": [
        "arn:aws:iam::123456789012:role/example-spoke-role",
        "arn:aws:iam::234567890123:role/example-spoke-role"
      ]
    }
  ]
}
```

Scope each `secretsmanager:GetSecretValue` resource to the prefix its own
provider is configured with — `connection.provider.prefix` (default
`clusters/`) for cluster credentials, `connection.addonSecretProvider.prefix`
for addon secret values (see
[Installation](../getting-started/installation.md#aws-secrets-manager-optional)) —
not to `*`, and not to one shared prefix for both. Splitting the two
statements onto two disjoint prefixes means a caller (or a bug) reading
under the wrong provider's assumption is refused by AWS itself, not just
by Sharko's own code. It is **not** full identity separation — both reads
still run as this one `sharko-hub-role` principal, since a plain
`GetSecretValue` call doesn't assume a per-provider role today (only the
EKS-token-mint path for cluster credentials supports a per-cluster
`roleArn`). See
[Security → A worked example](security.md#a-worked-example-a-hardened-installation)
for the fuller picture and that limit spelled out. Scope the
`sts:AssumeRole` resource to the same explicit spoke-role list as ArgoCD's
role in (a) — Sharko never needs to assume a role ArgoCD itself couldn't
also assume, since it's reaching the same clusters for a narrower purpose.

If Sharko has **no** identity at all (no IRSA annotation, no Pod
Identity association, nothing in the default credential chain), it
degrades honestly rather than guessing: `GET /api/v1/system/capabilities`
reports `"aws": {"detected": false, "method": "none"}`, and any
operation that needs an AWS identity (minting a token for an
`awsAuthConfig` cluster, pushing an addon secret from AWS-SM) fails with
a typed, actionable error instead of a silent no-op.

## Why identity-based access is the recommended path

Sharko supports three ways to give it a spoke cluster's credentials at
registration time (point at a stored kubeconfig, mint an Amazon EKS
token, or — legacy and off by default — paste a kubeconfig; see
[Managing Clusters](../user-guide/clusters.md#adding-a-cluster)), plus
this hub-and-spoke recipe for clusters whose ArgoCD Secret already
exists. **The identity-based path — Sharko minting its own EKS token via
IRSA / Pod Identity, the same way this page sets up ArgoCD's access — is
the recommended one for AWS.** Nothing long-lived is stored anywhere;
every token is minted on demand and expires in minutes; there's no
kubeconfig or static bearer token sitting in a secrets backend for
someone to leak or forget to rotate.

Paste-a-kubeconfig and point-at-a-stored-secret remain supported
fallbacks — and today they're the **only** path for non-AWS clusters.
Sharko does not mint GCP or Azure identities; if you're registering a
GKE or AKS cluster, one of those two fallbacks is what you use (see
[`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)
for the honest state of native GCP/Azure credential minting).

### What about clusters that already have an exec-plugin Secret?

If you're adopting a cluster whose ArgoCD Secret was written by
`eksctl`, `aws eks update-kubeconfig`, or ArgoCD's own
`argocd-k8s-auth aws` helper, its Secret carries an
`execProviderConfig` block naming a command like `aws-iam-authenticator`
or `argocd-k8s-auth aws`. Sharko recognizes **exactly those two AWS
authenticator commands** — it parses the `--cluster-name` /
`--role-arn` arguments out of them and mints a token with its own
identity, following the same (a)/(b)/(c) chain as above. It never
executes the plugin binary itself; it only reads the arguments that
were meant for it.

Every other exec command — `gke-gcloud-auth-plugin`, `kubelogin`, a
custom corporate helper script — stays genuinely unsupported. Sharko
has no way to authenticate as GCP or Azure, and it will not shell out to
run an arbitrary binary on your behalf. See
[`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
for what that looks like and what to do about it.

## Verifying the setup

1. Check what Sharko detected about itself:

   ```bash
   curl -s https://sharko.example.com/api/v1/system/capabilities \
     -H "Authorization: Bearer $SHARKO_TOKEN" | jq
   ```

   ```json
   {
     "aws": {
       "detected": true,
       "method": "irsa",
       "identity_arn": "arn:aws:sts::123456789012:assumed-role/sharko-hub-role/..."
     },
     "hub_platform": "eks"
   }
   ```

   `method` is one of `pod-identity`, `irsa`, `chain` (some other link
   in the default AWS credential chain, e.g. an instance profile), or
   `none`. `hub_platform` is Sharko's best-effort guess at whether the
   cluster it's running on is EKS, from the hub's own Kubernetes version
   string.

2. Check what Sharko thinks a given spoke cluster is:

   ```bash
   curl -s https://sharko.example.com/api/v1/clusters/prod-us \
     -H "Authorization: Bearer $SHARKO_TOKEN" | jq '.target_platform'
   ```

   `"eks"` or `"unknown"` — derived from the cluster's registered server
   URL (an `.eks.amazonaws.com` hostname) or its stored credentials
   source, no extra network call.

3. Click **Test connection** on the cluster, or:

   ```bash
   curl -s -X POST https://sharko.example.com/api/v1/clusters/prod-us/test \
     -H "Authorization: Bearer $SHARKO_TOKEN"
   ```

   A `200` with `"reachable": true` means the whole chain worked. A
   `503` with `"error_code": "argocd_provider_iam_required"` means the
   Secret's shape was recognized but Sharko couldn't turn it into a
   usable token — see
   [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) for the
   diagnosis path.

## Related pages

- [Cluster Connectivity Model](cluster-connectivity-model.md) — the
  full picture of which credential shapes Sharko reads, and where each
  piece of connection truth lives.
- [Managing Cluster Connections Yourself](self-managed-connections.md)
  — the exact ArgoCD cluster Secret shapes, including the EKS/IAM one.
- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — what it looks
  like when this chain is broken.
- [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
  — what happens for exec commands Sharko doesn't recognize.
- [`eks-token-generation-failed.md`](eks-token-generation-failed.md) —
  Sharko's own IRSA/Pod-Identity chain is broken (not a spoke-role
  problem).
- [Reference — Security](security.md) — the wider IAM/RBAC posture
  reference.
