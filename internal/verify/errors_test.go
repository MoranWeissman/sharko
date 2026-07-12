package verify

import (
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestClassifyError_TypedAndNewPhrases covers the V2-cleanup-26 additions:
// typed-HTTP-status primary classification plus the real client-go 401 phrase
// fallback. Broad string-match regression coverage lives in TestClassifyError
// (stage1_test.go).
func TestClassifyError_TypedAndNewPhrases(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorCode
	}{
		// --- Primary path: typed HTTP status (robust, wording-independent) ---
		{
			name: "typed Unauthorized (401) -> ERR_AUTH",
			err:  apierrors.NewUnauthorized("Unauthorized"),
			want: ERR_AUTH,
		},
		{
			name: "typed Forbidden (403) -> ERR_RBAC",
			err:  apierrors.NewForbidden(schema.GroupResource{}, "x", nil),
			want: ERR_RBAC,
		},
		// --- Fallback path: string match for non-typed / wrapped errors ---
		{
			name: "real client-go 401 phrase -> ERR_AUTH",
			err:  errors.New("the server has asked for the client to provide credentials"),
			want: ERR_AUTH,
		},
		{
			name: "case-insensitive unauthorized -> ERR_AUTH",
			err:  errors.New("request failed: unauthorized"),
			want: ERR_AUTH,
		},
		// --- Regression spot-checks for non-auth codes still resolving ---
		{
			name: "connection refused still -> ERR_NETWORK",
			err:  errors.New("dial tcp 10.0.0.1:6443: connection refused"),
			want: ERR_NETWORK,
		},
		{
			name: "x509 cert still -> ERR_TLS",
			err:  errors.New("x509: certificate signed by unknown authority"),
			want: ERR_TLS,
		},
		{
			name: "deadline exceeded still -> ERR_TIMEOUT",
			err:  errors.New("context deadline exceeded"),
			want: ERR_TIMEOUT,
		},
		{
			name: "nil error -> ERR_UNKNOWN",
			err:  nil,
			want: ERR_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyError(tt.err); got != tt.want {
				t.Errorf("ClassifyError(%v) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

func TestHint(t *testing.T) {
	if got := Hint(ERR_AUTH); !strings.Contains(got, "HTTP 401") || !strings.Contains(got, "regenerate") {
		t.Errorf("ERR_AUTH hint missing actionable guidance: %q", got)
	}
	if got := Hint(ERR_RBAC); !strings.Contains(got, "HTTP 403") {
		t.Errorf("ERR_RBAC hint missing 403 guidance: %q", got)
	}
	if got := Hint(ERR_AWS_STS); got == "" || !strings.Contains(got, "STS") {
		t.Errorf("ERR_AWS_STS hint must be non-empty and mention STS: %q", got)
	}
	if got := Hint(ERR_AWS_ASSUME); got == "" || !strings.Contains(got, "assume-role") {
		t.Errorf("ERR_AWS_ASSUME hint must be non-empty and mention assume-role: %q", got)
	}
	if got := Hint(ERR_NETWORK); got != "" {
		t.Errorf("expected empty hint for ERR_NETWORK, got %q", got)
	}
}

func TestFriendlyMessage_AuthIncludesHintAndRawCause(t *testing.T) {
	raw := "the server has asked for the client to provide credentials"
	r := Result{
		Success:      false,
		ErrorCode:    ERR_AUTH,
		ErrorMessage: raw,
	}
	msg := FriendlyMessage(r)

	// Actionable hint is present.
	if !strings.Contains(msg, "HTTP 401") || !strings.Contains(msg, "regenerate") {
		t.Errorf("friendly message missing actionable ERR_AUTH hint: %q", msg)
	}
	// Raw cause is preserved for diagnosis.
	if !strings.Contains(msg, raw) {
		t.Errorf("friendly message dropped the raw cause %q: got %q", raw, msg)
	}
	// Error code is still surfaced.
	if !strings.Contains(msg, "[ERR_AUTH]") {
		t.Errorf("friendly message missing error code: %q", msg)
	}
}

func TestFriendlyMessage_NoHintKeepsRawCause(t *testing.T) {
	raw := "dial tcp 10.0.0.1:6443: connection refused"
	r := Result{
		Success:      false,
		ErrorCode:    ERR_NETWORK,
		ErrorMessage: raw,
	}
	msg := FriendlyMessage(r)

	if !strings.Contains(msg, raw) {
		t.Errorf("friendly message dropped the raw cause %q: got %q", raw, msg)
	}
	if strings.Contains(msg, " — ") {
		t.Errorf("did not expect a hint suffix for ERR_NETWORK: %q", msg)
	}
}

func TestAssumeRoleHint(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantContains []string
		wantNotContains []string
	}{
		{
			name:         "trust policy rejection - not authorized to assume",
			err:          errors.New("User: arn:aws:sts::123456789012:assumed-role/sharko-role/session is not authorized to assume role arn:aws:iam::123456789012:role/target-role"),
			wantContains: []string{"trust policy", "Sharko's identity"},
			wantNotContains: []string{"sts:AssumeRole permission", "sts:TagSession"},
		},
		{
			name:         "trust policy rejection - AccessDenied on AssumeRole",
			err:          errors.New("operation error STS: AssumeRole, https response error StatusCode: 403, api error AccessDenied: User is not authorized to perform: sts:AssumeRole on resource"),
			wantContains: []string{"trust policy", "IAM principal"},
			wantNotContains: []string{"sts:TagSession"},
		},
		{
			name:         "missing sts:TagSession permission",
			err:          errors.New("User is not authorized to perform: sts:TagSession on resource"),
			wantContains: []string{"sts:TagSession", "EKS Pod Identity", "session tags"},
			wantNotContains: []string{"trust policy"},
		},
		{
			name:         "nil error returns empty string",
			err:          nil,
			wantContains: nil,
			wantNotContains: nil,
		},
		{
			name:         "generic error falls back to combined hint",
			err:          errors.New("timeout waiting for AssumeRole response"),
			wantContains: []string{"assume-role", "trust policy", "sts:AssumeRole", "sts:TagSession"},
			wantNotContains: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AssumeRoleHint(tt.err)
			if tt.err == nil {
				if got != "" {
					t.Errorf("AssumeRoleHint(nil) = %q, want empty string", got)
				}
				return
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("AssumeRoleHint() = %q, want it to contain %q", got, want)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("AssumeRoleHint() = %q, must not contain %q", got, notWant)
				}
			}
		})
	}
}
