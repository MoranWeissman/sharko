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

// Ensure satisfies orchestrator.ArgoSecretManager by translating the orchestrator-local
// ArgoSecretSpec into an argosecrets.ClusterSecretSpec and delegating to the real Manager.
func (a *argoManagerAdapter) Ensure(ctx context.Context, spec orchestrator.ArgoSecretSpec) error {
	_, err := a.mgr.Ensure(ctx, argosecrets.ClusterSecretSpec{
		Name:        spec.Name,
		Server:      spec.Server,
		Region:      spec.Region,
		RoleARN:     spec.RoleARN,
		Labels:      spec.Labels,
		Annotations: spec.Annotations,
	})
	return err
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
