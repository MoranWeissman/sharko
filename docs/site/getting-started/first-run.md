# First-Run Wizard

When you access Sharko for the first time, a setup wizard appears automatically. It walks you through every configuration step needed before the dashboard is usable. You cannot accidentally skip it — but you can dismiss it at any step with the X button if you prefer to configure things manually later.

## Before You Start

Have these ready:

- **Git personal access token** — GitHub PAT with `repo` scope, or Azure DevOps PAT with `Code (Read & Write)` permissions
- **Addons repo URL** — the Git repository where Sharko will store cluster configuration and addon assignments (can be an existing repo or a new empty one)
- **ArgoCD account token** — an ArgoCD account token with at minimum `applications:get` and `projects:get` permissions

## Step 1: Welcome

An overview of what the wizard will set up. Read through it and click **Get Started**.

## Step 2: Git Connection

Sharko needs a Git repo to store your fleet configuration (the engine pin, your approved-addon catalog, per-cluster assignments, and addon values). All write operations (adding clusters, upgrading addons) create PRs in this repo.

| Field | What to enter |
|-------|--------------|
| **Provider** | GitHub or Azure DevOps |
| **Repository URL** | e.g., `https://github.com/your-org/addons-repo` |
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
    ```bash
    argocd account generate-token --account sharko
    ```
    Create a dedicated `sharko` account. The account reads cluster and application status, and also writes a few things during repository setup and cluster registration (creates the repository connection, updates cluster labels). See [ArgoCD RBAC policy for Sharko](../operator/security.md#smallest-argocd-policy-for-sharko) for the exact policy. Do not use the admin token.

**Secrets Provider (optional, same step):** If you use AWS Secrets Manager or Kubernetes Secrets to store cluster kubeconfigs, configure the provider here:

- **AWS Secrets Manager** — select `aws-sm` and enter the AWS region. The optional **Prefix** field lets you add a prefix to the cluster name when looking up secrets (for example, with prefix `k8s-`, Sharko looks for a secret named `k8s-prod-eu` when registering cluster `prod-eu`). Sharko uses IRSA for auth (configure the IRSA annotation at Helm install time — see [Installation](installation.md#aws-secrets-manager-optional)).
- **Kubernetes Secrets** — select `k8s-secrets`. For each cluster, Sharko reads a Kubernetes Secret with the same name as the cluster (for example `prod-eu`), and takes the kubeconfig from its `kubeconfig` key. **Default case** (when Sharko is installed in namespace `sharko`): you do not need to fill in anything else, and Sharko reads from namespace `sharko`. **Using a different namespace**: set both Helm values `connection.provider.type: k8s-secrets` and `connection.provider.namespace` to the namespace where your Secrets live (see [Where Sharko gets cluster credentials](../operator/configuration.md#provider-3mech)). Once the type is set in Helm, it wins over what you picked in the wizard. The chart gives Sharko read access to Secrets in its own release namespace; to read from a different namespace, also add that namespace to `rbac.k8sSecretsProviderNamespaces` in your Helm values. The wizard shows a **Prefix** field, but Sharko does not use it for Kubernetes Secrets, so leave it empty.

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
- **Discovered clusters** — existing ArgoCD clusters not yet managed by Sharko, collapsed into a single hint line with a count (e.g. "ArgoCD knows 3 more clusters Sharko doesn't manage")

Click that hint to open **Register New Cluster** pre-set to adopt one of them. Click **Test connection** to verify connectivity to any cluster.

## What's Next

- [Add addons to the catalog](../user-guide/addons.md)
- [Register additional clusters](../user-guide/clusters.md)
- [Configure Settings](../user-guide/connections.md)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
