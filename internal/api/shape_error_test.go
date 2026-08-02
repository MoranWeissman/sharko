package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// TestShapeError_ArgoCDTokenInvalid — error review package 2. A wrapped
// ArgoCD token error must produce an honest headline (passed through
// unchanged), a cause naming the actual token problem, a hint pointing at
// the in-app destination that fixes it (Settings → Connections), and the
// auth machine code.
func TestShapeError_ArgoCDTokenInvalid(t *testing.T) {
	err := fmt.Errorf("listing argocd applications: %w", argocd.ErrTokenInvalid)
	headline, cause, hint, code := shapeError(err, "Sharko could not list applications")

	if headline != "Sharko could not list applications" {
		t.Errorf("headline = %q, want the caller's own sentence unchanged", headline)
	}
	if cause != argocd.ErrTokenInvalid.Error() {
		t.Errorf("cause = %q, want the token sentence %q", cause, argocd.ErrTokenInvalid.Error())
	}
	if !strings.Contains(hint, "Settings") || !strings.Contains(hint, "Connections") {
		t.Errorf("hint = %q, want it to name the Settings → Connections destination", hint)
	}
	if code != string(verify.ERR_AUTH) {
		t.Errorf("code = %q, want %q (the auth code)", code, verify.ERR_AUTH)
	}
}

// TestShapeError_ArgoCDPermissionDenied — the RBAC sibling of the above.
func TestShapeError_ArgoCDPermissionDenied(t *testing.T) {
	err := fmt.Errorf("syncing application: %w", argocd.ErrPermissionDenied)
	_, cause, hint, code := shapeError(err, "Sharko could not sync the application")

	if cause != argocd.ErrPermissionDenied.Error() {
		t.Errorf("cause = %q, want the permission sentence %q", cause, argocd.ErrPermissionDenied.Error())
	}
	if !strings.Contains(hint, "Settings") || !strings.Contains(hint, "Connections") {
		t.Errorf("hint = %q, want it to name the Settings → Connections destination", hint)
	}
	if code != string(verify.ERR_RBAC) {
		t.Errorf("code = %q, want %q", code, verify.ERR_RBAC)
	}
}

// TestShapeError_GitProviderFamilies locks the two gitprovider sentinels'
// bespoke cause text.
func TestShapeError_GitProviderFamilies(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCause string
	}{
		{
			name:      "file not found",
			err:       fmt.Errorf("reading values file: %w", gitprovider.ErrFileNotFound),
			wantCause: "the file was not found in the Git repository",
		},
		{
			name:      "pull request not found",
			err:       fmt.Errorf("polling PR: %w", gitprovider.ErrPullRequestNotFound),
			wantCause: "the pull request was not found in the Git repository",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cause, _, _ := shapeError(tt.err, "headline")
			if cause != tt.wantCause {
				t.Errorf("cause = %q, want %q", cause, tt.wantCause)
			}
		})
	}
}

// TestShapeError_ConfigValidation — a Sharko input-validation error (see
// internal/service/connection.go's validationError, error review package 2
// step 1b) is already a clean, actionable sentence, so it's used as-is with
// no code/hint (validation isn't a connectivity classification).
func TestShapeError_ConfigValidation(t *testing.T) {
	// Drive the real production path (service.ConnectionService.Create)
	// rather than hand-rolling a stand-in error — the wrapper type behind
	// service.ErrValidation is unexported by design; errors.Is still works
	// across the package boundary, which is exactly the contract shapeError
	// depends on.
	store := config.NewFileStore(t.TempDir() + "/shape-error-test-config.yaml")
	svc := service.NewConnectionService(store)
	verr := svc.Create(models.CreateConnectionRequest{
		Git: models.GitRepoConfig{Provider: "gitlab"},
	})
	if verr == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !errors.Is(verr, service.ErrValidation) {
		t.Fatalf("test setup: expected errors.Is(verr, service.ErrValidation), got: %v", verr)
	}

	headline, cause, hint, code := shapeError(verr, "Sharko could not save the connection")

	if headline != "Sharko could not save the connection" {
		t.Errorf("headline = %q, want the caller's sentence unchanged", headline)
	}
	if cause != verr.Error() {
		t.Errorf("cause = %q, want the clean validation message %q as-is", cause, verr.Error())
	}
	if strings.Contains(cause, "validation failed") {
		t.Errorf("cause must not carry the meaningless 'validation failed' segment, got: %q", cause)
	}
	if hint != "" || code != "" {
		t.Errorf("hint/code should stay empty for a validation error, got hint=%q code=%q", hint, code)
	}
}

// TestShapeError_PlainUnclassifiableError — the JSON-hygiene contract: a
// plain error that matches none of the four bespoke families and doesn't
// classify against verify.ClassifyError still leaves `error` populated, and
// every field with nothing to say is OMITTED from the JSON entirely (never
// sent as an empty string).
func TestShapeError_PlainUnclassifiableError(t *testing.T) {
	err := errors.New("something completely unexpected happened")
	headline, cause, hint, code := shapeError(err, "Sharko could not complete the request")

	if headline == "" {
		t.Fatal("headline (error field) must stay populated")
	}
	if cause != "" {
		t.Errorf("cause = %q, want empty for a plain unclassifiable error", cause)
	}
	if hint != "" {
		t.Errorf("hint = %q, want empty", hint)
	}
	if code != "" {
		t.Errorf("code = %q, want empty", code)
	}

	// Marshal exactly like writeErrorWithCause does and check no field
	// renders as an empty string — omitempty must actually drop them.
	body, err2 := json.Marshal(errorResponse{Error: headline, Cause: cause, Hint: hint, Code: code})
	if err2 != nil {
		t.Fatalf("marshal: %v", err2)
	}
	for _, key := range []string{`"cause":""`, `"hint":""`, `"code":""`} {
		if strings.Contains(string(body), key) {
			t.Errorf("response body must never carry an empty-string field, got %s in %s", key, body)
		}
	}
}

// TestShapeError_NilError — defensive: nil must not panic, and produces an
// entirely empty additive-field set.
func TestShapeError_NilError(t *testing.T) {
	headline, cause, hint, code := shapeError(nil, "headline")
	if headline != "headline" || cause != "" || hint != "" || code != "" {
		t.Errorf("shapeError(nil, ...) = (%q, %q, %q, %q), want (headline, \"\", \"\", \"\")", headline, cause, hint, code)
	}
}

// TestShapeError_GenericUpstreamError_NoLeakedCause pins the safety
// decision behind the design (see shapeError's doc comment): a generic
// upstream failure that ISN'T one of the four bespoke families gets code/
// hint from the fixed verify table, but no cause — because there's no safe,
// general way to know whether an arbitrary error's text is fit to expose
// (the yaml.TypeError counter-example that motivated this is exercised at
// the writeServerError level in upstream_error_test.go /
// values_preview_merge_test.go).
func TestShapeError_GenericUpstreamError_NoLeakedCause(t *testing.T) {
	err := errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	_, cause, hint, code := shapeError(err, "Sharko could not reach the cluster")

	if cause != "" {
		t.Errorf("cause = %q, want empty (no generic cause extraction — see doc comment)", cause)
	}
	if code != string(verify.ERR_NETWORK) {
		t.Errorf("code = %q, want %q", code, verify.ERR_NETWORK)
	}
	_ = hint // ERR_NETWORK's hint is empty today; not asserted to avoid over-pinning verify's table.
}

// TestShapeError_StatusError_NotFound_MessagePassesThrough — review findings
// r1, M3. NotFound is on the bounded-reason allowlist, and client-go's
// NotFound message is a controlled, structured surface (`namespaces "x" not
// found") — safe to echo verbatim.
func TestShapeError_StatusError_NotFound_MessagePassesThrough(t *testing.T) {
	err := apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "sharko-test")
	_, cause, _, _ := shapeError(err, "headline")

	if cause != err.Error() {
		t.Errorf("cause = %q, want the NotFound message %q to pass through", cause, err.Error())
	}
}

// TestShapeError_StatusError_Invalid_NeverLeaksFieldValue — review findings
// r1, M3. A NewInvalid error's field.ErrorList can embed arbitrary
// caller-supplied values verbatim into Status().Message. Reason=Invalid is
// NOT on the bounded allowlist, so cause must be a safe summary naming only
// the Reason — the offending value must never reach the response.
func TestShapeError_StatusError_Invalid_NeverLeaksFieldValue(t *testing.T) {
	secretLookingValue := "sk-should-never-leak-abc123"
	fieldErrs := field.ErrorList{
		field.Invalid(field.NewPath("spec", "template", "spec", "containers").Index(0).Child("image"), secretLookingValue, "invalid image reference"),
	}
	err := apierrors.NewInvalid(schema.GroupKind{Group: "apps", Kind: "Deployment"}, "my-deploy", fieldErrs)

	// Sanity: prove the raw error DOES embed the value (otherwise this test
	// proves nothing about the leak this fix closes).
	if !strings.Contains(err.Error(), secretLookingValue) {
		t.Fatalf("test setup: expected the raw NewInvalid error to embed %q, got: %v", secretLookingValue, err)
	}

	_, cause, _, _ := shapeError(err, "headline")

	if strings.Contains(cause, secretLookingValue) {
		t.Errorf("cause leaked the field value: %q", cause)
	}
	if cause == "" {
		t.Error("cause should still be populated with a safe summary, not empty")
	}
	if !strings.Contains(cause, string(metav1.StatusReasonInvalid)) {
		t.Errorf("cause = %q, want it to name the Reason (%q)", cause, metav1.StatusReasonInvalid)
	}
}

// TestShapeError_StatusError_AdmissionWebhookDenial_NeverLeaksMessage —
// review findings r1, M3. An admission webhook can return an arbitrary
// denial message that client-go surfaces as a StatusError. Even when the
// Reason happens to land on the bounded allowlist (Forbidden), a message
// mentioning "admission webhook" must still get the safe summary — webhook
// authors control that text, not Sharko.
func TestShapeError_StatusError_AdmissionWebhookDenial_NeverLeaksMessage(t *testing.T) {
	secretLookingValue := "internal-policy-detail-should-not-leak"
	err := &apierrors.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Message: fmt.Sprintf("admission webhook \"my-policy.example.com\" denied the request: %s", secretLookingValue),
			Reason:  metav1.StatusReasonForbidden, // on the allowlist — the webhook mention must still win
			Code:    403,
		},
	}

	_, cause, _, _ := shapeError(err, "headline")

	if strings.Contains(cause, secretLookingValue) {
		t.Errorf("cause leaked the webhook denial message: %q", cause)
	}
	if strings.Contains(cause, "admission webhook") {
		t.Errorf("cause should be a safe summary, not the raw webhook message: %q", cause)
	}
}

// TestShapeError_StatusError_Forbidden_MessagePassesThrough — the bounded
// sibling: a genuine Forbidden (RBAC) StatusError with a plain, controlled
// message (no webhook mention) still passes its message through as cause.
func TestShapeError_StatusError_Forbidden_MessagePassesThrough(t *testing.T) {
	err := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "my-secret", errors.New("user cannot get resource"))
	_, cause, _, _ := shapeError(err, "headline")

	if cause != err.Error() {
		t.Errorf("cause = %q, want the Forbidden message %q to pass through", cause, err.Error())
	}
}

// TestWriteErrorWithCause_EmitsShapedEnvelope is the HTTP-level integration
// check: writeErrorWithCause must produce the errorResponse{} shape.
func TestWriteErrorWithCause_EmitsShapedEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	err := fmt.Errorf("checking connection: %w", argocd.ErrTokenInvalid)
	writeErrorWithCause(w, 502, "Sharko could not verify the ArgoCD connection", err)

	var got errorResponse
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &got); decodeErr != nil {
		t.Fatalf("response is not valid JSON: %v", decodeErr)
	}
	if got.Error != "Sharko could not verify the ArgoCD connection" {
		t.Errorf("error = %q, want the headline unchanged", got.Error)
	}
	if got.Cause == "" || got.Hint == "" || got.Code == "" {
		t.Errorf("expected cause/hint/code all populated for a token-invalid error, got %+v", got)
	}
}
