package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/notifications"
)

// ep4, serious 5: the secrets store turned Sharko's own login down. The
// credential check could not read the cluster's login, so:
//   - the connection answer must not say synced or "all checks passed";
//   - the bell gets ONE entry for that cluster, however many checks fail;
//   - the entry is cleared when the check works again.

const bellRawBackendText = "UnrecognizedClientException: The security token included in the request is invalid zz9-bell-raw"

// refusedBackendErr is shaped like AWS turning down a switched-off key.
func refusedBackendErr() error {
	return credsafe.Mark(fmt.Errorf("reading secret: %w", &smithy.OperationError{
		ServiceID: "Secrets Manager", OperationName: "GetSecretValue",
		Err: &smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: bellRawBackendText},
	}))
}

func credentialBellEntries(srv *Server) []notifications.Notification {
	var out []notifications.Notification
	for _, n := range srv.NotificationStore().List() {
		if n.Code == notifications.CodeClusterCredentialCheckFailed {
			out = append(out, n)
		}
	}
	return out
}

const wantBellTitle = "Sharko can't read the login for " + comparisonCluster
const wantBellDetail = "Sharko could not read this cluster's login from your secrets store, so it can't check that the cluster's connection is right. " +
	"Check Sharko's login to the secrets store in Settings → Secrets Provider. " +
	"The sign-in details Sharko uses were refused, or Sharko could not read them. The server log for this check says which step failed."

func TestCredentialCheckBell_OneEntryPerClusterThenCleared(t *testing.T) {
	working := comparisonFakeVault{}
	srv, _, argoClient := comparisonFixture(t, backendManagedYAML, nil, working)
	seedSyncedLiveSecret(t, srv, argoClient)
	loop := newTestCheckLoop(srv)
	ctx := context.Background()

	loop.runOnce(ctx)
	if got := credentialBellEntries(srv); len(got) != 0 {
		t.Fatalf("a working check put %d entries on the bell, want 0", len(got))
	}

	installCredProvider(srv, comparisonFakeVault{failWith: refusedBackendErr()}, nil, nil)
	for i := 0; i < 4; i++ {
		loop.runOnce(ctx)
	}
	got := credentialBellEntries(srv)
	if len(got) != 1 {
		t.Fatalf("four failing checks put %d entries on the bell, want exactly 1", len(got))
	}
	if got[0].Title != wantBellTitle {
		t.Errorf("title:\n got: %q\nwant: %q", got[0].Title, wantBellTitle)
	}
	if got[0].Description != wantBellDetail {
		t.Errorf("detail:\n got: %q\nwant: %q", got[0].Description, wantBellDetail)
	}
	if got[0].ID != "credential-check-"+comparisonCluster {
		t.Errorf("id = %q", got[0].ID)
	}
	blob, _ := json.Marshal(srv.NotificationStore().List())
	for _, banned := range []string{"zz9-bell-raw", "UnrecognizedClientException", "security token", "Settings → Connections"} {
		if strings.Contains(string(blob), banned) {
			t.Errorf("the bell carries %q: %s", banned, blob)
		}
	}

	// Read it, keep failing: still one, no new unread entry.
	srv.NotificationStore().MarkAllRead()
	loop.runOnce(ctx)
	if n := len(credentialBellEntries(srv)); n != 1 || srv.NotificationStore().UnreadCount() != 0 {
		t.Fatalf("after reading it and one more failing check: %d entries, %d unread; want 1 and 0", n, srv.NotificationStore().UnreadCount())
	}

	installCredProvider(srv, working, nil, nil)
	loop.runOnce(ctx)
	if got := credentialBellEntries(srv); len(got) != 0 {
		t.Fatalf("the check works again but the bell still has %d entries for it", len(got))
	}

	// Breaks again later: a fresh entry.
	installCredProvider(srv, comparisonFakeVault{failWith: refusedBackendErr()}, nil, nil)
	loop.runOnce(ctx)
	if n := len(credentialBellEntries(srv)); n != 1 {
		t.Fatalf("a second outage put %d entries on the bell, want 1", n)
	}
}

// Two clusters, one store: each has its own entry, and clearing one leaves
// the other.
func TestCredentialCheckBell_EachClusterHasItsOwnEntry(t *testing.T) {
	bell := notifications.NewStore(10, nil)
	st := newConnectionCredentialCheckStore(nil, bell)
	failed := connectionComparisonView{Status: string(connectioncompare.StatusCheckFailed), FailureReason: failBackendRead, failure: connectioncompare.CheckFailureBackendRead}
	synced := connectionComparisonView{Status: string(connectioncompare.StatusSynced)}

	st.record("prod-eu", failed)
	st.record("prod-us", failed)
	st.record("prod-eu", failed)
	if n := len(bell.List()); n != 2 {
		t.Fatalf("two failing clusters gave %d entries, want 2", n)
	}
	st.record("prod-eu", synced)
	list := bell.List()
	if len(list) != 1 || list[0].ID != "credential-check-prod-us" {
		t.Fatalf("after prod-eu recovered the bell holds %+v, want only prod-us's entry", list)
	}
}

// A check that failed for a reason that is not the secrets store leaves the
// bell alone: it neither raises nor clears the entry.
func TestCredentialCheckBell_OtherFailuresLeaveItAlone(t *testing.T) {
	bell := notifications.NewStore(10, nil)
	st := newConnectionCredentialCheckStore(nil, bell)
	backend := connectionComparisonView{Status: string(connectioncompare.StatusCheckFailed), failure: connectioncompare.CheckFailureBackendRead}
	gitRead := connectionComparisonView{Status: string(connectioncompare.StatusCheckFailed), failure: connectioncompare.CheckFailureGitRead}

	st.record("prod-eu", gitRead)
	if n := len(bell.List()); n != 0 {
		t.Fatalf("a Git read failure put %d entries on the bell, want 0", n)
	}
	st.record("prod-eu", backend)
	st.record("prod-eu", gitRead)
	if n := len(bell.List()); n != 1 {
		t.Fatalf("a Git read failure after a backend failure left %d entries, want the 1 still there", n)
	}
}

// The connection answer for the same cluster, while the secrets store turns
// Sharko down: unknown, nothing verified, the check-failed headline — never
// synced. The live Secret here is one that WOULD be synced if the check
// could finish.
func TestCredentialCheckBell_ConnectionAnswerNeverSaysSynced(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	live.Annotations = map[string]string{
		clusterreconciler.AnnotationRevision:  reconTestCommit,
		clusterreconciler.AnnotationWrittenAt: "2026-08-18T09:00:00Z",
	}
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{failWith: refusedBackendErr()})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconciliationReq(comparisonCluster))
	out := decodeRecon(t, w)
	if out.Sync.State != syncStateUnknown {
		t.Errorf("sync.state = %q, want unknown", out.Sync.State)
	}
	if out.Sync.VerificationScope != verificationScopeNone {
		t.Errorf("sync.verification_scope = %q, want none", out.Sync.VerificationScope)
	}
	if out.Sync.Headline != "Unknown — check failed" {
		t.Errorf("sync.headline = %q, want %q", out.Sync.Headline, "Unknown — check failed")
	}
	if out.Sync.Reason != failBackendRead {
		t.Errorf("sync.reason = %q, want %q", out.Sync.Reason, failBackendRead)
	}
	body := w.Body.String()
	for _, banned := range []string{"Connection synced", "checks passed", `"state":"synced"`, "zz9-bell-raw"} {
		if strings.Contains(body, banned) {
			t.Errorf("the connection answer carries %q while the check could not finish: %s", banned, body)
		}
	}
}
