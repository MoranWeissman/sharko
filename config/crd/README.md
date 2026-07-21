# CRD Directory

Custom Resource Definitions (CRDs) for the Sharko operator.

## Phase 0 (current)

Empty. `make manifests` is a no-op scaffold (exits 0 with a note).

## Phase 1+

Populated by `make manifests` (runs `controller-gen` over Go types in `internal/` with `+kubebuilder` markers). Expected files:

- `sharko.io_clusteraddons.yaml` — defines the `ClusterAddons` CRD (one CR per cluster)
- `sharko.io_clusteraddonsets.yaml` — defines the `ClusterAddonSet` CRD (one CR per multi-cluster addon family)

## Applying CRDs

```bash
# Apply to the cluster
make install

# Remove from the cluster
make uninstall
```

CRDs are also copied into `charts/sharko/crds/` for Helm-based installs (see `charts/sharko/crds/README.md`).
