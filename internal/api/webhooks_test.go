package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// makeWebhookSig generates a GitHub-style HMAC-SHA256 signature header value.
func makeWebhookSig(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postWebhook drives the handler once and hands back the recorder.
func postWebhook(srv *Server, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	srv.handleGitWebhook(rr, req)
	return rr
}

func webhookErrorBody(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response was not JSON: %v (body %q)", err, rr.Body.String())
	}
	return resp["error"]
}

func TestHandleGitWebhook_Ping(t *testing.T) {
	const secret = "mysecret"
	t.Setenv("SHARKO_WEBHOOK_SECRET", secret)

	srv := &Server{}
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1}`)

	rr := postWebhook(srv, body, map[string]string{
		"X-GitHub-Event":      "ping",
		"X-Hub-Signature-256": makeWebhookSig(body, secret),
	})

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

// TestHandleGitWebhook_PingIsBehindTheSignature pins that the ping carve-out
// did not become a hole. GitHub signs its ping like anything else, so an
// unsigned ping is just an unsigned request.
func TestHandleGitWebhook_PingIsBehindTheSignature(t *testing.T) {
	t.Setenv("SHARKO_WEBHOOK_SECRET", "mysecret")

	srv := &Server{}
	rr := postWebhook(srv, []byte(`{"zen":"x"}`), map[string]string{"X-GitHub-Event": "ping"})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("an unsigned ping must be refused; got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitWebhook_PushToBaseBranch(t *testing.T) {
	const secret = "mysecret"
	t.Setenv("SHARKO_WEBHOOK_SECRET", secret)

	srv := &Server{auditLog: audit.NewLog(10)}
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})

	body := []byte(`{"ref":"refs/heads/main","pusher":{"name":"bot"},"commits":[{"id":"abc123","message":"chore: bump"}]}`)

	rr := postWebhook(srv, body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": makeWebhookSig(body, secret),
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleGitWebhook_ValidSignature(t *testing.T) {
	const secret = "mysecret"
	t.Setenv("SHARKO_WEBHOOK_SECRET", secret)

	srv := &Server{auditLog: audit.NewLog(10)}
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})

	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)

	rr := postWebhook(srv, payload, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": makeWebhookSig(payload, secret),
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitWebhook_InvalidSignature(t *testing.T) {
	t.Setenv("SHARKO_WEBHOOK_SECRET", "mysecret")

	srv := &Server{}
	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)

	rr := postWebhook(srv, payload, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": "sha256=badhash",
	})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandleGitWebhook_MissingSignatureWhenSecretSet(t *testing.T) {
	t.Setenv("SHARKO_WEBHOOK_SECRET", "mysecret")

	srv := &Server{}
	payload := []byte(`{"ref":"refs/heads/main","pusher":{"name":"ci"},"commits":[]}`)

	rr := postWebhook(srv, payload, map[string]string{"X-GitHub-Event": "push"})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestHandleGitWebhook_ClosedWithNoSecret is the defect this story exists for.
//
// With no shared secret set, the handler used to skip the signature check
// entirely and run the push path — on a default chart install, which ships the
// value empty. Now it does nothing and refuses.
func TestHandleGitWebhook_ClosedWithNoSecret(t *testing.T) {
	t.Setenv("SHARKO_WEBHOOK_SECRET", "")

	payload := []byte(`{"ref":"refs/heads/main","commits":[{"id":"a"}]}`)

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"no signature at all", map[string]string{"X-GitHub-Event": "push"}},
		{
			// Exactly what someone would try if "no secret" meant "the
			// empty secret": a signature that is correct for "".
			name: "a signature computed with the empty secret",
			headers: map[string]string{
				"X-GitHub-Event":      "push",
				"X-Hub-Signature-256": makeWebhookSig(payload, ""),
			},
		},
		{"a ping", map[string]string{"X-GitHub-Event": "ping"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := audit.NewLog(10)
			srv := &Server{auditLog: log}
			srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})

			rr := postWebhook(srv, payload, tc.headers)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("with no secret configured the webhook must refuse; got %d body %s",
					rr.Code, rr.Body.String())
			}
			if got := len(log.List(0)); got != 0 {
				t.Fatalf("a refused call must write no audit entry; got %d", got)
			}
		})
	}
}

// TestHandleGitWebhook_RefusalRevealsNothing is the free-oracle check.
//
// Every refusal must read exactly the same, whatever the server's own
// configuration is — otherwise an outsider can ask the endpoint whether it is
// protected, which is the one answer that must never be obtainable.
func TestHandleGitWebhook_RefusalRevealsNothing(t *testing.T) {
	bodies := map[string]string{}

	run := func(t *testing.T, label, secret string, headers map[string]string) {
		t.Helper()
		t.Setenv("SHARKO_WEBHOOK_SECRET", secret)
		srv := &Server{auditLog: audit.NewLog(10)}
		srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
		rr := postWebhook(srv, []byte(`{"ref":"refs/heads/main","commits":[]}`), headers)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", label, rr.Code)
		}
		bodies[label] = webhookErrorBody(t, rr)
	}

	run(t, "no secret, no signature", "", map[string]string{"X-GitHub-Event": "push"})
	run(t, "no secret, some signature", "", map[string]string{
		"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=deadbeef"})
	run(t, "secret set, no signature", "a-very-long-configured-secret", map[string]string{
		"X-GitHub-Event": "push"})
	run(t, "secret set, wrong signature", "a-very-long-configured-secret", map[string]string{
		"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=deadbeef"})
	run(t, "short secret set, wrong signature", "x", map[string]string{
		"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=deadbeef"})

	if len(bodies) != 5 {
		t.Fatalf("expected 5 refusals to compare, collected %d — the sweep is broken, not the code", len(bodies))
	}

	var first, firstLabel string
	for label, body := range bodies {
		if body == "" {
			t.Fatalf("%s: refusal carried no message at all", label)
		}
		if first == "" {
			first, firstLabel = body, label
			continue
		}
		if body != first {
			t.Errorf("refusals differ, which tells a caller how the server is configured:\n  %s: %q\n  %s: %q",
				firstLabel, first, label, body)
		}
	}

	if first != webhookRefusal {
		t.Errorf("refusal is not the fixed sentence: got %q want %q", first, webhookRefusal)
	}
	for _, banned := range []string{"SHARKO_WEBHOOK_SECRET", "not configured", "unset", "empty", "missing X-Hub"} {
		if strings.Contains(first, banned) {
			t.Errorf("refusal contains %q, which says something about how the server is set up: %q", banned, first)
		}
	}
}

// TestHandleGitWebhook_AuditCarriesNoSenderText proves the audit record is
// built from constants, a count, and Sharko's own configuration — never from
// words in the request body.
func TestHandleGitWebhook_AuditCarriesNoSenderText(t *testing.T) {
	const secret = "mysecret"
	t.Setenv("SHARKO_WEBHOOK_SECRET", secret)

	log := audit.NewLog(10)
	srv := &Server{auditLog: log}
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})

	// Every string below is chosen by the sender.
	const (
		fakeActor   = "admin-impersonated-by-sender"
		fakeStory   = "deleted every cluster on purpose"
		fakeCommit  = "0000000000000000000000000000000000000000"
		fakeAccount = "someone-elses-account"
	)
	payload := []byte(`{"ref":"refs/heads/main",` +
		`"pusher":{"name":"` + fakeActor + `","email":"` + fakeAccount + `"},` +
		`"sender":{"login":"` + fakeAccount + `"},` +
		`"commits":[{"id":"` + fakeCommit + `","message":"` + fakeStory + `"},{"id":"b","message":"second"}]}`)

	rr := postWebhook(srv, payload, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": makeWebhookSig(payload, secret),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected the signed push to be accepted, got %d body %s", rr.Code, rr.Body.String())
	}

	entries := log.List(0)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d", len(entries))
	}
	e := entries[0]

	if e.User != webhookAuditUser {
		t.Errorf("User must be the fixed %q, got %q", webhookAuditUser, e.User)
	}
	if e.Resource != "branch:main" {
		t.Errorf("Resource must name the configured branch, got %q", e.Resource)
	}
	if e.Source != audit.SourceWebhook {
		t.Errorf("Source must be %q so a reader can tell this apart from a person's action, got %q",
			audit.SourceWebhook, e.Source)
	}
	if e.Detail != "2 commit(s)" {
		t.Errorf("Detail must be the count Sharko computed, got %q", e.Detail)
	}

	// The whole entry, marshalled, must not contain anything the sender wrote.
	blob, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("could not marshal the entry: %v", err)
	}
	for _, senderText := range []string{fakeActor, fakeStory, fakeCommit, fakeAccount} {
		if bytes.Contains(blob, []byte(senderText)) {
			t.Errorf("the audit entry carries text the sender chose (%q):\n%s", senderText, blob)
		}
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

// TestGitWebhook_ThroughTheRealRouterWithNoCredentials is the end-to-end
// version of the whole story: what an outsider with nothing — no session, no
// token, no signature — actually gets back off the wire.
//
// The handler tests above call handleGitWebhook directly, so on their own they
// would still pass if the route were wired somewhere that never reached it.
// This one drives the real router.
func TestGitWebhook_ThroughTheRealRouterWithNoCredentials(t *testing.T) {
	// No shared secret: the state a default chart install is in.
	t.Setenv("SHARKO_WEBHOOK_SECRET", "")

	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git",
		bytes.NewReader([]byte(`{"ref":"refs/heads/main","commits":[{"id":"a"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("an outsider must be refused; got %d body %s", rr.Code, rr.Body.String())
	}
	if got := webhookErrorBody(t, rr); got != webhookRefusal {
		t.Fatalf("the wire body is not the fixed sentence:\n  got  %q\n  want %q", got, webhookRefusal)
	}
}
