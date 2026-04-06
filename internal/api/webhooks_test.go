package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// makeWebhookSig generates a GitHub-style HMAC-SHA256 signature header value.
func makeWebhookSig(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHandleGitWebhook_Ping(t *testing.T) {
	srv := &Server{}

	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp["status"] != "pong" {
		t.Errorf("expected status=pong, got %q", resp["status"])
	}
}

func TestHandleGitWebhook_PushToBaseBranch(t *testing.T) {
	srv := &Server{
		gitopsCfg: orchestrator.GitOpsConfig{BaseBranch: "main"},
	}

	payload := map[string]interface{}{
		"ref": "refs/heads/main",
		"pusher": map[string]string{
			"name": "bot",
		},
		"commits": []map[string]string{
			{"id": "abc123", "message": "chore: bump"},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleGitWebhook_ValidSignature(t *testing.T) {
	secret := "mysecret"
	t.Setenv("SHARKO_WEBHOOK_SECRET", secret)

	srv := &Server{
		gitopsCfg: orchestrator.GitOpsConfig{BaseBranch: "main"},
	}

	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)
	sig := makeWebhookSig(payload, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitWebhook_InvalidSignature(t *testing.T) {
	os.Setenv("SHARKO_WEBHOOK_SECRET", "mysecret")
	defer os.Unsetenv("SHARKO_WEBHOOK_SECRET")

	srv := &Server{}

	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=badhash")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandleGitWebhook_MissingSignatureWhenSecretSet(t *testing.T) {
	os.Setenv("SHARKO_WEBHOOK_SECRET", "mysecret")
	defer os.Unsetenv("SHARKO_WEBHOOK_SECRET")

	srv := &Server{}

	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestVerifyGitHubSignature(t *testing.T) {
	cases := []struct {
		name      string
		payload   []byte
		secret    string
		sig       string
		wantValid bool
	}{
		{
			name:      "valid signature",
			payload:   []byte("hello"),
			secret:    "secret",
			sig:       makeWebhookSig([]byte("hello"), "secret"),
			wantValid: true,
		},
		{
			name:      "wrong secret",
			payload:   []byte("hello"),
			secret:    "other",
			sig:       makeWebhookSig([]byte("hello"), "secret"),
			wantValid: false,
		},
		{
			name:      "tampered payload",
			payload:   []byte("world"),
			secret:    "secret",
			sig:       makeWebhookSig([]byte("hello"), "secret"),
			wantValid: false,
		},
		{
			name:      "missing sha256 prefix",
			payload:   []byte("hello"),
			secret:    "secret",
			sig:       hex.EncodeToString([]byte("anyhex")),
			wantValid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verifyGitHubSignature(tc.payload, tc.sig, tc.secret)
			if got != tc.wantValid {
				t.Errorf("verifyGitHubSignature() = %v, want %v", got, tc.wantValid)
			}
		})
	}
}
