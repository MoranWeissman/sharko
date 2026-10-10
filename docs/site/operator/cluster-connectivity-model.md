# Cluster Connectivity Model

This page explains how Sharko gets the credentials it needs to reach a
cluster, for example when you click **Test connection**. It covers what
works with no extra setup, what needs more setup, and where each piece of
"how do I reach this cluster" information is kept.

If you are chasing a specific error, these pages are quicker:

- [AWS IAM Cluster Auth](aws-iam-cluster-auth.md): Test connection returns
  `503` for an EKS cluster that uses AWS IAM.
- [ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md):
  the cluster uses a login helper program Sharko does not support.
- [EKS token generation failed](eks-token-generation-failed.md): Sharko's
  own AWS identity is broken.
- [Failure mode index](failure-mode-index.md): the full list.

For the AWS roles and trust policies, see
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md).

## Where Sharko reads the credentials

By default, Sharko reads a cluster's credentials straight from its ArgoCD
cluster Secret. That is the Secret in the `argocd` namespace that ArgoCD
itself uses to reach the cluster. Sharko writes it when it registers a
cluster, or you create it yourself when you
[manage the connection yourself](self-managed-connections.md). Sharko looks
at the shape of the Secret's `config` field to decide how to log in. You
don't need a separate secrets backend for this.

## What just works today

**Kubernetes you run yourself** (on VMs, on-prem or bare metal) with a
bearer token. The ArgoCD Secret has a `bearerToken` in its `config`.
Sharko reads it back and Test connection works.

**Clusters with a client certificate** (kubeadm or on-prem kubeconfigs that
use `tlsClientConfig.certData` and `keyData` instead of a token). Sharko
reads them back the same way.

**Amazon EKS clusters that use AWS IAM.** This works too. The ArgoCD Secret
uses either the `awsAuthConfig` shape, or an `execProviderConfig` that names
one of the two known AWS login helpers (`argocd-k8s-auth aws` or
`aws-iam-authenticator`). Sharko reads the cluster name, role ARN and
region from the Secret. It then gets a short-lived EKS token **using its own
AWS identity** (IRSA, EKS Pod Identity, or the normal AWS credential chain).
If a role is named, Sharko assumes it first. Sharko never runs the helper
program. It only reads the arguments that were meant for it. See
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the AWS
roles this needs.

This only fails when Sharko really can't get a working token. Either no AWS
region can be found, or AWS refuses the request (for example, the Sharko
pod has no AWS identity, or the role's trust policy does not include
Sharko). Sharko then returns `503` with
`"error_code": "argocd_provider_iam_required"`. See
[AWS IAM Cluster Auth](aws-iam-cluster-auth.md) for what to do.

None of these three shapes need a separate secrets backend.

## What still needs additional setup

### Clusters that use a login helper Sharko doesn't know

Some kubeconfigs run a separate program to log in, like
`gke-gcloud-auth-plugin`, `kubelogin`, or a script your company wrote. If
the program is not one of the two AWS helpers above, Test connection shows
this error:

> *"cluster "\<name\>" uses exec-plugin auth (command "\<helper\>"). Sharko
> never executes exec-plugin binaries; only the known AWS authenticators
> (argocd-k8s-auth aws, aws-iam-authenticator) are parsed and minted with
> Sharko's own AWS identity — every other command stays unsupported."*

This will not change. Sharko can't log in as a Google Cloud or Azure
identity (see
[Azure and GCP providers not implemented](azure-gcp-provider-unimplemented.md)),
and it will never run a program it does not know. See
[ArgoCD exec plugin auth unsupported](argocd-exec-plugin-auth-unsupported.md)
for what to do instead. In short: switch the cluster to a bearer token or a
client certificate.

## Two different kinds of secret

People say "secret" for two different things. Sharko keeps them fully
apart. They are stored in different places, read by different parts of
Sharko, and fail in different ways.

- **Connection credentials** let Sharko and ArgoCD *reach a cluster's API
  at all*. This is the bearer token, client certificate or AWS role that
  this page is about. Sharko reads them from the ArgoCD cluster Secret
  (described above), or from a credentials backend (AWS Secrets Manager or
  Kubernetes Secrets) when it needs a kubeconfig at registration.
- **Addon secrets** are values that an addon needs *on* the cluster while
  it runs, like a Datadog API key. An addon lists the secrets it needs in
  its catalog entry. Sharko reads the values from your secrets backend and
  puts them on the cluster as a normal Kubernetes Secret, with the label
  `app.kubernetes.io/managed-by: sharko`. See
  [Managing Addons → Addon Secrets](../user-guide/addons.md#addon-secrets).

Both kinds can live in the same backend, for example AWS Secrets Manager.
They are still different secrets, under different paths, read for
different reasons. Nothing on this page changes how addon secrets work.

## Registration works with zero credentials (lazy credentials)

**You never need connection credentials to register a cluster**, whoever
manages its connection. Addons reach the cluster the normal way: Git, then
ArgoCD, then the cluster. That needs no credentials from Sharko. The only
time Sharko needs *its own* access to a cluster is to put addon secrets on
it, because then Sharko writes to the cluster's API itself.

So Sharko only asks for credentials when it really needs them:

- **Register a cluster with no credentials.** This works, whoever manages
  the connection. The cluster's entry goes into `managed-clusters.yaml`
  like any other.
- **Turn on an addon that needs no secrets, on a cluster with no
  credentials.** This works too. There is nothing for Sharko to put on the
  cluster.
- **Turn on an addon that needs secrets, on a cluster with no
  credentials.** This is the one case Sharko refuses. Turning the addon on
  (`POST /api/v1/v4/clusters/{name}/addons/{addon}`) returns `422` with the
  code `validation_failed`, and a message that says exactly what is
  missing:

  > *"addon "datadog" needs 2 secrets pushed to the cluster, but Sharko has
  > no credentials for cluster "prod-1" — add connection credentials
  > (secret path or EKS role) to the cluster, or choose an addon without
  > secrets"*

  Sharko really tries to fetch the credentials to decide this, the same
  way it does when it puts the secrets on the cluster. So a pass means the
  push can really happen, not just that the cluster looks set up.

**In the UI**, a cluster with no usable credentials has
`addon_secrets_ready: false` (in `GET /api/v1/clusters/{name}`). The
cluster page uses it to warn you *before* you click Apply on an addon that
needs secrets.

## Checking a connection end to end

The [Connection Doctor](connection-doctor.md) runs six checks on a
cluster's connection in one go: the credentials, the addon secret paths,
the AWS role, access to the cluster, whether another ArgoCD Application
also writes the connection Secret (only when you manage the connection),
and the connectivity check app. Use it when Test connection fails and you
want to know *which* part is broken.

## Where each piece of connection information lives

What Sharko needs to reach a cluster is kept in three places, on purpose.
Each piece is kept where a mistake in it is easiest to fix:

| What | Where it lives | How you fix a mistake |
| --- | --- | --- |
| Cluster name, labels, who manages the connection (`connectionManagedBy`), the `secretPath` or `roleArn`, and which addons are on | `managed-clusters.yaml` and `cluster-addons/<cluster>.yaml` in Git | Edit the file and open a pull request, or let Sharko's UI, CLI or API do it for you. |
| The secret values themselves (a kubeconfig, or `{server, ca, role_arn}` JSON) | Your credentials backend (AWS Secrets Manager or a Kubernetes Secret) | Edit the secret where it is. Sharko reads it again the next time it fetches credentials. **You don't need to register the cluster again.** |
| The ArgoCD cluster Secret in the `argocd` namespace | **Built by Sharko** from the two rows above | **Don't edit it by hand** (unless you manage the connection yourself, see below). If you delete it, Sharko creates it again within 30 seconds. Changes you make by hand are not saved anywhere else, so they are lost when Sharko builds it again. |

One way of registering does not follow this split: **pasting a kubeconfig
when you register.** The pasted kubeconfig is written only into the ArgoCD
cluster Secret. It is **not** saved in your credentials backend, and it is
not in Git. So Sharko can't build that Secret again if it is lost. Pasting
is off by default (the "Allow legacy inline credentials" setting,
`settings.allowInlineCredentials` in the Helm values). Pointing at a stored
secret, or using an EKS token, is the better way.

If **you** manage the cluster's connection (`connectionManagedBy: user`,
see [Managing Cluster Connections Yourself](self-managed-connections.md)),
the last row is different. The ArgoCD Secret is yours, and Sharko only adds
addon labels to it. The table above is for the default case, where Sharko
manages the connection.

## Recommended for AWS: log in with an identity, store nothing

For EKS, the best way is to let Sharko get short-lived tokens with an AWS
role (IRSA or EKS Pod Identity), instead of storing a kubeconfig or a token
anywhere. There is nothing to rotate and nothing to leak. Choose the
**Amazon EKS token** option when you register (see
[Managing Clusters → Adding a Cluster](../user-guide/clusters.md#adding-a-cluster)).
For clusters whose ArgoCD Secret already exists, the `awsAuthConfig` and
known-helper reading described above does the same thing.

For clusters outside AWS, a stored kubeconfig or a pasted one are the only
ways today. The full AWS setup is on
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md#why-identity-based-access-is-the-recommended-path).

## Local test clusters

For testing on your own machine, the bearer token case also covers:

- **kind** (`kind create cluster` gives a kubeconfig with a fixed token)
- **minikube**
- **Docker Desktop Kubernetes**
- **k3d**

It works the same as above: Sharko writes the ArgoCD Secret when you
register the cluster (`sharko add-cluster`), reads it back, and Test
connection works. No secrets backend is needed.

## Configuration

Sharko reads cluster credentials from the ArgoCD cluster Secrets
(`argocd`) by default when both of these are true:

- Sharko runs inside a Kubernetes cluster, and
- no credentials backend is set.

To choose a different one, set it in the UI under **Settings → Secrets
Provider**, or in the Helm values:

```yaml
connection:
  provider:
    type: argocd      # or aws-sm, or k8s-secrets
```

You can also set the environment variable `SHARKO_CONN_PROVIDER_TYPE` to
the same values.

- `argocd` reads the ArgoCD cluster Secrets.
- `aws-sm` reads from AWS Secrets Manager. Set `connection.provider.region`,
  and `connection.provider.prefix` if your secret names start with one
  (for example `clusters/`).
- `k8s-secrets` reads a Kubernetes Secret named after the cluster, with the
  kubeconfig under the key `kubeconfig`. Set `connection.provider.namespace`
  (the default is `sharko`).

If Sharko runs **outside Kubernetes** (for example the plain program on
your laptop) and no backend is set, Test connection returns `503` with "no
credentials provider configured". The default can't apply there, because
reading ArgoCD's Secrets needs access to the cluster's Kubernetes API.

## Related pages

- [AWS IAM Cluster Auth](aws-iam-cluster-auth.md) — when Sharko can't get
  an EKS token.
- [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) — the AWS
  roles and trust policies.
- [Connection Doctor](connection-doctor.md) — the six checks.
- [Managing Cluster Connections Yourself](self-managed-connections.md) —
  when you own the ArgoCD cluster Secret.
