# Helm CRDs Directory

Custom Resource Definitions (CRDs) for the Sharko operator, installed by Helm.

## Helm CRD Handling

Helm treats the `crds/` directory specially:

1. **Install:** CRDs in this directory are applied to the cluster BEFORE any templates are rendered. This ensures that the Sharko Deployment (which may create CRs) does not fail due to missing CRDs.

2. **Upgrade:** Helm does NOT re-apply CRDs during `helm upgrade`. Once installed, CRDs must be managed manually (via `kubectl apply` or `make install`) or by a CRD upgrade tool. This is a deliberate Helm design choice to avoid breaking running CRs.

3. **Uninstall:** Helm does NOT delete CRDs during `helm uninstall`. CRDs and their CRs persist on the cluster. Delete them manually if needed:
   ```bash
   kubectl delete clusteraddons --all -n sharko
   kubectl delete clusteraddonsets --all -n sharko
   kubectl delete crd clusteraddons.sharko.io clusteraddonsets.sharko.io
   ```

## Phase 0 (current)

Empty. CRDs are not defined yet. `helm lint` and `helm template` stay green with an empty `crds/` directory.

## Phase 1+

Populated by copying `config/crd/*.yaml` (output of `make manifests`) into this directory. During a Helm install, the CRDs are applied first, then the Sharko Deployment is created with operator mode enabled.

## Managing CRD Upgrades

When the CRD schema changes (e.g., adding a new field), run `make manifests` to regenerate `config/crd/*.yaml`, copy the updated files here, then:

```bash
# Apply the updated CRDs manually
kubectl apply -f charts/sharko/crds/

# OR use the Makefile target
make install
```

Then commit the updated CRD YAML files to git. The next `helm upgrade` will pick up the new Deployment image but will NOT upgrade the CRDs (you already did that manually).

## Further Reading

- [Helm CRD Best Practices](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)
- [controller-tools CRD Generation](https://book.kubebuilder.io/reference/generating-crd.html)
