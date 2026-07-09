package capabilities

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// defaultAssumeRoleTimeout bounds a single sts:AssumeRole attempt so a
// network-partitioned or unreachable STS endpoint can never hang a caller —
// mirrors defaultSTSTimeout's role for the identity detector.
const defaultAssumeRoleTimeout = 5 * time.Second

// assumeRoleSessionName is the fixed RoleSessionName used for every doctor
// probe — this is a real, attempted assumption (not a persisted session),
// so a static, clearly-labelled name is sufficient; it never appears
// anywhere but AWS's own CloudTrail record of the attempt.
const assumeRoleSessionName = "sharko-connection-doctor"

// assumeRoleDurationSeconds requests the shortest session STS allows
// (the global minimum). Nothing from the session is stored or reused —
// the checker only cares whether the AssumeRole call itself succeeds.
const assumeRoleDurationSeconds = int32(900)

// fallbackAssumeRoleRegion is used when no region could be resolved for the
// target cluster. AssumeRole is (practically) a global STS operation; the
// region only selects which regional STS endpoint signs the request, so any
// valid region value produces a correct pass/fail result.
const fallbackAssumeRoleRegion = "us-east-1"

// assumeRoleFn abstracts the single AWS network call the checker makes, so
// tests can inject success/failure without touching real AWS. Production
// code uses defaultAssumeRole.
type assumeRoleFn func(ctx context.Context, roleARN, region string) error

// AssumeRoleChecker attempts a real, short-lived STS AssumeRole call with
// Sharko's own AWS identity — an ATTEMPT, not IAM policy simulation. Nothing
// returned by a successful call is stored or reused; the checker only
// reports whether the attempt succeeded.
//
// Unlike AWSDetector, results are NOT cached — a role's assumability can
// change independently of Sharko's own identity (trust policy edits,
// permission boundary changes, ...), and the connection doctor
// (V2-cleanup-88.4) that is this type's only caller is itself an
// on-demand, attempt-based tool. Every Check call makes a fresh AWS request.
type AssumeRoleChecker struct {
	// Test seam (per-instance field, not a package-level var — see
	// go-expert.md "Per-instance test seams" convention). Defaulted in
	// NewAssumeRoleChecker; overridden per-test.
	assumeRoleFn assumeRoleFn
	timeout      time.Duration
}

// NewAssumeRoleChecker returns a checker wired to the real AWS SDK default
// credential chain.
func NewAssumeRoleChecker() *AssumeRoleChecker {
	return &AssumeRoleChecker{
		assumeRoleFn: defaultAssumeRole,
		timeout:      defaultAssumeRoleTimeout,
	}
}

// Check attempts to assume roleARN with Sharko's own AWS identity. Returns
// nil on success (the role was assumed — nothing about that session is kept),
// or the AWS error otherwise (access denied, no usable identity, etc.).
// region may be empty; the checker falls back to fallbackAssumeRoleRegion.
func (c *AssumeRoleChecker) Check(ctx context.Context, roleARN, region string) error {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultAssumeRoleTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.assumeRoleFn(cctx, roleARN, region)
}

// defaultAssumeRole loads the default AWS config (resolving IRSA / EKS Pod
// Identity / instance-profile / static credentials via the standard SDK
// credential chain — the SAME chain AWSDetector and getEKSToken use) and
// calls sts:AssumeRole. It never persists or returns the resulting
// short-lived credentials — only whether the call succeeded.
func defaultAssumeRole(ctx context.Context, roleARN, region string) error {
	if region == "" {
		region = fallbackAssumeRoleRegion
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	client := sts.NewFromConfig(cfg)
	sessionName := assumeRoleSessionName
	duration := assumeRoleDurationSeconds
	_, err = client.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         &roleARN,
		RoleSessionName: &sessionName,
		DurationSeconds: &duration,
	})
	return err
}
