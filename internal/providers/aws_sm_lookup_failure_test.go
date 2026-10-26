package providers

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ep4 round R, R6: when every lookup of a cluster's secret fails, the error
// that comes back must still say, by TYPE, why. Before, the AWS error was
// dropped and the read-only check (StoredConnectionFacts) could not tell a
// refused login from an AWS that never answered, or from a missing secret.
//
// Every value here is fake.

// awsWordsSentinel stands for words AWS sent back. They must never be in the
// text of the error a caller sees.
const awsWordsSentinel = "FAKE-AWS-WORDS-arn-of-a-fake-role"

// perNameSMClient answers each secret name with its own error.
type perNameSMClient struct {
	errs  map[string]error
	calls []string
}

func (f *perNameSMClient) GetSecretValue(_ context.Context, params *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	name := aws.ToString(params.SecretId)
	f.calls = append(f.calls, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	return nil, smOp(&types.ResourceNotFoundException{Message: aws.String(awsWordsSentinel)})
}

func (f *perNameSMClient) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	return &secretsmanager.ListSecretsOutput{}, nil
}

func smOp(err error) error {
	return &smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "GetSecretValue", Err: err}
}

func TestAWSLookupFailure_KeepsWhyByType(t *testing.T) {
	refused := smOp(&smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: awsWordsSentinel})
	unreachable := smOp(&smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(awsWordsSentinel)}})
	noLogin := smOp(imdsOp(errors.New(awsWordsSentinel)))
	missing := smOp(&types.ResourceNotFoundException{Message: aws.String(awsWordsSentinel)})
	denied := smOp(&smithy.GenericAPIError{Code: "AccessDeniedException", Message: awsWordsSentinel})
	deniedS3Code := smOp(&smithy.GenericAPIError{Code: "AccessDenied", Message: awsWordsSentinel})

	const prefixed = "clusters/fake-cluster"
	const exact = "fake-cluster"

	cases := []struct {
		name           string
		errs           map[string]error
		wantKind       AWSFailure
		wantNotFound   bool
		wantTried      []string
		wantNotAllowed []string
	}{
		{"login refused on both names", map[string]error{prefixed: refused, exact: refused}, AWSFailureLoginRefused, false, nil, nil},
		{"AWS never answered", map[string]error{prefixed: unreachable, exact: unreachable}, AWSFailureUnreachable, false, nil, nil},
		{"no AWS login at all", map[string]error{prefixed: noLogin, exact: noLogin}, AWSFailureNoLogin, false, nil, nil},
		{"secret is not there under either name", map[string]error{prefixed: missing, exact: missing}, AWSFailureNone, true, []string{prefixed, exact}, nil},
		{"missing with the prefix, refused without it", map[string]error{prefixed: missing, exact: refused}, AWSFailureLoginRefused, false, nil, nil},
		{"refused with the prefix, missing without it", map[string]error{prefixed: refused, exact: missing}, AWSFailureLoginRefused, false, nil, nil},
		// ep4 round S, S3: the docs' policy only lets Sharko read names under
		// the prefix. A secret that is really missing then gives "not found"
		// for the prefixed name and "access denied" for the plain one. That
		// is a missing secret, not a refused login.
		{"missing with the prefix, access denied without it", map[string]error{prefixed: missing, exact: denied}, AWSFailureNone, true, []string{prefixed}, []string{exact}},
		{"missing with the prefix, AccessDenied code without it", map[string]error{prefixed: missing, exact: deniedS3Code}, AWSFailureNone, true, []string{prefixed}, []string{exact}},
		// Denied on the prefixed name is a real refusal: Sharko should be
		// allowed to read under its own prefix.
		{"access denied with the prefix, missing without it", map[string]error{prefixed: denied, exact: missing}, AWSFailureLoginRefused, false, nil, nil},
		{"access denied on both names", map[string]error{prefixed: denied, exact: denied}, AWSFailureLoginRefused, false, nil, nil},
	}

	paths := []struct {
		name string
		call func(p *AWSSecretsManagerProvider) error
	}{
		{"StoredConnectionFacts (the read-only check)", func(p *AWSSecretsManagerProvider) error {
			_, err := p.StoredConnectionFacts(exact)
			return err
		}},
		{"GetCredentials", func(p *AWSSecretsManagerProvider) error {
			_, err := p.GetCredentials(exact)
			return err
		}},
	}

	for _, path := range paths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				client := &perNameSMClient{errs: tc.errs}
				p := &AWSSecretsManagerProvider{client: client, prefix: "clusters/"}
				p.eksTokenFn = func(context.Context, string, string, string) (string, error) {
					t.Fatal("a failed lookup must never make a sign-in token")
					return "", nil
				}

				err := path.call(p)
				if err == nil {
					t.Fatal("want an error, got nil")
				}
				if len(client.calls) != 2 {
					t.Fatalf("want both names tried, got %v", client.calls)
				}
				if got := ClassifyAWSFailure(err); got != tc.wantKind {
					t.Errorf("ClassifyAWSFailure = %v, want %v", got, tc.wantKind)
				}
				if got := credsafe.IsNotFound(err); got != tc.wantNotFound {
					t.Errorf("credsafe.IsNotFound = %v, want %v", got, tc.wantNotFound)
				}
				if !credsafe.Is(err) {
					t.Error("the error must carry the credsafe mark, so its text is the safe sentence")
				}
				tried, notAllowed, ok := SecretNotFoundNames(err)
				if ok != tc.wantNotFound {
					t.Errorf("SecretNotFoundNames ok = %v, want %v", ok, tc.wantNotFound)
				}
				if strings.Join(tried, "|") != strings.Join(tc.wantTried, "|") {
					t.Errorf("names not found = %q, want %q", tried, tc.wantTried)
				}
				if strings.Join(notAllowed, "|") != strings.Join(tc.wantNotAllowed, "|") {
					t.Errorf("names not allowed = %q, want %q", notAllowed, tc.wantNotAllowed)
				}
				if strings.Contains(err.Error(), awsWordsSentinel) {
					t.Errorf("AWS's own words reached the error text: %q", err.Error())
				}
			})
		}
	}
}

// With no prefix there is only the plain name, so an access denied on it is
// a real refusal (ep4 round S, S3: the not-found answer needs the prefixed
// name to have been not found first).
func TestAWSLookupFailure_NoPrefixDeniedIsStillRefused(t *testing.T) {
	denied := smOp(&smithy.GenericAPIError{Code: "AccessDeniedException", Message: awsWordsSentinel})
	client := &perNameSMClient{errs: map[string]error{"fake-cluster": denied}}
	p := &AWSSecretsManagerProvider{client: client}
	_, err := p.StoredConnectionFacts("fake-cluster")
	if got := ClassifyAWSFailure(err); got != AWSFailureLoginRefused {
		t.Fatalf("ClassifyAWSFailure = %v, want %v", got, AWSFailureLoginRefused)
	}
	if credsafe.IsNotFound(err) {
		t.Fatal("an access denied with no prefix was called not found")
	}
	if _, _, ok := SecretNotFoundNames(err); ok {
		t.Fatal("SecretNotFoundNames answered for an access denied")
	}
}
