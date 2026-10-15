# Install ArgoCD for Sharko

Sharko needs ArgoCD to deploy addons to your clusters. ArgoCD is the tool that actually runs the Helm releases and keeps them in sync with Git. Sharko writes the connection secrets and opens the pull requests, and ArgoCD deploys what you merged.

## ArgoCD version

Sharko is tested with **ArgoCD v3.3 to v3.5**. Install one of those versions. The commands below install ArgoCD **v3.5.3**, the v3.5 release Sharko was last tested with. Newer ArgoCD versions are not tested with Sharko yet.

You can check which version Sharko found after connecting to it. The **System** page in the Sharko UI shows the ArgoCD version and the tested range.

## Install ArgoCD with Helm

This is the recommended way. The Helm chart lets you change settings by editing your values file.

Add the ArgoCD Helm repository and install the chart. Chart version `10.9.6` installs ArgoCD v3.5.3:

```bash
helm repo add argo https://argoproj.github.io/argo-helm
helm install argocd argo/argo-cd \
  --version 10.9.6 \
  --namespace argocd --create-namespace
```

**For a test setup only**, you can install ArgoCD with its TLS server disabled:

```bash
helm install argocd argo/argo-cd \
  --version 10.9.6 \
  --namespace argocd --create-namespace \
  --set configs.params."server\.insecure"=true
```

With `server.insecure` set to true, ArgoCD serves plain http on port 80. This avoids certificate errors when connecting to ArgoCD from inside the cluster. For production, give ArgoCD a trusted certificate. The "Skip TLS certificate verification" box in the Sharko wizard turns off the certificate check, so tick it only on a test setup, never in production.

The chart has its own version number, separate from the ArgoCD version. To see which chart version installs which ArgoCD version, run `helm search repo argo/argo-cd --versions` and read the **APP VERSION** column.

For production use, set up a values file with your Ingress and resource settings. See the [argo-cd chart documentation](https://github.com/argoproj/argo-helm/tree/main/charts/argo-cd) for all the values you can change.

## Install ArgoCD with a manifest

If you prefer to install from a manifest instead of Helm, use the official install manifest for v3.5.3:

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.3/manifests/install.yaml
```

Do not use the `stable` address in place of `v3.5.3`. It always points at the newest ArgoCD release, which may be newer than what Sharko is tested with.

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

## What access ArgoCD and Sharko get on your clusters

ArgoCD needs wide access on every cluster it deploys to, and Sharko uses that same access for its own work there. [What access Sharko and ArgoCD need on your clusters](cluster-access.md) explains what each one does with it.

## What's next

Return to [Installation](installation.md) to continue installing Sharko.
