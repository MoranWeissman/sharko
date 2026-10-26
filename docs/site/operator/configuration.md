# Configuration Reference

This page lists the settings you can give Sharko. Most of them are Helm
values in `values.yaml`. A few are environment variables that you pass in
with `extraEnv`.

Some settings can be set in two places: in Helm values, or later in
**Settings** in the UI. When you set one in Helm, the Helm value wins and
Sharko puts it back if someone changes it in the UI. When you leave it
empty in Helm, the UI is where you change it. See
[Git-Native Server Configuration](git-native-config.md) for which settings
work this way.

## The listen port {#listen-port}

The port Sharko's web server listens on is `SHARKO_HTTP_PORT`. The chart
sets it to `8080` and you do not normally touch it.

| Setting | Default | Description |
|---------|---------|-------------|
| `SHARKO_HTTP_PORT` | `8080` | TCP port the server listens on. A whole number from 1 to 65535 |
| `SHARKO_PORT` | — | The old name for the same thing. Still works, and warns once at startup. Do not use it in new installs |

If the value is not a whole number in range, Sharko does not start. It says
which setting is wrong and stops, rather than listening on some other port
and leaving you to work out why nothing reaches it.

If you set `SHARKO_PORT` yourself, it keeps working and Sharko logs one line
at startup telling you the new name. Rename it to `SHARKO_HTTP_PORT` when you
can. Do not set both to different numbers: Sharko will refuse to start
rather than guess which one you meant. Setting both to the same number is
fine. If you install with the Helm chart, the chart already sets
`SHARKO_HTTP_PORT` for you.

### If you write your own Kubernetes manifests {#service-links}

**You must set `enableServiceLinks: false` on the Sharko Pod.** The chart
does this. A Deployment you write by hand will not, unless you add it.

```yaml
spec:
  template:
    spec:
      enableServiceLinks: false   # <- required
      containers:
        - name: sharko
          # ...
```

It belongs in the Pod specification, next to `containers`. It is not a
container field.

Here is what goes wrong without it. Kubernetes puts an environment variable
into every Pod for each Service in the same namespace, named after that
Service. Sharko's own Service is normally called `sharko`, so Sharko's Pod
gets about ten variables starting with `SHARKO_` that nobody asked for,
including:

```
SHARKO_SERVICE_HOST=10.96.35.88
SHARKO_SERVICE_PORT=80
SHARKO_PORT=tcp://10.96.35.88:80
SHARKO_PORT_80_TCP=tcp://10.96.35.88:80
```

The third one has the same name as Sharko's old port setting. Sharko knows
about this. It ignores a `SHARKO_PORT` that looks like the address
Kubernetes writes, so it will not read it as a port, and it will not warn
you about an old setting you never set. But only that one name is handled,
and all of those variables are noise in your Pod. Sharko does not use them.
Turn them off.

Setting the field is the only fix that always works. A list of variable
names to ignore does not work: the names come from the Service name, so
they change as soon as somebody installs under a different release name.

## A setting Sharko does not recognise stops it starting {#unknown-settings}

Every environment variable starting with `SHARKO_` has to be one Sharko
really reads. If you set one it does not know, Sharko names it and does not
start.

```
<the name you set> is not a Sharko setting. Did you mean <the nearest real
one>? Sharko has not started ...
```

The "did you mean" part only appears when a real setting is close enough
to be the one you meant, for example `SHARKO_LOG_LEVEL` with one letter
missing.

This is on purpose. If Sharko accepted the misspelling in silence, you
would have a server running on the default value and no way to tell that
your setting did nothing.

What the check does and does not cover:

- **It only looks at names starting with `SHARKO_`.** The chart also sets
  `CONNECTION_SECRET_NAME`, `ARGOCD_NAMESPACE`, `APP_ENVIRONMENTS`,
  `GITOPS_ACTIONS_ENABLED` and the `AI_*` settings. Those are real
  settings, and this check does not look at them.
- **The message names the setting, never the value.** That is true for
  every message Sharko writes about configuration.
- **The secrets.env file is checked too.** That is the file you can use
  when you run Sharko on your own machine. A misspelling in that file is
  caught the same way.
- **The variables Kubernetes writes are fine.** If you run your own
  manifests without `enableServiceLinks: false` (see
  [above](#service-links)), your Pod has `SHARKO_SERVICE_HOST`,
  `SHARKO_PORT_80_TCP` and others like them. Sharko knows those and starts
  normally. You should still turn service links off.
- **There is no way to switch the check off.** If you need a setting
  Sharko does not have, that needs a change to Sharko. Ask for it in an
  [issue](https://github.com/MoranWeissman/sharko/issues).

If you set a setting that only the end-to-end test setup reads (for example
`SHARKO_E2E_IMAGE_TAG`), Sharko starts. It logs one line saying the name is
set and the server never reads it.

`SHARKO_GITOPS_BASE_BRANCH` and `SHARKO_GITOPS_PR_AUTO_MERGE` are **not**
Sharko settings, so a values file that sets either one will not start.
Set `connection.gitops.baseBranch` and `connection.gitops.prAutoMerge` in
Helm values instead. The chart turns those into the real settings,
`SHARKO_CONN_GITOPS_BASE_BRANCH` and `SHARKO_CONN_GITOPS_PR_AUTO_MERGE`.
See [Git-Native Server Configuration](git-native-config.md).

## Server settings you set as environment variables {#env-settings}

You set these with `extraEnv`, or however your own manifests set
environment variables. None of them has a Helm value of its own.

| Setting | Default | What it does |
|---------|---------|--------------|
| `SHARKO_CORS_ORIGIN` | unset — same-origin only | Which web origin may call the API from a browser. Read [below](#cors-origin) before you set this |
| `SHARKO_API_TOKENS_FILE` | `~/.sharko/api-tokens.json` | Where API tokens are saved when Sharko is NOT running in Kubernetes. In Kubernetes this setting does nothing: tokens go into the `sharko-api-tokens` Secret instead. The file holds bcrypt hashes, never the tokens themselves, and is written `0600` |
| `SHARKO_REPO_PATH_MANAGED_CLUSTERS` | `configuration/managed-clusters.yaml` | Only for a repo in the older (v3) layout: where the list of managed clusters lives. A v4 repo always keeps this list in `managed-clusters.yaml` at the repo root, so leave this setting alone there |
| `SHARKO_SETTINGS_RECONCILE_INTERVAL` | `60s` | How often Sharko puts server settings back to what your Helm values say. Only runs in Kubernetes. A value that is not a duration is ignored with a warning and the default is used |
| `SHARKO_CONNECTION_CHECK_INTERVAL` | `60s` | How often Sharko checks its own two connections (Sharko to Git, and ArgoCD to the repository) and raises or clears the bell alert. A value that is not a positive duration is ignored with a warning and the default is used |
| `SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL` | `15m` | How often Sharko checks, in the background, whether a cluster's stored credentials still match what the connection says. It only looks. Repairing is still a click. A value that is not a duration is ignored with a warning and the default is used |
| `SHARKO_SIGSTORE_TUF_CACHE` | `/tmp/sigstore-tuf` | Folder where Sharko keeps the Sigstore trust data it uses to check catalog signatures. The default is inside the container and is lost on restart, which costs one download. Point it at a mounted volume to keep it |

Durations are written like `30s`, `5m` or `1h30m`.

### `SHARKO_CORS_ORIGIN` — leave it unset {#cors-origin}

**Short version: leave it unset. If you must set it, set one exact origin.
Never set it to `*`.**

When it is unset, Sharko answers browser calls from its own address and
nowhere else. That is what you want almost always, including when the UI
and the API are the same Sharko you are already logged into.

Setting it to one origin allows that one origin. Include the scheme, and no
slash at the end:

```yaml
extraEnv:
  - name: SHARKO_CORS_ORIGIN
    value: "https://sharko.example.com"
```

Sharko does not check what you put here. A typo, a slash at the end or the
wrong scheme will never match a real browser origin. The calls you wanted
to allow will keep being refused, and no message will say why. Copy the
origin out of the browser's address bar.

**Why `*` is the dangerous one.** Sharko uses no cookies anywhere and
never sends the header that would let a browser attach your credentials to
a call from another site. So `*` cannot hand out anything that already
needs a login. What it does open is everything that needs no login, and
that includes `POST /api/v1/auth/login`, which returns a working session
token.

Put plainly: with `*` set on a Sharko your colleagues can reach, any web
page any of them opens can keep trying passwords against your Sharko from
inside your network, and read the answers. The only thing slowing it down
is the login limit of five tries per minute per address. `*` also opens the
built-in UI files and `/swagger/` to any page, because this check runs
before Sharko knows who is calling.

There is no case where `*` is the right answer on a real installation.

## Connection Config

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `config.connectionSecretName` | string | `"sharko-connections"` | Name of the Kubernetes Secret where connections are stored (encrypted) |
| `config.nodeAccess` | bool | `true` | Lets Sharko read Kubernetes Nodes (get/list) across the whole cluster it runs on. **On unless you turn it off.** It adds a rule to the cluster-wide role and feeds the node count on the Dashboard. Set it to `false` and that widget shows nothing |
| `config.environments` | string | `""` | Comma-separated words Sharko looks for in cluster names to guess each cluster's environment. Example: `"dev,qa,staging,prod"` — a cluster called `"my-app-prod-eks"` gets the environment `"prod"` |

The address of your addons repo is `connection.git.repoURL`. It is one of
the connection fields you can set in Helm, listed in
[Git-Native Server Configuration](git-native-config.md).

### Dev mode (credential env var fallback)

`SHARKO_DEV_MODE` is an environment variable, not a Helm value. Set it to
`"true"` with `extraEnv` and Sharko will read tokens from the `GITHUB_TOKEN`,
`ARGOCD_TOKEN`, `AZURE_DEVOPS_PAT` and `GITEA_TOKEN` environment variables
when they are not set in the Settings UI. Use it only on your own machine,
never on a shared Sharko.

```yaml
extraEnv:
  - name: SHARKO_DEV_MODE
    value: "true"
  - name: GITHUB_TOKEN
    value: "ghp_xxxx"
```

## Signing in {#auth}

On first install, Sharko creates an `admin` account with a random password
and puts that password in the `sharko-initial-admin-secret` Secret. You can
also choose the first password yourself with `bootstrapAdmin.password` or
`bootstrapAdmin.existingSecret`. See
[Installation](installation.md#initial-credentials)
for both ways, and [Security](security.md#admin-password) for how passwords
are kept.

When you run Sharko on your own machine, outside Kubernetes, you can set
`SHARKO_AUTH_USER` and `SHARKO_AUTH_PASSWORD` instead.

## Git Token

There is no Helm value for the Git token (GitHub, Azure DevOps or Gitea).
You enter it after install, either in **Settings → Connection** in the UI or
with `sharko connect --git-token ...` on the command line. Sharko stores it
encrypted in the `sharko-connections` Kubernetes Secret and never writes it
into a Helm values file, so it never shows up in `helm get values`. See
[Connections](../user-guide/connections.md#connection) for the steps.

The only other way is `SHARKO_DEV_MODE` (see
[Dev mode](#dev-mode-credential-env-var-fallback) above), which is for your
own machine only.

## GitOps Actions

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `gitops.actions.enabled` | bool | `false` | Lets Sharko write: open pull requests from the UI and from the AI assistant. Set to `true` to allow Sharko to push branches and open pull requests |

## AI Provider {#ai-provider}

These Helm values set up the AI assistant. For what the AI features do,
how much they cost and how to watch them, see
[AI configuration](ai-config.md).

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `ai.enabled` | bool | `false` | Turns the AI assistant on |
| `ai.provider` | string | `""` | One of: `ollama`, `claude`, `openai`, `gemini`, `custom-openai`. Empty means no provider |
| `ai.apiKey` | string | `""` | API key for a cloud provider (claude, openai, gemini). The chart puts it in its own Secret |
| `ai.cloudModel` | string | `""` | Model name, for example `gpt-4o` or `gemini-2.5-flash` |
| `ai.baseURL` | string | `""` | Base address for a `custom-openai` provider, such as a company AI gateway |
| `ai.authHeader` | string | `""` | Header name the `custom-openai` provider expects the key in (default: `Authorization`) |
| `ai.maxIterations` | int | `8` | How many tool-calling steps the assistant may take for one question. Raise it for longer tasks |

!!! warning "`ai.baseURL` and `ai.ollama.url` must carry no credential"
    Both are written into the pod as ordinary environment values, so anyone
    who can read the Deployment can read them. The chart refuses to install
    unless it can read the address and finds no user information in the host
    part, no query string and no fragment. Those are the only three places a
    credential can sit in an address. It also turns away most addresses it
    cannot read, but it looks at the shape of the address rather than at
    whether the host is a real machine, so a broken IPv6 address in square
    brackets can still install. Nothing that carries a credential gets
    through either way. `connection.git.repoURL` and
    `connection.argocd.serverURL` go through the same chart check — see
    [Git-Native Server Configuration](git-native-config.md). The credential
    for a cloud provider is `ai.apiKey`, which goes into the chart's Secret.

### Ollama (self-hosted)

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `ai.ollama.deploy` | bool | `false` | Run an Ollama pod next to Sharko |
| `ai.ollama.image` | string | `"ollama/ollama:0.6.8"` | Ollama container image (about 1.2 GB compressed). It is a fixed version, not `latest`, so two installs a month apart run the same thing |
| `ai.ollama.url` | string | `""` | Address of an Ollama you already run. Filled in for you when `deploy` is `true` |
| `ai.ollama.model` | string | `"llama3.2"` | Model for simple questions |
| `ai.ollama.agentModel` | string | `""` | A second model for tool-calling. Leave empty to use `model` |
| `ai.ollama.gpu` | bool | `false` | Use a GPU (needs the NVIDIA device plugin on the cluster) |
| `ai.ollama.persistence` | bool | `false` | Keep downloaded models across restarts. **Strongly recommended** — without it, models are downloaded again every time the pod restarts |
| `ai.ollama.storageClassName` | string | `""` | StorageClass for the model volume (empty = cluster default) |
| `ai.ollama.storageSize` | string | `"10Gi"` | Volume size. 10 Gi fits 1–2 small models; use 50 Gi or more for larger ones |

**How much memory each model needs:**

| Model | RAM | Tool Calling |
|-------|-----|-------------|
| `llama3.2` (3B) | 2–4 GB | Weak |
| `llama3.1:8b` | 6–8 GB | Moderate |
| `qwen2.5` (7B) | 4–6 GB | Good |
| `mistral` (7B) | 4–6 GB | Moderate |
| `llama3.1:70b` | 40+ GB | Strong (needs GPU) |

## Resources

| Value | Default | Description |
|-------|---------|-------------|
| `resources.requests.memory` | `128Mi` | Memory request |
| `resources.requests.cpu` | `100m` | CPU request |
| `resources.limits.memory` | `512Mi` | Memory limit |
| `resources.limits.cpu` | `500m` | CPU limit |

Change these to fit how many clusters you have and how busy Sharko is. For
large fleets (100 or more clusters), think about raising the memory limit.

## Probes

The liveness and readiness probes call `/api/v1/health`. The defaults work
for most installs:

| Value | Default |
|-------|---------|
| `livenessProbe.initialDelaySeconds` | `5` |
| `livenessProbe.periodSeconds` | `10` |
| `readinessProbe.initialDelaySeconds` | `3` |
| `readinessProbe.periodSeconds` | `5` |

## Security Context

By default Sharko runs as a non-root user with a read-only root filesystem:

| Value | Default |
|-------|---------|
| `podSecurityContext.runAsNonRoot` | `true` |
| `podSecurityContext.runAsUser` | `1001` |
| `securityContext.readOnlyRootFilesystem` | `true` |
| `securityContext.allowPrivilegeEscalation` | `false` |
| `securityContext.capabilities.drop` | `["ALL"]` |

## Persistence

| Value | Default | Description |
|-------|---------|-------------|
| `persistence.enabled` | `false` | Back the `/app/data` folder with a PersistentVolumeClaim |
| `persistence.storageClassName` | `""` | StorageClass (empty = cluster default) |
| `persistence.size` | `1Gi` | Volume size |

You can leave this off. When Sharko runs in Kubernetes it keeps the things
it needs to remember in Kubernetes Secrets and ConfigMaps, not in
`/app/data`. For example, cleared notifications stay cleared because they
are kept in the `sharko-notifications` ConfigMap.

## Scheduling

| Value | Description |
|-------|-------------|
| `nodeSelector` | Node selector labels |
| `tolerations` | Pod tolerations |
| `affinity` | Affinity and anti-affinity rules |
| `hostAliases` | Extra host names for private DNS |

## Where Sharko gets cluster credentials {#provider-3mech}

Sharko needs a kubeconfig, or something it can turn into one, to talk to
each cluster you register. You choose where those credentials live in
**Settings → Secrets Provider**, or with the Helm value
`connection.provider.type`. There are three choices:

| `connection.provider.type` | Where the credentials are |
|----------------------------|---------------------------|
| `argocd` | In the ArgoCD cluster Secret for that cluster. This is what Sharko uses when it runs in a cluster and you set nothing |
| `aws-sm` | In AWS Secrets Manager. See [the secret formats](#aws-secrets-manager-secret-formats) below |
| `k8s-secrets` | In Kubernetes Secrets. The Secret is named after the cluster, and the kubeconfig is under the key `kubeconfig`. The namespace is `connection.provider.namespace` (default `sharko`) |

One exception: a cluster you add by pasting a kubeconfig keeps its
credentials only in its ArgoCD cluster Secret, whatever you chose above.

Google Secret Manager (`gcp-sm`) and Azure Key Vault (`azure-kv`) are not
built yet. See [Azure and GCP providers](azure-gcp-provider-unimplemented.md).

For the full picture of how Sharko signs in to clusters, read
[Cluster Connectivity Model](cluster-connectivity-model.md).

### Where addon secrets come from

Some addons need secret values, such as an API key, on each cluster. Sharko
reads those values from a secrets backend and writes them into the cluster
as Kubernetes Secrets labelled `app.kubernetes.io/managed-by: sharko`. See
[Addon Secrets](../user-guide/addons.md#addon-secrets) for how an addon
asks for them.

The backend for addon secret values is set with
`connection.addonSecretProvider` (same fields as `connection.provider`), or
in the Settings UI. It accepts `aws-sm` and `k8s-secrets`. It does not
accept `argocd`, because ArgoCD holds cluster credentials, not addon
values. If you leave `connection.addonSecretProvider` empty, Sharko uses
the cluster credentials settings from `connection.provider` for addon
secrets too.

### Which namespace ArgoCD is in

```yaml
# charts/sharko/values.yaml
clusterTest:
  # The namespace where ArgoCD keeps its cluster Secrets.
  # Leave empty to use "argocd".
  argocdNamespace: ""
```

Sharko picks the namespace in this order:

1. `clusterTest.argocdNamespace`, when it is not empty.
2. `SHARKO_ARGOCD_NAMESPACE`. This is the old way. It still works, and
   Sharko warns once at startup. Move to `clusterTest.argocdNamespace`.
3. `argocd`, the namespace a standard ArgoCD install uses.

`rbac.argocdNamespace` is a separate value. It sets where the chart creates
the Role and RoleBinding that let Sharko read and write ArgoCD's Secrets. On
a standard install both values are `argocd`.

### `clusterRegSource` — leave it empty

The chart has a `clusterRegSource` block (`clusterRegSource.type` and
`clusterRegSource.argocdNamespace`). Sharko reads it at startup, logs the
values, and then does nothing with them. Leave both empty.

This does not switch anything off. Sharko writes the ArgoCD cluster Secret
for each cluster in `managed-clusters.yaml` either way. See
[Cluster Reconciler](cluster-reconciler.md).

## AWS Secrets Manager — Secret Formats {#aws-secrets-manager-secret-formats}

When cluster credentials come from `aws-sm`, each cluster's secret in AWS
Secrets Manager can be stored in one of two formats. Sharko works out which
one it is by itself.

### Format 1 — a kubeconfig

The secret value is a kubeconfig, written as YAML:

```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://...
    certificate-authority-data: <base64>
  name: my-cluster
# ... (full kubeconfig)
```

### Format 2 — JSON describing an EKS cluster

The secret value is a JSON object describing an EKS cluster. Sharko treats a
value as this format when it is JSON **and** has a `host` key that is not
empty. Sharko then creates a short-lived token for the cluster each time,
instead of reading a stored credential:

```json
{
  "clusterName": "prod-eu",
  "host": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "caData": "<base64-encoded-ca-data>",
  "region": "us-east-1",
  "roleArn": "arn:aws:iam::111122223333:role/EKSReadRole"
}
```

The field names must match exactly:

| Field | Required | Meaning |
|-------|----------|---------|
| `host` | yes | The cluster's API server address. This is also how Sharko knows the format |
| `caData` | yes | The cluster's CA certificate, base64-encoded |
| `clusterName` | yes | The EKS cluster name. Sharko puts it in the token request |
| `region` | recommended | The AWS region to ask for the token in |
| `roleArn` | optional | The IAM role to take on when creating the token for this cluster |

A role can come from three places. Sharko uses the first one it finds:

1. The `roleArn` in the secret itself (above).
2. The role saved on the cluster's entry in `managed-clusters.yaml` when it
   was registered. This is how a cluster found by discovery in another AWS
   account keeps using the role that found it.
3. The default role on the connection (`connection.provider.roleArn`).

When all three are empty, Sharko takes on no role and uses the pod's own
AWS identity (IRSA).

The token Sharko creates is a `k8s-aws-v1.*` bearer token. AWS makes it
stop working after about 15 minutes. Sharko makes a new one before the old
one runs out, so the connection keeps working without you doing anything. If
Sharko cannot make a new token, the cluster shows as a connection problem
and not as connected.

If you want to use a fixed bearer token instead, store a full kubeconfig
(Format 1) with the token inside it. A bare JSON object with only `server`
and `token` in it is not a format Sharko reads.

### IRSA Setup {#irsa-setup}

IRSA (IAM Roles for Service Accounts) gives the Sharko pod its own AWS
identity. To create EKS tokens, the pod needs an IAM role that can read the
secrets and take on the target roles:

```yaml
# charts/sharko/values.yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/SharkoIRSARole"
```

Permissions the Sharko IRSA role needs:

```json
{
  "Effect": "Allow",
  "Action": [
    "secretsmanager:GetSecretValue",
    "secretsmanager:ListSecrets"
  ],
  "Resource": "*"
}
```

For EKS clusters in other AWS accounts, also add:

```json
{
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": "arn:aws:iam::*:role/EKSReadRole"
}
```

### Give Sharko an AWS login when it does not run on EKS {#aws-login-outside-eks}

Sharko needs its own AWS login to read secrets from AWS Secrets Manager and
to make EKS tokens. Where the login comes from depends on where Sharko runs.

**On EKS.** Use IRSA (see above) or EKS Pod Identity. The cluster gives the
pod its login. You do not store a key anywhere.

**Anywhere else** (kind, on-premises, or another cloud). The pod has no AWS
login of its own, so you give it an access key. The key belongs to an IAM
user. It has to be allowed to do only what Sharko needs (the policy is
below).

1. Put the key in a Kubernetes Secret in the Sharko namespace:

    ```bash
    kubectl create secret generic sharko-aws-login \
      --namespace sharko \
      --from-literal=AWS_ACCESS_KEY_ID=<your access key id> \
      --from-literal=AWS_SECRET_ACCESS_KEY=<your secret access key>
    ```

2. Tell the chart to give those two values to the pod, together with the
   AWS region, in `extraEnv` (see
   [Extra Environment Variables](#extra-environment-variables)):

    ```yaml
    extraEnv:
      - name: AWS_ACCESS_KEY_ID
        valueFrom:
          secretKeyRef:
            name: sharko-aws-login
            key: AWS_ACCESS_KEY_ID
      - name: AWS_SECRET_ACCESS_KEY
        valueFrom:
          secretKeyRef:
            name: sharko-aws-login
            key: AWS_SECRET_ACCESS_KEY
      - name: AWS_REGION
        value: eu-west-1
    ```

3. Install or upgrade with that file, for example
   `helm upgrade --install sharko ... -f aws-login-values.yaml`.

4. In **Settings → Secrets Provider**, choose AWS Secrets Manager and enter
   the same region.

The smallest policy for the IAM user. It reads only the secrets whose names
start with `sharko/`, lists secrets, and takes on the EKS role. Change
the account number, the region, the prefix and the role name to yours:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:eu-west-1:123456789012:secret:sharko/*"
    },
    {
      "Effect": "Allow",
      "Action": "secretsmanager:ListSecrets",
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": "arn:aws:iam::123456789012:role/SharkoEKSRole"
    }
  ]
}
```

`secretsmanager:ListSecrets` can only be given for all resources. AWS does
not let you limit it to a prefix. Leave out the `sts:AssumeRole` part if
you do not use an IAM role for EKS clusters.

**You cannot change this key in the Sharko screens.** The key is read once,
when the pod starts. To change it, update the Secret, then restart the pod:

```bash
kubectl patch secret sharko-aws-login --namespace sharko \
  --type merge -p '{"stringData":{"AWS_SECRET_ACCESS_KEY":"<new secret access key>","AWS_ACCESS_KEY_ID":"<new access key id>"}}'
kubectl rollout restart deployment/sharko --namespace sharko
```

If the key is wrong or has no permission, AWS answers and refuses. Sharko
then shows that it cannot read the secret. That is a problem with this
login, not a network problem.

## Default addons and the host cluster

Two connection settings you can set in Helm values:

- `connection.gitops.defaultAddons` — a comma-separated list of addons that
  are switched on when you register a cluster without choosing any, for
  example `"cert-manager,metrics-server"`.
- `connection.gitops.hostClusterName` — the name of the cluster Sharko and
  ArgoCD run on.

See [Git-Native Server Configuration](git-native-config.md) for the rest of
the `connection` block.

## Addon secret sync timer and the Git webhook {#secrets-reconciler}

Sharko copies addon secret values onto your clusters on a timer. It starts
by itself as soon as a credentials backend and an addon-secret backend are
both set. The timer is the `SHARKO_SECRET_RECONCILE_INTERVAL` environment
variable, `5m` unless you change it. There is no Helm value that switches
this sync on or off or sets its timer. If your values file has a reconciler
block under `secrets`, delete it: the chart has never read it.

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `secrets.webhookSecret` | string | `""` | The shared secret your Git host signs webhook calls with. Becomes `SHARKO_WEBHOOK_SECRET` |

While `secrets.webhookSecret` is empty, which is the default, Sharko refuses
every call to `POST /api/v1/webhooks/git`. Nothing else stops working:
Sharko notices changes on its own anyway, and the webhook only makes it
notice sooner. To switch the webhook on, set the same secret here and in
your Git host's webhook settings. See
[Webhook Security](security-network-and-addresses.md#webhook-security) for how the check works.

```yaml
# Example: webhook secret, and a different sync timer
secrets:
  webhookSecret: "your-hmac-secret"
extraEnv:
  - name: SHARKO_SECRET_RECONCILE_INTERVAL
    value: "10m"
```

The webhook secret can also come from a Secret you already have:

```yaml
extraEnv:
  - name: SHARKO_WEBHOOK_SECRET
    valueFrom:
      secretKeyRef:
        name: my-webhook-secret
        key: secret
```

## ArgoCD Resource Exclusions {#argocd-resource-exclusions}

Sharko writes Kubernetes Secrets labelled `app.kubernetes.io/managed-by:
sharko` onto your clusters when it delivers addon secrets. Without an
exclusion rule, ArgoCD may try to manage those Secrets, or delete them,
when it syncs.

### Required configuration

Add this block to your `argocd-cm` ConfigMap in the `argocd` namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.exclusions: |
    - apiGroups: [""]
      kinds: ["Secret"]
      clusters: ["*"]
      labelSelector:
        matchLabels:
          app.kubernetes.io/managed-by: sharko
```

Apply it with:

```bash
kubectl apply -f argocd-cm-patch.yaml
```

Or change it in place:

```bash
kubectl patch configmap argocd-cm -n argocd --type merge -p '
{
  "data": {
    "resource.exclusions": "- apiGroups: [\"\"]\n  kinds: [\"Secret\"]\n  clusters: [\"*\"]\n  labelSelector:\n    matchLabels:\n      app.kubernetes.io/managed-by: sharko\n"
  }
}'
```

!!! warning "Restart ArgoCD after the change"
    After you change `argocd-cm`, restart the ArgoCD application controller
    so it picks up the change:
    ```bash
    kubectl rollout restart deployment argocd-application-controller -n argocd
    ```

### Checking the exclusion

Ask Sharko whether the exclusion is set:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  https://<sharko-host>/api/v1/argocd/resource-exclusions | jq .
```

When it is set:

```json
{
  "configured": true,
  "detail": "resource.exclusions is configured with a sharko Secret exclusion"
}
```

When it is not set:

```json
{
  "configured": false,
  "detail": "argocd-cm has no resource.exclusions configured",
  "recommendation": "Add the following to your argocd-cm ConfigMap ..."
}
```

## Extra Environment Variables

Add any environment variables you need to the Sharko pod:

```yaml
extraEnv:
  - name: SHARKO_LOG_LEVEL
    value: "debug"
  - name: SHARKO_CONNECTION_CHECK_INTERVAL
    value: "5m"
```

Names starting with `SHARKO_` have to be settings Sharko really reads — see
[A setting Sharko does not recognise stops it starting](#unknown-settings).
