# Git-Native Server Configuration

You can set many of Sharko's server settings and connection settings in Helm
`values.yaml`, so they live in Git with the rest of your setup. When you set
one there, the Helm value wins. If someone changes it later in the UI or the
API, Sharko puts the Helm value back within about 60 seconds.

When you leave a value empty in Helm, nothing changes: you set it in the UI
or the API, and it stays the way you set it.

## How it works

The chart turns each value you set into an environment variable on the
Sharko pod. Sharko reads those values and:

1. **At startup**, writes them over its own stored settings.
2. **Every 60 seconds after that**, does the same again. So if someone
   changes one of these settings in the UI, Sharko puts the Helm value back
   on the next round. You can change the timer with
   `SHARKO_SETTINGS_RECONCILE_INTERVAL`.

If you deploy Sharko itself through ArgoCD, this means the values in your
Git repository are always the ones that count.

A value you leave empty is not set on the pod at all. For that setting,
whatever is stored in Sharko is kept, and the UI and the API are where you
change it.

## Server settings

| Helm value | Type | Default | What it controls |
|------------|------|---------|------------------|
| `settings.probeMode` | string | `""` (not set) | How Sharko checks that a new cluster with no addons can be reached. `"check-app"` (the default) puts a small check Application on the cluster for a short time. `"api-test"` never puts anything on the cluster and only looks at what ArgoCD says about its connection. Leave empty to set it in the UI |
| `settings.allowInlineCredentials` | string | `""` (not set) | The **Allow legacy inline credentials** switch. It decides whether you can add a cluster by pasting a kubeconfig. `"false"` is the default. `"true"` allows it, but a pasted kubeconfig lives only in the cluster's Secret and cannot be got back from Git. Adding a cluster through a connection works either way. Leave empty to set it in the UI |

When you set one of these in Helm, a change through
`PUT /api/v1/settings/probe-mode` or
`PUT /api/v1/settings/allow-inline-credentials` is accepted but put back
within 60 seconds. The `GET` answer for each setting includes
`managed_by_git: true`, so the UI can tell you that your change will not
stay.

**Example:**

```yaml
# charts/sharko/values.yaml
settings:
  probeMode: "check-app"                # the UI cannot change it
  allowInlineCredentials: "false"       # keeps pasting a kubeconfig switched off
```

!!! note "Both settings only work when Sharko runs in Kubernetes"
    Sharko keeps these settings in a Kubernetes ConfigMap. When it runs
    outside Kubernetes there is nowhere to keep them, so both read as their
    defaults and a `PUT` on either returns 503. For
    `allow_inline_credentials` that means adding a cluster by pasting a
    kubeconfig stays refused, with no way to turn it on. See
    [Connections → Allow legacy inline credentials](../user-guide/connections.md#allow-legacy-inline-credentials).

## Connection settings (the parts that are not secret)

The connection is how Sharko reaches your Git repository and ArgoCD. You can
set the parts of it that are not secret in Helm. The tokens are never set
here. Sharko keeps the whole connection encrypted in the
`sharko-connections` Secret. When it applies your Helm values, it changes
only the fields you set and leaves the stored tokens as they are.

!!! warning "The two addresses must carry no credential"
    `connection.git.repoURL` and `connection.argocd.serverURL` are written
    into the pod as ordinary environment values, so anyone who can read the
    Deployment can read them. The chart refuses to install unless it can
    read the address and finds no user information in the host part, no
    query string and no fragment. Those are the only three places a
    credential can sit in an address. `ai.baseURL` and `ai.ollama.url` go
    through the same check.

    The chart also turns away most addresses it cannot read, such as a port
    that is not a number, or a `git@host:org/repo.git` address, which Sharko
    has never supported. It looks at the shape of the address, not at
    whether the host is a real machine, so a broken IPv6 address in square
    brackets such as `[::1::2]:80` still installs. Sharko itself turns that
    one away when it starts.

    You do not have to write `https://`. `https://argocd.example.com`, a
    plain `argocd.example.com` and `localhost:8080` all install. So does an
    `@` inside the path, as in `https://github.com/org/repo@v1`. An ordinary
    `?ref=main` is refused, because the check never guesses which text looks
    secret. Use a plain address. Sharko never reads a credential out of an
    address. The tokens are entered once in the Settings UI.

**Connection fields you can set in Helm:**

```yaml
# charts/sharko/values.yaml
connection:
  git:
    provider: "github"           # "github" | "azuredevops" | "gitea"
    repoURL: "https://github.com/example-org/addons"  # full repo address
    owner: ""                    # GitHub or Gitea owner (read from repoURL when empty)
    repo: ""                     # GitHub or Gitea repo (read from repoURL when empty)
    organization: ""             # Azure DevOps organization
    project: ""                  # Azure DevOps project
    repository: ""               # Azure DevOps repository
  argocd:
    serverURL: "https://argocd.example.com"  # ArgoCD API server address
    namespace: "argocd"          # namespace where ArgoCD is installed
    insecure: "false"            # "true" | "false" — skip checking ArgoCD's TLS certificate (see below)
  # Where cluster credentials come from
  provider:
    type: "argocd"               # "aws-sm" | "k8s-secrets" | "argocd"
    region: "us-east-1"          # AWS region (aws-sm only)
    prefix: "clusters/"          # secret name prefix
    namespace: "sharko"          # Kubernetes namespace (k8s-secrets only)
    roleArn: "arn:aws:iam::000000000000:role/SharkoRole"  # IAM role (a name, not a credential)
  # Where addon secret values come from
  addonSecretProvider:
    type: "aws-sm"
    region: "us-west-2"
    prefix: "addons/"
    namespace: ""
    roleArn: ""
  gitops:
    baseBranch: "main"           # default: "main"
    branchPrefix: "sharko/"      # default: "sharko/"
    commitPrefix: "sharko:"      # default: "sharko:"
    prAutoMerge: "true"          # "true" | "false"
    hostClusterName: "management"  # the cluster ArgoCD runs on
    defaultAddons: "cert-manager,metrics-server"  # comma-separated addon names
```

For what `provider` and `addonSecretProvider` mean, see
[Where Sharko gets cluster credentials](configuration.md#provider-3mech).

`prAutoMerge` and `insecure` take `true`/`false` or `"true"`/`"false"`. Leave
them empty to set them in the UI instead.

**Tokens are never set in values.yaml.** The Git token and the ArgoCD token
stay in the encrypted `sharko-connections` Secret, and you enter them once
in the Settings UI. This block holds only names and settings that are safe
to keep in Git.

**Only the active connection.** These values apply to the active (default)
connection. If there is no connection yet, for example on a fresh install
before anyone entered a token, this block does nothing. Sharko never makes a
connection without its tokens.

### `connection.argocd.insecure` — checking ArgoCD's TLS certificate

Sharko **checks the ArgoCD server's TLS certificate by default**. Sharko
sends its ArgoCD token on that connection. If it accepted any certificate,
anyone on the network path could read the token.

If your ArgoCD serves `https://` with a **self-signed or internal-CA
certificate**, Sharko's connection test, and every call to ArgoCD, fails with
a message like:

> cannot verify the ArgoCD server's TLS certificate — Sharko refuses to send its ArgoCD token over a connection it cannot verify. If this ArgoCD uses a self-signed or internal-CA certificate, either add that CA to the trust store of the Sharko pod, or explicitly turn verification off for this connection: tick "Skip TLS certificate verification" in Settings → Connection, or set the Helm value connection.argocd.insecure=true (env SHARKO_CONN_ARGOCD_INSECURE=true)

Pick one of these. The first is better:

1. **Trust the CA.** Give the Sharko pod your internal CA (mount it into the
   container's trust store). The check then passes with no Sharko setting at
   all. Do this on anything that matters.
2. **Turn the check off for this connection.** Set
   `connection.argocd.insecure: "true"` in Helm values, or tick "Skip TLS
   certificate verification" on the connection in Settings, or send
   `"argocd": {"insecure": true}` on the connection through the API. Sharko
   then skips the check for that one connection, and logs a warning at
   startup so the choice stays visible.

Sharko never tries again without the check on its own. A plain `http://`
ArgoCD address inside the cluster is not affected, because there is no
certificate to check.

## AI settings

The `ai.*` Helm values are listed in
[Configuration → AI Provider](configuration.md#ai-provider).

They work a little differently from the settings above. Sharko applies them
**only at startup**, not every 60 seconds. If someone changes the provider
or the model in the UI, that change stays until the pod restarts. Then the
Helm values are applied again.

`ai.apiKey` is the API key itself. The chart puts it into its own Secret,
never into a ConfigMap. You can leave it empty and enter the key in the
Settings UI instead, where Sharko stores it encrypted.

## Which value wins

For each setting:

1. **You set it in Helm (not empty):**
   - The chart puts it on the pod.
   - Sharko treats it as the right value.
   - A change in the UI or the API is accepted, then put back: at startup
     and every 60 seconds for settings and the connection, and only at
     startup for AI.
   - The settings `GET` answers say `managed_by_git: true`. The connection
     and AI pages show the value from Helm.

2. **You left it empty in Helm:**
   - The chart does not put it on the pod.
   - Whatever is stored in Sharko is kept. The UI and the API are where you
     change it.

3. **You set it to something Sharko cannot use** (for example
   `probeMode: "typo"` or `maxIterations: "not-a-number"`):
   - Sharko logs a warning and acts as if you had left it empty.
   - Sharko still starts.

4. **You set some fields of `provider` or `addonSecretProvider` but not
   `type`:**
   - Sharko logs a warning naming the fields you set, and ignores the whole
     block.
   - `type` is required whenever you set any other field in that block.

## What stays out of Helm values

Secret values are **never** set in Helm `values.yaml` and never written as
plain text into a ConfigMap. These stay in encrypted Secrets, and you enter
them once in the Settings UI:

- **Git connection:** the token (GitHub token, Azure DevOps PAT or Gitea
  token)
- **ArgoCD connection:** the token
- **AI provider:** the API key. `ai.apiKey` is the one exception: if you set
  it, the chart puts it into its own Secret.

When Sharko applies your Helm values to the connection, it leaves these
tokens as they are and changes only the fields that are not secret.

## Things to know

1. **AI settings are applied at startup only**, not every 60 seconds. A UI
   change to the AI provider or model stays until the pod restarts.
2. **Only the active connection.** Sharko can hold more than one
   connection. Helm values only apply to the active (default) one. You
   manage the others in the UI or the API.
3. **Removing a value does not reset it.** Say you set
   `settings.probeMode: "api-test"` and later delete that line. The setting
   is no longer on the pod, so Sharko keeps the last value it had
   (`"api-test"`), and the UI can change it again. It does **not** go back
   to the built-in default. To reset it, set the default in Helm, or change
   it in the UI.

## Example: everything that is not secret, set in Helm

```yaml
# charts/sharko/values.yaml
settings:
  probeMode: "check-app"
  allowInlineCredentials: "false"  # keeps pasting a kubeconfig switched off

connection:
  git:
    provider: "github"
    repoURL: "https://github.com/example-org/k8s-config"
  argocd:
    serverURL: "https://argocd.prod.example.com"
    namespace: "argocd"
    insecure: "false"
  provider:
    type: "argocd"
  addonSecretProvider:
    type: "aws-sm"
    region: "us-west-2"
    prefix: "prod/addons/"
  gitops:
    baseBranch: "main"
    branchPrefix: "sharko/"
    prAutoMerge: "true"
    hostClusterName: "prod-management"
    defaultAddons: "cert-manager,metrics-server,karpenter"

ai:
  enabled: true
  provider: "claude"
  cloudModel: "claude-sonnet-4-20250514"
  maxIterations: 12
```

With this, Git decides every setting that is not secret. A change in the UI
to any of them is put back within 60 seconds (AI settings: at the next
restart). The Git token, the ArgoCD token and the AI API key are still
entered once in the Settings UI and stored encrypted, never in the values
file.

## How to see what Sharko applied

- **Logs:** at startup Sharko logs which settings it took from Helm and
  which it left alone. It logs a warning for any value it could not use.
- **Settings:** `GET /api/v1/settings/probe-mode` and
  `GET /api/v1/settings/allow-inline-credentials` include
  `managed_by_git: true` when the setting comes from Helm.
- **Connection and AI:** the Settings pages, and `GET /api/v1/connections/`
  and `GET /api/v1/ai/config`, show the values Sharko is using now, with
  the Helm values already applied.

## See also

- [Configuration Reference](configuration.md) — all the Helm values
- [Cluster Reconciler](cluster-reconciler.md) — how Sharko keeps ArgoCD's
  cluster Secrets matching your Git repository
- [Marketplace Sources Config](marketplace-sources-config.md) — another
  setting you can keep in Git
