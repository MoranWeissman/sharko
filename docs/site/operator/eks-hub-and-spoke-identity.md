# Connecting EKS Clusters with IAM (Hub-and-Spoke Identity)

This page shows how to set up AWS IAM so that ArgoCD, and Sharko, can reach
your EKS clusters. It uses the **hub and spoke** setup: ArgoCD and Sharko
run on one main cluster (the hub), and deploy to many other clusters (the
spokes), often in other AWS accounts. See the
[glossary](../glossary.md#main-cluster-and-managed-clusters-hub-and-spoke)
for more on these words.

This page is a setup guide. If something is already broken, see
[AWS IAM Cluster Auth](aws-iam-cluster-auth.md) (Sharko could not get a
token for a cluster) or
[EKS token generation failed](eks-token-generation-failed.md) (Sharko's own
AWS identity is broken).

Every EKS cluster ArgoCD deploys to needs one question answered before
anything else works: **who is allowed to log in to the cluster's Kubernetes
API, and as which identity?** On EKS this is an IAM question, not only a
Kubernetes RBAC question. The cluster's EKS **access entries** (or its
older `aws-auth` ConfigMap) decide which IAM roles get in.

This page walks through the three IAM identities in a hub and spoke EKS
setup, with JSON and commands you can copy and change. **Setting this up is
the job of whoever installs ArgoCD, not Sharko's.** Sharko never creates
IAM roles, policies or EKS access entries for you. Sharko *uses* whatever
identity it has, and tells you what it found at
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

### (a) ArgoCD's role — the one that really talks to the spoke cluster

ArgoCD's `argocd-application-controller` and `argocd-server` are the parts
that connect to each spoke cluster to sync Applications. On EKS they need an
AWS identity to get an EKS login token.

Give them that identity in one of two ways:

- **IRSA (IAM Roles for Service Accounts).** Add a role ARN to the
  `argocd-application-controller` and `argocd-server` service accounts:

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

- **EKS Pod Identity.** Create a Pod Identity association instead. This
  works on newer EKS clusters and needs no OIDC provider trust:

  ```bash
  aws eks create-pod-identity-association \
    --cluster-name hub-cluster \
    --namespace argocd \
    --service-account argocd-application-controller \
    --role-arn arn:aws:iam::123456789012:role/argocd-hub-role
  ```

Either way, the policy on `argocd-hub-role` needs `sts:AssumeRole` on every
spoke role it must reach. **List those roles by name.** A `*` here does
work, but then anyone who takes over the hub role can assume *any* role in
*any* account that trusts it. That is far more than the hub needs:

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

Each time you add a spoke account, add its role to this list.

### (b) The spoke account's role — one per spoke account, trusting the hub

In each spoke AWS account, create (or reuse) a role that:

1. **Trusts the hub role.** Its trust policy allows `sts:AssumeRole` from
   the ARN of `argocd-hub-role`:

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

2. **Is allowed into the spoke cluster** through an EKS access entry. This
   is the step that gives Kubernetes permissions once AWS lets the hub
   assume the role. Without it, the role is assumed fine but the cluster
   still answers with a Kubernetes `403`:

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

   If ArgoCD does not need full admin on that cluster, pick a smaller
   access policy than `AmazonEKSClusterAdminPolicy`. EKS comes with several
   built-in access policies with different levels of access. Clusters still
   on the older `aws-auth` ConfigMap need a matching `mapRoles` entry there
   instead.

This is the role ARN that goes into the ArgoCD cluster Secret, as
`awsAuthConfig.roleARN` or as the `--role-arn` argument in
`execProviderConfig`. See
[Managing Cluster Connections Yourself](self-managed-connections.md#the-secret-you-create)
for the exact Secret shape.

### (c) Sharko's own role — for pushing addon secrets and running tests

Sharko needs its own AWS identity for two things. Both are **separate from
how ArgoCD syncs**. Sharko never deploys addons to spoke clusters itself.

- **`secretsmanager:GetSecretValue`**, to read cluster credentials and addon
  secret values from AWS Secrets Manager, if that is your secrets backend.
- **`sts:AssumeRole`** on the same spoke roles from (b), for two smaller
  jobs than ArgoCD's:
    - getting an EKS token to put addon secrets straight onto a spoke
      cluster, and
    - getting one to answer **Test connection** for an ArgoCD cluster Secret
      that uses `awsAuthConfig` or `execProviderConfig` (see
      [Cluster Connectivity Model](cluster-connectivity-model.md)).

Sharko's jobs are smaller than ArgoCD's, but its access is not. Because
Sharko assumes the same spoke roles, it gets the same Kubernetes access on
each spoke as ArgoCD. [What access Sharko and ArgoCD need on your
clusters](cluster-access.md) lists everything Sharko does on a spoke, and
how to protect the Sharko pod.

Give Sharko its role with IRSA in Sharko's Helm values:

```yaml
# Sharko Helm values
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/sharko-hub-role"
```

### Sharko's IAM policy {#sharko-iam-policy}

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

Limit each `secretsmanager:GetSecretValue` statement to its own prefix:
`connection.provider.prefix` (for example `clusters/`) for cluster
credentials, and `connection.addonSecretProvider.prefix` for addon secret
values (see
[Installation → AWS Secrets Manager](../getting-started/installation.md#aws-secrets-manager-optional)).
Don't use `*`, and don't share one prefix for both. Why this matters, and
where its limits are, is explained in
[Security: a hardened install, step by step](security-hardened-example.md#a-worked-example-a-hardened-installation).

Limit the `sts:AssumeRole` statement to the same list of spoke roles as
ArgoCD's role in (a). Sharko never needs a role that ArgoCD could not also
assume, because it reaches the same clusters for a smaller job.

If Sharko has **no** AWS identity at all (no IRSA annotation, no Pod
Identity association, nothing in the normal AWS credential chain), it says
so instead of guessing. `GET /api/v1/system/capabilities` reports
`"aws": {"detected": false, "method": "none"}`. Anything that needs an AWS
identity, like getting a token for an `awsAuthConfig` cluster or reading an
addon secret from AWS Secrets Manager, then fails with a clear error
instead of silently doing nothing.

## Why identity-based access is the recommended path

For EKS, the best way is for Sharko to get its own short-lived EKS tokens
with IRSA or Pod Identity, the same way this page sets up ArgoCD. The EKS
details are kept in your secrets backend, and Sharko makes a short-lived
token from them. Sharko runs on EKS here, with its own AWS role, so no
long-lived AWS key is stored, and there is no key to leak or forget to rotate.
If Sharko does not run on EKS, you give it an AWS access key, and that key is
stored in a Kubernetes Secret (see
[Give Sharko an AWS login](configuration.md#aws-login)). The
other ways (a stored kubeconfig, or a pasted one) are still supported, and
they are the only ways for clusters outside AWS. See
[Cluster Connectivity Model](cluster-connectivity-model.md) for all the
options.

### What about clusters that already have an exec plugin Secret?

You may adopt a cluster whose ArgoCD Secret was written by `eksctl`,
`aws eks update-kubeconfig`, or ArgoCD's own `argocd-k8s-auth aws` helper.
Its Secret has an `execProviderConfig` block that names a login helper,
like `aws-iam-authenticator` or `argocd-k8s-auth aws`. Sharko knows
**exactly those two AWS helpers**. It reads the `--cluster-name` and
`--role-arn` arguments from them, and gets a token with its own identity,
using the same (a), (b), (c) chain as above. It never runs the helper
program. It only reads the arguments that were meant for it.

Every other helper, like `gke-gcloud-auth-plugin`, `kubelogin`, or a script
your company wrote, is not supported. Sharko can't log in as a Google Cloud
or Azure identity, and it will not run an unknown program for you. See
[ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md)
for what that looks like and what to do.

## Checking the setup

1. See what Sharko found about itself:

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

   `method` is `pod-identity`, `irsa`, `chain` (some other part of the
   normal AWS credential chain, like an instance profile), or `none`.
   `hub_platform` is Sharko's best guess at whether the cluster it runs on
   is EKS, based on the cluster's Kubernetes version string.

2. See what Sharko thinks a spoke cluster is:

   ```bash
   curl -s https://sharko.example.com/api/v1/clusters/prod-us \
     -H "Authorization: Bearer $SHARKO_TOKEN" | jq '.target_platform'
   ```

   You get `"eks"` or `"unknown"`. Sharko works this out from the
   cluster's server address (an `.eks.amazonaws.com` host name) or from how
   its credentials are stored. It makes no extra network call for this.

3. Click **Test connection** on the cluster, or:

   ```bash
   curl -s -X POST https://sharko.example.com/api/v1/clusters/prod-us/test \
     -H "Authorization: Bearer $SHARKO_TOKEN"
   ```

   A `200` with `"reachable": true` means the whole chain works. A `503`
   with `"error_code": "argocd_provider_iam_required"` means Sharko
   understood the Secret's shape, but could not turn it into a working
   token. See [AWS IAM Cluster Auth](aws-iam-cluster-auth.md) for what to
   do.

## Related pages

- [Cluster Connectivity Model](cluster-connectivity-model.md) — which
  credential shapes Sharko reads, and where each piece of connection
  information lives.
- [Managing Cluster Connections Yourself](self-managed-connections.md) —
  the exact ArgoCD cluster Secret shapes, including the EKS one.
- [AWS IAM Cluster Auth](aws-iam-cluster-auth.md) — what it looks like when
  this chain is broken.
- [ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md)
  — what happens for login helpers Sharko doesn't know.
- [EKS token generation failed](eks-token-generation-failed.md) — Sharko's
  own IRSA or Pod Identity setup is broken (not a spoke role problem).
- [Security](security.md) — the wider IAM and RBAC reference.
