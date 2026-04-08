# Installation

## Prerequisites

- Kubernetes 1.27+ with **ArgoCD** installed and running
- **Helm 3.x** (`helm version` to verify)

That's it. Connection credentials (Git token, ArgoCD token) are entered through the first-run wizard after install — not at Helm install time.

## Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace
```

Verify the pod is running:

```bash
kubectl get pods -n sharko
```

## AWS Secrets Manager (optional)

If you plan to use AWS Secrets Manager as the secrets provider for cluster credentials, annotate the service account with your IRSA role at install time:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/sharko-role
```

This lets the Sharko pod assume the IAM role via IRSA and read secrets from AWS SM without static credentials.

## Get the Admin Password

Sharko generates a random admin password on first install:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

Save this password — you will use it to log in for the first time.

## Access the UI

**Port-forward (quickest for initial setup):**

```bash
kubectl port-forward svc/sharko 8080:80 -n sharko
```

Open [http://localhost:8080](http://localhost:8080).

**Via Ingress (production):**

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set "ingress.hosts[0].host=sharko.your-domain.com" \
  --set "ingress.hosts[0].paths[0].path=/" \
  --set "ingress.hosts[0].paths[0].pathType=Prefix" \
  --set "ingress.tls[0].secretName=sharko-tls" \
  --set "ingress.tls[0].hosts[0]=sharko.your-domain.com"
```

Or use a values file:

```yaml
# sharko-values.yaml
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
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  -f sharko-values.yaml
```

## What's Next

After accessing the UI, the first-run wizard appears automatically. See [First-Run Wizard](first-run.md) for a step-by-step walkthrough.

## Upgrading Sharko

```bash
helm upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko \
  -f sharko-values.yaml
```

## Uninstalling

```bash
helm uninstall sharko -n sharko
```

!!! warning
    Uninstalling does not delete the `sharko-connections` secret containing your connection credentials. Delete it manually if needed: `kubectl delete secret sharko-connections -n sharko`.
