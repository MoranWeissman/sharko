# Cluster Connectivity Model

How Sharko picks credentials for the **Test cluster** feature, what just works, and what needs additional setup.

## What V125-1-10 changed

Before v1.25, the Test cluster feature required an explicit **secrets backend** (Vault / AWS-SM / k8s-secrets) to be configured on the active connection — even for self-hosted Kubernetes installs where the credentials Sharko already wrote into ArgoCD's namespace would do. Operators with no separate secrets backend hit a 503 "no credentials provider configured" dead end.

v1.25 introduces a built-in `argocd` provider that reads cluster credentials directly from the ArgoCD cluster Secret Sharko already creates during `sharko register-cluster`. Test now routes by inspecting the Secret's `config` JSON shape — no extra configuration required for the production happy path.

## What just works today

**Self-hosted Kubernetes** (EC2 / VMs / on-prem / bare-metal) registered via `sharko register-cluster` with a kubeconfig containing a static bearer token. ArgoCD stores the credentials with the `bearerToken` config shape; the `argocd` provider reads them back; the Test cluster 12-step flow runs end-to-end.

This is the **primary production target** for v1.25. No secrets-backend configuration needed.

## What needs additional setup

### AWS-managed (EKS) clusters using IAM authentication

If you registered an EKS cluster whose kubeconfig uses AWS IAM authentication (`aws eks get-token` / `awsAuthConfig` shape), Test surfaces a clear error:

> *"This cluster uses AWS IAM authentication. Configure AWS credentials for the Sharko pod's role to enable Test."*

The Sharko pod needs an IAM role with permission to call `eks:GetToken` (or an OIDC trust setup) to mint per-call tokens. Until the cloud-creds plumbing ships, Test cannot run for IAM-auth EKS clusters in v1.x. See [AWS IAM cluster authentication](aws-iam-cluster-auth.md) for the v1.x limitation and the v2 plan.

### Cloud-managed clusters using exec-plugin auth

If your kubeconfig uses an out-of-process exec plugin to mint credentials (`gcloud config helper`, `azure-cli`, `aws-iam-authenticator`, `kubelogin`, etc. — the `execProviderConfig` shape), Test surfaces:

> *"Exec-plugin auth is not supported in v1.x. Tracked for v2."*

Exec-plugin support is not on the v1.x roadmap. Test cannot run for these cluster types until v2.

## Dev examples

For local development, the bearer-token happy path also covers:

- **kind** (`kind create cluster` produces a kubeconfig with a static bearer token)
- **minikube**
- **Docker Desktop Kubernetes**
- **k3d**

Same setup as self-hosted production — Sharko writes the ArgoCD Secret during `register-cluster`, the `argocd` provider reads it back, Test runs end-to-end. No secrets backend needed.

## What hasn't changed

The `argocd` provider is for **cluster credentials only** — not for addon secret values. Addon secret values (Datadog API keys, GitHub tokens, anything referenced by `secrets:` in an addon catalog entry) still flow through your configured `SecretProvider`:

- **Vault**
- **AWS Secrets Manager**
- **GCP Secret Manager**
- **Azure Key Vault**
- **Kubernetes Secrets** (`KubernetesSecretProvider`)

The two interfaces are intentionally separate (per the 2026-04-07 secrets-provider design). Adding the `argocd` cluster-credentials provider does not affect addon-secret resolution at all.

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

After V125-1-8 ships (cluster Secret reconciler with the `app.kubernetes.io/managed-by: sharko` ownership label), the `argocd` provider will gain label-gated reads as a defense-in-depth follow-up — Sharko reads only Secrets it provably wrote. This does not change observable behavior for the production happy path; it tightens the read surface in adopt scenarios.

See `docs/design/2026-05-13-cluster-connectivity-test-redesign.md` for the full design rationale.
