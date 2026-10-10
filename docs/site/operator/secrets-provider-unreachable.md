# Sharko can't reach its secrets backend

Sharko reads each cluster's sign-in details from a secrets backend: AWS
Secrets Manager (`aws-sm`), Kubernetes Secrets (`k8s-secrets`), or ArgoCD's
own cluster Secrets (`argocd`). This page is for when the whole backend
can't be reached, so **every** cluster fails, not just one.

**How urgent:** Fix it now. While this lasts, Sharko can't register new
clusters, can't connect to existing ones, and can't refresh addon secrets.

If only **one** cluster fails and the others work, go to
[Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)
instead.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- Registering a cluster, or the Test button on any cluster, fails with the
  same fixed sentence. Sharko always shows this sentence, whatever the real
  cause, so secret details never leak:

  ```text
  Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed.
  ```

- The backend status says `error`:

  ```bash
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/providers" | jq '.configured_provider'
  ```

  When it works, `status` is `connected`. When it doesn't, `status` is
  `error`. The `type`, `region` and `prefix` fields tell you what Sharko is
  trying to reach.

- The log shows the same failure for every cluster, and a failed health
  check:

  ```bash
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
    | grep -E 'HealthCheck failed|GetCredentials failed|EKS token generation failed'
  ```

  ```text
  {"level":"WARN","msg":"[provider] HealthCheck failed","type":"aws-sm","region":"eu-west-1","prefix":"clusters/",...}
  {"level":"ERROR","msg":"[clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)","cluster":"...","step":"get-credentials"}
  ```

  The sign that the whole backend is down is that the **same** `step` shows
  up for **every** cluster at about the same time.

- These alerts may fire if it goes on for a while:
  `SharkoClusterRegistrationFastBurn` and `SharkoAddonCycleFastBurn`. See
  [Budget burn alerts](budget-burn-runbook.md).

## Why it happens

There are three common reasons:

- **Sharko's AWS role is broken.** The Sharko pod gets its AWS rights from
  IRSA ("IAM Roles for Service Accounts": AWS gives the pod a role, based on
  an annotation on its Kubernetes service account). If the annotation is
  gone, the role was deleted, the role's trust policy is wrong, or the
  role's policy lost a permission, every AWS call fails.
- **The network path is blocked.** A NetworkPolicy blocks the pod from
  calling out, a VPC endpoint for Secrets Manager is missing or broken, or a
  company proxy breaks the TLS connection.
- **The backend itself is down**, for example an AWS outage in that region.

For the `k8s-secrets` backend, the usual cause is that Sharko lost its right
to read Secrets in the namespace it uses.

Good to know: the AWS health check lists secrets (it asks for just one,
under the prefix). So if the role lost `secretsmanager:ListSecrets`, the
status shows `error` even though reading single secrets may still work.

## How to fix it

Start by asking Sharko to test the backend again:

```bash
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/providers/test" | jq
```

### Check the AWS role

This is the one place in the docs that explains how to check the Sharko
pod's AWS role. Run every command from your own machine, not inside the pod.

1. Find the service account the pod runs as, and the role on it:

   ```bash
   SA=$(kubectl -n "$SHARKO_NS" get pod -l app.kubernetes.io/name=sharko \
     -o jsonpath='{.items[0].spec.serviceAccountName}')
   kubectl -n "$SHARKO_NS" get sa "$SA" \
     -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'
   ```

   You should see a role ARN, for example
   `arn:aws:iam::123456789012:role/SharkoIRSARole`. If it's empty, the
   annotation is missing.

2. Check that AWS gave the pod the role. EKS adds two environment variables
   to the pod when IRSA works:

   ```bash
   kubectl -n "$SHARKO_NS" get pod -l app.kubernetes.io/name=sharko \
     -o jsonpath='{.items[0].spec.containers[0].env[*].name}' | tr ' ' '\n' | grep '^AWS_'
   ```

   You should see `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE`. If not,
   the pod started before the annotation was there. Restart it (step 5).

3. Check that the role exists and trusts the pod:

   ```bash
   aws iam get-role --role-name SharkoIRSARole --query 'Role.AssumeRolePolicyDocument'
   aws eks describe-cluster --name <hub-cluster> --query 'cluster.identity.oidc.issuer'
   ```

   The trust policy must name the cluster's OIDC address from the second
   command, and the service account `system:serviceaccount:<namespace>:<sa>`.
   A wrong account number, region or service account name is a common
   mistake.

4. Check that the role's policy allows what Sharko needs:

   ```bash
   aws iam list-attached-role-policies --role-name SharkoIRSARole
   aws iam list-role-policies --role-name SharkoIRSARole
   ```

   It needs `secretsmanager:GetSecretValue`, `secretsmanager:ListSecrets`
   and `eks:DescribeCluster`. If Sharko takes on other roles for some
   clusters, it also needs `sts:AssumeRole` on those roles. If the secrets
   use your own KMS key, it needs `kms:Decrypt` on that key.

5. Fix what you found. Set the annotation through your Helm values, not by
   hand, so the next upgrade doesn't remove it:

   ```yaml
   serviceAccount:
     annotations:
       eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/SharkoIRSARole"
   ```

   Run `helm upgrade` with those values, then restart the pod so it picks
   up the role:

   ```bash
   kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
   kubectl -n "$SHARKO_NS" rollout status deploy/sharko
   ```

### Check the network path

Sharko's image has no tools for testing the network from inside, so check
the rules from outside:

```bash
kubectl -n "$SHARKO_NS" get networkpolicy
```

If a policy blocks traffic out of the pod, add a rule that allows port 443
to the Secrets Manager endpoint (or to your VPC endpoint's address range).
If you use a VPC endpoint for Secrets Manager, check in the AWS console that
it exists and that its security group allows the cluster's nodes. If your
company runs a proxy that opens TLS traffic, see
[Corporate TLS proxy](corporate-mitm-tls.md).

### Check the Kubernetes Secrets backend

For `k8s-secrets`, check that Sharko can still read Secrets in its namespace
(the value of `connection.provider.namespace`, or `sharko` if empty):

```bash
SHARKO_SECRETS_NS=sharko
SA=$(kubectl -n "$SHARKO_NS" get pod -l app.kubernetes.io/name=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}')
kubectl auth can-i list secrets -n "$SHARKO_SECRETS_NS" --as=system:serviceaccount:"$SHARKO_NS":"$SA"
kubectl auth can-i get secrets -n "$SHARKO_SECRETS_NS" --as=system:serviceaccount:"$SHARKO_NS":"$SA"
```

Both must say `yes`. If not, make sure `rbac.create` is `true` and the
namespace is in `rbac.k8sSecretsProviderNamespaces`, then run `helm upgrade`
to put the Role back. The health check lists Secrets with the label
`app.kubernetes.io/managed-by=sharko`.

### If AWS itself is down

Check the AWS Health Dashboard for an incident in your region. If there is
one, Sharko recovers by itself when AWS does.

If your secrets are copied to a second region (Secrets Manager replication),
you can point Sharko there for now. Set `connection.provider.region` to the
second region and run `helm upgrade`. Set it back the same way when the
outage is over. If the secrets are not in the second region, every cluster
will then fail with "not found" instead.

## How to check it's fixed

1. The backend status is `connected`:

   ```bash
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/providers" | jq '.configured_provider.status'
   ```

2. The Test button works on a cluster that failed before.
3. New `HealthCheck failed` and `GetCredentials failed` lines stop:

   ```bash
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep -c 'GetCredentials failed'
   ```

   The count should be `0`.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `HealthCheck failed` or `GetCredentials failed`
- the output of `GET /api/v1/providers`
- what you tried from this page

Related pages:

- [Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)
- [AWS IAM cluster sign-in](aws-iam-cluster-auth.md)
- [EKS hub and spoke identity](eks-hub-and-spoke-identity.md)
- [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md)
- [Sharko can't reach the Git host](git-provider-unreachable.md)
- [Failure mode index](failure-mode-index.md)
