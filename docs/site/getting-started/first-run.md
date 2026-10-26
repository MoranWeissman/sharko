# First-Run Wizard

When you access Sharko for the first time, a setup wizard appears automatically. It walks you through every configuration step needed before the dashboard is usable. You cannot accidentally skip it — but you can dismiss it at any step with the X button if you prefer to configure things manually later.

## Before You Start

Have these ready:

- **Git personal access token** — GitHub PAT with `repo` scope, or Azure DevOps PAT with `Code (Read & Write)` permissions
- **Addons repo URL** — the Git repository where Sharko will store cluster configuration and addon assignments (can be an existing repo or a new empty one)
- **ArgoCD account token** — an ArgoCD account token with at minimum `applications:get` and `projects:get` permissions

## Make the ArgoCD token first

Step 3 of the wizard asks for an ArgoCD token. Make it before you open the wizard. You need the `argocd` command line tool, and ArgoCD must already be installed (see [Install ArgoCD for Sharko](../operator/install-argocd.md)). The commands below use the namespace `argocd`. If yours is different, use yours.

Your computer may have more than one cluster in its kubectl settings, and the default one may not be the cluster where ArgoCD runs. So every `kubectl` line below names the cluster with `--context <argocd-context>`. Replace `<argocd-context>` with the name of the kubectl context of the cluster where ArgoCD runs. `kubectl config get-contexts` lists them. Do not leave the flag out.

### 1. Create the `sharko` account and give it rights

Do not use the `admin` account for Sharko. Create an account just for Sharko. Save this as `sharko-account-patch.yaml`:

```yaml
data:
  accounts.sharko: apiKey
```

Apply it:

```bash
kubectl patch configmap argocd-cm -n argocd \
  --context <argocd-context> \
  --type merge --patch-file sharko-account-patch.yaml
```

`apiKey` means the account can have tokens, but it cannot log in with a password.

A new account can do nothing yet. Give it rights with the policy in [Smallest ArgoCD policy for Sharko](../operator/security.md#smallest-argocd-policy-for-sharko). That page has the patch file and the command to apply it. Use the same `--context <argocd-context>` flag on that command.

### 2. Save the ArgoCD admin password to a private file

You need the `admin` password of ArgoCD for the next step. Save it to a private file, so it is not printed on the screen. The `umask 077` part makes the file readable by you only:

```bash
(umask 077; kubectl get secret argocd-initial-admin-secret -n argocd \
  --context <argocd-context> -o jsonpath='{.data.password}' | base64 -d \
  > ~/argocd-admin-password.txt)
```

The password is now in `~/argocd-admin-password.txt`. Delete the file when you have the token (the last part of step 3).

### 3. Make the token

There are two ways. The first one is the simplest.

**Option 1: the ArgoCD web page.**

1. Open the ArgoCD web page. To reach it, run this in a second terminal and go to [http://localhost:8081](http://localhost:8081):

   ```bash
   kubectl port-forward svc/argocd-server 8081:80 -n argocd --context <argocd-context>
   ```

   Log in as `admin`. You must type the password, so this one step must show it on the screen. Do it where nobody is looking: run `cat ~/argocd-admin-password.txt`, copy the password, then clear the terminal with `clear`.
2. In the menu on the left, click **Settings**, then **Accounts**, then the account **sharko**.
3. Under **Tokens**, click **Generate New**.
4. Copy the token right away. ArgoCD shows it only once. You paste it into step 3 of the wizard.

**Option 2: the command line.** Do not start your own `kubectl port-forward` for this. On a kind cluster it drops with `connection reset by peer`, and the login then fails. Let the `argocd` tool open its own port forward instead. This needs the same `<argocd-context>` name as above.

Put the flags in a small function, because every `argocd` command needs the same flags:

```bash
argo() {
  argocd "$@" --port-forward --port-forward-namespace argocd \
    --kube-context <argocd-context> --plaintext
}
```

`--plaintext` is right when ArgoCD serves plain http, as in the test setup in [Install ArgoCD for Sharko](../operator/install-argocd.md). If your ArgoCD uses TLS, take `--plaintext` out.

Log in as `admin`. The password is read from the file, so it is never printed on the screen. Then save the token to a private file too:

```bash
argo login --username admin --password "$(cat ~/argocd-admin-password.txt)"

(umask 077; argo account generate-token --account sharko > ~/sharko-argocd-token.txt)
```

The token is made once, and it is now in `~/sharko-argocd-token.txt`. You paste the token into step 3 of the wizard, so this one step must show it on the screen. Do it where nobody is looking:

```bash
cat ~/sharko-argocd-token.txt
```

Copy the token, then clear the terminal with `clear`.

To see the tokens the account has, run `argo account get --account sharko` (option 2), or look at the **Tokens** list of the account in the web page.

**Whichever option you used, when the wizard has accepted the token,** delete the private files:

```bash
rm -f ~/argocd-admin-password.txt ~/sharko-argocd-token.txt
```

## Step 1: Welcome

An overview of what the wizard will set up. Read through it and click **Get Started**.

## Step 2: Git Connection

Sharko needs a Git repo to store your fleet configuration (the engine pin, your approved-addon catalog, per-cluster assignments, and addon values). All write operations (adding clusters, upgrading addons) create PRs in this repo.

| Field | What to enter |
|-------|--------------|
| **Repository URL** | e.g., `https://github.com/your-org/addons-repo` |
| **Git provider** | Leave it on **Work it out from the address** for GitHub, GitHub Enterprise and Azure DevOps. Choose **GitHub (including GitHub Enterprise)** or **Gitea** yourself when the address does not show which one it is, for example a Gitea server or a server you run yourself. |
| **Personal Access Token** | PAT with read/write access to the repo |

Sharko validates the connection before letting you proceed. If validation fails, check that the token has not expired and the repo URL is correct.

## Step 3: ArgoCD Connection

Sharko reads cluster health, sync status, and resource trees from ArgoCD.

| Field | What to enter |
|-------|--------------|
| **Server URL** | ArgoCD server URL reachable from within the cluster (auto-discovered if ArgoCD is in the same cluster) |
| **Token** | ArgoCD account token |

!!! tip "Auto-discovery"
    Sharko probes all services in the ArgoCD namespace and suggests the correct URL. In most cases you can accept the suggested value without changes.

!!! tip "Generating a token"
    See [Make the ArgoCD token first](#make-the-argocd-token-first) above for the exact steps: create the `sharko` account, log in, and run `argocd account generate-token --account sharko`.

    Use a dedicated `sharko` account. The account reads cluster and application status, and also writes a few things during repository setup and cluster registration (creates the repository connection, updates cluster labels). See [ArgoCD RBAC policy for Sharko](../operator/security.md#smallest-argocd-policy-for-sharko) for the exact policy. Do not use the admin token.

**Secrets Provider (optional, same step):** If you use AWS Secrets Manager or Kubernetes Secrets to store cluster kubeconfigs, configure the provider here:

- **AWS Secrets Manager** — select `aws-sm` and enter the AWS region. The optional **Prefix** field lets you add a prefix to the cluster name when looking up secrets (for example, with prefix `k8s-`, Sharko looks for a secret named `k8s-prod-eu` when registering cluster `prod-eu`). Sharko uses IRSA for auth (configure the IRSA annotation at Helm install time — see [Installation](installation.md#aws-secrets-manager-optional)). If Sharko does not run on EKS, it needs an access key instead: see [Give Sharko an AWS login when it does not run on EKS](../operator/configuration.md#aws-login-outside-eks).
- **Kubernetes Secrets** — select `k8s-secrets`. For each cluster, Sharko reads a Kubernetes Secret with the same name as the cluster (for example `prod-eu`), and takes the kubeconfig from its `kubeconfig` key. **Default case** (when Sharko is installed in namespace `sharko`): you do not need to fill in anything else, and Sharko reads from namespace `sharko`. **Using a different namespace**: set both Helm values `connection.provider.type: k8s-secrets` and `connection.provider.namespace` to the namespace where your Secrets live (see [Where Sharko gets cluster credentials](../operator/configuration.md#provider-3mech)). Once the type is set in Helm, it wins over what you picked in the wizard. The chart gives Sharko read access to Secrets in its own release namespace; to read from a different namespace, also add that namespace to `rbac.k8sSecretsProviderNamespaces` in your Helm values. There is no **Prefix** field for Kubernetes Secrets. Only AWS Secrets Manager uses a prefix, so the wizard shows the field only when `aws-sm` is picked.

You can skip this and configure the secrets provider later in **Settings → Secrets Provider**.

## Step 4: Initialize Repository

This step creates the starting layout in your Git repo — nothing more:

- **`sharko-engine.yaml`** — the pointer to Sharko's engine chart, the one moving part Sharko ships
- **`cluster-addons/`** and **`values/`** — empty folders, ready for the first cluster and the first addon
- **`managed-clusters.yaml`** and **`catalog.yaml`** are not created yet — a file that isn't there means "empty," and Sharko creates each one the first time you have something to put in it

Sharko shows you a step-by-step progress log as it creates each file and opens the PR.

You have two choices for how the PR is merged:

- **Set Up & Auto-Merge** — Sharko opens the PR and merges it immediately. The setup completes without you needing to visit GitHub or Azure DevOps.
- **Set Up (Manual PR Review)** — Sharko opens the PR but does not merge it. You review and merge it yourself in GitHub or Azure DevOps.

This choice is only for this one setup pull request. It does not change how later pull requests are merged. Those follow the **Auto-merge PRs** switch in **Settings**, **GitOps**, which is off until you turn it on. So with the switch off, the pull request Sharko opens when you register a cluster or add an addon waits for you to merge it.

Click the button that matches how you want to work. When the step completes, you move to the Catalog step.

## Step 5: Catalog

Your Catalog starts **empty**. This is not a dead end — the step gives
you two doors into it, both producing a pull request to `catalog.yaml`:

- **Pick from the Marketplace** — a list of the built-in curated addons.
  Common picks (like cert-manager) are highlighted at the top, but nothing
  is ever pre-selected — you choose every addon that goes in.
- **Add your own chart** — for an in-house or otherwise uncurated chart,
  fill in its repo location, version, and namespace yourself.

Pick as many addons as you want from either door; Sharko opens **one**
pull request for everything you picked, not one per addon. You can also
skip this step entirely and click through to the dashboard — an empty
Catalog is a normal working state, and you can add to it later from the
Marketplace or Catalog tab.

## Dashboard

The dashboard loads with clusters discovered from ArgoCD. You will see:

- **Managed clusters** — clusters already registered with Sharko, listed on the Clusters page
- **Discovered clusters** — existing ArgoCD clusters not yet managed by Sharko, shown as one line under the page title (e.g. "3 clusters ArgoCD already knows that Sharko doesn't manage yet.")

Click that line to open **Add Cluster** and adopt one of them. Click **Test connection** to verify connectivity to any cluster.

## What's Next

- [Add addons to the catalog](../user-guide/addons.md)
- [Register additional clusters](../user-guide/clusters.md)
- [Configure Settings](../user-guide/connections.md)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
