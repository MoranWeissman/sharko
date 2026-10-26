package providers

import (
	"errors"
	"net"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// AWSFailure says what kind of failure a call to AWS was, in the two cases a
// person needs told apart (ep4, serious 6):
//
//   - AWS answered and turned down Sharko's login (a wrong, switched-off or
//     out-of-date access key, a bad signature, or a login that is not allowed
//     to read the secret);
//   - Sharko never got an answer from AWS at all.
//
// Before this, both fell into "Sharko can't reach your secrets store", which is
// false for the first one: AWS was reached, and it said no.
//
// It is decided by TYPE and by AWS's own error CODE (a fixed machine word such
// as "UnrecognizedClientException"). It never reads an error's message, so no
// text AWS sent back can reach anything that uses the answer.
type AWSFailure int

const (
	// AWSFailureNone means this is not an AWS failure Sharko can name. The
	// caller keeps whatever answer it had before.
	AWSFailureNone AWSFailure = iota
	// AWSFailureLoginRefused means AWS answered and turned Sharko's login down.
	AWSFailureLoginRefused
	// AWSFailureUnreachable means the request never got an answer from AWS.
	AWSFailureUnreachable
)

// awsLoginRefusedCodes are the AWS error codes that mean "AWS answered and did
// not accept this login, or did not let it do this". They are AWS's own fixed
// codes, the same across Secrets Manager and STS.
var awsLoginRefusedCodes = map[string]bool{
	// The access key is unknown to AWS, or it has been switched off
	// (Inactive). This is what an inactive key gives on Secrets Manager.
	"UnrecognizedClientException": true,
	"InvalidClientTokenId":        true,
	// A temporary login that has run out.
	"ExpiredToken":          true,
	"ExpiredTokenException": true,
	// The secret half of the key is wrong.
	"InvalidSignatureException":  true,
	"SignatureDoesNotMatch":      true,
	"IncompleteSignature":        true,
	"MissingAuthenticationToken": true,
	// The login is fine, but it is not allowed to read this.
	"AccessDeniedException": true,
	"AccessDenied":          true,
}

// ClassifyAWSFailure says which AWSFailure err is. nil, and any error that did
// not come from the AWS SDK, give AWSFailureNone.
func ClassifyAWSFailure(err error) AWSFailure {
	if err == nil {
		return AWSFailureNone
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		// AWS answered. Only its code is read, never its message.
		if awsLoginRefusedCodes[apiErr.ErrorCode()] {
			return AWSFailureLoginRefused
		}
		return AWSFailureNone
	}
	// No answer from AWS. Only an error from the AWS SDK counts, so a network
	// error from some other system is never called an AWS one.
	var opErr *smithy.OperationError
	if !errors.As(err, &opErr) {
		return AWSFailureNone
	}
	var sendErr *smithyhttp.RequestSendError
	var netErr net.Error
	if errors.As(err, &sendErr) || errors.As(err, &netErr) {
		return AWSFailureUnreachable
	}
	return AWSFailureNone
}
