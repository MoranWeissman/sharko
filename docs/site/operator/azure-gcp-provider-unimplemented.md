# Azure Key Vault and GCP Secret Manager don't work in Sharko

Use this page when a secrets provider type is set to Azure Key Vault or
GCP Secret Manager. Sharko has no working code for either one. This page
says what works today and what to use instead.

**How urgent:** fix it soon. Sharko keeps running. But addon secret
values are not copied to your clusters, and Sharko reads cluster sign-in
details from somewhere other than you meant.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

In the UI, under **Settings > Secrets Provider**, both lists (**Provider
Type** and **Addon Secret Provider Type**) show these choices greyed out:

- Azure Key Vault — not yet supported
- GCP Secret Manager — not yet supported

You can't pick them in the UI. So if one is set, it came from the API,
from Helm (`connection.provider.type` or
`connection.addonSecretProvider.type`), or from the environment variables
`SHARKO_CONN_PROVIDER_TYPE` or `SHARKO_CONN_ADDON_SECRET_PROVIDER_TYPE`.

**For addon secret values**, Sharko logs this at WARN when it starts, and
the job that copies addon secrets to your clusters does not run:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko | grep 'could not create secret provider for reconciler'
```

The `error` field is one of these:

- `Azure Key Vault provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko`
- `GCP Secret Manager provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko`

**For cluster sign-in details**, there is no error at all. Sharko ignores
the Azure or GCP type and acts as if the type were empty. Inside a
Kubernetes cluster, that means it reads sign-in details from ArgoCD's own
cluster Secrets. You see this in the logs:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko \
  | grep -E 'auto-defaulting to argocd|credentials provider constructed|initializing provider'
```

- `[provider] auto-defaulting to argocd (no provider configured + in-cluster K8s detected)`
- `credentials provider constructed` with an empty `type`
- after you save the connection, `[startup] initializing provider` with
  the Azure or GCP type you set

The pod does not crash, and no alert fires for this.

## Why it happens

Sharko only has working code for these providers:

| What it is for | Provider types that work |
|---|---|
| Cluster sign-in details (**Provider Type**) | `argocd`, `aws-sm`, `k8s-secrets` |
| Addon secret values (**Addon Secret Provider Type**) | `aws-sm`, `k8s-secrets` |

`argocd` works only for cluster sign-in details. For addon secret values
it gives the error `argocd provider is cluster-credentials-only; ...`.

Sharko knows the names `azure`, `azure-kv`, `azure-key-vault`, `gcp`,
`gcp-sm` and `google-secret-manager`. But behind those names there is
only a stub that returns the "not yet implemented" error. Any other name
gives `unknown provider type "<name>"`.

If the addon secret provider type is empty, Sharko uses the cluster
provider's settings for addon secrets too. So an Azure or GCP type in
**Provider Type** alone is enough to stop the addon secrets job.

Two names you may have seen in older notes: `SHARKO_PROVIDER` and
`SHARKO_CLUSTER_TEST_PROVIDER`. They are not read; use
`SHARKO_CONN_PROVIDER_TYPE`.

## How to fix it

### See what is set

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" \
  | jq '.connections[] | select(.is_active) | {name, provider, addon_secret_provider}'
```

If you use Helm for the connection settings, also look at what you set
there:

```sh
helm get values <release> -n "$SHARKO_NS" | grep -A6 -E 'provider:|addonSecretProvider:'
```

### Pick a provider that works

- **Your secrets are in Azure Key Vault or GCP Secret Manager and must
  stay there.** Copy them into Kubernetes Secrets with your own tool (for
  example External Secrets Operator), then use `k8s-secrets` for both.
  See [The Kubernetes Secret is not in the namespace](single-cluster-credential-fetch-failed.md#the-kubernetes-secret-is-not-in-the-namespace)
  for how Sharko expects those Secrets to look.
- **Your clusters are on EKS, or your secrets can live in AWS.** Use
  `aws-sm`. See
  [The secret is not in AWS Secrets Manager](single-cluster-credential-fetch-failed.md#the-secret-is-not-in-aws-secrets-manager)
  and
  [Sharko is not allowed to read AWS Secrets Manager](single-cluster-credential-fetch-failed.md#sharko-is-not-allowed-to-read-aws-secrets-manager).
- **You only need cluster sign-in details, and ArgoCD already has them.**
  Use `argocd` for **Provider Type**. Use `aws-sm` or `k8s-secrets` for
  **Addon Secret Provider Type** if you need addon secrets.

Sharko does not have to run in the same cloud as the clusters it manages.

### Change the setting

In the UI: open **Settings > Secrets Provider**, pick the new types, and
save.

Or through the API. This changes only the providers and keeps the rest of
the connection:

```sh
CONN=$(curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" | jq -r .active_connection)

curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/connections/$CONN" \
  -d '{"provider":{"type":"k8s-secrets","namespace":"sharko-secrets"},
       "addon_secret_provider":{"type":"k8s-secrets","namespace":"sharko-secrets"}}' | jq
```

If Helm manages these settings, change `connection.provider.type` and
`connection.addonSecretProvider.type` in your values too, and run
`helm upgrade`. A value set in Helm wins over a value saved in the UI or
the API, so otherwise Sharko puts the old type back.

The new cluster provider is used at once. The addon secrets job is only
started when Sharko starts, so restart Sharko after the change:

```sh
kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
kubectl -n "$SHARKO_NS" rollout status deploy/sharko --timeout=120s
```

## How to check it's fixed

1. The warning is gone since the restart. This should print nothing:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko | grep 'could not create secret provider for reconciler'
    ```

2. The connection shows the new types:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" \
      | jq '.connections[] | select(.is_active) | {provider, addon_secret_provider}'
    ```

3. The log line `credentials provider constructed` shows the type you
   picked, not an empty one:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko | grep 'credentials provider constructed'
    ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `provider`
- what you tried from this page

If you would like to add Azure Key Vault or GCP Secret Manager support to
Sharko yourself, say so in the issue.

Related pages:

- [Secrets provider in the user guide](../user-guide/secrets-provider.md)
- [Sharko can't reach its secrets backend](secrets-provider-unreachable.md)
- [Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)
