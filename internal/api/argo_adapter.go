package api

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
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
