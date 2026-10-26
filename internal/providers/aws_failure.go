package providers

import (
	"errors"
	"net"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// AWSFailure says what kind of failure a call to AWS was, in the three cases a
// person needs told apart (ep4, serious 6, and ep4 round R, R2):
//
//   - Sharko has no AWS login at all, so it never even asked AWS;
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
	// AWSFailureNoLogin means Sharko found no AWS login anywhere it looks, so
	// it never sent the request to AWS at all.
	AWSFailureNoLogin
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

// isAWSAccessDenied reports whether AWS answered err with its "the login is
// fine, but it is not allowed to read this" code. Read by AWS's own fixed
// error CODE, never by the message.
func isAWSAccessDenied(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDeniedException", "AccessDenied":
		return true
	}
	return false
}

// ClassifyAWSFailure says which AWSFailure err is. nil, and any error that did
// not come from the AWS SDK, give AWSFailureNone.
func ClassifyAWSFailure(err error) AWSFailure {
	if err == nil {
		return AWSFailureNone
	}
	// No login at all is checked FIRST. When the SDK finds no access key, no
	// profile and no web identity, the last place it looks is the EC2 instance
	// metadata service (IMDS). Off EC2 that call fails to connect, so the
	// error chain holds a RequestSendError and a net.Error, and the check below
	// would call it "AWS did not answer" — which is false: Sharko never asked
	// AWS, because it had no login to ask with (ep4 round R, R2).
	if hasNoAWSLogin(err) {
		return AWSFailureNoLogin
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

// hasNoAWSLogin reports whether err says Sharko had no AWS login to send.
//
// It is decided by TYPE only, never by the error's words:
//
//   - an *smithy.OperationError from the IMDS client (service "ec2imds")
//     anywhere in the chain. The SDK only calls IMDS for a login when every
//     other place it looks came up empty, so an IMDS failure inside a Secrets
//     Manager or STS call means "no login found". This covers IMDS being out
//     of reach (off EC2) and IMDS being switched off
//     (AWS_EC2_METADATA_DISABLED), which both come back as this type;
//   - a *credentials.StaticCredentialsEmptyError: a login was configured with
//     an empty access key.
//
// The whole chain is walked by hand, including errors.Join-style branches,
// because the outer error is ALSO an *smithy.OperationError (the Secrets
// Manager one), so a single errors.As would stop at the outer one and miss the
// IMDS one inside it.
func hasNoAWSLogin(err error) bool {
	for err != nil {
		if opErr, ok := err.(*smithy.OperationError); ok && opErr.Service() == imds.ServiceID {
			return true
		}
		if _, ok := err.(*credentials.StaticCredentialsEmptyError); ok {
			return true
		}
		if many, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range many.Unwrap() {
				if hasNoAWSLogin(e) {
					return true
				}
			}
			return false
		}
		err = errors.Unwrap(err)
	}
	return false
}
