# AWS IAM Cluster Auth — Sharko Couldn't Mint a Token

Use this page when **Test connection** fails with `503` on an EKS cluster
that logs in with AWS IAM. Sharko understood how the cluster logs in, but
could not get a working EKS login token for it.

Some ArgoCD cluster Secrets for EKS use AWS IAM to log in. They use either
the `awsAuthConfig` shape, or an `execProviderConfig` that names
`argocd-k8s-auth aws` or `aws-iam-authenticator`. Sharko supports these
shapes. It reads them and tries to get an EKS token with its own AWS
identity (IRSA, EKS Pod Identity, or the normal AWS credential chain). If a
role is named, it assumes that role first.

That can still fail in two ways:

- Sharko can't find out which AWS region the cluster is in, or
- AWS refuses to give Sharko the token.

If Test connection works for your cluster, you don't need this page. The
working setup is described in
[Cluster Connectivity Model](cluster-connectivity-model.md) and
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md).

---

## Symptoms

- **In the UI:** clicking **Test connection** on the cluster page shows one
  of these messages.

    When no region can be found (`awsAuthConfig` shape):

    > "cluster "\<name\>" uses AWS IAM authentication (awsAuthConfig) but no
    > AWS region could be determined (no region field in awsAuthConfig, the
    > cluster Secret has no "region" label, and the server URL "\<url\>"
    > isn't a recognizable EKS endpoint). Set the region label on the ArgoCD
    > cluster Secret, or add a region field to awsAuthConfig."

    When no region can be found (`execProviderConfig` shape):

    > "cluster "\<name\>" uses exec-plugin AWS authentication (command
    > "\<command\>") but no AWS region could be determined (no AWS_REGION in
    > execProviderConfig.env, the cluster Secret has no "region" label, and
    > the server URL "\<url\>" isn't a recognizable EKS endpoint). Set the
    > region label on the ArgoCD cluster Secret, or add AWS_REGION to
    > execProviderConfig.env."

    When AWS refused the token:

    > "Cluster "\<name\>" needs Sharko's own AWS identity (IRSA / EKS Pod
    > Identity) to use this cluster's IAM-based connection, and minting an
    > EKS sign-in token failed. The server log for this request id says
    > which step failed."

- **In the API:** `POST /api/v1/clusters/{name}/test` returns
  `503 Service Unavailable` with a body like this:

  ```json
  {"error":"Cluster \"prod-eks-1\" needs Sharko's own AWS identity (IRSA / EKS Pod Identity) to use this cluster's IAM-based connection, and minting an EKS sign-in token failed. The server log for this request id says which step failed.","error_code":"argocd_provider_iam_required"}
  ```

  `error_code` is always `argocd_provider_iam_required` for this problem,
  whichever of the two causes it is. The UI uses this field to decide what
  to show.

- **In the Sharko log** (`kubectl logs -n sharko deploy/sharko`), at the
  moment of the failed test. When no region can be found:

  ```
  {"time":"...","level":"WARN","msg":"[provider] argocd cluster awsAuthConfig has no resolvable AWS region — cannot mint an EKS token","cluster":"prod-eks-1","server":"https://...","eksClusterName":"prod-eks-1"}
  ```

  (For the `execProviderConfig` shape, the message says
  `execProviderConfig` instead, and also has a `command` field.)

  When AWS refused the token:

  ```
  {"time":"...","level":"ERROR","msg":"[provider] EKS token mint failed for argocd cluster — Sharko has no usable AWS identity for this cluster","cluster":"prod-eks-1","server":"https://...","eksClusterName":"prod-eks-1","region":"us-east-1","step":"mint-eks-token"}
  ```

  This line does not include the AWS error text. To see which part
  failed, use the steps under Diagnosis.

- **A Kubernetes Warning event** with the reason `AWSTokenMintFailed` is
  also recorded.
- **No Sharko alert fires.** Test connection is something you run by hand,
  so no alert watches it. You only see it on the screen.
- **ArgoCD may still reach the cluster fine.** ArgoCD has its own AWS role,
  separate from Sharko's (see
  [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md)). A cluster
  can be healthy in ArgoCD while Sharko's Test connection fails, if only
  Sharko's identity is broken. They are two different roles doing two
  different jobs.

If you see a different error, this is **not** the right page:

- `argocd_provider_exec_unsupported` → see
  [ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md).
- `no credentials provider configured` → see
  [Secrets provider unreachable](secrets-provider-unreachable.md).
- `cluster secret malformed` → see
  [ArgoCD cluster Secret corruption](argocd-cluster-secret-corruption.md).

---

## Diagnosis

Four checks, in this order. The first confirms the Secret's shape. The
second tells the two causes apart. The third and fourth check Sharko's own
identity.

The [Connection Doctor](connection-doctor.md) also helps here. Its
`assume-role` check really tries to assume the role, and its
`cluster-access` check tells you if the cluster itself does not trust the
role.

### 1. Check that the cluster Secret uses `awsAuthConfig` or a known AWS helper

The Secret has the same name as the cluster in Sharko.

```sh
kubectl -n argocd get secret <cluster-name> -o json \
  | jq -r '.data.config | @base64d' \
  | jq '{ awsAuthConfig, execProviderConfig, bearerToken: (.bearerToken // null | tostring | .[0:10]) }'
```

For this problem you should see either `awsAuthConfig` set, or
`execProviderConfig.command` set to `"aws-iam-authenticator"`, or to
`"argocd-k8s-auth"` with `args[0] == "aws"`. `bearerToken` should be
empty. If it is set, the cluster does not use IAM at all.

### 2. Check which cause it is: no region, or AWS refused

The message under Symptoms tells you.

- **No region:** Sharko looks for the region in this order:
    1. `awsAuthConfig.region` (or `AWS_REGION` in `execProviderConfig.env`),
    2. the `region` label on the Secret,
    3. the cluster's server address, if it is a normal EKS address
       (`<id>.gr7.<region>.eks.amazonaws.com`).

    None of these was there, so Sharko has nothing to go on.
- **AWS refused:** Sharko read the shape and knows the region. The call to
  AWS failed.

### 3. If AWS refused, check Sharko's own AWS identity

```sh
curl -s https://sharko.example.com/api/v1/system/capabilities \
  -H "Authorization: Bearer $SHARKO_TOKEN" | jq '.aws'
```

- `"detected": false` means the Sharko pod has no AWS identity at all: no
  IRSA annotation, no Pod Identity association, and nothing in the normal
  AWS credential chain. Fix that first (see Mitigation step 2).
- `"detected": true` with a `method` and `identity_arn` means Sharko has an
  identity. Then either it is not allowed `sts:AssumeRole` on the cluster's
  role, or the role's trust policy does not list Sharko's role.

### 4. Check that the cluster's role really trusts Sharko

```sh
aws iam get-role --role-name example-spoke-role \
  --query 'Role.AssumeRolePolicyDocument' --output json
```

The `Principal.AWS` list must include the `identity_arn` from step 3 (or
the role it is a session of). If it only lists ArgoCD's hub role, that is
the problem. Sharko needs its own trust entry. It does not get ArgoCD's.
See [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the
full trust policy setup.

---

## Mitigation (try in order)

### 1. No region found — set one

Add a `region` label to the ArgoCD cluster Secret:

```sh
kubectl -n argocd label secret <cluster-name> region=us-east-1 --overwrite
```

If **you** manage this cluster's connection (`connectionManagedBy: user`),
you can also put the region in the Secret's own `config` instead:
`awsAuthConfig.region`, or `AWS_REGION` in `execProviderConfig.env`. Both
are permanent fixes.

If **Sharko** manages the Secret, the label is a quick fix only. Sharko
does not write a region into the Secrets it creates. For a normal EKS
address it doesn't need to, because it reads the region from the address.
Sharko leaves your `region` label alone, but if it ever builds the Secret
again (for example after the Secret was deleted), the label is gone and you
must add it again. For a cluster on its own DNS name, or in an AWS
partition with a different address (GovCloud or China), there is no lasting
fix on Sharko's side today. If this affects you, please
[open an issue](https://github.com/MoranWeissman/sharko/issues) and say
which kind of address the cluster has.

### 2. Sharko has no AWS identity at all — give it one

If step 3 showed `"detected": false`, add an IRSA role annotation to
Sharko's service account (the `serviceAccount.annotations` Helm value), or
create an EKS Pod Identity association. See
[EKS Hub-and-Spoke Identity → Sharko's IAM policy](eks-hub-and-spoke-identity.md#sharko-iam-policy)
for the policy Sharko needs: `secretsmanager:GetSecretValue` limited to
your secret prefix, and `sts:AssumeRole` on the spoke roles.

### 3. Sharko has an identity but can't assume the role — fix the trust policy

Add Sharko's role ARN to the trust policy of the cluster's role (step 4
shows what is there now):

```json
{
  "Effect": "Allow",
  "Principal": {
    "AWS": "arn:aws:iam::123456789012:role/sharko-hub-role"
  },
  "Action": "sts:AssumeRole"
}
```

Also check that Sharko's own role policy allows `sts:AssumeRole` on that
role's ARN. See
[EKS Hub-and-Spoke Identity → Sharko's IAM policy](eks-hub-and-spoke-identity.md#sharko-iam-policy)
for the statement.

### 4. Check the cluster outside Sharko while you fix the above

These checks do not depend on Sharko's AWS setup:

```sh
# What ArgoCD sees (ArgoCD has its own, separate AWS role)
kubectl -n argocd get application -o wide | grep <cluster-name>

# Reach the cluster's API with your own AWS identity
aws eks update-kubeconfig --name <eks-cluster-name> --region <region> \
  --kubeconfig /tmp/eks-check.kubeconfig
kubectl --kubeconfig /tmp/eks-check.kubeconfig get nodes
```

If ArgoCD shows the cluster as Healthy and Synced, the cluster is fine.
Only Sharko's own Test connection is stuck on Sharko's AWS setup.

### 5. Last choice — register the cluster again with a bearer token

**Not recommended.** You can add an EKS cluster to ArgoCD with a fixed
bearer token (a long-lived service account token), and skip IAM. But such
tokens are less safe than IAM: they don't rotate, they keep working after
a role is removed, and IAM policies can't limit them. Only do this if your
security rules allow it, and only for a short time while you fix the IAM
setup.

---

## Common causes

### The Sharko pod has no AWS identity

This is the most common cause right after an install. Sharko's Helm chart
was installed without the
`serviceAccount.annotations."eks.amazonaws.com/role-arn"` value, and no EKS
Pod Identity association was created either.
`GET /api/v1/system/capabilities` reports
`"detected": false, "method": "none"`. Fix: Mitigation step 2.

### The cluster's role only trusts ArgoCD's role, not Sharko's

This happens when a platform team sets up IAM for ArgoCD only, and doesn't
know Sharko needs its own trust entry on the same role. ArgoCD syncs fine,
but Sharko's Test connection fails. Fix: Mitigation step 3.

### No region anywhere on the Secret

Sharko does not write a region into the Secrets it creates, in any form.
For those clusters, the server address is the only place Sharko can find
the region. That only works for normal `*.eks.amazonaws.com` addresses. A
cluster on its own DNS name, or in GovCloud or China with a different
address, has no region Sharko can find. Fix: Mitigation step 1.

---

## Prevention

- **Set up IAM once, when you install.** Give your platform team
  [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) before the
  first EKS cluster is registered. It covers Sharko's role next to
  ArgoCD's, so Sharko is never left without a trust entry.
- **Check `/api/v1/system/capabilities` right after you install.** If it
  says `"detected": true`, Sharko has an AWS identity. This catches a
  missing IRSA annotation before it shows up on every cluster.
- **Give every cluster that does not use a normal EKS address a region**
  (GovCloud, China, or your own DNS name), as in Mitigation step 1. Don't
  count on Sharko reading the region from the address.

---

## Related pages

- [ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md)
  — a close problem: the Secret's login helper is not one of the two known
  AWS helpers.
- [EKS token generation failed](eks-token-generation-failed.md) — a close
  problem, seen from Sharko's own IRSA setup instead of one cluster's
  Secret. Read it if steps 3 and 4 point at Sharko's role rather than the
  cluster's role.
- [Cluster Connectivity Model](cluster-connectivity-model.md) — which login
  shapes Sharko supports today.
- [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) — the full
  IAM setup that the steps above point back to.
- [Failure mode index](failure-mode-index.md) — the full list of problems.

## If this does not fix it

Sharko is a technical preview, and it is looked after by one person. There
is no on-call support. If the steps above don't fix Test connection,
[open an issue on GitHub](https://github.com/MoranWeissman/sharko/issues).
Please include:

- a link to this page,
- the output of `GET /api/v1/system/capabilities`,
- the `awsAuthConfig` or `execProviderConfig` part of
  `kubectl get secret <cluster-name> -n argocd -o yaml` (remove the role
  ARN if your rules say so, the problem does not depend on it),
- the full `error` text from the failed `POST /api/v1/clusters/{name}/test`.

Don't post tokens, keys or other secret values in the issue.
