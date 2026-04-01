# ArgoCD Health Checks for External Secrets Operator

## Problem

ArgoCD shows External Secrets Operator (ESO) applications as **"Degraded"** even though all resources are healthy and functioning correctly. This happens because ArgoCD doesn't have built-in health assessment for ESO Custom Resources (ClusterSecretStore, ExternalSecret, etc.).

## Solution

Add custom health checks for ESO CRDs to the ArgoCD ConfigMap (`argocd-cm`).

> **Note:** This configuration must be applied to the ArgoCD installation managed by ArgoFleet. This is a one-time configuration that benefits all teams using ESO.

## Configuration

Add the following to the `argocd-cm` ConfigMap in the `argocd` namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  # Health check for ClusterSecretStore
  resource.customizations.health.external-secrets.io_ClusterSecretStore: |
    hs = {}
    if obj.status ~= nil then
      if obj.status.conditions ~= nil then
        for i, condition in ipairs(obj.status.conditions) do
          if condition.type == "Ready" and condition.status == "False" then
            hs.status = "Degraded"
            hs.message = condition.message
            return hs
          end
          if condition.type == "Ready" and condition.status == "True" then
            hs.status = "Healthy"
            hs.message = condition.message
            return hs
          end
        end
      end
    end
    hs.status = "Progressing"
    hs.message = "Waiting for ClusterSecretStore"
    return hs

  # Health check for SecretStore
  resource.customizations.health.external-secrets.io_SecretStore: |
    hs = {}
    if obj.status ~= nil then
      if obj.status.conditions ~= nil then
        for i, condition in ipairs(obj.status.conditions) do
          if condition.type == "Ready" and condition.status == "False" then
            hs.status = "Degraded"
            hs.message = condition.message
            return hs
          end
          if condition.type == "Ready" and condition.status == "True" then
            hs.status = "Healthy"
            hs.message = condition.message
            return hs
          end
        end
      end
    end
    hs.status = "Progressing"
    hs.message = "Waiting for SecretStore"
    return hs

  # Health check for ExternalSecret
  resource.customizations.health.external-secrets.io_ExternalSecret: |
    hs = {}
    if obj.status ~= nil then
      if obj.status.conditions ~= nil then
        for i, condition in ipairs(obj.status.conditions) do
          if condition.type == "Ready" and condition.status == "False" then
            hs.status = "Degraded"
            hs.message = condition.message
            return hs
          end
          if condition.type == "Ready" and condition.status == "True" then
            hs.status = "Healthy"
            hs.message = condition.message
            return hs
          end
        end
      end
    end
    hs.status = "Progressing"
    hs.message = "Waiting for ExternalSecret"
    return hs

  # Health check for PushSecret (optional, if used)
  resource.customizations.health.external-secrets.io_PushSecret: |
    hs = {}
    if obj.status ~= nil then
      if obj.status.conditions ~= nil then
        for i, condition in ipairs(obj.status.conditions) do
          if condition.type == "Ready" and condition.status == "False" then
            hs.status = "Degraded"
            hs.message = condition.message
            return hs
          end
          if condition.type == "Ready" and condition.status == "True" then
            hs.status = "Healthy"
            hs.message = condition.message
            return hs
          end
        end
      end
    end
    hs.status = "Progressing"
    hs.message = "Waiting for PushSecret"
    return hs
```

## How to Apply

### Option 1: kubectl patch (Recommended)

```bash
kubectl patch configmap argocd-cm -n argocd --type merge -p "$(cat <<'EOF'
{
  "data": {
    "resource.customizations.health.external-secrets.io_ClusterSecretStore": "hs = {}\nif obj.status ~= nil then\n  if obj.status.conditions ~= nil then\n    for i, condition in ipairs(obj.status.conditions) do\n      if condition.type == \"Ready\" and condition.status == \"False\" then\n        hs.status = \"Degraded\"\n        hs.message = condition.message\n        return hs\n      end\n      if condition.type == \"Ready\" and condition.status == \"True\" then\n        hs.status = \"Healthy\"\n        hs.message = condition.message\n        return hs\n      end\n    end\n  end\nend\nhs.status = \"Progressing\"\nhs.message = \"Waiting for ClusterSecretStore\"\nreturn hs\n",
    "resource.customizations.health.external-secrets.io_ExternalSecret": "hs = {}\nif obj.status ~= nil then\n  if obj.status.conditions ~= nil then\n    for i, condition in ipairs(obj.status.conditions) do\n      if condition.type == \"Ready\" and condition.status == \"False\" then\n        hs.status = \"Degraded\"\n        hs.message = condition.message\n        return hs\n      end\n      if condition.type == \"Ready\" and condition.status == \"True\" then\n        hs.status = \"Healthy\"\n        hs.message = condition.message\n        return hs\n      end\n    end\n  end\nend\nhs.status = \"Progressing\"\nhs.message = \"Waiting for ExternalSecret\"\nreturn hs\n",
    "resource.customizations.health.external-secrets.io_SecretStore": "hs = {}\nif obj.status ~= nil then\n  if obj.status.conditions ~= nil then\n    for i, condition in ipairs(obj.status.conditions) do\n      if condition.type == \"Ready\" and condition.status == \"False\" then\n        hs.status = \"Degraded\"\n        hs.message = condition.message\n        return hs\n      end\n      if condition.type == \"Ready\" and condition.status == \"True\" then\n        hs.status = \"Healthy\"\n        hs.message = condition.message\n        return hs\n      end\n    end\n  end\nend\nhs.status = \"Progressing\"\nhs.message = \"Waiting for SecretStore\"\nreturn hs\n"
  }
}
EOF
)"
```

### Option 2: Edit ConfigMap directly

```bash
kubectl edit configmap argocd-cm -n argocd
```

Then add the health check entries under the `data:` section.

## Verification

After applying the configuration:

1. **Restart ArgoCD Application Controller** (to pick up new health checks):
   ```bash
   kubectl rollout restart deployment argocd-application-controller -n argocd
   ```

2. **Check application health** (should change from Degraded to Healthy):
   ```bash
   kubectl get application external-secrets-bootstrap -n argocd -o jsonpath='{.status.health.status}'
   ```

3. **Verify in ArgoCD UI**: Application should now show as "Healthy"

## Impact

- **Scope**: Global - affects all Applications using ESO across all teams
- **Downtime**: None (requires controller restart but doesn't affect managed resources)
- **Benefits**: Proper health status for all ESO-based applications

## Alternative: Document Expected Behavior

If ArgoFleet cannot apply these health checks immediately, document in your solution that:

1. ESO applications may show "Degraded" status in ArgoCD
2. This is a known limitation of ArgoCD's health assessment for ESO CRDs
3. Verify actual health using:
   ```bash
   kubectl get deployments -n external-secrets
   kubectl get clustersecretstore
   kubectl get externalsecret --all-namespaces
   ```
4. Request ArgoFleet team to add ESO health checks using this documentation

## References

- [ArgoCD Resource Health Assessment](https://argo-cd.readthedocs.io/en/stable/operator-manual/health/)
- [ArgoCD Custom Health Checks](https://argo-cd.readthedocs.io/en/stable/operator-manual/health/#custom-health-checks)
- [External Secrets Operator Status Conditions](https://external-secrets.io/latest/api/externalsecret/)
