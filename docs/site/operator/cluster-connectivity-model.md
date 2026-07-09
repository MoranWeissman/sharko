# Cluster Connectivity Model

> **Reference page, not a runbook.** This page explains the credential
> selection model behind the **Test connection** feature, where each
> piece of "how does Sharko reach this cluster" truth actually lives,
> and the two different things people mean when they say "secret." If
> you are diagnosing a specific failure mode (Test returns 503,
> exec-plugin auth refused, IAM token minting failed), search
> [`failure-mode-index.md`](failure-mode-index.md) or jump directly to
> [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) /
> [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
> / [`eks-token-generation-failed.md`](eks-token-generation-failed.md).
> For the step-by-step IAM recipe (which roles, which trust policies),
> see [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md).

How Sharko picks credentials for the **Test connection** feature, what just works, and what needs additional setup.

## What V125-1-10 changed

Before v1.25, the Test connection feature required an explicit **secrets backend** (Vault / AWS-SM / k8s-secrets) to be configured on the active connection — even for self-hosted Kubernetes installs where the credentials Sharko already wrote into ArgoCD's namespace would do. Operators with no separate secrets backend hit a 503 "no credentials provider configured" dead end.

v1.25 introduces a built-in `argocd` provider that reads cluster credentials directly from the ArgoCD cluster Secret Sharko already creates during `sharko register-cluster` (or that you created yourself, in a self-managed connection). Test now routes by inspecting the Secret's `config` JSON shape — no extra configuration required for the production happy path.

## What just works today

**Self-hosted Kubernetes** (EC2 / VMs / on-prem / bare-metal) registered via `sharko register-cluster` with a kubeconfig containing a static bearer token. ArgoCD stores the credentials with the `bearerToken` config shape; the `argocd` provider reads them back; the Test connection flow runs end-to-end.

**Client-certificate clusters** (kubeadm / on-prem kubeconfigs using `tlsClientConfig.certData` + `keyData` instead of a token) — read back the same way.

**AWS-managed (EKS) clusters using IAM authentication** — as of V2-cleanup-88.2, this is also a happy path, not a limitation. If the ArgoCD cluster Secret uses the `awsAuthConfig` shape, or an `execProviderConfig` naming one of the two well-known AWS authenticators (`argocd-k8s-auth aws` or `aws-iam-authenticator`), Sharko parses the cluster name, role ARN, and region straight out of the Secret and mints a short-lived EKS token **using its own AWS identity** (IRSA / EKS Pod Identity / the default AWS credential chain) — assuming the named role first if one is set. Sharko never shells out to run the exec-plugin binary; it only reads the arguments that were meant for it. See [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for how to wire the IAM roles this depends on.

This only fails when Sharko genuinely can't produce a usable token — no resolvable AWS region, or the mint attempt itself fails (no AWS identity on the Sharko pod, the assumed role's trust policy doesn't include Sharko's role, insufficient STS permissions). That failure surfaces as a `503` with `"error_code": "argocd_provider_iam_required"` — see [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) for the diagnosis path.

This is the **primary production target**. No secrets-backend configuration needed for any of the three shapes above.

## What still needs additional setup

### Cloud-managed clusters using an exec plugin Sharko doesn't recognize

If your kubeconfig uses an out-of-process exec plugin Sharko doesn't know how to parse — `gke-gcloud-auth-plugin`, `kubelogin`, a custom corporate helper script — Test surfaces a clear, typed error:

> *"cluster "\<name\>" uses exec-plugin auth (command "\<helper\>"). Sharko never executes exec-plugin binaries; only the known AWS authenticators (argocd-k8s-auth aws, aws-iam-authenticator) are parsed and minted with Sharko's own AWS identity — every other command stays unsupported."*

This is a real, permanent limitation, not a v1.x-vs-v2 scope cut: Sharko has no way to authenticate as GCP or Azure (see [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)), and it deliberately never shells out to run an arbitrary binary. See [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md) for the workarounds (migrate to a bearer-token or AWS-SM-structured shape, or a static token for GCP/AKS clusters).

## Two names for two different secrets

"Secret" gets used loosely enough in Kubernetes and ArgoCD conversations that it's worth being precise, because Sharko treats these as two entirely separate concerns with separate storage, separate providers, and separate failure modes:

- **Connection credentials** — what lets Sharko (and ArgoCD) *reach a cluster's API server at all*. This is the bearer token / client certificate / AWS IAM role everything on this page is about. It flows through the `ClusterCredentialsProvider` interface — the `argocd` provider described above, or one of `aws-sm` / `k8s-secrets` when Sharko needs to fetch a kubeconfig at registration time.
- **Addon secrets** — the values an addon *deployed to* a cluster needs at runtime (a Datadog API key, a GitHub token for an ingress controller, anything referenced by a `secrets:` block in `addons-catalog.yaml`). These flow through the separate `SecretProvider` interface and get pushed onto the target cluster as a plain Kubernetes Secret, labeled `app.kubernetes.io/managed-by: sharko`.

Each addon declares its own addon-secret paths directly in `configuration/addons-catalog.yaml` (hand-editable in Git, with API/UI equivalents):

```yaml
addons:
  - name: datadog
    chart: datadog
    repo: https://helm.datadoghq.com
    version: 3.74.0
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
          app-key: secrets/datadog/app-key
```

The two concerns can even use the same backend (AWS Secrets Manager, say) without being the same secret — a cluster's connection credentials and an addon's API key just happen to both live in AWS SM, under different paths, read by different code paths, for different reasons. Adding the `argocd` cluster-credentials provider, or the AWS IAM minting in V2-cleanup-88.2, changes nothing about how addon-secret resolution works.

## Registration works with zero credentials (lazy credentials)

Since V2-cleanup-88.3, **registering a cluster never requires connection
credentials at all**, regardless of which connection mode you pick.
Addon workloads deploy the normal way — Git → ArgoCD → the cluster —
and that path needs no credentials from Sharko whatsoever. The one and
only place Sharko needs *its own* access to a spoke cluster is pushing
addon secrets (the `secrets:` block on a catalog entry), because that's
a direct Kubernetes API write Sharko itself performs.

So the gate sits where the need actually is, not at registration:

- **Register a cluster with no credentials source at all.** This
  succeeds for every connection mode — Sharko-managed and self-managed
  alike. The cluster's entry goes into `configuration/managed-clusters.yaml`
  exactly like any other registration.
- **Enable a secret-*less* addon on a credential-less cluster.** Also
  succeeds with zero friction — there was never anything for Sharko to
  push, so there's nothing to gate.
- **Enable a secret-*bearing* addon on a credential-less cluster.** This
  is the one case that's rejected — `POST
  /clusters/{name}/addons/{addon}` returns `422` with a plain message
  naming exactly what's missing:

  > *"addon "datadog" needs 2 secrets pushed to the cluster, but Sharko
  > has no credentials for cluster "prod-1" — add connection
  > credentials (secret path or EKS role) to the cluster, or choose an
  > addon without secrets."*

  The check performs a real credential-fetch attempt (the same
  `credsRouter`-aware path the secret push itself uses) — a `nil`
  result means the push can actually proceed, not just that the
  cluster record *looks* configured.

**In the UI**, a cluster with no resolvable credentials carries
`addon_secrets_ready: false` on its read model (`GET
/clusters/{name}`), and the cluster detail page uses that flag to
pre-warn *before* you click Apply on a secret-bearing addon, instead of
letting the request round-trip into the same 422 the API would return
anyway.

This changes nothing about the two-secret split above: addon secrets
still flow through the same `SecretProvider` interface either way. What
changed is *when* Sharko insists on being able to reach that interface
for a given cluster — at the moment an addon that actually needs it is
enabled, not upfront at registration.

## Checking a connection end to end

The [Connection Doctor](connection-doctor.md) runs five real-attempt
checks against a cluster's connection in one call — credentials,
addon-secret paths, IAM role assumption, cluster access, and (for
self-managed connections) whether another ArgoCD Application is
fighting Sharko over the connection secret. Use it when Test
connection fails and you need to know *which* link in the chain broke,
not just that one did.

## Where each piece of connection truth lives

A registered cluster's "how do I connect to this" story is split across three places on purpose — each piece lives where it's cheapest to fix a typo in it, and each has a different blast radius when it's wrong:

| What | Lives in | How you fix a typo |
|------|----------|---------------------|
| Cluster name, labels, addon selections, `connectionManagedBy` mode, `secretPath`/`roleArn` pointer | `configuration/managed-clusters.yaml` in Git | Edit the file, open a PR (or let Sharko's UI/CLI/API do it for you) — the normal GitOps flow. |
| The actual sensitive bytes (kubeconfig, or a structured `{server, ca, role_arn}` JSON) | Your configured secrets backend (AWS Secrets Manager or a Kubernetes Secret) | Edit the secret in place. Sharko re-reads it on the next credential fetch — **no re-registration needed**. |
| The ArgoCD cluster Secret in the `argocd` namespace | **Derived state** — Sharko's reconciler rebuilds it from the two rows above | **Never hand-edit it** (unless the cluster is in `connectionManagedBy: user` mode — see below). It self-heals: delete it, and the next reconcile tick (within 30s) recreates it. Hand edits to a Sharko-managed one just get overwritten. |

The one edge that doesn't follow this split: **pasting a kubeconfig directly at registration time.** That kubeconfig's bytes get written straight into the secrets backend on your behalf — it's quick, and it works, but it's the one path where the "sensitive bytes live in the secrets backend, not in Git" separation happens automatically instead of being something you set up yourself. Everything else about the registration (name, labels, addons) still goes through Git like normal. Label this path "quick, but not GitOps-clean" if you're documenting your own fleet's setup — pointing at an already-stored secret, or minting an EKS token, keeps the sensitive bytes entirely out of any request Sharko receives.

If a cluster is in **`connectionManagedBy: user` mode** (see [Managing Cluster Connections Yourself](self-managed-connections.md)), the middle row doesn't apply — you own the ArgoCD Secret's contents entirely, and Sharko only ever merges addon labels onto it. The truth table above describes the default, Sharko-managed case.

## Recommended AWS path: identity-based, nothing stored

For AWS, the recommended connectivity path is **identity-based**: Sharko mints short-lived tokens on demand using an IAM role (IRSA or EKS Pod Identity), rather than storing a kubeconfig or a static bearer token anywhere. Nothing to rotate, nothing to leak, nothing that outlives the request that needed it. This applies in two places:

- **At registration time**, choosing the **Amazon EKS token** credentials source (see [Managing Clusters](../user-guide/clusters.md#adding-a-cluster)) instead of pasting or pointing at a kubeconfig.
- **For clusters whose ArgoCD Secret already exists** (adopted, or registered outside Sharko), the `awsAuthConfig` / known-exec-plugin parsing described above achieves the same thing automatically.

Paste-a-kubeconfig and point-at-a-stored-secret remain fully supported fallbacks, and today they're the **only** path for non-AWS clusters — Sharko does not mint GCP or Azure identities natively (see [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)). If you're registering a GKE or AKS cluster, one of those two fallbacks is what you use.

See [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the full IAM setup recipe — which roles, which trust policies, which EKS access entries — that makes the identity-based path work.

## Dev examples

For local development, the bearer-token happy path also covers:

- **kind** (`kind create cluster` produces a kubeconfig with a static bearer token)
- **minikube**
- **Docker Desktop Kubernetes**
- **k3d**

Same setup as self-hosted production — Sharko writes the ArgoCD Secret during `register-cluster`, the `argocd` provider reads it back, Test runs end-to-end. No secrets backend needed.

## Configuration

The `argocd` provider type is the **auto-default** when:

- Sharko runs in-cluster (Kubernetes ServiceAccount available), AND
- No provider is explicitly configured via env var or Helm value

To override, set one of:

```bash
# env var
SHARKO_SECRETS_PROVIDER=vault   # or aws-sm / gcp-sm / azure-kv / k8s-secrets / argocd
```

```yaml
# Helm value
provider:
  type: argocd      # or vault / aws-sm / gcp-sm / azure-kv / k8s-secrets
```

When Sharko runs **outside Kubernetes** (local dev binary, no in-cluster ServiceAccount) and no provider is configured, Test surfaces the existing "no credentials provider configured" 503 — the auto-default cannot apply because the `argocd` provider needs in-cluster K8s API access.

## Future direction

The `argocd` provider already gains label-gated reads as a defense-in-depth follow-up from V125-1-8 (cluster Secret reconciler with the `app.kubernetes.io/managed-by: sharko` ownership label) — Sharko reads only Secrets it provably wrote, or that a self-managed cluster's operator explicitly opted into. This does not change observable behavior for the production happy path; it tightens the read surface in adopt scenarios.

See `docs/design/2026-05-13-cluster-connectivity-test-redesign.md` for the original design rationale, and the V2-cleanup-88 story series for the AWS-identity minting work described above.
