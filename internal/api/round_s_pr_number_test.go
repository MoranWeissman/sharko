package api

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
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
