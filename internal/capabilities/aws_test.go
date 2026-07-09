package capabilities

import (
	"context"
	"errors"
	"testing"
)

// envMap builds a lookupEnv func backed by a plain map, for
// permutation-testing the env-marker classification without touching real
// process env vars.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func newTestDetector(env map[string]string, callerIdentityFn getCallerIdentityFn) *AWSDetector {
	return &AWSDetector{
		lookupEnv:        envMap(env),
		callerIdentityFn: callerIdentityFn,
	}
}

func TestAWSDetector_MethodClassification(t *testing.T) {
	const testARN = "arn:aws:sts::123456789012:assumed-role/SharkoRole/session"

	tests := []struct {
		name       string
		env        map[string]string
		wantMethod string
	}{
		{
			name: "irsa markers present",
			env: map[string]string{
				"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
				"AWS_ROLE_ARN":                "arn:aws:iam::123456789012:role/SharkoIRSARole",
			},
			wantMethod: MethodIRSA,
		},
		{
			name: "irsa requires BOTH markers - token file only",
			env: map[string]string{
				"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
			},
			// Falls through to pod-identity check (absent), then STS still
			// succeeds via the injected fn in this test → chain.
			wantMethod: MethodChain,
		},
		{
			name: "pod identity marker present",
			env: map[string]string{
				"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://169.254.170.23/v1/credentials",
			},
			wantMethod: MethodPodIdentity,
		},
		{
			name:       "no markers, STS still resolves via default chain",
			env:        map[string]string{},
			wantMethod: MethodChain,
		},
		{
			name: "irsa takes precedence over pod identity when both present",
			env: map[string]string{
				"AWS_WEB_IDENTITY_TOKEN_FILE":        "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
				"AWS_ROLE_ARN":                       "arn:aws:iam::123456789012:role/SharkoIRSARole",
				"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://169.254.170.23/v1/credentials",
			},
			wantMethod: MethodIRSA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDetector(tt.env, func(ctx context.Context) (string, error) {
				return testARN, nil
			})
			got := d.Detect(context.Background())
			if !got.Detected {
				t.Fatalf("Detected = false, want true")
			}
			if got.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.IdentityARN != testARN {
				t.Errorf("IdentityARN = %q, want %q", got.IdentityARN, testARN)
			}
		})
	}
}

func TestAWSDetector_STSFailure_DegradesToNone(t *testing.T) {
	env := map[string]string{
		"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
		"AWS_ROLE_ARN":                "arn:aws:iam::123456789012:role/SharkoIRSARole",
	}
	d := newTestDetector(env, func(ctx context.Context) (string, error) {
		return "", errors.New("sts: connection refused")
	})

	got := d.Detect(context.Background())
	if got.Detected {
		t.Fatalf("Detected = true, want false on STS failure")
	}
	if got.Method != MethodNone {
		t.Errorf("Method = %q, want %q", got.Method, MethodNone)
	}
	if got.IdentityARN != "" {
		t.Errorf("IdentityARN = %q, want empty", got.IdentityARN)
	}
}

func TestAWSDetector_NoMarkers_STSFails_DegradesToNone(t *testing.T) {
	d := newTestDetector(map[string]string{}, func(ctx context.Context) (string, error) {
		return "", errors.New("no credentials found")
	})

	got := d.Detect(context.Background())
	if got.Detected {
		t.Fatalf("Detected = true, want false")
	}
	if got.Method != MethodNone {
		t.Errorf("Method = %q, want %q", got.Method, MethodNone)
	}
}

// TestAWSDetector_CachesResult verifies sts:GetCallerIdentity is called at
// MOST ONCE across repeated Detect calls — the hard "never per-request"
// requirement.
func TestAWSDetector_CachesResult(t *testing.T) {
	calls := 0
	d := newTestDetector(map[string]string{}, func(ctx context.Context) (string, error) {
		calls++
		return "arn:aws:sts::123456789012:assumed-role/SharkoRole/session", nil
	})

	for i := 0; i < 5; i++ {
		d.Detect(context.Background())
	}

	if calls != 1 {
		t.Errorf("sts:GetCallerIdentity called %d times, want exactly 1 (cached)", calls)
	}
}

// TestAWSDetector_CachesFailureToo verifies a failed detection is ALSO
// cached — a transient STS outage at startup must not turn into a
// per-request retry storm.
func TestAWSDetector_CachesFailureToo(t *testing.T) {
	calls := 0
	d := newTestDetector(map[string]string{}, func(ctx context.Context) (string, error) {
		calls++
		return "", errors.New("timeout")
	})

	for i := 0; i < 5; i++ {
		got := d.Detect(context.Background())
		if got.Detected {
			t.Fatalf("Detected = true on call %d, want false", i)
		}
	}

	if calls != 1 {
		t.Errorf("sts:GetCallerIdentity called %d times, want exactly 1 (cached even on failure)", calls)
	}
}

func TestAWSDetector_EmptyARN_TreatedAsFailure(t *testing.T) {
	d := newTestDetector(map[string]string{}, func(ctx context.Context) (string, error) {
		return "", nil
	})
	got := d.Detect(context.Background())
	if got.Detected {
		t.Fatalf("Detected = true with empty ARN, want false")
	}
}

func TestNewAWSDetector_DefaultsWired(t *testing.T) {
	d := NewAWSDetector()
	if d.lookupEnv == nil {
		t.Error("lookupEnv not defaulted")
	}
	if d.callerIdentityFn == nil {
		t.Error("callerIdentityFn not defaulted")
	}
	if d.timeout != defaultSTSTimeout {
		t.Errorf("timeout = %v, want %v", d.timeout, defaultSTSTimeout)
	}
}
