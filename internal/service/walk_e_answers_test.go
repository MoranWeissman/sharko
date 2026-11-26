package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Walk 2 finding 10, walk 8 L08: the list said managed: true for a cluster and
// the single-cluster answer said managed: false for the very same cluster.
func TestClusterService_GetClusterDetail_ManagedMatchesTheList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[{"name":"prod-eks","server":"https://x.example.com","info":{"connectionState":{"status":"Successful"}}}]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: prod-eks\n    labels: {}\n"),
		"configuration/addons-catalog.yaml":   []byte("applicationsets: []"),
	}}

	list, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatal(err)
	}
	var inList *models.Cluster
	for i := range list.Clusters {
		if list.Clusters[i].Name == "prod-eks" {
			inList = &list.Clusters[i]
		}
	}
	if inList == nil || !inList.Managed {
		t.Fatalf("precondition: the list must say prod-eks is managed: %+v", inList)
	}

	detail, err := svc.GetClusterDetail(context.Background(), "prod-eks", gp, ac)
	if err != nil || detail == nil {
		t.Fatalf("detail: %v %v", detail, err)
	}
	if !detail.Cluster.Managed {
		t.Fatal("the single-cluster answer says managed: false for a cluster the list calls managed")
	}
}

// Walk 1 L16: describe-addon said "0 generated app(s)" next to one running app,
// because ArgoCD only fills the ApplicationSet's applicationStatus for a
// progressive rollout.
func TestCountRealApplications_CountsDeploymentsThatHaveAnApp(t *testing.T) {
	deps := []models.AddonDeploymentInfo{
		{ClusterName: "a", ApplicationName: "reloader-a"},
		{ClusterName: "b", Enabled: false},
		{ClusterName: "c", ApplicationName: "reloader-c"},
	}
	if got := countRealApplications(deps); got != 2 {
		t.Fatalf("countRealApplications = %d, want 2", got)
	}
	if got := countRealApplications(nil); got != 0 {
		t.Fatalf("countRealApplications(nil) = %d, want 0", got)
	}
}
