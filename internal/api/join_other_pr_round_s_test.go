package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// EP3 dry run round S, S5: adopt and take over never finish as each other.
// An open pull request of the other kind is a 409 other_pr_open, with the
// plain sentence, the pull request number (never 0) and its address.

func assertOtherPROpen(t *testing.T, w *httptest.ResponseRecorder, wantErr string, wantPR int, wantURL string) {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	body := decodeMap(t, w)
	if body["code"] != "other_pr_open" {
		t.Fatalf("code = %v (body=%s)", body["code"], w.Body.String())
	}
	if body["error"] != wantErr {
		t.Fatalf("error = %q, want %q", body["error"], wantErr)
	}
	n, has := body["pr_number"]
	if wantPR == 0 {
		if has {
			t.Fatalf("pr_number must be left out when it is not known: %s", w.Body.String())
		}
	} else if n != float64(wantPR) {
		t.Fatalf("pr_number = %v, want %d", n, wantPR)
	}
	if body["pr_url"] != wantURL {
		t.Fatalf("pr_url = %v, want %q", body["pr_url"], wantURL)
	}
	if strings.Contains(w.Body.String(), "#0") {
		t.Fatalf("the answer says #0: %s", w.Body.String())
	}
}

func TestTakeover_OpenAdoptPR_Is409OtherPROpen_RoundS(t *testing.T) {
	gp := newTakeoverFakeGP()
	gp.open = append(gp.open, gitprovider.PullRequest{ID: 12, Title: "sharko: adopt cluster prod-eu", URL: "https://example.invalid/pr/12"})
	_, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{takeover.FindingSecretOwner}}))
	assertOtherPROpen(t, w, "A pull request to adopt prod-eu is already open. Merge or close it first.", 12, "https://example.invalid/pr/12")
	if gp.prs != 0 {
		t.Fatalf("a pull request was opened: %d", gp.prs)
	}
	if live := liveSecret(t, k8s, "prod-eu"); len(live.Annotations) != 0 {
		t.Fatalf("the connection changed: %v", live.Annotations)
	}
}

func TestOtherPROpen_NoNumberIsLeftOut_RoundS(t *testing.T) {
	w := httptest.NewRecorder()
	writeOtherPROpen(w, &orchestrator.OtherJoinPRError{Cluster: "prod-eu", Kind: "take over"})
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d", w.Code)
	}
	body := decodeMap(t, w)
	if _, has := body["pr_number"]; has {
		t.Fatalf("pr_number must be left out: %s", w.Body.String())
	}
	if _, has := body["pr_url"]; has {
		t.Fatalf("pr_url must be left out: %s", w.Body.String())
	}
	if body["error"] != "A pull request to take over prod-eu is already open. Merge or close it first." {
		t.Fatalf("error = %v", body["error"])
	}
}

func TestAdopt_OpenTakeoverPR_Is409OtherPROpen_RoundS(t *testing.T) {
	router, gp := adoptAckTestRouter(t)
	gp.open = append(gp.open, gitprovider.PullRequest{ID: 9, Title: "sharko: take over cluster prod-eu", URL: "https://example.invalid/pr/9"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/adopt", "admin",
		orchestrator.AdoptClustersRequest{Clusters: []string{"prod-eu"}, AcknowledgedFindings: []string{takeover.FindingSecretOwner, takeover.FindingAppSetSafety}}))
	assertOtherPROpen(t, w, "A pull request to take over prod-eu is already open. Merge or close it first.", 9, "https://example.invalid/pr/9")
	if gp.prs != 0 {
		t.Fatalf("a pull request was opened: %d", gp.prs)
	}
}

// S7: a take over whose waiting mark cannot go on is a 207 partial in plain
// words. The pull request is open and tracked, not merged, and the
// connection keeps its owner.
func TestTakeover_MarkCannotGoOn_Is207_RoundS(t *testing.T) {
	gp := newTakeoverFakeGP()
	srv, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)}, fakeAppSetReader{})
	srv.SetPRTracker(prtracker.NewTracker(cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs"),
		func() prtracker.GitProvider { return nil }, func(audit.Entry) {}))
	k8s.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secret update refused")
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{takeover.FindingSecretOwner}}))
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("got %d, want 207 (body=%s)", w.Code, w.Body.String())
	}
	body := decodeMap(t, w)
	want := "Sharko opened pull request https://example.invalid/pr/1, but could not mark prod-eu's ArgoCD connection as waiting for it. Nothing has changed on the connection. Run the take over again to put the mark on, then merge the pull request."
	if body["status"] != "partial" || body["message"] != want {
		t.Fatalf("got %s\nwant message %q", w.Body.String(), want)
	}
	if gp.prs != 1 {
		t.Fatalf("want one pull request, got %d", gp.prs)
	}
	tracked, err := srv.prTracker.GetPR(t.Context(), 1)
	if err != nil || tracked == nil {
		t.Fatalf("the pull request must still be tracked: %+v %v", tracked, err)
	}
	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != "" || live.Labels["env"] != "prod" {
		t.Fatalf("the connection changed: %v", live.Labels)
	}
}
