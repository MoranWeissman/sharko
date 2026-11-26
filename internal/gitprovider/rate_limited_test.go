package gitprovider

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/go-github/v68/github"
)

func TestIsRateLimited(t *testing.T) {
	yes := map[string]error{
		"primary limit, wrapped": fmt.Errorf("get file content: %w", &github.RateLimitError{
			Response: &http.Response{StatusCode: http.StatusForbidden, Request: &http.Request{Method: http.MethodGet, URL: &url.URL{Host: "api.github.com"}}},
			Message:  "API rate limit exceeded",
		}),
		"abuse limit":           &github.AbuseRateLimitError{Response: &http.Response{StatusCode: http.StatusForbidden, Request: &http.Request{Method: http.MethodGet, URL: &url.URL{Host: "api.github.com"}}}, Message: "slow down"},
		"429 from the host":     &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusTooManyRequests}, Message: "x"},
		"other host, text only": errors.New("unexpected status: 429 Too Many Requests"),
	}
	for name, err := range yes {
		if !IsRateLimited(err) {
			t.Errorf("%s: want rate limited", name)
		}
	}
	no := map[string]error{
		"nil":            nil,
		"timeout":        errors.New("dial tcp: i/o timeout"),
		"missing file":   fmt.Errorf("x: %w", ErrFileNotFound),
		"plain 403":      &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusForbidden}, Message: "Resource not accessible"},
		"bad credential": &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusUnauthorized}, Message: "Bad credentials"},
	}
	for name, err := range no {
		if IsRateLimited(err) {
			t.Errorf("%s: must NOT read as a rate limit", name)
		}
	}
}
