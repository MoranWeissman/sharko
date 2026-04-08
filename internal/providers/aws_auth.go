package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	clusterIDHeader = "x-k8s-aws-id"
	v1Prefix        = "k8s-aws-v1."
)

// getEKSToken generates a short-lived bearer token for an EKS cluster using a
// presigned STS GetCallerIdentity URL. This is the same mechanism used by
// aws-iam-authenticator and argocd-k8s-auth.
//
// If roleARN is non-empty, the function assumes that role first (via
// AssumeRole) before presigning — matching ArgoCD's --role-arn behaviour.
// This is required when the pod's IRSA role does not have direct access to the
// target cluster and must assume a cross-account / cluster-specific role.
func getEKSToken(ctx context.Context, clusterName, region, roleARN string) (string, error) {
	slog.Info("[auth] generating EKS token", "cluster", clusterName, "region", region)

	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		slog.Error("[auth] EKS token generation failed", "cluster", clusterName, "region", region, "error", err)
		return "", fmt.Errorf("loading AWS config for EKS token: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)

	// If a target role is specified, assume it before presigning so that the
	// resulting token is signed by that role's credentials (not the pod's).
	if roleARN != "" {
		slog.Info("[auth] assuming role for EKS token", "role", roleARN, "cluster", clusterName)
		appCreds := stscreds.NewAssumeRoleProvider(stsClient, roleARN)
		cfg.Credentials = aws.NewCredentialsCache(appCreds)
		stsClient = sts.NewFromConfig(cfg)
	}

	presignClient := sts.NewPresignClient(stsClient)

	// Presign a GetCallerIdentity request that includes the cluster-name header.
	// The x-k8s-aws-id header is required so the EKS authenticator knows which
	// cluster the token is intended for, preventing token reuse across clusters.
	// X-Amz-Expires caps the token lifetime at 60 seconds (matching ArgoCD).
	req, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(po *sts.PresignOptions) {
			po.ClientOptions = append(po.ClientOptions, func(o *sts.Options) {
				o.APIOptions = append(o.APIOptions,
					smithyhttp.SetHeaderValue(clusterIDHeader, clusterName),
					smithyhttp.SetHeaderValue("X-Amz-Expires", "60"),
				)
			})
		},
	)
	if err != nil {
		slog.Error("[auth] EKS token generation failed", "cluster", clusterName, "region", region, "error", err)
		return "", fmt.Errorf("presigning GetCallerIdentity for cluster %q: %w", clusterName, err)
	}

	slog.Debug("[auth] STS presigned URL generated", "cluster", clusterName, "urlLength", len(req.URL))
	slog.Debug("[auth] STS presigned URL details", "cluster", clusterName, "urlHost", req.URL[:60])

	// Encode the presigned URL as a k8s-aws-v1 token (base64url, no padding).
	token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(req.URL))
	slog.Info("[auth] EKS token generated", "cluster", clusterName, "tokenLength", len(token))
	return token, nil
}
