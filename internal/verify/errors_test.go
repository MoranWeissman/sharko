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

// These two tests used to assert the OPPOSITE — that FriendlyMessage
// "preserves the raw cause for diagnosis". That was the leak, pinned by a test
// and written down as the design in the doc comment above the function. The
// product owner's ruling retired it: no raw provider, credential-store, Git,
// Kubernetes or internal error text may be part of a user-facing message.
//
// They are kept rather than deleted, inverted, so the old behaviour cannot
// come back quietly.

func TestFriendlyMessage_IsActionableAndCarriesNoRawCause(t *testing.T) {
	msg := FriendlyMessage(ERR_AUTH).String()

	// Still actionable — this is the counterweight. A safe message that
	// said nothing useful would trade one defect for another.
	if !strings.Contains(msg, "HTTP 401") || !strings.Contains(msg, "regenerate") {
		t.Errorf("the ERR_AUTH sentence stopped being actionable: %q", msg)
	}

	// FriendlyMessage now takes only an ErrorCode, so there is no parameter
	// for raw text to arrive through — this asserts the RESULT of that.
	for _, raw := range []string{
		"the server has asked for the client to provide credentials",
		"dial tcp 10.0.0.1:6443",
		"x509: certificate signed by unknown authority",
	} {
		if strings.Contains(msg, raw) {
			t.Errorf("the ERR_AUTH sentence carries raw error text %q: %q", raw, msg)
		}
	}

	// error review package 2: no bracketed machine token leading a sentence
	// meant for a person. Callers that need the code read Result.ErrorCode.
	if strings.HasPrefix(msg, "[") {
		t.Errorf("friendly message must not carry a bracketed code prefix: %q", msg)
	}
}

func TestFriendlyMessage_EveryCodeIsAWholeActionableSentence(t *testing.T) {
	// Every code, including the ones with no Hint() entry, must still get a
	// complete sentence. ERR_NETWORK is the one that used to return the bare
	// raw cause with no hint at all.
	for _, code := range allDeclaredErrorCodes(t) {
		msg := FriendlyMessage(code).String()
		if msg == "" {
			t.Errorf("%s renders an empty message", code)
			continue
		}
		if !strings.HasSuffix(msg, ".") {
			t.Errorf("%s does not render a whole sentence (no full stop): %q", code, msg)
		}
		// "which category of thing to go and look at" — every sentence has
		// to tell the operator to do or check something.
		if !strings.Contains(msg, "Check") && !strings.Contains(msg, "Grant") &&
			!strings.Contains(msg, "Wait") && !strings.Contains(msg, "server log") &&
			!strings.Contains(msg, "regenerate") {
			t.Errorf("%s says what broke but not what to look at: %q", code, msg)
		}
	}

	// An unrecognised code must not render blank — stage 2 emits
	// ERR_NOT_IMPLEMENTED, which is not in the const block.
	if got := FriendlyMessage(ErrorCode("ERR_NOT_IMPLEMENTED")).String(); got != safeSentences[ERR_UNKNOWN] {
		t.Errorf("an unknown code must fall back to the ERR_UNKNOWN sentence, got %q", got)
	}
}

// TestClassifyError_NamespaceTightened — error review package 2, step 0.
// A bare "namespace" substring used to be enough to classify ERR_NAMESPACE,
// which misfired on almost any Kubernetes error that happens to mention a
// namespace in passing. This locks the tightened contract: the phrase alone
// isn't enough — it needs a genuine not-found/already-exists/terminating
// signal (or the admission-webhook phrase) alongside it.
func TestClassifyError_NamespaceTightened(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorCode
	}{
		{
			name: "namespace + not found -> ERR_NAMESPACE",
			err:  errors.New(`namespaces "sharko-test" not found`),
			want: ERR_NAMESPACE,
		},
		{
			name: "namespace + already exists -> ERR_NAMESPACE",
			err:  errors.New(`namespaces "sharko-test" already exists`),
			want: ERR_NAMESPACE,
		},
		{
			name: "admission webhook alone -> ERR_NAMESPACE",
			err:  errors.New("admission webhook denied the request"),
			want: ERR_NAMESPACE,
		},
		{
			name: "unrelated error mentioning namespace does NOT misfire",
			err:  errors.New("listing pods in namespace sharko-system: unexpected server response"),
			want: ERR_UNKNOWN,
		},
		{
			name: "namespace mention with an unrelated failure reason does NOT misfire",
			err:  errors.New("watching namespace sharko-system: connection reset by peer"),
			want: ERR_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyError(tt.err); got != tt.want {
				t.Errorf("ClassifyError(%q) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

func TestAssumeRoleHint(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "trust policy rejection - not authorized to assume",
			err:             errors.New("User: arn:aws:sts::123456789012:assumed-role/sharko-role/session is not authorized to assume role arn:aws:iam::123456789012:role/target-role"),
			wantContains:    []string{"trust policy", "Sharko's identity"},
			wantNotContains: []string{"sts:AssumeRole permission", "sts:TagSession"},
		},
		{
			name:            "trust policy rejection - AccessDenied on AssumeRole",
			err:             errors.New("operation error STS: AssumeRole, https response error StatusCode: 403, api error AccessDenied: User is not authorized to perform: sts:AssumeRole on resource"),
			wantContains:    []string{"trust policy", "IAM principal"},
			wantNotContains: []string{"sts:TagSession"},
		},
		{
			name:            "missing sts:TagSession permission",
			err:             errors.New("User is not authorized to perform: sts:TagSession on resource"),
			wantContains:    []string{"sts:TagSession", "EKS Pod Identity", "session tags"},
			wantNotContains: []string{"trust policy"},
		},
		{
			name:            "nil error returns empty string",
			err:             nil,
			wantContains:    nil,
			wantNotContains: nil,
		},
		{
			name:            "generic error falls back to combined hint",
			err:             errors.New("timeout waiting for AssumeRole response"),
			wantContains:    []string{"assume-role", "trust policy", "sts:AssumeRole", "sts:TagSession"},
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
