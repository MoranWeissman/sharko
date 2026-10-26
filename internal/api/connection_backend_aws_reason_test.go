package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ep4 round R, R6. When the cluster connection check cannot read the
// cluster's login from an AWS secrets store, the connection page used to show
// only the general "could not read" sentence, whatever AWS had said — so a
// switched-off key looked the same as a missing secret. Now the page shows
// the same AWS sentence Settings → Secrets Provider shows for the same
// failure, picked by the error's types. Anything else keeps the general
// sentence. AWS's own words never reach the answer.
func TestConnectionCheck_BackendReadSaysTheAWSCause(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"AWS turned the login down", awsRefusedErr(), wantAWSLoginRefusedSentence},
		{"AWS never answered", awsUnreachableErr(), wantAWSUnreachableSentence},
		{"Sharko has no AWS login", awsNoLoginErr(), wantAWSNoLoginSentence},
		{"some other store failure", credsafe.Mark(errors.New("zz9-other-store-words")), failBackendRead},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := expectedLiveSecretForFixture(t)
			live.Annotations = map[string]string{
				clusterreconciler.AnnotationRevision:  reconTestCommit,
				clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
			}
			_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{failWith: tc.err})

			// The comparison answer.
			w := httptest.NewRecorder()
			router.ServeHTTP(w, comparisonReq(comparisonCluster))
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
			}
			var cmp connectionComparisonView
			if err := json.Unmarshal(w.Body.Bytes(), &cmp); err != nil {
				t.Fatalf("decoding the comparison answer: %v", err)
			}
			if cmp.Status != "check_failed" {
				t.Fatalf("status = %q, want check_failed", cmp.Status)
			}
			if cmp.FailureReason != tc.want {
				t.Errorf("failure_reason:\n got: %q\nwant: %q", cmp.FailureReason, tc.want)
			}
			assertNoStoreWords(t, w.Body.String())

			// The connection answer the page shows: same words, still a
			// check that did not finish, never synced.
			w = httptest.NewRecorder()
			router.ServeHTTP(w, reconciliationReq(comparisonCluster))
			out := decodeRecon(t, w)
			if out.Sync.State != syncStateUnknown {
				t.Errorf("sync.state = %q, want unknown", out.Sync.State)
			}
			if out.Sync.Reason != tc.want {
				t.Errorf("sync.reason:\n got: %q\nwant: %q", out.Sync.Reason, tc.want)
			}
			assertNoStoreWords(t, w.Body.String())
		})
	}
}

func assertNoStoreWords(t *testing.T, body string) {
	t.Helper()
	for _, banned := range []string{awsOwnWords, "acct-words-zz9", "zz9-other-store-words", "UnrecognizedClientException", "ec2imds", "GetMetadata"} {
		if strings.Contains(body, banned) {
			t.Errorf("the answer carries the store's own words %q: %s", banned, body)
		}
	}
}
