# Troubleshooting

Common issues and how to resolve them.

## "Connection refused" When Accessing the UI

**Symptom:** Browser shows "Connection refused" or times out when opening the Sharko URL.

**Causes and fixes:**

1. **Port-forward not running** — Start a port-forward:
   ```bash
   kubectl port-forward svc/sharko -n sharko 8080:80
   ```

2. **Service type is ClusterIP** — The service is not exposed externally by default. Either use port-forward or configure an Ingress:
   ```bash
   kubectl get svc -n sharko
   # If TYPE is ClusterIP, use port-forward or add ingress
   ```

3. **Pod not running** — Check pod status:
   ```bash
   kubectl get pods -n sharko
   kubectl describe pod -n sharko -l app.kubernetes.io/name=sharko
   kubectl logs -n sharko -l app.kubernetes.io/name=sharko
   ```

## "401 Unauthorized"

**Symptom:** `401 Unauthorized` responses from the API or CLI.

**Causes and fixes:**

1. **Session token expired** — Log in again:
   ```bash
   sharko login --server https://sharko.your-domain.com
   ```

2. **Wrong credentials** — Verify the admin password:
   ```bash
   kubectl get secret sharko -n sharko \
     -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
   ```

3. **API key revoked** — If using an API key, verify it still exists:
   ```bash
   sharko token list
   ```

4. **No users configured** — If the `sharko-users` ConfigMap is empty or missing, authentication falls back and may fail unexpectedly:
   ```bash
   kubectl get configmap sharko-users -n sharko -o yaml
   ```

## "502 Bad Gateway"

**Symptom:** Sharko UI loads but data requests fail with 502 errors, or the UI shows "ArgoCD unreachable".

**Causes and fixes:**

1. **ArgoCD URL misconfigured** — Check the ArgoCD connection in **Settings → Connections**. The URL must be reachable from within the Sharko pod (not from your laptop):
   ```bash
   # Test connectivity from the Sharko pod
   kubectl exec -n sharko deploy/sharko -- \
     wget -qO- https://argocd-server.argocd.svc.cluster.local/api/v1/applications
   ```

2. **ArgoCD token expired** — Regenerate the ArgoCD account token and update the connection in Settings.

3. **TLS certificate issues** — If ArgoCD uses a self-signed certificate, add a `hostAliases` entry and disable TLS verification (not recommended for production).

## Init Fails on Empty Repository

**Symptom:** `sharko init` returns an error like "repository not found" or "failed to clone".

**Cause:** The Git repository must exist before running `sharko init`. Sharko clones the repo and commits the initial structure — it does not create the repository.

**Fix:**
1. Create the repository in GitHub (or Azure DevOps) if it does not exist yet
2. Push at least one commit (e.g., an initial `README.md`) so the repo is not completely empty
3. Re-run `sharko init`

## GitOps Errors: "gitopsCfg not initialized"

**Symptom:** Write operations (add-cluster, add-addon, upgrade) fail with errors about missing GitOps configuration.

**Cause:** GitOps actions require environment variables that are not set.

**Fix:** Ensure the following are configured:

| Env Var | Required For | Set Via |
|---------|-------------|---------|
| `GITHUB_TOKEN` | GitHub PR creation | `secrets.GITHUB_TOKEN` in Helm values |
| `SHARKO_GITOPS_REPO_URL` | Init and PR target | `config.repoURL` in Helm values |

Also verify that `gitops.actions.enabled: true` is set in your Helm values.

Check current configuration:

```bash
kubectl exec -n sharko deploy/sharko -- env | grep -E "GITHUB|SHARKO_GITOPS"
```

## Addon Drift Detection Shows All Clusters Out of Sync

**Symptom:** The version matrix shows every cluster as "drifted" even after recent upgrades.

**Causes and fixes:**

1. **ArgoCD not synced** — Check ArgoCD app sync status:
   ```bash
   kubectl get applications -n argocd
   ```

2. **Git connection inactive** — Navigate to **Settings → Connections** and verify the Git connection is active and the repo URL matches your addons repository.

3. **ApplicationSet not generated** — Verify the ApplicationSet exists in ArgoCD:
   ```bash
   kubectl get applicationset -n argocd
   ```

## Pod Crashes with "read-only filesystem" Error

**Symptom:** Pod logs show write errors like "read-only file system".

**Cause:** Sharko's security context sets `readOnlyRootFilesystem: true`. Any component writing to the local filesystem (e.g., temporary files) must use a `tmpfs` mount or a PVC.

**Fix:** Enable persistence for migration state if needed:

```yaml
persistence:
  enabled: true
  size: 1Gi
```

Or add an `emptyDir` mount via `extraEnv` and a custom deployment patch.

## Checking Logs

```bash
# Live logs
kubectl logs -f -n sharko -l app.kubernetes.io/name=sharko

# Previous container logs (after crash)
kubectl logs -n sharko -l app.kubernetes.io/name=sharko --previous

# Events
kubectl get events -n sharko --sort-by='.lastTimestamp'
```
