package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3-DRYRUN-FIXES join: open adopt and take over pull requests show in
// pending_registrations with their own operation. The titles are the ones
// the orchestrator really opens: "<prefix> adopt cluster <name>"
// (adopt_v4.go, adopt.go) and "<prefix> take over cluster <name>"
// (takeover.go). "unadopt cluster" stays out of the list.
func TestResolvePendingRegistrations_AdoptPRsShowWithOperation(t *testing.T) {
	gp := &fakeGP{prs: []gitprovider.PullRequest{
		{ID: 21, Title: "sharko: adopt cluster legacy-1", URL: "https://github.com/org/repo/pull/21", SourceBranch: "sharko/adopt-cluster-legacy-1-ab12"},
		{ID: 22, Title: "sharko: unadopt cluster legacy-2", URL: "https://github.com/org/repo/pull/22"},
	}}
	got := resolvePendingRegistrations(context.Background(), nil, gp, "sharko:")
	if len(got) != 1 {
		t.Fatalf("want only the adopt row, got %+v", got)
	}
	want := models.PendingRegistration{
		ClusterName: "legacy-1",
		PRURL:       "https://github.com/org/repo/pull/21",
		Branch:      "sharko/adopt-cluster-legacy-1-ab12",
		Operation:   models.PendingOperationAdopt,
		PRNumber:    21,
	}
	if got[0] != want {
		t.Fatalf("adopt row = %+v, want %+v", got[0], want)
	}
	if models.PendingOperationAdopt != "adopt" {
		t.Fatalf("PendingOperationAdopt = %q, the API contract says \"adopt\"", models.PendingOperationAdopt)
	}
}

func TestResolvePendingRegistrations_TakeoverPRsShowWithOperation(t *testing.T) {
	gp := &fakeGP{prs: []gitprovider.PullRequest{
		{ID: 31, Title: "sharko: take over cluster legacy-3", URL: "https://github.com/org/repo/pull/31", SourceBranch: "sharko/take-over-cluster-legacy-3-cd34"},
	}}
	got := resolvePendingRegistrations(context.Background(), nil, gp, "sharko:")
	if len(got) != 1 {
		t.Fatalf("want the take over row, got %+v", got)
	}
	want := models.PendingRegistration{
		ClusterName: "legacy-3",
		PRURL:       "https://github.com/org/repo/pull/31",
		Branch:      "sharko/take-over-cluster-legacy-3-cd34",
		Operation:   models.PendingOperationTakeover,
		PRNumber:    31,
	}
	if got[0] != want {
		t.Fatalf("take over row = %+v, want %+v", got[0], want)
	}
	if models.PendingOperationTakeover != "takeover" {
		t.Fatalf("PendingOperationTakeover = %q, the API contract says \"takeover\"", models.PendingOperationTakeover)
	}
	// A register PR for a cluster already in Git is dropped; an adopt or
	// take over row never is.
	kept := dropPendingForManagedClusters(got, map[string]struct{}{"legacy-3": {}})
	if len(kept) != 1 {
		t.Fatalf("take over row dropped by the already-managed filter: %+v", kept)
	}
}

// A cluster ArgoCD knows, with an open adopt pull request, stays in
// clusters[] (not_in_git) while the pull request waits, and shows in
// pending_registrations as "adopt". A cluster with an open register pull
// request still shows only as pending. The UI uses the same rule.
func TestListClusters_OpenAdoptPRKeepsClusterInList(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "legacy-1", "server": "https://legacy-1.local:6443"},
		{"name": "new-1", "server": "https://new-1.local:6443"},
	}, 0)
	gp := &orphanFakeGP{
		managedYAML: []byte("clusters: []"),
		prs: []gitprovider.PullRequest{
			{ID: 41, Title: "sharko: adopt cluster legacy-1", URL: "https://github.com/o/r/pull/41"},
			{ID: 42, Title: "sharko: register cluster new-1", URL: "https://github.com/o/r/pull/42"},
		},
	}
	_, router := orphanTestServer(t, gp, argo.URL, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /clusters: %d %s", w.Code, w.Body.String())
	}
	var resp models.ClustersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	inList := map[string]string{}
	for _, c := range resp.Clusters {
		inList[c.Name] = c.ConnectionStatus
	}
	if inList["legacy-1"] != "not_in_git" {
		t.Fatalf("legacy-1 (open adopt PR) must stay in clusters[] as not_in_git; clusters = %v", inList)
	}
	if _, ok := inList["new-1"]; ok {
		t.Fatalf("new-1 (open register PR) must show only as pending; clusters = %v", inList)
	}
	ops := map[string]string{}
	for _, p := range resp.PendingRegistrations {
		ops[p.ClusterName] = p.Operation
	}
	if ops["legacy-1"] != "adopt" || ops["new-1"] != "register" {
		t.Fatalf("pending operations = %v, want legacy-1 adopt and new-1 register", ops)
	}
}
