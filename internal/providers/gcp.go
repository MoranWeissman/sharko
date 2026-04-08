package providers

import (
	"context"
	"fmt"
)

// GCPSecretManagerProvider reads credentials from Google Cloud Secret Manager.
// Status: STUB — not yet implemented. Community contributions welcome.
//
// Implementation guidance:
//   - Authentication: use golang.org/x/oauth2/google.DefaultTokenSource (picks up
//     Workload Identity when running on GKE, or ADC for local dev).
//   - Secret access: cloud.google.com/go/secretmanager/apiv1.NewClient +
//     secretmanagerpb.AccessSecretVersionRequest.
//   - GKE token: obtain short-lived tokens via google.DefaultTokenSource with
//     scope "https://www.googleapis.com/auth/cloud-platform", then build a
//     kubeconfig with the token as the bearer credential.
//   - ListClusters: iterate projects via Resource Manager API or rely on
//     a configured prefix convention (e.g. "clusters/{cluster-name}").
type GCPSecretManagerProvider struct{}

// NewGCPSecretManagerProvider creates a provider backed by Google Cloud Secret Manager.
// This function always returns an error — the provider is not yet implemented.
// Community contributions welcome at https://github.com/MoranWeissman/sharko
func NewGCPSecretManagerProvider(cfg Config) (*GCPSecretManagerProvider, error) {
	return nil, fmt.Errorf("GCP Secret Manager provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko")
}

// GetCredentials is not implemented.
func (p *GCPSecretManagerProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	return nil, fmt.Errorf("GCP provider not implemented")
}

// ListClusters is not implemented.
func (p *GCPSecretManagerProvider) ListClusters() ([]ClusterInfo, error) {
	return nil, fmt.Errorf("GCP provider not implemented")
}

// GetSecretValue is not implemented.
func (p *GCPSecretManagerProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	return nil, fmt.Errorf("GCP provider not implemented")
}
