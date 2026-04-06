# Operator Installation

This guide is for platform engineers and cluster operators installing Sharko in a production environment.

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Kubernetes 1.27+ | Any CNCF-conformant distribution |
| ArgoCD | Must be installed and accessible from within the cluster |
| Helm 3.x | `helm version` to verify |
| GitHub PAT or Azure DevOps PAT | For GitOps write operations |
| (Optional) AWS IAM role | If using AWS Secrets Manager as the credentials provider |

## Helm Installation

### Minimal Install

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat>
```

### Recommended Production Install

Use a values file for production deployments:

```yaml
# sharko-values.yaml
secrets:
  GITHUB_TOKEN: "<github-pat>"

config:
  connectionSecretName: "sharko-connections"
  devMode: false

gitops:
  actions:
    enabled: true

ingress:
  enabled: true
  className: nginx
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
  hosts:
    - host: sharko.your-domain.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: sharko-tls
      hosts:
        - sharko.your-domain.com

resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"
    cpu: "500m"
```

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  -f sharko-values.yaml
```

## Retrieve the Admin Password

On first install, an admin account is created with a randomly generated password. Retrieve it:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

!!! note
    This password is stored in the Helm release secret. If you delete and reinstall, a new password is generated. Store the initial password securely.

## Port-Forward for First Access

Before ingress is configured (or for CLI access during setup):

```bash
kubectl port-forward svc/sharko -n sharko 8080:80
```

Open [http://localhost:8080](http://localhost:8080).

## Production: Ingress Setup

For production, configure ingress so the UI and API are reachable from outside the cluster. The example below uses nginx-ingress with cert-manager for TLS:

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
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

## Verify Installation

```bash
# Check pod is running
kubectl get pods -n sharko

# Check service
kubectl get svc -n sharko

# Check health endpoint
kubectl exec -n sharko deploy/sharko -- \
  wget -qO- http://localhost:8080/api/v1/health
```

Expected health response: `{"status":"ok"}`

## Upgrading Sharko

```bash
helm upgrade sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko \
  -f sharko-values.yaml
```

Check the [releases page](https://github.com/MoranWeissman/sharko/releases) for changelogs before upgrading.
