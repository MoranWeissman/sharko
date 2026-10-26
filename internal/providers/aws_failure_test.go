package providers

import (
	"errors"
	"fmt"
	"net"
	"testing"

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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAWSFailure(tc.err); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
