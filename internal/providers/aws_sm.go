package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"k8s.io/client-go/tools/clientcmd"
)

// AWSSecretsManagerProvider reads kubeconfigs from AWS Secrets Manager.
// Secret path: {prefix}{cluster-name}. Supports IRSA for authentication.
type AWSSecretsManagerProvider struct {
	client  *secretsmanager.Client
	prefix  string
	roleARN string // default IAM role to assume for EKS token generation
}

// NewAWSSecretsManagerProvider creates a provider backed by AWS Secrets Manager.
// Uses default AWS credential chain (IRSA when in-cluster, env vars or profile for local dev).
func NewAWSSecretsManagerProvider(cfg Config) (*AWSSecretsManagerProvider, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(awsCfg)

	// No default prefix — secret name equals cluster name by default.
	// Users who want a prefix can set SHARKO_PROVIDER_PREFIX.
	prefix := cfg.Prefix

	return &AWSSecretsManagerProvider{client: client, prefix: prefix, roleARN: cfg.RoleARN}, nil
}

// GetSecretValue retrieves a raw secret value from AWS Secrets Manager.
// path is the full secret name/ARN in AWS Secrets Manager.
func (p *AWSSecretsManagerProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	slog.Debug("[provider] GetSecretValue called", "path", path)
	output, err := p.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(path),
	})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q from AWS Secrets Manager: %w", path, err)
	}
	if output.SecretString != nil {
		value := []byte(*output.SecretString)
		slog.Debug("[provider] GetSecretValue success", "path", path, "size", len(value))
		return value, nil
	}
	if output.SecretBinary != nil {
		slog.Debug("[provider] GetSecretValue success", "path", path, "size", len(output.SecretBinary))
		return output.SecretBinary, nil
	}
	return nil, fmt.Errorf("secret %q has no value", path)
}

// structuredEKSSecret is the JSON schema used when secrets contain EKS cluster
// metadata rather than a raw kubeconfig YAML.
type structuredEKSSecret struct {
	ClusterName string `json:"clusterName"`
	Host        string `json:"host"`
	CAData      string `json:"caData"`
	AccountId   string `json:"accountId"`
	Region      string `json:"region"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	RoleARN     string `json:"roleArn"` // optional — IAM role to assume for cluster access
}

// fetchSecret retrieves and parses credentials from the secret at the given exact name.
func (p *AWSSecretsManagerProvider) fetchSecret(secretName string) (*Kubeconfig, error) {
	output, err := p.client.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q from AWS Secrets Manager: %w", secretName, err)
	}

	if output.SecretString == nil {
		return nil, fmt.Errorf("secret %q has no string value", secretName)
	}

	raw := []byte(*output.SecretString)

	// Try structured JSON first. If the secret contains an EKS cluster descriptor
	// (host + caData + region), build credentials via STS token generation.
	var structured structuredEKSSecret
	if err := json.Unmarshal(raw, &structured); err == nil && structured.Host != "" {
		slog.Info("[provider] secret fetched", "secretName", secretName, "format", "structured", "size", len(raw))
		return p.buildFromStructured(structured)
	}

	// Fallback: treat the secret value as raw kubeconfig YAML.
	slog.Info("[provider] secret fetched", "secretName", secretName, "format", "raw", "size", len(raw))
	return p.buildFromRawKubeconfig(raw, secretName)
}

// GetCredentials fetches credentials for the named cluster using a three-step lookup:
//
//  1. Try with the configured prefix (e.g. "clusters/prod-eu").
//  2. Try the exact name as-is (supports secretPath passthrough from the orchestrator).
//  3. Search Secrets Manager for names containing the query and return suggestions.
func (p *AWSSecretsManagerProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	slog.Info("[provider] GetCredentials called", "cluster", clusterName)

	// Step 1: Try with prefix (skipped when prefix is empty or name already contains prefix).
	if p.prefix != "" {
		withPrefix := p.prefix + clusterName
		slog.Debug("[provider] trying with prefix", "secretName", withPrefix)
		if kc, err := p.fetchSecret(withPrefix); err == nil {
			return kc, nil
		}
	}

	// Step 2: Try exact name (handles explicit secretPath values that don't need a prefix).
	slog.Debug("[provider] trying exact name", "secretName", clusterName)
	if kc, err := p.fetchSecret(clusterName); err == nil {
		return kc, nil
	}

	// Step 3: Search for similar names and include them in the error message.
	suggestions, searchErr := p.searchSimilar(clusterName)
	if searchErr == nil && len(suggestions) > 0 {
		slog.Info("[provider] searching for similar secrets", "query", clusterName, "found", len(suggestions))
		return nil, fmt.Errorf("secret for cluster %q not found in AWS Secrets Manager. Similar secrets: %s",
			clusterName, strings.Join(suggestions, ", "))
	}

	slog.Error("[provider] GetCredentials failed", "cluster", clusterName, "step", "fetch", "error", "secret not found in AWS Secrets Manager")
	return nil, fmt.Errorf("secret for cluster %q not found in AWS Secrets Manager", clusterName)
}

// searchSimilar returns secret names that contain query as a substring.
// The ListSecrets filter with key=name does substring matching on the secret name.
func (p *AWSSecretsManagerProvider) searchSimilar(query string) ([]string, error) {
	slog.Info("[provider] searching for similar secrets", "query", query)
	paginator := secretsmanager.NewListSecretsPaginator(p.client, &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{query},
			},
		},
	})

	var matches []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, err
		}
		for _, secret := range page.SecretList {
			matches = append(matches, aws.ToString(secret.Name))
		}
	}
	slog.Info("[provider] similar secrets search complete", "query", query, "found", len(matches))
	return matches, nil
}

// buildFromStructured constructs a Kubeconfig from the EKS JSON metadata format.
// It base64-decodes the CA cert, obtains a short-lived STS bearer token, and
// builds a kubeconfig YAML for use by remoteclient.NewClientFromKubeconfig.
func (p *AWSSecretsManagerProvider) buildFromStructured(s structuredEKSSecret) (*Kubeconfig, error) {
	hostPreview := s.Host
	if len(hostPreview) > 30 {
		hostPreview = hostPreview[:30] + "..."
	}
	slog.Debug("[provider] parsed structured JSON",
		"cluster", s.ClusterName,
		"host", hostPreview,
		"region", s.Region,
		"hasCAData", s.CAData != "",
	)

	caData, err := base64.StdEncoding.DecodeString(s.CAData)
	if err != nil {
		return nil, fmt.Errorf("decoding caData for cluster %q: %w", s.ClusterName, err)
	}

	// Determine cluster name: prefer the clusterName field; fall back to empty
	// string which will cause getEKSToken to omit the header (should not happen).
	name := s.ClusterName
	if name == "" {
		name = s.Environment
	}

	// Prefer the per-cluster roleArn from the secret; fall back to the
	// provider-level default configured via ProviderConfig.RoleARN.
	roleARN := s.RoleARN
	if roleARN == "" {
		roleARN = p.roleARN
	}

	token, err := getEKSToken(context.Background(), name, s.Region, roleARN)
	if err != nil {
		slog.Error("[provider] GetCredentials failed", "cluster", name, "step", "sts", "error", err)
		return nil, fmt.Errorf("generating EKS token for cluster %q: %w", name, err)
	}

	tokenPreview := token
	if len(tokenPreview) > 20 {
		tokenPreview = tokenPreview[:20] + "..."
	}
	slog.Info("[provider] STS token generated", "cluster", name, "region", s.Region, "tokenPrefix", tokenPreview)

	// Build kubeconfig YAML so remoteclient.NewClientFromKubeconfig can use Raw.
	// certificate-authority-data expects base64 — use s.CAData (original base64 string).
	kubeconfigYAML := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    token: %s
`, s.Host, s.CAData, name, name, name, name, name, name, token)

	slog.Info("[provider] kubeconfig built", "server", s.Host, "hasCA", len(caData) > 0, "hasToken", token != "")
	return &Kubeconfig{
		Raw:    []byte(kubeconfigYAML),
		Server: s.Host,
		CAData: caData,
		Token:  token,
	}, nil
}

// buildFromRawKubeconfig parses a raw kubeconfig YAML secret and extracts
// connection info using the client-go clientcmd library.
func (p *AWSSecretsManagerProvider) buildFromRawKubeconfig(raw []byte, secretName string) (*Kubeconfig, error) {
	kc := &Kubeconfig{Raw: raw}

	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig from secret %q: %w", secretName, err)
	}

	kc.Server = config.Host
	kc.CAData = config.TLSClientConfig.CAData
	kc.Token = config.BearerToken

	return kc, nil
}

func (p *AWSSecretsManagerProvider) ListClusters() ([]ClusterInfo, error) {
	slog.Info("[provider] ListClusters called", "prefix", p.prefix)
	var clusters []ClusterInfo
	paginator := secretsmanager.NewListSecretsPaginator(p.client, &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{p.prefix},
			},
		},
	})

	ctx := context.Background()
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing secrets with prefix %q: %w", p.prefix, err)
		}
		slog.Debug("[provider] ListClusters page", "count", len(page.SecretList))
		for _, secret := range page.SecretList {
			name := strings.TrimPrefix(aws.ToString(secret.Name), p.prefix)
			info := ClusterInfo{Name: name}

			// Extract region from SM tags first (cheap, no extra API call).
			for _, tag := range secret.Tags {
				if aws.ToString(tag.Key) == "region" {
					info.Region = aws.ToString(tag.Value)
				}
			}

			// Try to fetch the secret and parse structured JSON for richer metadata.
			// This adds one API call per cluster, which is acceptable for discovery.
			val, err := p.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
				SecretId: secret.Name,
			})
			if err == nil && val.SecretString != nil {
				var meta struct {
					Region      string `json:"region"`
					Project     string `json:"project"`
					Environment string `json:"environment"`
				}
				if json.Unmarshal([]byte(*val.SecretString), &meta) == nil {
					if meta.Region != "" {
						info.Region = meta.Region
					}
					if meta.Project != "" || meta.Environment != "" {
						if info.Tags == nil {
							info.Tags = make(map[string]string)
						}
						if meta.Project != "" {
							info.Tags["project"] = meta.Project
						}
						if meta.Environment != "" {
							info.Tags["environment"] = meta.Environment
						}
					}
				}
			}

			clusters = append(clusters, info)
		}
	}
	slog.Info("[provider] ListClusters complete", "total", len(clusters))
	return clusters, nil
}
