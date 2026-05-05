package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// V124-3.2 / M2 extended — classifyUpstreamError pins the mapping from
// upstream error → HTTP status so handlers stop reporting every Git/ArgoCD
// failure as a flat 500. Operators and clients gain meaningful 502 / 504 /
// 429 signals without the body ever leaking the underlying error string
// (sanitization is preserved by writeUpstreamError → writeServerError).
//
// The branches under test:
//   - syscall.ECONNREFUSED        → 502 Bad Gateway
//   - *net.DNSError               → 502 Bad Gateway
//   - *url.Error with Timeout()   → 504 Gateway Timeout
//   - "rate limit" / "too many requests" / "429" string match → 429
//   - default                     → 500 Internal Server Error

func TestClassifyUpstreamError_ConnRefused_502(t *testing.T) {
	// Realistic shape: a net.OpError wrapping an os.SyscallError carrying
	// the ECONNREFUSED errno. errors.Is unwraps both.
	wrapped := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}
	got := classifyUpstreamError(wrapped)
	if got != http.StatusBadGateway {
		t.Errorf("ECONNREFUSED → %d, want %d (502 Bad Gateway)", got, http.StatusBadGateway)
	}
}

func TestClassifyUpstreamError_DNSError_502(t *testing.T) {
	wrapped := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{Name: "no.such.host.invalid", Err: "no such host", IsNotFound: true},
	}
	got := classifyUpstreamError(wrapped)
	if got != http.StatusBadGateway {
		t.Errorf("DNS lookup failure → %d, want %d (502 Bad Gateway)", got, http.StatusBadGateway)
	}
}

func TestClassifyUpstreamError_URLTimeout_504(t *testing.T) {
	// A *url.Error with Timeout() == true is what http.Client surfaces
	// when the request deadline elapses or the dialer times out.
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://api.github.com/repos/foo/bar",
		// timeoutError implements net.Error with Timeout() = true.
		Err: timeoutError{},
	}
	got := classifyUpstreamError(wrapped)
	if got != http.StatusGatewayTimeout {
		t.Errorf("url timeout → %d, want %d (504 Gateway Timeout)", got, http.StatusGatewayTimeout)
	}
}

func TestClassifyUpstreamError_URLNonTimeout_DefaultsTo500(t *testing.T) {
	// A *url.Error that is NOT a timeout (e.g. a TLS error) should NOT
	// be miscategorised as 504. With no other branch matching, we expect
	// the default 500.
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://example.com",
		Err: errors.New("x509: certificate signed by unknown authority"),
	}
	got := classifyUpstreamError(wrapped)
	if got != http.StatusInternalServerError {
		t.Errorf("non-timeout url err → %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestClassifyUpstreamError_RateLimit_429(t *testing.T) {
	// Three phrasings observed in the wild: GitHub uses "API rate limit
	// exceeded", Azure DevOps / Helm registries echo "Too Many Requests",
	// and bare "429" appears in some upstream error wrappings. All three
	// should classify identically.
	cases := []string{
		"GET https://api.github.com/repos/foo/bar: 403 API rate limit exceeded",
		"unexpected status: 429 Too Many Requests",
		"upstream replied 429",
		"RATE LIMIT EXCEEDED",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := classifyUpstreamError(errors.New(msg))
			if got != http.StatusTooManyRequests {
				t.Errorf("rate-limit string %q → %d, want %d (429)", msg, got, http.StatusTooManyRequests)
			}
		})
	}
}

func TestClassifyUpstreamError_DefaultIs500(t *testing.T) {
	// A plain opaque error has no category — return 500.
	got := classifyUpstreamError(errors.New("something blew up upstream"))
	if got != http.StatusInternalServerError {
		t.Errorf("opaque err → %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestClassifyUpstreamError_NilIs500(t *testing.T) {
	// Defensive: nil should not panic and should return the safe default.
	got := classifyUpstreamError(nil)
	if got != http.StatusInternalServerError {
		t.Errorf("nil err → %d, want %d", got, http.StatusInternalServerError)
	}
}

// TestWriteUpstreamError_PreservesSanitization is the integration check
// that pairs classifyUpstreamError with writeServerError: the response body
// must NOT include the underlying error string, the status MUST reflect
// the classification, and the op identifier MUST appear in the body for
// log correlation.
func TestWriteUpstreamError_PreservesSanitization(t *testing.T) {
	w := httptest.NewRecorder()
	leak := fmt.Errorf("dial tcp 10.0.0.1:443: connect: %w", syscall.ECONNREFUSED)

	writeUpstreamError(w, "list_addons", leak)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (classified ECONNREFUSED)", w.Code)
	}
	body := w.Body.String()
	for _, leaked := range []string{"10.0.0.1", "dial tcp", "connect"} {
		if strings.Contains(body, leaked) {
			t.Errorf("response body leaked %q: %s", leaked, body)
		}
	}
	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if parsed["error"] != http.StatusText(http.StatusBadGateway) {
		t.Errorf("error field = %q, want %q", parsed["error"], http.StatusText(http.StatusBadGateway))
	}
	if parsed["op"] != "list_addons" {
		t.Errorf("op field = %q, want %q", parsed["op"], "list_addons")
	}
}

// TestWriteUpstreamError_TimeoutBecomes504 mirrors the above for the 504
// branch — a different code path through classifyUpstreamError, same
// sanitization invariants.
func TestWriteUpstreamError_TimeoutBecomes504(t *testing.T) {
	w := httptest.NewRecorder()
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://api.github.com/repos/secret-org/secret-repo/contents/clusters/prod.yaml",
		Err: timeoutError{},
	}
	writeUpstreamError(w, "get_cluster_values", wrapped)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", w.Code)
	}
	body := w.Body.String()
	// The URL contains org/repo identifiers — must not leak.
	for _, leaked := range []string{"secret-org", "secret-repo", "api.github.com"} {
		if strings.Contains(body, leaked) {
			t.Errorf("response body leaked %q: %s", leaked, body)
		}
	}
}

// timeoutError is a tiny net.Error implementation whose Timeout() method
// returns true. We use it instead of waiting for a real http.Client timeout
// so the test runs in microseconds and stays hermetic.
type timeoutError struct{}

func (timeoutError) Error() string                        { return "i/o timeout" }
func (timeoutError) Timeout() bool                        { return true }
func (timeoutError) Temporary() bool                      { return true }
func (timeoutError) Deadline() (time.Time, bool)          { return time.Time{}, false }
