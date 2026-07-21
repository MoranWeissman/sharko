# RBAC Directory

RBAC manifests (ClusterRole, ServiceAccount, RoleBinding) for the Sharko operator controller.

## Phase 0 (current)

Empty. `make manifests` does not generate RBAC yet.

## Phase 1+

Populated by `make manifests` (runs `controller-gen` over Go controller code with `+kubebuilder:rbac` markers). Expected files:

- `role.yaml` — ClusterRole granting the controller permissions to watch/list/patch CRDs + ArgoCD ApplicationSets
- `role_binding.yaml` — ClusterRoleBinding tying the ClusterRole to the controller ServiceAccount
- `service_account.yaml` — ServiceAccount for the controller pod
- `leader_election_role.yaml` / `leader_election_role_binding.yaml` — (optional) RBAC for leader-election ConfigMap/Lease if HA is enabled

## Applying RBAC

```bash
# Apply to the cluster (also applies manager Deployment)
make deploy

# Remove from the cluster
make undeploy
```

RBAC is cluster-scoped (ClusterRole + ClusterRoleBinding) because the controller watches CRDs and ApplicationSets across all namespaces.
