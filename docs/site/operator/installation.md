# Operator Installation

This guide is for platform engineers and cluster operators installing Sharko in a production environment.

!!! danger "Don't install yet — there is no release to install"
    The published chart still installs `v3.0.0`, which is retired and unsafe.
    Do not install it. There is no patch for the `v3` line — wait for `v4`.
    See [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md#why-v300-is-retired).

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Kubernetes | Any CNCF-conformant distribution. Sharko's e2e suite is tested against **1.31** in CI (kind's `kindest/node:v1.31.0`, the harness default). Sharko's Kubernetes client library (`client-go` v0.35.2) follows the standard Kubernetes version-skew policy, so nearby minor versions in both directions are expected to interoperate, but 1.31 is the only version CI actually exercises today. |
| ArgoCD | Must be installed and accessible from within the cluster. Tested weekly against the three newest ArgoCD minor releases — the CI-verified range is currently **v3.2–v3.4** (last confirmed 2026-07-05 by the `argocd-matrix` workflow). The running app's **System** page shows the detected ArgoCD version against this same tested range. |
| Helm 3.x | `helm version` to verify |
| GitHub PAT or Azure DevOps PAT | For GitOps write operations |
| (Optional) AWS IAM role | If using AWS Secrets Manager as the credentials provider |

## Helm Installation

### Minimal Install

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace
```

There is no Helm value for the GitHub PAT (or Azure DevOps PAT). Sharko never
takes a Git token as a chart value — you give it one after the pod is up, via
the Settings UI (or `sharko connect` from the CLI). Sharko stores it encrypted
in the `sharko-connections` Kubernetes Secret. See
[Connections](../user-guide/connections.md#connection) for the steps.

!!! warning "Not using the chart?"

    If you write your own Deployment instead of installing this chart, you
    must set `enableServiceLinks: false` on the Sharko Pod. Without it
    Kubernetes injects around ten `SHARKO_*` environment variables that no
    operator set, one of which collides with Sharko's own former port
    setting. The chart does this for you. See
    [the listen port](configuration.md#service-links).

### Recommended Production Install

Use a values file for production deployments:

```yaml
# sharko-values.yaml
config:
  connectionSecretName: "sharko-connections"

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

If you set neither `bootstrapAdmin.password` nor `bootstrapAdmin.existingSecret.name`, Sharko generates a random 16-character password on first install. There are then two ways to retrieve it:

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
    The annotation `sharko.dev/initial-secret: rotated-on-reset-admin` reflects the actual lifecycle. Earlier (V124-6.3) versions used the wording `delete-after-first-password-change`, which became misleading once V124-7 made `sharko reset-admin` rotate the secret instead of deleting it. If you see the older annotation on an existing cluster, the next reset-admin (or pod re-bootstrap) will refresh it.

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

!!! note "There is no third window"
    Earlier Sharko versions could be queried directly for the password
    while it briefly sat on the release Secret's `admin.initialPassword`
    key (`kubectl get secret sharko -o jsonpath='{.data.admin\.initialPassword}'`).
    That key is deleted in the same startup step that logs the password
    and writes the dedicated secret, so by the time you could run the
    command it already returns empty — don't rely on it. Use (a) or (b)
    above.

#### Recovery path

If you missed both windows above (no dedicated secret because you opted out, log scrolled off), run `kubectl exec -n sharko deployment/sharko -- sharko reset-admin` to mint a fresh random password. The reset command also rotates `sharko-initial-admin-secret` to carry the new plaintext (or, in opt-out mode, deletes any stale copy). The new password is also printed to stdout so you can capture it in the same step.

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

The password does not go into the Deployment. The chart writes the plaintext into its own Secret under the key `admin.bootstrapPassword`, and the pod reads it from there with `valueFrom.secretKeyRef` — the same way the existing-Secret path below works. So `kubectl get deployment -o yaml`, `helm get manifest` and a rendered-manifest Git repository all show a reference and never the value.

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
