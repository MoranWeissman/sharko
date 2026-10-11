# Operator Installation

This guide is for platform engineers and cluster operators installing Sharko on a cluster.

!!! warning "Sharko v4 is a technical preview, for evaluation and staging environments. It is not supported in production."
    Start with the
    [latest published v4 release](https://github.com/MoranWeissman/sharko/releases/latest)
    and read the [current limitations](../technical-preview.md) before deploying
    it more widely.

    Install only published `v4.0.1`-or-later artifacts. Do not install any
    Sharko chart version below `v4.0.1` — all earlier release lines are
    retired and unsupported. There is no patch for the `v3` line.
    See [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md#why-v300-is-retired).

## What you'll do

1. Install the Sharko chart with Helm.
2. Get the first `admin` password.
3. Open the UI and connect your Git repo and ArgoCD.

## Prerequisites

| You need | Notes |
|---|---|
| Kubernetes | Tested on 1.31. Nearby versions are expected to work. |
| ArgoCD | Installed in the same cluster. Tested on v3.3 to v3.5. The **System** page in Sharko shows the ArgoCD version it found and the range that was tested. See [Install ArgoCD for Sharko](install-argocd.md) if you need to install it. |
| Helm 3 | Run `helm version` to check. |
| A Git repo and a token for it | GitHub, Azure DevOps or Gitea. The token is a personal access token (PAT): a token you create in your Git host's settings that lets Sharko open pull requests on the repo. |
| An AWS IAM role (only if you use AWS Secrets Manager) | Needed only if Sharko reads cluster or addon credentials from AWS Secrets Manager. |

Sharko runs as one pod only. The chart ships `replicaCount: 1`. Leave it
at 1. See [the preview limits](../technical-preview.md).

## Helm Installation

### Minimal Install

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace
```

There is no Helm value for your Git token. You give Sharko the token after
the pod is up, in the Settings UI or with `sharko connect` from the CLI.
Sharko stores it encrypted in the `sharko-connections` Kubernetes Secret.
See [Connections](../user-guide/connections.md#connection) for the steps.

!!! warning "Not using the chart?"

    If you write your own Deployment instead of installing this chart, you
    must set `enableServiceLinks: false` on the Sharko Pod. Without it,
    Kubernetes adds about ten `SHARKO_*` environment variables that nobody
    set, and one of them clashes with a setting Sharko used to read for its
    port. The chart does this for you. See
    [the listen port](configuration.md#service-links).

### Recommended Installation

Use a values file, so all your settings sit in one file you can review:

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
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  -f sharko-values.yaml
```

## Initial Credentials

Sharko starts with one user, `admin`. You choose how that user gets its
first password:

1. **Let Sharko make one** (the default).
2. **Put your own in the values file.** Fine for a test cluster.
3. **Put your own in a Secret you create.** Best for a sensitive
   environment.

### 1. Let Sharko make one (default)

If you set neither `bootstrapAdmin.password` nor
`bootstrapAdmin.existingSecret.name`, Sharko makes a random password on
the first install. You can read it in two places.

**From the `sharko-initial-admin-secret` Secret.** This is the easiest
way:

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

This Secret always holds the current first password. When you reset the
password with `sharko reset-admin` (see below), Sharko writes the new one
into the same Secret, so the same command still works.

Once you have stored the password somewhere safe, you can delete the
Secret:

```bash
kubectl delete secret sharko-initial-admin-secret -n sharko
```

A later `sharko reset-admin` creates it again. If you never want this
Secret to exist, set `bootstrapAdmin.writeInitialSecret: false` in your
values file.

**From the pod log.** Sharko also prints the password once, when it first
starts:

```bash
kubectl logs -n sharko deployment/sharko | grep -A5 "ADMIN CREDENTIAL"
```

The block looks like this:

```
=== BOOTSTRAP ADMIN CREDENTIAL ===
username: admin
password: <the generated password>
This is the only time this credential will be shown. Store it securely.
...
```

It is printed only once. A restart does not print it again. Save it in a
password manager or your secrets store straight away.

#### If you lost the password

Run:

```bash
kubectl exec -n sharko deployment/sharko -- sharko reset-admin
```

This makes a new random password for `admin` and prints it. It also
writes it into `sharko-initial-admin-secret`, unless you set
`bootstrapAdmin.writeInitialSecret: false`.

### 2. Put your own in the values file (`bootstrapAdmin.password`)

For a test cluster, you can set the password in your values file:

```yaml
bootstrapAdmin:
  password: "<your-password>"
```

!!! warning "Not safe in a sensitive environment"
    The password sits in plain text in your values file, and in the
    release history Helm keeps. In a sensitive environment, use option 3.

Sharko does not log a password you chose. The block from option 1 does
not appear.

The password is not written into the Deployment. The chart puts it in its
own Secret, and the pod reads it from there. So
`kubectl get deployment -o yaml` and `helm get manifest` show only a
reference to the Secret, never the password.

### 3. Put your own in a Secret you create (best for a sensitive environment)

Create a Secret in the Sharko namespace, then point Helm at it:

```bash
kubectl create secret generic sharko-bootstrap-admin \
  -n sharko \
  --from-literal=password="$(openssl rand -base64 24)"
```

```yaml
bootstrapAdmin:
  existingSecret:
    name: sharko-bootstrap-admin
    key: password   # the default; change it if your Secret uses another key
```

The pod reads the password from your Secret when it starts. Sharko stores
only a hash of it and never logs it.

To change the password, update your Secret and restart the pod:

```bash
kubectl create secret generic sharko-bootstrap-admin -n sharko \
  --from-literal=password="$(openssl rand -base64 24)" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart -n sharko deployment/sharko
```

### Changing the password from the UI

After you sign in with the first password, change it: open the user menu
at the top right, choose **Account**, and fill in **Current Password**,
**New Password** and **Confirm New Password**. The new password must be
at least 12 characters.

From the API, send `POST /api/v1/auth/update-password` with this body:

```json
{ "current_password": "<old>", "new_password": "<new>" }
```

## Port-Forward for First Access

Before you set up an Ingress (or to use the CLI during setup):

```bash
kubectl port-forward svc/sharko -n sharko 8080:80
```

Open [http://localhost:8080](http://localhost:8080).

## Ingress Setup

Set up an Ingress so people can reach the UI and API from outside the
cluster. This example uses nginx-ingress, with cert-manager for TLS:

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
# Check the pod is running
kubectl get pods -n sharko

# Check the service
kubectl get svc -n sharko

# Check the health endpoint
kubectl exec -n sharko deploy/sharko -- \
  wget -qO- http://localhost:8080/api/v1/health
```

The health answer starts with `{"status":"healthy"` and also shows the
Sharko version.

## Upgrading Sharko

See [Upgrading and Rolling Back](upgrading-and-rollback.md). It covers
`helm upgrade`, moving the engine version in your repo, `helm rollback`,
and what is kept when the pod restarts.
