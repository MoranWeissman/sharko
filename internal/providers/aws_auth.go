package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

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
		// THE ERROR VALUE IS NOT LOGGED. An AWS SDK error can carry credential
		// material in its own text — a wrapped presigned URL, a token fragment,
		// a credential a provider chain put into its message. So the log line
		// carries the cluster, the region, and WHICH step failed, and nothing
		// else. The step name is what makes the two failures in this function
		// tellable apart without the error text.
		//
		// The returned error still wraps the cause with %w, which is correct and
		// unchanged: the caller decides what to do with it, and the API layer
		// already refuses to pass provider error text out to a user or into a log.
		slog.Error("[auth] EKS token generation failed", "cluster", clusterName, "region", region, "step", "load-aws-config")
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
		// Same rule as the load-config failure above, and this one is the more
		// dangerous of the two: a presigning error comes from the code path that
		// builds the credential itself, so its text is the likeliest place for a
		// signed URL or a signature fragment to turn up. The step name is
		// different from the one above so a person reading the log knows which
		// half of the function gave up. The returned error still wraps the cause.
		slog.Error("[auth] EKS token generation failed", "cluster", clusterName, "region", region, "step", "presign-get-caller-identity")
		return "", fmt.Errorf("presigning GetCallerIdentity for cluster %q: %w", clusterName, err)
	}

	// THE PRESIGNED URL IS THE CREDENTIAL. Anyone holding it can sign in as
	// Sharko for as long as it lives, so it is never logged — not in full, not
	// truncated, not as a length, and not as a hash. A prefix, a length and a
	// hash are all on the forbidden list for the same reason: each one narrows
	// a guess at the thing itself.
	//
	// The one diagnostic kept is a bool: was the cluster-name header actually
	// attached. That is the setting that stops a token for one cluster being
	// replayed against another, it is the thing that has actually gone wrong
	// before, and a yes/no carries no part of the URL.
	slog.Debug("[auth] STS presigned URL check",
		"cluster", clusterName,
		"hasClusterHeader", strings.Contains(req.URL, "x-k8s-aws-id"),
	)

	// Encode the presigned URL as a k8s-aws-v1 token (base64url, no padding).
	token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(req.URL))
	// "a token was minted for this cluster" is worth a line. Its length is not:
	// the length of a presigned URL narrows what is inside it, and this line
	// runs at Info, so it is on by default in production.
	slog.Info("[auth] EKS token generated", "cluster", clusterName)
	return token, nil
}
