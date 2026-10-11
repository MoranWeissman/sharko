# A cluster uses an exec-plugin sign-in that Sharko can't run

Use this page when one cluster fails its test because its ArgoCD Secret signs
in with an exec plugin (`execProviderConfig`) that Sharko does not know. Common
examples are `gke-gcloud-auth-plugin` (Google), `kubelogin` (Azure) and
in-house helper scripts.

**How urgent:** Fix it when you can. Only this one cluster is affected. Sharko
can't test it or change addons on it until it signs in another way. Every other
cluster keeps working.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

- **Test** on the cluster fails. `POST /api/v1/clusters/{name}/test` answers
  `503` with this body:

    ```json
    {"error":"cluster \"<name>\" uses exec-plugin auth (command \"<helper>\"). Sharko never executes exec-plugin binaries; only the known AWS authenticators (argocd-k8s-auth aws, aws-iam-authenticator) are parsed and minted with Sharko's own AWS identity — every other command stays unsupported.","error_code":"argocd_provider_exec_unsupported"}
    ```

    The `error_code` field is the part to match on. `<helper>` is the command
    in the Secret.

- The log has two lines. One at level `INFO` with `cluster`, `server` and
  `command`:

    ```
    [provider] argocd cluster uses execProviderConfig with an unrecognized command — Sharko never executes exec-plugin binaries
    ```

    And one at level `WARN` with `code=argocd_provider_exec_unsupported`:

    ```
    [cluster-test] argocd provider unavailable shape
    ```

- No alert fires. This is about one cluster only.

Find the log lines like this:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | grep -E 'unrecognized command|argocd provider unavailable shape'
```

If the `error_code` is `argocd_provider_iam_required` instead, Sharko knows the
sign-in type but could not get an AWS token. Go to
[AWS IAM cluster sign-in](aws-iam-cluster-auth.md).

## Why it happens

An exec plugin is a program that runs on the machine that wants to talk to the
cluster, and hands back a short-lived token. Sharko never runs such programs.
Running any program named in a Secret would let whoever can edit that Secret
run code inside Sharko.

Sharko makes two exceptions, both for AWS. It does not run them either. It
reads their arguments and gets the AWS token itself, with its own AWS identity:

- `aws-iam-authenticator`
- `argocd-k8s-auth` when the first argument is `aws`

Any other command gets the message above. That includes:

- `gke-gcloud-auth-plugin` and `kubelogin`. Sharko has no Google or Azure
  identity to sign in with (see
  [Azure Key Vault and GCP Secret Manager don't work in Sharko](azure-gcp-provider-unimplemented.md)).
- an in-house helper, or a script that wraps `aws-iam-authenticator` under a
  different name. Sharko matches the command name exactly, so a wrapper is
  not recognised even if it does AWS sign-in underneath.

## How to fix it

1. **Look at the command in the Secret.** Use the safe commands in
   [Look at the cluster's ArgoCD Secret](argocd-cluster-secret-corruption.md#look-at-the-clusters-argocd-secret).
   The `exec_command` field shows the command. To see the arguments as well
   (they name the cluster and role, not secrets, but check before you paste
   them anywhere):

    ```sh
    kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
      | jq '.data.config | @base64d | fromjson | .execProviderConfig | {command, args}'
    ```

    If the command is `aws-iam-authenticator`, or `argocd-k8s-auth` with `aws`
    first, you are on the wrong page. Go to
    [AWS IAM cluster sign-in](aws-iam-cluster-auth.md).

2. **If it is really an EKS cluster behind a wrapper**, change the Secret to a
   shape Sharko knows. The simplest is ArgoCD's own `awsAuthConfig`, set up as
   shown in [AWS IAM cluster sign-in](aws-iam-cluster-auth.md). Or keep the
   exec shape and use the ArgoCD helper by its real name:

    ```json
    "execProviderConfig": {
      "command": "argocd-k8s-auth",
      "args": ["aws", "--cluster-name", "<eks-cluster-name>", "--role-arn", "arn:aws:iam::123456789012:role/<role-name>"],
      "apiVersion": "client.authentication.k8s.io/v1beta1"
    }
    ```

    Sharko reads `--cluster-name` and `--role-arn` from the arguments.
    Leave out `--role-arn` to sign in with Sharko's own AWS identity.

    If Sharko wrote this connection (label `app.kubernetes.io/managed-by:
    sharko`), change the values in your secrets backend and run
    `sharko repair-connection <cluster>`. If you look after the Secret
    yourself, patch it as shown in
    [How to fix it](argocd-cluster-secret-corruption.md#how-to-fix-it) on the
    broken-Secret page. If another tool writes the Secret, change it in that
    tool.

3. **If it is a Google, Azure or other cluster**, give Sharko a fixed token
   instead. On the target cluster, create a ServiceAccount, give it the rights
   Sharko needs to manage addons there, and make a token for it. Then put that token in the connection as
   `bearerToken`, along with the cluster's CA in `tlsClientConfig.caData`.

    A fixed token lives for a long time. Store it in your secrets backend,
    give it only the rights it needs, and plan when to replace it.

4. **If you can't change the Secret** (another team owns the cluster),
   take the cluster out of Sharko so it stops showing as failed:

    ```sh
    sharko remove-cluster <cluster> --cleanup git -y
    ```

    `--cleanup git` removes only Sharko's files in Git. The ArgoCD connection
    stays, so ArgoCD keeps working with the cluster. (`--cleanup all`, the
    default, also leaves a connection alone when Sharko does not own it.)

## How to check it's fixed

- Run **Test** on the cluster again:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/clusters/<cluster>/test" | jq '{success, error_code}'
    ```

    You want `"success": true`. If the answer moves to
    `argocd_provider_iam_required`, Sharko now knows the sign-in type and the
    next problem is the AWS role. Go to
    [AWS IAM cluster sign-in](aws-iam-cluster-auth.md).

- No new `unrecognized command` lines for this cluster in the last few
  minutes.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the two log lines above for this cluster
- the `command` value from the Secret (not the token)
- what you tried from this page

Related pages:

- [A cluster's ArgoCD Secret is broken](argocd-cluster-secret-corruption.md)
- [AWS IAM cluster sign-in](aws-iam-cluster-auth.md)
- [EKS hub-and-spoke identity](eks-hub-and-spoke-identity.md)
- [Azure Key Vault and GCP Secret Manager don't work in Sharko](azure-gcp-provider-unimplemented.md)
- [Cluster connectivity](cluster-connectivity-model.md)
