# First-Run Wizard

When you access Sharko for the first time, a setup wizard appears automatically. It walks you through every configuration step needed before the dashboard is usable. You cannot accidentally skip it — but you can dismiss it at any step with the X button if you prefer to configure things manually later.

## Before You Start

Have these ready:

- **Git personal access token** — GitHub PAT with `repo` scope, or Azure DevOps PAT with `Code (Read & Write)` permissions
- **Addons repo URL** — the Git repository where Sharko will store ApplicationSet and cluster configuration (can be an existing repo or a new empty one)
- **ArgoCD account token** — an ArgoCD account token with at minimum `applications:get` and `projects:get` permissions

## Step 1: Welcome

An overview of what the wizard will set up. Read through it and click **Get Started**.

## Step 2: Git Connection

Sharko needs a Git repo to store your fleet configuration (ApplicationSet, base values, per-cluster overrides). All write operations (adding clusters, upgrading addons) create PRs in this repo.

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
    Create a dedicated `sharko` account with read permissions. Do not use the admin token.

**Secrets Provider (optional, same step):** If you use AWS Secrets Manager or Kubernetes Secrets to store cluster kubeconfigs, configure the provider here:

- **AWS Secrets Manager** — select `aws-sm` and enter the AWS region. Sharko uses IRSA for auth (configure the IRSA annotation at Helm install time — see [Installation](installation.md#aws-secrets-manager-optional)).
- **Kubernetes Secrets** — select `k8s-secrets` and enter the namespace where cluster secrets live.

You can skip this and configure the secrets provider later in **Settings → Secrets Provider**.

## Step 4: Initialize Repository

This step creates the initial structure in your Git repo:

- An **ApplicationSet** that manages all cluster addons via ArgoCD
- A **base values directory** with per-addon default configuration
- A **clusters directory** where per-cluster overrides live

Sharko shows you a step-by-step progress log as it creates each file and opens the PR.

**Auto-merge:** Toggle this on if you want Sharko to merge the PR immediately after creating it. Toggle it off if you want to review the PR yourself in GitHub or Azure DevOps first.

Click **Initialize** and watch the progress. When the step completes, click **Go to Dashboard**.

## Dashboard

The dashboard loads with clusters discovered from ArgoCD. You will see:

- **Managed clusters** — clusters already registered with Sharko
- **Discovered clusters** — existing ArgoCD clusters not yet managed by Sharko

Click **Start Managing** on a discovered cluster to bring it under Sharko management. Click **Test Connection** to verify connectivity to any cluster.

## What's Next

- [Add addons to the catalog](../user-guide/addons.md)
- [Register additional clusters](../user-guide/clusters.md)
- [Configure Settings](../user-guide/connections.md)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
