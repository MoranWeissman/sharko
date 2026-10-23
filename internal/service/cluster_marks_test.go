package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
)

// GET /clusters items carry adopted, taken_over and (for a cluster only
// ArgoCD knows) its ArgoCD labels — the EP3-DRYRUN-FIXES API contract.
func TestClusterService_ListClusters_AdoptedTakenOverAndLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"name":"took","server":"https://took.example.com","annotations":{"sharko.dev/taken-over-at":"2026-10-23T00:00:00Z","sharko.dev/adopted":"true"}},
			{"name":"old-adopt","server":"https://old.example.com","annotations":{"sharko.sharko.io/adopted":"true"}},
			{"name":"plain","server":"https://plain.example.com"},
			{"name":"argo-only","server":"https://argo-only.example.com","labels":{"env":"dev"},"annotations":{"sharko.dev/adopt-pending-pr":"4"}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	gp := &fakeGP{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: took\n    labels: {}\n  - name: old-adopt\n    labels: {}\n  - name: plain\n    labels: {}\n"),
	}}
	resp, err := NewClusterService("").ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatal(err)
	}
	type marks struct {
		adopted, takenOver bool
		labels             map[string]string
	}
	got := map[string]marks{}
	for _, c := range resp.Clusters {
		got[c.Name] = marks{c.Adopted, c.TakenOver, c.Labels}
	}
	want := map[string][2]bool{
		"took":      {true, true},
		"old-adopt": {true, false},
		"plain":     {false, false},
		"argo-only": {false, false},
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Fatalf("%s missing from the list", name)
		}
		if g.adopted != w[0] || g.takenOver != w[1] {
			t.Errorf("%s: adopted=%v taken_over=%v, want %v %v", name, g.adopted, g.takenOver, w[0], w[1])
		}
	}
	if got["argo-only"].labels["env"] != "dev" {
		t.Errorf("a cluster only ArgoCD knows must show its ArgoCD labels, got %v", got["argo-only"].labels)
	}

	// Both fields are always sent, true or false.
	body, _ := json.Marshal(resp.Clusters[0])
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	for _, key := range []string{"adopted", "taken_over", "labels"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%q is not in the JSON: %s", key, body)
		}
	}
}
