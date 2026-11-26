package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Walk 1 L20: moving to another page while the dashboard numbers loaded was
// logged as a server error with status 500 and the text "canceled".
func TestWriteDashboardUpstreamError_PageLeft_IsNotAServerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the person went to another page
	r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	writeDashboardUpstreamError(w, r, "dashboard_stats", context.Canceled)

	if w.Code != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d (the person left; the server did not fail)", w.Code, statusClientClosedRequest)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("a body was written for a person who is gone: %q", w.Body.String())
	}
}

// A real upstream failure on a live request is still a server error.
func TestWriteDashboardUpstreamError_LiveRequest_StillReportsTheFailure(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	w := httptest.NewRecorder()

	writeDashboardUpstreamError(w, r, "dashboard_stats", context.DeadlineExceeded)

	if w.Code < 500 {
		t.Fatalf("status = %d, a live request that failed must still be a 5xx", w.Code)
	}
}

// Walk 9 finding 4: the removal check said "There are no running workloads to
// disturb" about a cluster that had a running workload, only because ArgoCD had
// no Application for it.
func TestUnregisterNoAppsMeans_DoesNotClaimTheClusterRunsNothing(t *testing.T) {
	want := "Sharko only sees what ArgoCD deploys. Anything that runs on the cluster in some other way is not on this list, and taking the cluster out of Sharko does not touch it."
	if unregisterNoAppsMeans != want {
		t.Fatalf("sentence changed:\n%s\nwant:\n%s", unregisterNoAppsMeans, want)
	}
	if strings.Contains(unregisterNoAppsMeans, "no running workloads") {
		t.Fatal("the false claim is back")
	}
}

func TestV4CatalogWriteDoor_NamesTheCommandLineWay(t *testing.T) {
	want := `this repo keeps its approved addons in catalog.yaml, and the only way in is the approval pull request — use "Add to catalog" in the UI, "sharko add-to-catalog" on the command line, or POST /api/v1/catalog/addons`
	if orchestrator.V4CatalogWriteDoor != want {
		t.Fatalf("sentence:\n%s\nwant:\n%s", orchestrator.V4CatalogWriteDoor, want)
	}
}

func TestUnadoptAndUnregisterUnknownCluster_Sentences(t *testing.T) {
	if got, want := UnadoptUnknownClusterMessage("nosuch"),
		`Sharko cannot find a cluster called "nosuch" in ArgoCD, so there is nothing to un-adopt. Nothing was changed. Check the name with: sharko list-clusters`; got != want {
		t.Errorf("un-adopt sentence:\n%s\nwant:\n%s", got, want)
	}
	if got, want := UnregisterUnknownClusterMessage("nosuch"),
		`Sharko has no cluster called "nosuch": it is not in the list of managed clusters and ArgoCD has no connection for it. There is nothing to remove. Check the name with: sharko list-clusters`; got != want {
		t.Errorf("unregister sentence:\n%s\nwant:\n%s", got, want)
	}
}

// Walk 9 finding 5, walk 8 L05: un-adopting a name that matches no connection
// answered 502 and showed the namespace and the Secret lookup behind it.
func TestUnadopt_UnknownCluster_Is404WithPlainSentence(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, nil, fakeAppSetReader{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/ghost-cluster/unadopt", "admin",
		map[string]interface{}{"yes": true}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	want := UnadoptUnknownClusterMessage("ghost-cluster")
	if !strings.Contains(w.Body.String(), strings.ReplaceAll(want, `"`, `\"`)) {
		t.Fatalf("body %q does not carry %q", w.Body.String(), want)
	}
	for _, leak := range []string{"namespace", "getting secret", "secrets \\\"ghost-cluster\\\" not found"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("the Secret lookup shows through: %q in %s", leak, w.Body.String())
		}
	}
}
