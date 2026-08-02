// client_test.go — error review package 1: pins the doGet status-code
// classification, in particular the ErrTokenInvalid sentinel on a 401. An
// expired/invalid ArgoCD token used to fall through to a plain (unwrapped)
// error, which made every caller that classified errors via errors.Is treat
// it the same as any other failure — this is the root cause of the wizard
// hijack bug this review package fixes.

package argocd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListApplications_401_WrapsErrTokenInvalid(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"token expired"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "expired-token", false)
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expected errors.Is(err, ErrTokenInvalid) to be true, got err=%v", err)
	}
	// The user-facing message must stay stable — callers (e.g. the
	// bootstrap-app probe) surface it verbatim via errors.Unwrap /
	// ErrTokenInvalid.Error(), even though ListApplications wraps it with
	// its own "listing applications: " prefix.
	if !strings.Contains(err.Error(), "invalid ArgoCD token — check that the token is correct and not expired") {
		t.Errorf("unexpected error message: %q", err.Error())
	}
	if ErrTokenInvalid.Error() != "invalid ArgoCD token — check that the token is correct and not expired" {
		t.Errorf("unexpected sentinel message: %q", ErrTokenInvalid.Error())
	}
}

func TestListApplications_403_WrapsErrPermissionDenied(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"permission denied"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "scoped-token", false)
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected errors.Is(err, ErrPermissionDenied) to be true, got err=%v", err)
	}
	// A 401 and a 403 must classify as DIFFERENT sentinels — a bad
	// credential and a valid-but-underpermissioned one are different
	// problems with different fixes.
	if errors.Is(err, ErrTokenInvalid) {
		t.Error("a 403 must NOT also satisfy errors.Is(err, ErrTokenInvalid)")
	}
}

func TestListApplications_GenericFailure_NoSentinel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"internal error"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "any-token", false)
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if errors.Is(err, ErrTokenInvalid) {
		t.Error("a generic 500 must NOT satisfy errors.Is(err, ErrTokenInvalid)")
	}
	if errors.Is(err, ErrPermissionDenied) {
		t.Error("a generic 500 must NOT satisfy errors.Is(err, ErrPermissionDenied)")
	}
}
