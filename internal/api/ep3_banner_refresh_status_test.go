package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3 banner fix: after a pull request merged by hand, the finish step
// worked but every refresh still answered status "finishing", so the
// un-adopt, remove, register, add-on and values banners never turned to
// done. The refresh must answer what the tracker holds now.

// bannerRefreshServer is a server with a real tracker on a fake ConfigMap
// and a Git provider for the not-tracked path, both answering gitStatus.
// finishFn may be nil (the finish works and the entry is dropped).
func bannerRefreshServer(t *testing.T, gitStatus string, finishFn prtracker.FinishFn, seed ...prtracker.PRInfo) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&statusGit{handlerFakeGitProvider: &handlerFakeGitProvider{}, status: gitStatus})
	store := cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs")
	tracker := prtracker.NewTracker(store, func() prtracker.GitProvider { return &fakeRefreshGitProvider{status: gitStatus} }, func(audit.Entry) {})
	if finishFn != nil {
		tracker.SetFinishFn(finishFn)
	}
	srv.SetPRTracker(tracker)
	for _, pr := range seed {
		if err := tracker.TrackPR(t.Context(), pr); err != nil {
			t.Fatalf("seed TrackPR(%d): %v", pr.PRID, err)
		}
	}
	return srv
}

func refreshItem(t *testing.T, srv *Server, id string) PRItem {
	t.Helper()
	rr := doRefreshPR(t, srv, id)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh %s: status %d, body %s", id, rr.Code, rr.Body.String())
	}
	var item PRItem
	if err := json.NewDecoder(rr.Body).Decode(&item); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return item
}

func tracked(t *testing.T, srv *Server, id int) bool {
	t.Helper()
	cur, err := srv.prTracker.GetPR(t.Context(), id)
	if err != nil {
		t.Fatalf("GetPR(%d): %v", id, err)
	}
	return cur != nil
}

var unadoptPR = prtracker.PRInfo{PRID: 5, Operation: "unadopt-cluster", Cluster: "legacy-1", LastStatus: "open"}

// Acceptance 1 + 5: the refresh that is first to see the merge runs the
// finish inline; it must answer "merged", not the pre-finish "finishing".
func TestRefreshPR_FirstToSeeMerge_FinishWorks_SaysMerged(t *testing.T) {
	srv := bannerRefreshServer(t, "merged", nil, unadoptPR)

	item := refreshItem(t, srv, "5")
	if item.Status == prtracker.StatusFinishing {
		t.Fatalf("status = %q after a finish that worked; the banner would stay on finishing", item.Status)
	}
	if item.Status != "merged" || item.LastStatus != "merged" {
		t.Fatalf("status = %q, last_status = %q, want merged / merged", item.Status, item.LastStatus)
	}
	if tracked(t, srv, 5) {
		t.Fatal("PR 5 is still tracked; the finish should have worked and dropped it")
	}
}

// Acceptance 2 + 5: once the PR is no longer tracked, every later refresh
// asks Git and must answer "merged" (or "closed"), never "finishing".
func TestRefreshPR_AfterFinish_Untracked_SaysWhatGitSays(t *testing.T) {
	for _, gitStatus := range []string{"merged", "closed"} {
		t.Run(gitStatus, func(t *testing.T) {
			srv := bannerRefreshServer(t, gitStatus, nil, unadoptPR)
			_ = refreshItem(t, srv, "5") // first refresh: runs the finish
			if tracked(t, srv, 5) {
				t.Fatal("PR 5 is still tracked after the first refresh")
			}

			item := refreshItem(t, srv, "5") // second refresh: not tracked any more
			if item.Status == prtracker.StatusFinishing {
				t.Fatalf("status = %q for a PR Sharko no longer tracks; the banner would stay on finishing", item.Status)
			}
			if item.Status != gitStatus || item.LastStatus != gitStatus {
				t.Fatalf("status = %q, last_status = %q, want %s / %s", item.Status, item.LastStatus, gitStatus, gitStatus)
			}
		})
	}
}

// Acceptance 3: a merged PR whose finish keeps failing is still tracked and
// not given up, so "finishing" is the honest answer, on every refresh.
func TestRefreshPR_MergedFinishFails_StillTracked_SaysFinishing(t *testing.T) {
	failing := func(context.Context, prtracker.PRInfo, string) error { return errors.New("argocd unreachable") }
	srv := bannerRefreshServer(t, "merged", failing, unadoptPR)

	for i := 1; i <= 2; i++ {
		item := refreshItem(t, srv, "5")
		if item.Status != prtracker.StatusFinishing || item.LastStatus != "merged" {
			t.Fatalf("refresh %d: status = %q, last_status = %q, want finishing / merged", i, item.Status, item.LastStatus)
		}
		if !tracked(t, srv, 5) {
			t.Fatalf("refresh %d: PR 5 was dropped though its finish failed", i)
		}
	}
}

// Acceptance 4: an open PR answers "open".
func TestRefreshPR_Open_SaysOpen(t *testing.T) {
	srv := bannerRefreshServer(t, "open", nil, unadoptPR)

	item := refreshItem(t, srv, "5")
	if item.Status != "open" || item.LastStatus != "open" {
		t.Fatalf("status = %q, last_status = %q, want open / open", item.Status, item.LastStatus)
	}
}
