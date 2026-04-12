package verify

import "strings"

// ErrorCode classifies a connectivity test failure.
type ErrorCode string

const (
	ERR_NETWORK    ErrorCode = "ERR_NETWORK"
	ERR_TLS        ErrorCode = "ERR_TLS"
	ERR_AUTH       ErrorCode = "ERR_AUTH"
	ERR_RBAC       ErrorCode = "ERR_RBAC"
	ERR_AWS_STS    ErrorCode = "ERR_AWS_STS"
	ERR_AWS_ASSUME ErrorCode = "ERR_AWS_ASSUME"
	ERR_QUOTA      ErrorCode = "ERR_QUOTA"
	ERR_NAMESPACE  ErrorCode = "ERR_NAMESPACE"
	ERR_TIMEOUT    ErrorCode = "ERR_TIMEOUT"
	ERR_UNKNOWN    ErrorCode = "ERR_UNKNOWN"
)

// ClassifyError examines the error message to determine the appropriate ErrorCode.
func ClassifyError(err error) ErrorCode {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp"):
		return ERR_NETWORK
	case strings.Contains(msg, "x509") || strings.Contains(msg, "certificate"):
		return ERR_TLS
	case strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "token expired"):
		return ERR_AUTH
	case strings.Contains(msg, "forbidden") || strings.Contains(msg, "Forbidden"):
		return ERR_RBAC
	case strings.Contains(msg, "GetToken") || strings.Contains(msg, "no identity provider"):
		return ERR_AWS_STS
	case strings.Contains(msg, "AssumeRole") || strings.Contains(msg, "not authorized to assume"):
		return ERR_AWS_ASSUME
	case strings.Contains(msg, "throttl") || strings.Contains(msg, "Too many requests"):
		return ERR_QUOTA
	case strings.Contains(msg, "admission webhook") || strings.Contains(msg, "namespace"):
		return ERR_NAMESPACE
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return ERR_TIMEOUT
	default:
		return ERR_UNKNOWN
	}
}
