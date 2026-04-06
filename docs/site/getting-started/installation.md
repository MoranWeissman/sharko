# Installation

This page covers a full production installation of Sharko.

## Prerequisites

- Kubernetes 1.27+ with **ArgoCD** installed and running
- **Helm 3.x** (`helm version` to verify)
- A **GitHub Personal Access Token** (PAT) with `repo` scope, or an Azure DevOps PAT with `Code (Read & Write)` permissions
- (Optional) AWS IAM role with Secrets Manager read access, if using `aws-sm` as the secrets provider

## Basic Installation

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<your-github-pat>
```

Verify the pod is running:

```bash
kubectl get pods -n sharko
kubectl get svc -n sharko
```

## Configuration at Install Time

All configuration is passed as Helm values. Common options:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat> \
  --set gitops.actions.enabled=true \
  --set ai.enabled=true \
  --set ai.provider=openai \
  --set ai.apiKey=<openai-api-key> \
  --set ai.cloudModel=gpt-4o
```

For a full list of values, see the [Configuration reference](configuration.md).

## Using an Existing Secret

If you manage secrets externally (e.g., with Sealed Secrets or External Secrets Operator), point Sharko at your secret:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set existingSecret=my-sharko-secret
```

The secret must contain the keys your configuration requires (e.g., `GITHUB_TOKEN`, `AI_API_KEY`).

## Ingress {#ingress}

For production access, enable the ingress resource:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat> \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=sharko.your-domain.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix \
  --set ingress.tls[0].secretName=sharko-tls \
  --set ingress.tls[0].hosts[0]=sharko.your-domain.com
```

Or use a values file:

```yaml
# sharko-values.yaml
secrets:
  GITHUB_TOKEN: "<github-pat>"

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: sharko.your-domain.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: sharko-tls
      hosts:
        - sharko.your-domain.com
```

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  -f sharko-values.yaml
```

## Upgrading Sharko

```bash
helm upgrade sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko \
  -f sharko-values.yaml
```

## Uninstalling

```bash
helm uninstall sharko -n sharko
```

!!! warning
    Uninstalling does not delete the `sharko-connections` secret containing your connection credentials. Delete it manually if needed: `kubectl delete secret sharko-connections -n sharko`.
