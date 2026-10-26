package providers

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// noWordsAPIError is an AWS answer whose words must never be read. Reading
// them panics, so a classifier that looks at the message fails the test.
type noWordsAPIError struct{ code string }

func (e noWordsAPIError) Error() string                 { panic("the error text was read") }
func (e noWordsAPIError) ErrorMessage() string          { panic("the AWS message was read") }
func (e noWordsAPIError) ErrorCode() string             { return e.code }
func (e noWordsAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func awsOp(err error) error {
	return &smithy.OperationError{ServiceID: "Secrets Manager", OperationName: "ListSecrets", Err: err}
}

// imdsOp is the error the SDK's IMDS client gives back, the one the login
// chain hits last when it found no login anywhere else.
func imdsOp(err error) error {
	return &smithy.OperationError{ServiceID: imds.ServiceID, OperationName: "GetMetadata", Err: err}
}

func TestClassifyAWSFailure(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	cases := []struct {
		name string
		err  error
		want AWSFailure
	}{
		{"nil", nil, AWSFailureNone},
		{"inactive access key", awsOp(noWordsAPIError{"UnrecognizedClientException"}), AWSFailureLoginRefused},
		{"unknown key id", awsOp(noWordsAPIError{"InvalidClientTokenId"}), AWSFailureLoginRefused},
		{"wrong secret key", awsOp(noWordsAPIError{"InvalidSignatureException"}), AWSFailureLoginRefused},
		{"login not allowed to read", awsOp(noWordsAPIError{"AccessDeniedException"}), AWSFailureLoginRefused},
		{"secret not there is not a refused login", awsOp(noWordsAPIError{"ResourceNotFoundException"}), AWSFailureNone},
		{"no answer from AWS", awsOp(&smithyhttp.RequestSendError{Err: dialErr}), AWSFailureUnreachable},
		{"a network error not from AWS", dialErr, AWSFailureNone},
		{"plain error", errors.New("something"), AWSFailureNone},
		// The way the providers hand errors back: wrapped, then marked.
		// (fmt.Errorf reads the inner text when it is built, so these use an
		// ordinary AWS error rather than the one that panics.)
		{"refused, wrapped and marked", credsafe.Mark(fmt.Errorf("listing secrets: %w", awsOp(&smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: "words"}))), AWSFailureLoginRefused},
		{"unreachable, wrapped and marked", credsafe.Mark(fmt.Errorf("listing secrets: %w", awsOp(&smithyhttp.RequestSendError{Err: dialErr}))), AWSFailureUnreachable},
		// No AWS login at all (ep4 round R, R2). The SDK's last place to look
		// for a login is IMDS; its failure sits INSIDE the Secrets Manager
		// error, and it carries a RequestSendError and a net.Error too, which
		// must not turn it into "AWS did not answer".
		{"no login: IMDS out of reach", awsOp(fmt.Errorf("get identity: get credentials: %w", imdsOp(&smithyhttp.RequestSendError{Err: dialErr}))), AWSFailureNoLogin},
		{"no login: IMDS switched off", awsOp(fmt.Errorf("get identity: get credentials: %w", imdsOp(errors.New("x")))), AWSFailureNoLogin},
		{"no login: IMDS answered with an error code", awsOp(fmt.Errorf("get credentials: %w", imdsOp(noWordsAPIError{"NotFound"}))), AWSFailureNoLogin},
		{"no login: empty static key", awsOp(fmt.Errorf("get credentials: %w", &credentials.StaticCredentialsEmptyError{})), AWSFailureNoLogin},
		{"no login, wrapped and marked", credsafe.Mark(fmt.Errorf("listing secrets: %w", awsOp(fmt.Errorf("get credentials: %w", imdsOp(&smithyhttp.RequestSendError{Err: dialErr}))))), AWSFailureNoLogin},
		{"no login inside a joined error", errors.Join(errors.New("first try"), awsOp(imdsOp(errors.New("x")))), AWSFailureNoLogin},
		// The outer call being something other than Secrets Manager changes
		// nothing; the outer call being IMDS itself is also "no login".
		{"a refused login is still refused", awsOp(fmt.Errorf("wrapped: %w", noWordsAPIError{"UnrecognizedClientException"})), AWSFailureLoginRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAWSFailure(tc.err); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
