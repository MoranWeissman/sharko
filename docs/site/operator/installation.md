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

## Initial Credentials

Sharko ships with a single bootstrap `admin` user. There are three ways to set the bootstrap password — pick one based on how production-grade your install is.

### 1. Auto-generated (default)

If you set neither `bootstrapAdmin.password` nor `bootstrapAdmin.existingSecret.name`, Sharko generates a random 16-character password on first install. There are then three ways to retrieve it:

#### (a) Dedicated `sharko-initial-admin-secret` (recommended for production)

Sharko writes a dedicated Secret carrying the plaintext of the **current** initial admin password — mirrors ArgoCD's `argocd-initial-admin-secret` pattern. Retrieve with:

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

The Secret is labeled `app.kubernetes.io/managed-by=sharko` and `app.kubernetes.io/component=bootstrap`, so you can also find it via:

```bash
kubectl get secret -n sharko -l app.kubernetes.io/component=bootstrap
```

The Secret persists across `sharko reset-admin` rotations: each rotation rewrites `data.password` to the **new** plaintext, so this is a stable retrieval path you can rely on after the first install (not just before the first rotation). After rotating, re-fetch with the same `kubectl get` above to read the current value. The reset command also prints the new plaintext to stdout, so both retrieval paths are equivalent.

To remove the secret permanently — for example, once you have stored the password in a vault and never want it accessible via kubectl again — delete it explicitly:

```bash
kubectl delete secret sharko-initial-admin-secret -n sharko
```

The next `sharko reset-admin` will recreate it with the new plaintext (rotation is the default). To prevent the secret from being created or recreated at all, set `bootstrapAdmin.writeInitialSecret: false` in your values file. In opt-out mode `sharko reset-admin` only deletes any stale secret left from a previous install; it does not recreate one.

!!! note "Annotation wording"
    The annotation `sharko.io/initial-secret: rotated-on-reset-admin` reflects the actual lifecycle. Earlier (V124-6.3) versions used the wording `delete-after-first-password-change`, which became misleading once V124-7 made `sharko reset-admin` rotate the secret instead of deleting it. If you see the older annotation on an existing cluster, the next reset-admin (or pod re-bootstrap) will refresh it.

#### (b) Pod logs (always works as fallback)

The credential is also logged ONCE to the pod's stdout in a clearly-marked block:

```bash
kubectl logs -n sharko deployment/sharko | grep -A4 "BOOTSTRAP ADMIN"
```

Expected output:

```
=== BOOTSTRAP ADMIN CREDENTIAL ===
bootstrap admin generated  username=admin password=6x5ayewdTvx833Jg
This is the only time this credential will be shown. Store it securely.
=== END BOOTSTRAP ADMIN CREDENTIAL ===
```

After logging, Sharko removes the marker from the Sharko Secret so the credential is never re-emitted on subsequent restarts. **Store the value somewhere durable immediately** (a password manager, your secrets vault).

#### (c) Sharko Secret marker (only works before first restart)

You can also retrieve the value directly from the Sharko Secret while the `admin.initialPassword` key is still present (i.e. before the first successful pod start):

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

#### Recovery path

If you missed all three windows above (no dedicated secret because you opted out, log scrolled off, marker already deleted), run `kubectl exec -n sharko deployment/sharko -- sharko reset-admin` to mint a fresh random password. The reset command also rotates `sharko-initial-admin-secret` to carry the new plaintext (or, in opt-out mode, deletes any stale copy). The new password is also printed to stdout so you can capture it in the same step.

### 2. Operator-supplied inline (`bootstrapAdmin.password`)

For test environments, you can set the password directly in your values file:

```yaml
bootstrapAdmin:
  password: "MyChosenBootstrap!42"
```

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set bootstrapAdmin.password='MyChosenBootstrap!42'
```

!!! warning "Insecure for production"
    The plaintext password lives in your Helm values file (and any release-history Secret Helm keeps). Use `bootstrapAdmin.existingSecret` for production installs.

Sharko bcrypt-hashes the value into `admin.password` and **does NOT log it**. The `BOOTSTRAP ADMIN CREDENTIAL` block does not appear when an operator-supplied password is in use.

### 3. Operator-supplied via existing Secret (recommended for production)

Pre-create a Secret in the Sharko namespace with the bootstrap password, then point Helm at it:

```bash
kubectl create secret generic sharko-bootstrap-admin \
  -n sharko \
  --from-literal=password="$(openssl rand -base64 24)"
```

```yaml
bootstrapAdmin:
  existingSecret:
    name: sharko-bootstrap-admin
    key: password   # default; override if your Secret uses a different key
```

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set bootstrapAdmin.existingSecret.name=sharko-bootstrap-admin
```

The Sharko deployment exposes the value as the `SHARKO_BOOTSTRAP_ADMIN_PASSWORD` env var via `valueFrom.secretKeyRef`. Sharko consumes it on startup, bcrypt-hashes it into `admin.password`, and **never logs the plaintext**.

To rotate the password, update the Secret and restart the pod:

```bash
kubectl create secret generic sharko-bootstrap-admin -n sharko \
  --from-literal=password="$(openssl rand -base64 24)" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart -n sharko deployment/sharko
```

### Changing the password from the UI

Once you have logged in with the bootstrap credential, change the password from **Settings → Users → Change Password** (or `PATCH /api/v1/users/me/password`). The new password is bcrypt-hashed and persisted to the Sharko Secret.

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
