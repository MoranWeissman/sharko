package api

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// argoManagerAdapter bridges argosecrets.Manager to orchestrator.ArgoSecretManager.
// It lives in the api package because only this layer can import both packages
// without creating an import cycle (orchestrator must not import argosecrets).
type argoManagerAdapter struct {
	mgr *argosecrets.Manager
}

// SetAnnotation delegates to the real Manager.
func (a *argoManagerAdapter) SetAnnotation(ctx context.Context, name, key, value string) error {
	return a.mgr.SetAnnotation(ctx, name, key, value)
}

// GetAnnotation delegates to the real Manager.
func (a *argoManagerAdapter) GetAnnotation(ctx context.Context, name, key string) (string, error) {
	return a.mgr.GetAnnotation(ctx, name, key)
}

// GetManagedByLabel delegates to the real Manager.
func (a *argoManagerAdapter) GetManagedByLabel(ctx context.Context, name string) (string, error) {
	return a.mgr.GetManagedByLabel(ctx, name)
}

// Unadopt delegates to the real Manager.
func (a *argoManagerAdapter) Unadopt(ctx context.Context, name string) error {
	return a.mgr.Unadopt(ctx, name)
}

// StripOwnershipLabel delegates to the real Manager. This method is not part
// of orchestrator.ArgoSecretManager — it satisfies the OPTIONAL
// ownershipLabelStripper capability that RemoveCluster type-asserts for the
// handover-at-removal-time strip on self-managed connections
// (V2-cleanup-60.1).
func (a *argoManagerAdapter) StripOwnershipLabel(ctx context.Context, name string) (bool, error) {
	return a.mgr.StripOwnershipLabel(ctx, name)
}

// Ensure converts the orchestrator-local spec to argosecrets.ClusterSecretSpec
// and delegates to the real Manager. Labels and config JSON are built inside
// Manager.Ensure (via buildLabels / buildSecretConfig), so the write is
// byte-identical to anything the reconciler would emit for the same spec.
func (a *argoManagerAdapter) Ensure(ctx context.Context, spec orchestrator.ArgoSecretSpec) (bool, error) {
	return a.mgr.Ensure(ctx, argosecrets.ClusterSecretSpec{
		Name:        spec.Name,
		Server:      spec.Server,
		Region:      spec.Region,
		RoleARN:     spec.RoleARN,
		CAData:      spec.CAData,
		Token:       spec.Token,
		CertData:    spec.CertData,
		KeyData:     spec.KeyData,
		Labels:      spec.Labels,
		Annotations: spec.Annotations,
	})
}

// GetTrackingOwner delegates to the real Manager. This method is not part
// of orchestrator.ArgoSecretManager — it satisfies the OPTIONAL
// foreignOwnerDetector capability that AdoptClusters / RegisterCluster
// type-assert for the foreign-ArgoCD-ownership warning (V2-cleanup-89.5),
// same optional-capability pattern as StripOwnershipLabel above.
func (a *argoManagerAdapter) GetTrackingOwner(ctx context.Context, name string) (string, bool, error) {
	return a.mgr.GetTrackingOwner(ctx, name)
}
