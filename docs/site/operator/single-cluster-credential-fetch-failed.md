# Sharko can't read the sign-in details for one cluster

Sharko keeps each cluster's sign-in details (a kubeconfig, or the facts it
needs to build one) in a secrets backend. This page is for when Sharko can't
read them for **one** cluster, while other clusters still work.

**How urgent:** Fix it soon. Sharko can't connect to or update this one
cluster until it's fixed. Other clusters keep working.

If **every** cluster fails at once, the whole secrets backend is the problem.
Go to [Sharko can't reach its secrets backend](secrets-provider-unreachable.md)
instead.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

On every screen and in every API answer, Sharko shows the same fixed
sentence. It does this on purpose, so the real error never leaks secret
details:

```text
Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed.
```

You may see it in these places:

- **The cluster's Test button** in the UI, or the test API. The answer has
  `stage` set to `credentials` and `error_code` set to `ERR_AUTH`:

  ```bash
  curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/clusters/prod-eu/test" | jq '{stage, error_code, error_message, suggestions}'
  ```

- **The cluster reconciler** skips this cluster and logs:

  ```text
  [clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)
  ```

  ("vault" is only an old internal name. The line is the same for every
  backend.) The audit log gets a failed entry with `action` set to
  `get_credentials` and `resource` set to `cluster:<name>`.

- **The metric** `sharko_reconciler_item_failures_total` goes up, with
  `reason="credentials"`.

The fixed sentence does not say what went wrong. The server log does. The
backend log lines don't carry a request id, so search by cluster name:

```bash
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep '"cluster":"prod-eu"'
```

Replace `prod-eu` with your cluster's name. Each log line is JSON. Look at
the `msg` and `step` fields, then find the matching section below.

## Why it happens

There are four common reasons. Each has its own section under
[How to fix it](#how-to-fix-it):

| Log line you find | Go to |
|---|---|
| `[provider] GetCredentials failed` with `"allMissing":true` | [The secret is not in AWS Secrets Manager](#the-secret-is-not-in-aws-secrets-manager) |
| `[provider] GetCredentials failed` with `"allMissing":false`, or `[provider] SearchSecrets failed` | [Sharko is not allowed to read AWS Secrets Manager](#sharko-is-not-allowed-to-read-aws-secrets-manager) |
| `[provider] GetCredentials failed (k8s)`, or `[provider] found similar secrets` | [The Kubernetes Secret is not in the namespace](#the-kubernetes-secret-is-not-in-the-namespace) |
| `[auth] EKS token generation failed`, or `GetCredentials failed` with `"step":"sts"` | [Sharko can't make an EKS sign-in token](#sharko-cant-make-an-eks-sign-in-token) |

To see which backend you use (`aws-sm`, `k8s-secrets` or `argocd`), plus its
region and prefix:

```bash
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/providers" | jq '.configured_provider'
```

## How to fix it

### The secret is not in AWS Secrets Manager

**What you see.** The log has a line like this (shown short; the real line
is JSON):

```text
[provider] GetCredentials failed   cluster=prod-eu step=all-lookups tried="clusters/prod-eu, prod-eu" allMissing=true
```

`tried` lists every name Sharko looked for. Sharko first tries the prefix
plus the cluster name (`clusters/prod-eu`), then the cluster name alone
(`prod-eu`). `allMissing=true` means AWS said "not found" for every one.
The Test answer may also carry a `suggestions` list: secrets with a similar
name that Sharko found.

**Why.** No secret has any of those names. Most often the secret was never
made, it was made in a different region or AWS account, or its name has a
typo or the wrong prefix.

**Fix.** Run this from your own machine, with an AWS profile that can see
the same account. Use the region from `GET /api/v1/providers`:

```bash
aws secretsmanager list-secrets --region eu-west-1 \
  --filters Key=name,Values=prod-eu --query 'SecretList[].Name'
```

Then do one of these:

- Create the secret under the name Sharko looks for (`clusters/prod-eu`).
- Or, if the secret exists under another name, point the cluster at it. Set
  `secretPath` on the cluster's entry in `managed-clusters.yaml` (at the
  root of your Git repo). You can do this with your own pull request, or ask
  Sharko to open one:

  ```bash
  curl -s -X PATCH -H "Authorization: Bearer $SHARKO_TOKEN" -H "Content-Type: application/json" \
    -d '{"secret_path":"clusters/prod-eu-v2"}' \
    "$SHARKO_URL/api/v1/clusters/prod-eu" | jq
  ```

The secret value is either a whole kubeconfig, or JSON with these fields:
`clusterName`, `host`, `caData` (base64), `accountId`, `region`, `project`,
`environment`, and an optional `roleArn`. If `caData` is not valid base64,
the error says `decoding caData`.

**Check.** Run the cluster's Test again. The `stage` should move past
`credentials`.

### Sharko is not allowed to read AWS Secrets Manager

**What you see.** One or both of these log lines:

```text
[provider] GetCredentials failed   cluster=prod-eu step=all-lookups allMissing=false
[provider] SearchSecrets failed (likely AccessDenied, returning empty)   query=prod-eu step=list-secrets
```

`allMissing=false` means at least one lookup failed for a reason other than
"not found". Most often that reason is "access denied". The Test answer has
no `suggestions` list, because Sharko could not search.

**Why.** The AWS role the Sharko pod runs as can't read this secret. Common
causes:

- The role's policy doesn't allow `secretsmanager:GetSecretValue` on this
  secret's name. For example, the policy only allows `clusters/*` and the
  secret is outside it.
- The policy doesn't allow `secretsmanager:ListSecrets`. Reads may still
  work, but suggestions and the backend health check fail.
- The secret is encrypted with your own KMS key, and the role is not allowed
  `kms:Decrypt` on that key.
- The secret has a resource policy that blocks the role.

**Fix.** Find the role and check its policy. The steps are in
[Check the AWS role](secrets-provider-unreachable.md#check-the-aws-role).
Then look at the secret itself from your machine:

```bash
aws secretsmanager describe-secret --region eu-west-1 --secret-id clusters/prod-eu \
  --query '{Name:Name,KmsKeyId:KmsKeyId}'
aws secretsmanager get-resource-policy --region eu-west-1 --secret-id clusters/prod-eu
```

Add the missing permission to the role's policy, or fix the resource policy.

**Check.** Run the cluster's Test again. The `GetCredentials failed` line
should stop showing up for this cluster.

### The Kubernetes Secret is not in the namespace

**What you see.** You use the `k8s-secrets` backend, and the log has one of:

```text
[provider] GetCredentials failed (k8s)   cluster=prod-eu namespace=sharko step=fetch missing=true
[provider] found similar secrets   query=prod-eu found=1
```

The second line means Sharko found Secrets with a similar name. Their names
show up in the Test `suggestions` list.

**Why.** Sharko reads a Secret named exactly like the cluster (or like the
cluster's `secretPath`, if one is set). It looks in the namespace set in
`connection.provider.namespace`. If that is empty, the namespace is
`sharko`. The Secret needs a data key named `kubeconfig`. Common causes:

- The Secret is missing, has another name, or is in another namespace.
- The Secret has no `kubeconfig` key. The error says
  `secret "prod-eu" has no 'kubeconfig' key`.
- The kubeconfig inside is broken. The error says `parsing kubeconfig from secret`.
- Sharko is not allowed to read Secrets in that namespace. Then the log
  says `missing=false`.

**Fix.** Look from your machine:

```bash
SHARKO_SECRETS_NS=sharko   # the value of connection.provider.namespace
kubectl -n "$SHARKO_SECRETS_NS" get secret prod-eu -o jsonpath='{.data}' | jq 'keys'
kubectl auth can-i get secrets -n "$SHARKO_SECRETS_NS" \
  --as=system:serviceaccount:"$SHARKO_NS":sharko
```

Use your real service account name if it is not `sharko`.

- If the Secret is missing, create it from a kubeconfig file. This needs no
  base64 step:

  ```bash
  kubectl -n "$SHARKO_SECRETS_NS" create secret generic prod-eu \
    --from-file=kubeconfig=./prod-eu.kubeconfig
  ```

- If `can-i` says `no`, the namespace is not in Sharko's allowed list. Add it
  to `rbac.k8sSecretsProviderNamespaces` in your Helm values and run
  `helm upgrade`. The chart then makes a Role that lets Sharko get and list
  Secrets in that namespace.
- If the Secret lives in another namespace on purpose, change
  `connection.provider.namespace` and add that namespace to
  `rbac.k8sSecretsProviderNamespaces`.

**Check.** `kubectl auth can-i` says `yes`, and the cluster's Test moves
past `credentials`.

### Sharko can't make an EKS sign-in token

**What you see.** For EKS clusters, Sharko reads the secret and then makes a
short-lived sign-in token. When that fails, the log has:

```text
[auth] EKS token generation failed   cluster=prod-eu region=eu-west-1 step=presign-get-caller-identity
[provider] GetCredentials failed   cluster=prod-eu region=eu-west-1 step=sts
```

The `step` field on the first line tells you what broke:

| `step` | What it means |
|---|---|
| `load-aws-config` | The AWS setup inside the pod is broken. This is rare. |
| `presign-get-caller-identity` | Sharko had no AWS credentials to sign with, or could not take on the role in `roleArn`. |

If the cluster uses the `argocd` backend, the line is
`[provider] EKS token mint failed for argocd cluster — Sharko has no usable AWS identity for this cluster`,
and the Test answers with `error_code` set to `argocd_provider_iam_required`.

**Why.** Sharko picks a role in this order: the `roleArn` in the cluster's
secret, then the `roleArn` on the cluster's entry in `managed-clusters.yaml`,
then the default in `connection.provider.roleArn`. If one is set, Sharko
must be allowed to take it on (`sts:AssumeRole`), and that role's trust
policy must allow Sharko's own role. If none is set, Sharko uses its own pod
role.

Making the token needs no extra IAM permission. So if the token is made but
the cluster later answers 401, the identity is not let into the cluster.
That is a different problem, covered in
[AWS IAM cluster sign-in](aws-iam-cluster-auth.md).

**Fix.** First check Sharko's own pod role with
[Check the AWS role](secrets-provider-unreachable.md#check-the-aws-role).
Then, if a `roleArn` is in use, check from your machine that the role exists
and trusts Sharko's role:

```bash
aws iam get-role --role-name SharkoClusterAccess \
  --query 'Role.AssumeRolePolicyDocument'
```

The trust policy must name Sharko's pod role, for example
`arn:aws:iam::123456789012:role/SharkoIRSARole`. Sharko's own role policy
must allow `sts:AssumeRole` on the target role. For roles in other accounts,
see [EKS hub and spoke identity](eks-hub-and-spoke-identity.md).

**Check.** The log shows `[provider] STS token generated` for this cluster,
and the cluster's Test passes.

## How to check it's fixed

1. Run the cluster's Test (the button in the UI, or the `POST .../test` call
   above). It should pass the `credentials` stage.
2. Wait for the next reconciler pass. The `skipping cluster` line should
   stop for this cluster:

   ```bash
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=15m \
     | grep 'vault GetCredentials failed' | grep '"cluster":"prod-eu"'
   ```

   No output means it's fixed.
3. `sharko_reconciler_item_failures_total{reason="credentials"}` stops going up.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention the cluster name
- what you tried from this page

Related pages:

- [Sharko can't reach its secrets backend](secrets-provider-unreachable.md)
- [AWS IAM cluster sign-in](aws-iam-cluster-auth.md)
- [EKS hub and spoke identity](eks-hub-and-spoke-identity.md)
- [Look at a cluster's ArgoCD Secret](argocd-cluster-secret-corruption.md#look-at-the-clusters-argocd-secret)
- [Failure mode index](failure-mode-index.md)
