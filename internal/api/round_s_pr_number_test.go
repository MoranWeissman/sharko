package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// EP3 dry run round S, S14: the take over sentences never say "PR #0" or
// "#0". When the number is not known they say "a pull request".
func TestTakeoverSentences_UnknownNumber_SayAPullRequest(t *testing.T) {
	const url = "https://git.example.com/o/r/pulls/open"
	got := map[string]string{
		"waiting": takeoverWaitingMessage("prod-eu", 0, url, true, nil),
		"plan":    takeoverPlanAlreadyWaiting(0, url),
		"already": takeoverGitSentence("prod-eu", &orchestrator.TakeoverGitResult{AlreadyWaiting: true, Git: &orchestrator.GitResult{PRUrl: url}}),
		"open":    takeoverGitSentence("prod-eu", &orchestrator.TakeoverGitResult{Git: &orchestrator.GitResult{PRUrl: url}}),
	}
	want := map[string]string{
		"waiting": "A pull request is open: " + url + ". When it is merged, Sharko takes ownership of prod-eu's ArgoCD connection. If it is closed without merging, Sharko changes nothing on the connection.",
		"plan":    "A pull request for this is already open: " + url + ". Running the take over for real will not open a second one.",
		"already": "Taking over prod-eu is already waiting in a pull request: " + url + ". Sharko did not open a second one. Merge it to finish.",
		"open":    "The pull request that adds it to the fleet record in Git is open: " + url + ". Merge it to finish.",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s:\n got %q\nwant %q", k, got[k], w)
		}
		if strings.Contains(got[k], "#0") {
			t.Errorf("%s says #0: %q", k, got[k])
		}
	}
}

func TestTakeoverSentences_KnownNumber_Unchanged(t *testing.T) {
	const url = "https://github.com/o/r/pull/12"
	if got, w := takeoverWaitingMessage("prod-eu", 12, url, true, nil), "Pull request #12 is open: "+url+". When it is merged, Sharko takes ownership of prod-eu's ArgoCD connection. If it is closed without merging, Sharko changes nothing on the connection."; got != w {
		t.Errorf("got %q want %q", got, w)
	}
	if got, w := takeoverPlanAlreadyWaiting(12, url), "A pull request for this is already open: #12, "+url+". Running the take over for real will not open a second one."; got != w {
		t.Errorf("got %q want %q", got, w)
	}
}

// EP3 dry run round S, S13: a take over with auto-merge whose merge does not
// go through is not a failure. The pull request is open and tracked, so the
// answer is a 207 with the same body as a take over that waits for its
// merge, plus merge_error.

type takeoverMergeFailsGP struct{ *takeoverFakeGP }

func (takeoverMergeFailsGP) MergePullRequest(context.Context, int) error {
	return errors.New("405 Method Not Allowed: branch protection token=ghp_fakeleak123")
}

func TestTakeover_AutoMergeFails_207WaitingWithMergeError(t *testing.T) {
	gp := takeoverMergeFailsGP{newTakeoverFakeGP()}
	autoMerge := true
	_, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AutoMerge: &autoMerge, AcknowledgedFindings: []string{takeover.FindingAppSetSafety, takeover.FindingSecretOwner}}))

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("got %d, want 207 (body=%s)", w.Code, w.Body.String())
	}
	var resp TakeoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.WaitingForMerge || resp.Status != "success" || resp.PRNumber != 1 || resp.PRURL != "https://example.invalid/pr/1" {
		t.Fatalf("not a waiting answer with the pull request: %s", w.Body.String())
	}
	want := "Sharko could not merge the pull request on its own. Nothing on the connection has changed. Merge the pull request yourself to finish the take over, or close it to leave things as they are."
	if resp.MergeError != want {
		t.Fatalf("merge_error = %q, want %q", resp.MergeError, want)
	}
	if resp.SecretSwapped {
		t.Fatal("the connection was swapped although the pull request is not merged")
	}
	for _, bad := range []string{"ghp_fakeleak123", "405", "PR created but merge failed"} {
		if strings.Contains(w.Body.String(), bad) {
			t.Fatalf("answer carries the raw merge error (%q): %s", bad, w.Body.String())
		}
	}
	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Labels[argosecrets.LabelManagedBy] == argosecrets.ManagedByValue {
		t.Fatalf("Sharko took ownership before the merge: %v", secret.Labels)
	}
}

func TestTakeover_Waiting_NamesThePullRequestAtTheTop(t *testing.T) {
	gp := newTakeoverFakeGP()
	autoMerge := false
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AutoMerge: &autoMerge, AcknowledgedFindings: []string{takeover.FindingAppSetSafety, takeover.FindingSecretOwner}}))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp TakeoverResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.WaitingForMerge || resp.PRNumber != 1 || resp.PRURL != "https://example.invalid/pr/1" || resp.MergeError != "" {
		t.Fatalf("waiting answer = %s", w.Body.String())
	}
}
