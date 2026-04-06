# Quick Start

Get Sharko running on your cluster in about 5 minutes.

## Prerequisites

- Kubernetes 1.27+ with ArgoCD installed
- Helm 3.x
- A GitHub Personal Access Token (PAT) with `repo` scope

## 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<your-github-pat>
```

## 2. Get the Admin Password

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

## 3. Access the UI

Port-forward the service to your local machine:

```bash
kubectl port-forward svc/sharko -n sharko 8080:80
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` and the password from step 2.

## 4. Configure Connections

In the UI, go to **Settings → Connections** and add:

1. An **ArgoCD connection** — server URL and account token
2. A **Git connection** — provider (GitHub or Azure DevOps), token, and repo URL

Set both connections as **active**.

## 5. Initialize the Addons Repo

```bash
sharko login --server http://localhost:8080
sharko init
```

This creates the initial directory structure in your Git repo (ApplicationSet, base values, etc.).

## 6. Add Your First Addon and Cluster

```bash
sharko add-addon cert-manager \
  --chart cert-manager \
  --repo https://charts.jetstack.io \
  --version 1.14.5

sharko add-cluster my-cluster \
  --addons cert-manager,metrics-server \
  --region us-east-1

sharko status
```

Each command creates a PR in your Git repo. Merge the PRs and ArgoCD will deploy the changes.

## Next Steps

- [Configure ingress for production access](installation.md#ingress)
- [Add more clusters](../user-guide/clusters.md)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
- [Set up API keys for CI/CD](../user-guide/connections.md#api-keys)
