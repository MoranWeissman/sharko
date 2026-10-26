package providers

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// TestClassifyAWSFailure_RealSDKErrors builds the errors with the REAL AWS
// SDK, not by hand, so the classifier is checked against the shape the SDK
// really returns (ep4 round R, R2). Nothing here reaches AWS: every address
// is a closed local port.
func TestClassifyAWSFailure_RealSDKErrors(t *testing.T) {
	// A local port that is closed for sure: listen, note it, close it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	_ = ln.Close()

	// No login anywhere the SDK looks.
	noLoginEnv := func(t *testing.T) {
		t.Setenv("AWS_CONFIG_FILE", "/dev/null")
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
		for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE",
			"AWS_DEFAULT_PROFILE", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
			"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_EC2_METADATA_DISABLED"} {
			t.Setenv(k, "")
		}
		t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", closed)
	}
	call := func(t *testing.T, opts ...func(*config.LoadOptions) error) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		opts = append(opts, config.WithRegion("us-east-1"),
			config.WithRetryer(func() aws.Retryer { return aws.NopRetryer{} }),
			config.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
		cfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			t.Fatalf("loading the AWS config: %v", err)
		}
		_, err = secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) {
			o.BaseEndpoint = aws.String(closed)
		}).ListSecrets(ctx, &secretsmanager.ListSecretsInput{})
		if err == nil {
			t.Fatal("the call worked, so this proves nothing")
		}
		return err
	}

	t.Run("no login, IMDS out of reach", func(t *testing.T) {
		noLoginEnv(t)
		if got := ClassifyAWSFailure(call(t)); got != AWSFailureNoLogin {
			t.Fatalf("got %d, want AWSFailureNoLogin (%d)", got, AWSFailureNoLogin)
		}
	})
	t.Run("no login, IMDS switched off", func(t *testing.T) {
		noLoginEnv(t)
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		if got := ClassifyAWSFailure(call(t)); got != AWSFailureNoLogin {
			t.Fatalf("got %d, want AWSFailureNoLogin (%d)", got, AWSFailureNoLogin)
		}
	})
	t.Run("a login, but AWS out of reach", func(t *testing.T) {
		noLoginEnv(t)
		err := call(t, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test-key-id", "test-secret", "")))
		if got := ClassifyAWSFailure(err); got != AWSFailureUnreachable {
			t.Fatalf("got %d, want AWSFailureUnreachable (%d)", got, AWSFailureUnreachable)
		}
	})
}
