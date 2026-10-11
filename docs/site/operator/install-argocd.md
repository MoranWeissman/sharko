# Install ArgoCD for Sharko

Sharko needs ArgoCD to deploy addons to your clusters. ArgoCD is the tool that actually runs the Helm releases and keeps them in sync with Git. Sharko writes the connection secrets and opens the pull requests, and ArgoCD deploys what you merged.

## ArgoCD version

Sharko is tested with **ArgoCD v3.3 to v3.5**. Install one of those versions. Nearby versions are expected to work.

You can check which version Sharko found after connecting to it. The **System** page in the Sharko UI shows the ArgoCD version and the tested range.

## Install ArgoCD with Helm

This is the recommended way. The Helm chart lets you change settings by editing your values file.

Add the ArgoCD Helm repository and install the chart:

```bash
helm repo add argo https://argoproj.github.io/argo-helm
helm install argocd argo/argo-cd \
  --namespace argocd --create-namespace
```

For production use, set up a values file with your Ingress and resource settings. See the [argo-cd chart documentation](https://github.com/argoproj/argo-helm/tree/main/charts/argo-cd) for all the values you can change.

## Install ArgoCD with a manifest

If you prefer to install from a manifest instead of Helm, use the official install manifest:

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
```

**Why `--server-side --force-conflicts`?** Some ArgoCD custom resource definitions (CRDs) are bigger than the 262 KB limit that client-side `kubectl apply` enforces. Server-side apply does not have that limit. The `--force-conflicts` flag lets the apply operation take ownership of fields that might have been managed by a previous install. This is safe for a fresh install and required for upgrades.

## Check that ArgoCD is running

Wait for the ArgoCD server deployment to be available:

```bash
kubectl wait --for=condition=available --timeout=180s \
  deployment/argocd-server -n argocd
```

Check the pods are running:

```bash
kubectl get pods -n argocd
```

You should see the ArgoCD server, repo-server, application controller, and other pods all in a running state.

## Do you need to add clusters to ArgoCD yourself?

No. When you register a cluster with Sharko, Sharko writes the ArgoCD cluster secret for you. ArgoCD sees the secret and starts using the cluster. Clusters that ArgoCD already knows about (added by hand, or by another tool) show up in Sharko as discovered clusters — you can adopt them into Sharko's management in one click.

## What's next

Return to [Installation](installation.md) to continue installing Sharko.
