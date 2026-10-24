package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3 dry run round S, S25: `sharko pr wait <n>` must still work after a
// fast merge. A pull request that merged at once is no longer tracked, so
// the refresh asks Git how it ended.

type statusGit struct {
	*handlerFakeGitProvider
	status string
}

func (g *statusGit) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return g.status, nil
}

func untrackedRefreshServer(t *testing.T, status string) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&statusGit{handlerFakeGitProvider: &handlerFakeGitProvider{}, status: status})
	store := cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs")
	srv.SetPRTracker(prtracker.NewTracker(store, func() prtracker.GitProvider { return &fakeRefreshGitProvider{status: status} }, func(audit.Entry) {}))
	return srv
}

func TestRefreshPR_UntrackedButMerged_SaysMerged(t *testing.T) {
	for _, status := range []string{"merged", "closed"} {
		rr := doRefreshPR(t, untrackedRefreshServer(t, status), "12")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", status, rr.Code, rr.Body.String())
		}
		var item PRItem
		if err := json.NewDecoder(rr.Body).Decode(&item); err != nil {
			t.Fatal(err)
		}
		if item.PRID != 12 || item.LastStatus != status {
			t.Fatalf("%s: got %+v", status, item)
		}
	}
}

func TestRefreshPR_UntrackedAndOpen_StillNotTracked(t *testing.T) {
	rr := doRefreshPR(t, untrackedRefreshServer(t, "open"), "12")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rr.Code, rr.Body.String())
	}
}
