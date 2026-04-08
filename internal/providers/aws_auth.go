package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
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
func getEKSToken(ctx context.Context, clusterName, region string) (string, error) {
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
	presignClient := sts.NewPresignClient(stsClient)

	// Presign a GetCallerIdentity request that includes the cluster-name header.
	// The x-k8s-aws-id header is required so the EKS authenticator knows which
	// cluster the token is intended for, preventing token reuse across clusters.
	req, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(po *sts.PresignOptions) {
			po.ClientOptions = append(po.ClientOptions,
				sts.WithAPIOptions(
					smithyhttp.AddHeaderValue(clusterIDHeader, clusterName),
				),
			)
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
