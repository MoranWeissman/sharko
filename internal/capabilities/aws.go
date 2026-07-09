// Package capabilities auto-detects facts about Sharko's own runtime
// environment that used to require the user to know and type them in by
// hand at cluster-registration time (V2-cleanup-88.1, design L11):
//
//   - Is Sharko itself running with an AWS identity, and which one
//     (IRSA / EKS Pod Identity / default credential chain)?
//   - Does the hub cluster Sharko runs on look like EKS?
//
// Both detectors are deliberately cheap and cached: the AWS identity check
// makes exactly one sts:GetCallerIdentity call for the lifetime of the
// process (never per-request — an AWS identity does not change at
// runtime), and the hub-platform check is a single cached Kubernetes
// Discovery call. Neither detector ever blocks or slows an unrelated
// request; both degrade gracefully to "nothing detected" on any error or
// timeout.
package capabilities

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWS identity detection method values (systemCapabilitiesResponse.AWS.Method).
const (
	// MethodPodIdentity means the EKS Pod Identity marker
	// (AWS_CONTAINER_CREDENTIALS_FULL_URI) was present.
	MethodPodIdentity = "pod-identity"
	// MethodIRSA means both IRSA markers (AWS_WEB_IDENTITY_TOKEN_FILE +
	// AWS_ROLE_ARN) were present.
	MethodIRSA = "irsa"
	// MethodChain means neither IRSA nor Pod Identity env markers were
	// present, but the default AWS credential chain still resolved to an
	// identity (e.g. an EC2 instance profile, static keys, or an
	// operator-mounted shared config file).
	MethodChain = "chain"
	// MethodNone means no AWS identity was detected at all.
	MethodNone = "none"
)

// defaultSTSTimeout bounds the single sts:GetCallerIdentity call so a
// network-partitioned or unreachable STS endpoint can never hang a request
// — the detector degrades to "no AWS identity" instead.
const defaultSTSTimeout = 3 * time.Second

var errNoCallerARN = errors.New("sts GetCallerIdentity returned no ARN")

// AWSIdentity is the result of detecting whether Sharko itself is running
// with an AWS identity.
type AWSIdentity struct {
	// Detected is true iff sts:GetCallerIdentity succeeded.
	Detected bool `json:"detected"`
	// Method is one of MethodPodIdentity, MethodIRSA, MethodChain, or
	// MethodNone.
	Method string `json:"method"`
	// IdentityARN is the caller's ARN, e.g.
	// "arn:aws:sts::123456789012:assumed-role/SharkoIRSARole/...". Empty
	// when Detected is false.
	IdentityARN string `json:"identity_arn,omitempty"`
}

// getCallerIdentityFn abstracts the single AWS network call the detector
// makes, so tests can inject success/failure/timeout without touching real
// AWS. Production code uses defaultGetCallerIdentity.
type getCallerIdentityFn func(ctx context.Context) (arn string, err error)

// AWSDetector detects and caches Sharko's own AWS runtime identity.
//
// The result is computed at most ONCE per process lifetime (sync.Once) and
// cached forever after — this is the "generous TTL" the design calls for,
// since an identity does not change while a pod is running. Detect is safe
// for concurrent use; sts:GetCallerIdentity is NEVER called per-request.
type AWSDetector struct {
	once   sync.Once
	result AWSIdentity

	// Test seams (per-instance fields, not package-level vars — see
	// go-expert.md "Per-instance test seams" convention). Defaulted in
	// NewAWSDetector; overridden per-test.
	lookupEnv        func(string) string
	callerIdentityFn getCallerIdentityFn
	timeout          time.Duration
}

// NewAWSDetector returns a detector wired to the real environment and AWS
// SDK default credential chain.
func NewAWSDetector() *AWSDetector {
	return &AWSDetector{
		lookupEnv:        os.Getenv,
		callerIdentityFn: defaultGetCallerIdentity,
		timeout:          defaultSTSTimeout,
	}
}

// Detect returns Sharko's own AWS identity, computing it on the first call
// and returning the cached result on every subsequent call.
func (d *AWSDetector) Detect(ctx context.Context) AWSIdentity {
	d.once.Do(func() {
		d.result = d.detect(ctx)
	})
	return d.result
}

func (d *AWSDetector) detect(ctx context.Context) AWSIdentity {
	method := d.methodFromEnvMarkers()

	timeout := d.timeout
	if timeout <= 0 {
		timeout = defaultSTSTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	arn, err := d.callerIdentityFn(cctx)
	if err == nil && arn == "" {
		// Defense in depth: an implementation (including test doubles)
		// that returns a nil error with no ARN is treated the same as an
		// explicit failure — an empty ARN is never a valid detection.
		err = errNoCallerARN
	}
	if err != nil {
		if method != MethodNone {
			// An IRSA/Pod-Identity marker was present but the identity
			// still couldn't be resolved — worth a WARN, not just a
			// quiet degrade, since it usually means a misconfigured role.
			slog.Warn("[capabilities] AWS identity markers present but sts:GetCallerIdentity failed",
				"marker_method", method, "error", err)
		} else {
			slog.Debug("[capabilities] no AWS identity detected", "error", err)
		}
		return AWSIdentity{Detected: false, Method: MethodNone}
	}

	if method == MethodNone {
		// STS resolved an identity despite no IRSA/Pod-Identity env
		// markers — some other link in the default credential chain
		// (instance profile, static keys, mounted shared config) is
		// supplying it.
		method = MethodChain
	}
	return AWSIdentity{Detected: true, Method: method, IdentityARN: arn}
}

// methodFromEnvMarkers classifies the AWS identity mechanism from
// well-known env vars alone — no network call. IRSA is checked first
// because a pod can carry both an IRSA projected-token mount AND (on newer
// EKS versions) the Pod Identity agent's env var; IRSA is the more specific
// signal Sharko's own Helm chart wires (serviceAccount.annotations.
// eks.amazonaws.com/role-arn), so it takes precedence when both are present.
func (d *AWSDetector) methodFromEnvMarkers() string {
	if d.lookupEnv("AWS_WEB_IDENTITY_TOKEN_FILE") != "" && d.lookupEnv("AWS_ROLE_ARN") != "" {
		return MethodIRSA
	}
	if d.lookupEnv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
		return MethodPodIdentity
	}
	return MethodNone
}

// defaultGetCallerIdentity loads the default AWS config (which itself
// resolves IRSA / EKS Pod Identity / instance-profile / static credentials
// via the standard SDK credential chain) and calls sts:GetCallerIdentity to
// resolve the actual identity ARN.
func defaultGetCallerIdentity(ctx context.Context) (string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}
	client := sts.NewFromConfig(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	if out.Arn == nil || *out.Arn == "" {
		return "", errNoCallerARN
	}
	return *out.Arn, nil
}
