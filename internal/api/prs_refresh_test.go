// v4-walkfix W1 item 3: handleRefreshPR used to map every PollSinglePR
// error to a flat 500 with the raw error string. The UI polls this
// endpoint every 7s after a submit, so one transient Git-host hiccup
// painted the browser console red on every poll. These tests pin the
// three-way split: "PR not tracked" → 404 (coded), a transient Git
// provider failure → 502 (coded), and a genuine internal fault (no Git
// provider configured) → 500 (sanitized, uncoded).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// fakeRefreshGitProvider is a minimal prtracker.GitProvider whose
// GetPullRequestStatus response/error is set per test.
type fakeRefreshGitProvider struct {
	status string
	err    error
}

func (f *fakeRefreshGitProvider) GetPullRequestStatus(ctx context.Context, prNumber int) (string, error) {
	return f.status, f.err
}

func (f *fakeRefreshGitProvider) DeleteBranch(ctx context.Context, branchName string) error {
	return nil
}

// newPrsRefreshTestServer mirrors newPrsFilterTestServer (prs_filter_test.go)
// but takes a caller-supplied GitProvider (nil allowed — mirrors "no Git
// provider configured") so each test controls exactly what PollSinglePR
// sees from "the Git host".
func newPrsRefreshTestServer(t *testing.T, gp prtracker.GitProvider, seed []prtracker.PRInfo) *Server {
	t.Helper()
	client := fake.NewSimpleClientset()
	store := cmstore.NewStore(client, "default", "sharko-pending-prs")
	tracker := prtracker.NewTracker(store, func() prtracker.GitProvider { return gp }, func(audit.Entry) {})

	srv := &Server{}
	srv.SetPRTracker(tracker)

	for _, pr := range seed {
		if err := tracker.TrackPR(t.Context(), pr); err != nil {
			t.Fatalf("seed TrackPR(%d): %v", pr.PRID, err)
		}
	}
	return srv
}

func doRefreshPR(t *testing.T, srv *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/prs/"+id+"/refresh", nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	srv.handleRefreshPR(rr, req)
	return rr
}

func TestHandleRefreshPR_NotTracked_Returns404(t *testing.T) {
	// A working Git provider, but PR 99 was never tracked.
	srv := newPrsRefreshTestServer(t, &fakeRefreshGitProvider{status: "open"}, nil)

	rr := doRefreshPR(t, srv, "99")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "pr_not_tracked" {
		t.Errorf("code = %q, want pr_not_tracked (body=%v)", body["code"], body)
	}
}

func TestHandleRefreshPR_ProviderUnreachable_Returns502(t *testing.T) {
	// PR IS tracked, but the Git provider call itself fails transiently —
	// the exact "one Gitea hiccup" case this fix targets.
	srv := newPrsRefreshTestServer(t,
		&fakeRefreshGitProvider{err: errors.New("dial tcp: connection refused")},
		[]prtracker.PRInfo{{PRID: 7, Operation: "register-cluster", LastStatus: "open"}},
	)

	rr := doRefreshPR(t, srv, "7")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d want 502, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "pr_provider_unreachable" {
		t.Errorf("code = %q, want pr_provider_unreachable (body=%v)", body["code"], body)
	}
}

func TestHandleRefreshPR_NoGitProviderConfigured_Returns500(t *testing.T) {
	// No Git provider configured at all — a genuine internal/config fault,
	// not a transient upstream blip, so it stays a 500.
	srv := newPrsRefreshTestServer(t, nil,
		[]prtracker.PRInfo{{PRID: 3, Operation: "register-cluster", LastStatus: "open"}},
	)

	rr := doRefreshPR(t, srv, "3")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500, body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// writeServerError intentionally omits a "code" field and never leaks
	// the raw error string in the body — only the sanitized status text.
	if body["error"] != http.StatusText(http.StatusInternalServerError) {
		t.Errorf("error = %q, want sanitized status text (body=%v)", body["error"], body)
	}
	if _, hasCode := body["code"]; hasCode {
		t.Errorf("expected no 'code' field on a genuine internal fault, body=%v", body)
	}
}
