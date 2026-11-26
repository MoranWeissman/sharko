package api

// rate_limit_sentences_test.go — pins the words a person reads when a request
// limit is hit, and the audit and status answers that go with them (walks 5,
// 6, 10: a limit used to read as "connection failing", "TLS or certificate
// problem", "Too Many Requests" and one generic audit sentence).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// githubLimit builds the error go-github returns when GitHub's hourly request
// allowance is used up (with the request attached, as the real one has).
func githubLimit() error {
	return &github.RateLimitError{
		Response: &http.Response{
			StatusCode: http.StatusForbidden,
			Request:    &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "api.github.com", Path: "/repos/o/r/contents/x"}},
		},
		Message: "API rate limit exceeded",
	}
}

func TestRateLimitSentences_ExactText(t *testing.T) {
	if SharkoRateLimitMessage != "Sharko is getting too many requests from your address right now. Try again in a moment." {
		t.Errorf("own-limit sentence changed: %q", SharkoRateLimitMessage)
	}
	if UpstreamRateLimitMessage != "GitHub, or another service Sharko uses, says Sharko has asked too often. Try again in a moment." {
		t.Errorf("upstream-limit sentence changed: %q", UpstreamRateLimitMessage)
	}
}

// Sharko's own limit says whose limit it is, carries its code, and no longer
// says the old two-word sentence.
func TestWriteRateLimiter_SaysItIsSharkosOwnLimit(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := writeRateLimiter(1, time.Minute, (*TrustedProxies)(nil).ClientIP)(inner)
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", strings.NewReader(`{}`))
		req.RemoteAddr = "10.0.0.7:1"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	post()
	w := post()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != SharkoRateLimitMessage {
		t.Errorf("error = %q, want %q", body["error"], SharkoRateLimitMessage)
	}
	if body["code"] != "sharko_rate_limit" {
		t.Errorf("code = %q, want sharko_rate_limit", body["code"])
	}
	if strings.Contains(w.Body.String(), "rate limit exceeded") {
		t.Errorf("the old sentence is back: %s", w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("a 429 should say when to come back (Retry-After)")
	}
}

// A service Sharko calls (GitHub) turned the call down: the answer says so,
// instead of the bare status text every page used to show.
func TestWriteUpstreamError_RateLimitSaysWhoAndWhatToDo(t *testing.T) {
	rec := httptest.NewRecorder()
	writeUpstreamError(rec, "test.op", fmt.Errorf("get file content: %w", githubLimit()))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != UpstreamRateLimitMessage {
		t.Errorf("error = %q, want %q", body["error"], UpstreamRateLimitMessage)
	}
	if body["code"] != "upstream_rate_limit" {
		t.Errorf("code = %q, want upstream_rate_limit", body["code"])
	}
	if body["error"] == "Too Many Requests" {
		t.Error("the bare status text is back")
	}
}

// The yellow bar and the System page used to read a GitHub limit as a broken
// connection (and the page told people to look for a TLS problem).
func TestRepoStatus_GitHubLimitIsNotAConnectionError(t *testing.T) {
	limited := fmt.Errorf("get file content: %w", githubLimit())
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{getErr: limited})
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var body RepoStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Reason != "git_rate_limited" {
		t.Errorf("reason = %q, want git_rate_limited", body.Reason)
	}

	// A real failure still says connection_error.
	srv2 := newTestServer()
	srv2.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	srv2.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{getErr: errors.New("dial tcp: i/o timeout")})
	w2 := httptest.NewRecorder()
	NewRouter(srv2, nil).ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil))
	_ = json.Unmarshal(w2.Body.Bytes(), &body)
	if body.Reason != "connection_error" {
		t.Errorf("a real failure must stay connection_error, got %q", body.Reason)
	}
}

// Audit rows for refused or limited requests carry their own reason.
func TestReasonFromStatus(t *testing.T) {
	cases := map[int]audit.Reason{
		403: audit.ReasonNotAllowed,
		429: audit.ReasonRateLimited,
		502: audit.ReasonUpstream,
		504: audit.ReasonTimeout,
		500: audit.ReasonUnspecified,
		// Several different causes behind each of these: no guess.
		400: "", 404: "", 409: "", 422: "", 503: "", 200: "", 201: "",
	}
	for code, want := range cases {
		if got := reasonFromStatus(code); got != want {
			t.Errorf("reasonFromStatus(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestAuditMiddleware_RefusedAndLimitedRowsSayWhy(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusTooManyRequests, "A request limit was reached, either Sharko's own or the one set by a service Sharko uses, such as GitHub. Nothing was changed. Try again in a moment."},
		{http.StatusForbidden, "The person or key that asked is not allowed to do this, so nothing was changed."},
	}
	for _, tc := range cases {
		srv := newTestServer()
		handler := srv.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		entries := srv.auditLog.List(10)
		if len(entries) == 0 {
			t.Fatalf("status %d: no audit row written", tc.status)
		}
		if got := entries[0].Error.String(); got != tc.want {
			t.Errorf("status %d: row says %q, want %q", tc.status, got, tc.want)
		}
		if entries[0].Error.String() == "" || strings.Contains(entries[0].Error.String(), "This operation failed") {
			t.Errorf("status %d: the row has no real reason", tc.status)
		}
	}
}
