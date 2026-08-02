package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMergeSharkoAccountCapability covers the pure decision logic behind
// ensureSharkoAccountCapability: whether argocd-cm's accounts.sharko field
// already grants apiKey capability, and what to write when it doesn't.
func TestMergeSharkoAccountCapability(t *testing.T) {
	tests := []struct {
		name            string
		current         string
		wantValue       string
		wantAlreadyDone bool
	}{
		{
			name:            "key absent (stock install) gets exactly apiKey",
			current:         "",
			wantValue:       "apiKey",
			wantAlreadyDone: false,
		},
		{
			name:            "whitespace-only value treated as absent",
			current:         "   ",
			wantValue:       "apiKey",
			wantAlreadyDone: false,
		},
		{
			name:            "apiKey already present — no change",
			current:         "apiKey",
			wantAlreadyDone: true,
		},
		{
			name:            "apiKey present among other capabilities — no change",
			current:         "login, apiKey",
			wantAlreadyDone: true,
		},
		{
			name:            "other capability present without apiKey — apiKey appended",
			current:         "login",
			wantValue:       "login, apiKey",
			wantAlreadyDone: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotAlready := mergeSharkoAccountCapability(tt.current)
			if gotAlready != tt.wantAlreadyDone {
				t.Errorf("alreadyPresent = %v, want %v", gotAlready, tt.wantAlreadyDone)
			}
			if !tt.wantAlreadyDone && gotValue != tt.wantValue {
				t.Errorf("newValue = %q, want %q", gotValue, tt.wantValue)
			}
			if tt.wantAlreadyDone && gotValue != tt.current {
				t.Errorf("newValue = %q, want unchanged %q when already present", gotValue, tt.current)
			}
		})
	}
}

// TestMergeSharkoRoleGrant covers the sharko-account specialization of the
// same merge logic mergeAdminRoleGrant already exercises for the admin
// grant — using mergeRoleGrantLine directly with sharkoRoleGrantLine so the
// two grants are proven to use the exact same, already-tested matching
// rules (no divergent off-by-one behavior between the two accounts).
func TestMergeSharkoRoleGrant(t *testing.T) {
	tests := []struct {
		name            string
		current         string
		wantPolicy      string
		wantAlreadyDone bool
	}{
		{
			name:            "empty policy.csv gets exactly the sharko grant line",
			current:         "",
			wantPolicy:      "g, sharko, role:admin",
			wantAlreadyDone: false,
		},
		{
			name:            "admin grant already present, sharko grant appended without disturbing it",
			current:         "g, admin, role:admin",
			wantPolicy:      "g, admin, role:admin\ng, sharko, role:admin",
			wantAlreadyDone: false,
		},
		{
			name:            "both grants already present — no change",
			current:         "g, admin, role:admin\ng, sharko, role:admin",
			wantAlreadyDone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPolicy, gotAlready := mergeRoleGrantLine(tt.current, sharkoRoleGrantLine)
			if gotAlready != tt.wantAlreadyDone {
				t.Errorf("alreadyGranted = %v, want %v", gotAlready, tt.wantAlreadyDone)
			}
			if !tt.wantAlreadyDone && gotPolicy != tt.wantPolicy {
				t.Errorf("newPolicy = %q, want %q", gotPolicy, tt.wantPolicy)
			}
			if tt.wantAlreadyDone && gotPolicy != tt.current {
				t.Errorf("newPolicy = %q, want unchanged %q when already granted", gotPolicy, tt.current)
			}
		})
	}
}

// TestCreateArgoCDAccountToken_Success covers the happy path against a fake
// ArgoCD account-token API: the helper returns the token from the response
// body, and the request it sent asks for no expiry (an empty JSON object —
// no "expiresIn" key at all).
func TestCreateArgoCDAccountToken_Success(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"token":"sharko-nonexpiring-token-value"}`))
	}))
	defer srv.Close()

	token, err := createArgoCDAccountToken(srv.Client(), srv.URL, "admin-session-token", sharkoAccountName)
	if err != nil {
		t.Fatalf("createArgoCDAccountToken: %v", err)
	}
	if token != "sharko-nonexpiring-token-value" {
		t.Errorf("token = %q, want the minted token", token)
	}
	if gotPath != "/api/v1/account/sharko/token" {
		t.Errorf("path = %q, want /api/v1/account/sharko/token", gotPath)
	}
	if gotAuth != "Bearer admin-session-token" {
		t.Errorf("Authorization header = %q, want Bearer admin-session-token", gotAuth)
	}
	if gotBody != "{}" {
		t.Errorf("request body = %q, want an empty JSON object (no expiresIn key — that is what asks ArgoCD for a token that never expires)", gotBody)
	}
}

// TestCreateArgoCDAccountToken_APIError covers ArgoCD rejecting the mint
// request (e.g. the sharko account doesn't have apiKey capability yet, or
// the admin session is invalid): the helper must surface a plain error, not
// panic or return a zero-value token silently.
func TestCreateArgoCDAccountToken_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"account 'sharko' does not have apiKey capability"}`))
	}))
	defer srv.Close()

	token, err := createArgoCDAccountToken(srv.Client(), srv.URL, "admin-session-token", sharkoAccountName)
	if err == nil {
		t.Fatalf("createArgoCDAccountToken: expected error, got token %q", token)
	}
	if token != "" {
		t.Errorf("token = %q, want empty on error", token)
	}
}

// TestCreateArgoCDAccountToken_EmptyTokenInResponse covers a 200 response
// that is missing the token field — should not be treated as success.
func TestCreateArgoCDAccountToken_EmptyTokenInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	token, err := createArgoCDAccountToken(srv.Client(), srv.URL, "admin-session-token", sharkoAccountName)
	if err == nil {
		t.Fatalf("createArgoCDAccountToken: expected error for missing token field, got token %q", token)
	}
	if token != "" {
		t.Errorf("token = %q, want empty on error", token)
	}
}
