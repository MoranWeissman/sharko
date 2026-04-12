package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// DiscoveredCluster represents an EKS cluster found during discovery.
type DiscoveredCluster struct {
	Name       string `json:"name"`
	Region     string `json:"region"`
	Account    string `json:"account"`
	K8sVersion string `json:"k8s_version"`
	Endpoint   string `json:"endpoint"`
	Status     string `json:"status"` // ACTIVE, CREATING, DELETING, FAILED, UPDATING, PENDING
	Error      string `json:"error,omitempty"`
}

// EKSDiscoveryAPI abstracts the EKS API calls needed for cluster discovery.
// This interface exists to enable unit testing with mocks.
type EKSDiscoveryAPI interface {
	ListClusters(ctx context.Context, params *eks.ListClustersInput, optFns ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	DescribeCluster(ctx context.Context, params *eks.DescribeClusterInput, optFns ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

// STSCallerIdentityAPI abstracts the STS GetCallerIdentity call for account ID extraction.
type STSCallerIdentityAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// DiscoverEKSClusters scans one or more AWS accounts for EKS clusters.
//
// If roleARNs is empty, it uses the default credentials (IRSA in-cluster).
// For each roleARN, it assumes the role via STS before scanning.
// The region parameter determines which AWS region to scan; if empty, it falls
// back to the SDK default (AWS_REGION / AWS_DEFAULT_REGION env vars).
func DiscoverEKSClusters(ctx context.Context, roleARNs []string, region string) ([]DiscoveredCluster, error) {
	return discoverEKSClustersWithFactory(ctx, roleARNs, region, nil, nil)
}

// discoverEKSClustersWithFactory is the internal implementation that accepts
// optional factory overrides for testing.
func discoverEKSClustersWithFactory(
	ctx context.Context,
	roleARNs []string,
	region string,
	eksClientFactory func(cfg aws.Config) EKSDiscoveryAPI,
	stsClientFactory func(cfg aws.Config) STSCallerIdentityAPI,
) ([]DiscoveredCluster, error) {
	// If no role ARNs, scan using default credentials (single identity).
	if len(roleARNs) == 0 {
		roleARNs = []string{""}
	}

	var allClusters []DiscoveredCluster
	var errors []string

	for _, roleARN := range roleARNs {
		clusters, err := discoverForIdentity(ctx, roleARN, region, eksClientFactory, stsClientFactory)
		if err != nil {
			errMsg := fmt.Sprintf("role %q: %s", roleARN, err.Error())
			slog.Error("[discover] failed to scan identity", "roleARN", roleARN, "error", err)

			if isAssumeRoleError(err) {
				errMsg += trustPolicyFix(roleARN)
			}
			errors = append(errors, errMsg)
			continue
		}
		allClusters = append(allClusters, clusters...)
	}

	if len(errors) > 0 && len(allClusters) == 0 {
		return nil, fmt.Errorf("all identity scans failed: %s", strings.Join(errors, "; "))
	}

	// Partial success — return results + error info.
	if len(errors) > 0 {
		return allClusters, fmt.Errorf("some identity scans failed: %s", strings.Join(errors, "; "))
	}

	return allClusters, nil
}

// discoverForIdentity scans EKS clusters using a single identity (default creds or assumed role).
func discoverForIdentity(
	ctx context.Context,
	roleARN, region string,
	eksClientFactory func(cfg aws.Config) EKSDiscoveryAPI,
	stsClientFactory func(cfg aws.Config) STSCallerIdentityAPI,
) ([]DiscoveredCluster, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	// Assume role if provided.
	if roleARN != "" {
		slog.Info("[discover] assuming role", "roleARN", roleARN)
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, roleARN)
		cfg.Credentials = aws.NewCredentialsCache(creds)
	}

	// Resolve account ID from caller identity.
	var accountID string
	var stsAPI STSCallerIdentityAPI
	if stsClientFactory != nil {
		stsAPI = stsClientFactory(cfg)
	} else {
		stsAPI = sts.NewFromConfig(cfg)
	}
	identity, err := stsAPI.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("getting caller identity: %w", err)
	}
	if identity.Account != nil {
		accountID = *identity.Account
	}

	// Build EKS client.
	var eksAPI EKSDiscoveryAPI
	if eksClientFactory != nil {
		eksAPI = eksClientFactory(cfg)
	} else {
		eksAPI = eks.NewFromConfig(cfg)
	}

	// List all cluster names.
	var clusterNames []string
	var nextToken *string
	for {
		out, err := eksAPI.ListClusters(ctx, &eks.ListClustersInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("listing EKS clusters: %w", err)
		}
		clusterNames = append(clusterNames, out.Clusters...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	slog.Info("[discover] found EKS clusters", "count", len(clusterNames), "account", accountID, "region", region)

	// Describe each cluster.
	effectiveRegion := region
	if effectiveRegion == "" {
		effectiveRegion = cfg.Region
	}

	var results []DiscoveredCluster
	for _, name := range clusterNames {
		out, err := eksAPI.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: aws.String(name),
		})
		if err != nil {
			slog.Warn("[discover] failed to describe cluster", "name", name, "error", err)
			results = append(results, DiscoveredCluster{
				Name:    name,
				Region:  effectiveRegion,
				Account: accountID,
				Status:  "UNKNOWN",
				Error:   err.Error(),
			})
			continue
		}

		c := out.Cluster
		dc := DiscoveredCluster{
			Name:    name,
			Region:  effectiveRegion,
			Account: accountID,
		}
		if c.Version != nil {
			dc.K8sVersion = *c.Version
		}
		if c.Endpoint != nil {
			dc.Endpoint = *c.Endpoint
		}
		dc.Status = string(c.Status)
		if c.Status == ekstypes.ClusterStatusFailed && c.Endpoint == nil {
			dc.Error = "cluster is in FAILED state"
		}

		results = append(results, dc)
	}

	return results, nil
}

// isAssumeRoleError checks if the error is related to STS AssumeRole failure.
func isAssumeRoleError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "AssumeRole") ||
		strings.Contains(msg, "AccessDenied") ||
		strings.Contains(msg, "not authorized to perform: sts:AssumeRole")
}

// trustPolicyFix returns a human-readable suggestion for fixing IAM trust policy issues.
func trustPolicyFix(roleARN string) string {
	return fmt.Sprintf(
		"\n\nTo fix this, update the trust policy on role %q to allow "+
			"your Sharko identity to assume it. Example trust policy statement:\n"+
			"{\n"+
			"  \"Effect\": \"Allow\",\n"+
			"  \"Principal\": { \"AWS\": \"<SHARKO_ROLE_ARN>\" },\n"+
			"  \"Action\": \"sts:AssumeRole\"\n"+
			"}", roleARN)
}
