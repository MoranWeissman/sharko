package providers

import (
	"context"
	"fmt"
)

// AzureKeyVaultProvider reads credentials from Azure Key Vault.
// Status: STUB — not yet implemented. Community contributions welcome.
//
// Implementation guidance:
//   - Authentication: use github.com/Azure/azure-sdk-for-go/sdk/azidentity.NewDefaultAzureCredential
//     (picks up Workload Identity on AKS, or Azure CLI / env vars for local dev).
//   - Secret access: github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets.NewClient +
//     client.GetSecret(ctx, secretName, version, nil).
//   - AKS token: obtain a short-lived token via azidentity.DefaultAzureCredential with
//     scope "6dae42f8-4368-4678-94ff-3960e28e3630/.default" (AKS resource), then build
//     a kubeconfig with the token as the bearer credential.
//   - ListClusters: iterate Key Vault secrets by a configured prefix convention
//     (e.g. "clusters-{cluster-name}") using client.NewListSecretPropertiesPager.
type AzureKeyVaultProvider struct{}

// NewAzureKeyVaultProvider creates a provider backed by Azure Key Vault.
// This function always returns an error — the provider is not yet implemented.
// Community contributions welcome at https://github.com/MoranWeissman/sharko
func NewAzureKeyVaultProvider(cfg Config) (*AzureKeyVaultProvider, error) {
	return nil, fmt.Errorf("Azure Key Vault provider is not yet implemented — community contributions welcome at https://github.com/MoranWeissman/sharko")
}

// GetCredentials is not implemented.
func (p *AzureKeyVaultProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	return nil, fmt.Errorf("Azure provider not implemented")
}

// ListClusters is not implemented.
func (p *AzureKeyVaultProvider) ListClusters() ([]ClusterInfo, error) {
	return nil, fmt.Errorf("Azure provider not implemented")
}

// SearchSecrets is not implemented.
func (p *AzureKeyVaultProvider) SearchSecrets(query string) ([]string, error) {
	return nil, nil
}

// HealthCheck is not implemented — Azure provider is a stub.
func (p *AzureKeyVaultProvider) HealthCheck(ctx context.Context) error {
	return fmt.Errorf("Azure provider not implemented")
}

// GetSecretValue is not implemented.
func (p *AzureKeyVaultProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	return nil, fmt.Errorf("Azure provider not implemented")
}
