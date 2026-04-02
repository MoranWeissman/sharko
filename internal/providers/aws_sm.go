package providers

import (
	"context"
	"fmt"
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
	client *secretsmanager.Client
	prefix string
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

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "clusters/"
	}

	return &AWSSecretsManagerProvider{client: client, prefix: prefix}, nil
}

func (p *AWSSecretsManagerProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	secretName := p.prefix + clusterName

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
	kc := &Kubeconfig{Raw: raw}

	// Parse kubeconfig to extract connection info
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
	var clusters []ClusterInfo
	paginator := secretsmanager.NewListSecretsPaginator(p.client, &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{p.prefix},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, fmt.Errorf("listing secrets with prefix %q: %w", p.prefix, err)
		}
		for _, secret := range page.SecretList {
			name := strings.TrimPrefix(aws.ToString(secret.Name), p.prefix)
			info := ClusterInfo{Name: name}
			// Extract region from tags if available
			for _, tag := range secret.Tags {
				if aws.ToString(tag.Key) == "region" {
					info.Region = aws.ToString(tag.Value)
				}
			}
			clusters = append(clusters, info)
		}
	}
	return clusters, nil
}
