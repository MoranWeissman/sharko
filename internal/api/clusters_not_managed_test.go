package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Opening a cluster ArgoCD knows but Sharko does not manage used to give a
// bare "cluster not found" (episode 3 dry run, finding 7). It now says what
// is going on, with a code the UI can show the adopt and take over choices on.
func TestHandleGetCluster_ArgoCDKnowsItButSharkoDoesNotManageIt(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
		{"name": "legacy-1", "server": "https://10.0.0.5:6443"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	_, router := reconcileTestServer(t, gp, argo.URL)

	cases := []struct {
		name, want string
	}{
		{"legacy-1", `{"error":"Sharko does not manage legacy-1 yet. ArgoCD already connects to it, so you can adopt it or take it over.","code":"not_managed","argocd_known":true}` + "\n"},
		// ArgoCD does not know it either: the plain answer stays.
		{"nowhere", `{"error":"cluster not found"}` + "\n"},
	}
	// EP3 dry run round R, R10: the comparison call is what the cluster
	// page reads, so it must answer the same 404 body.
	for _, suffix := range []string{"", "/comparison"} {
		for _, tc := range cases {
			t.Run(tc.name+suffix, func(t *testing.T) {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+tc.name+suffix, nil))
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
				}
				if w.Body.String() != tc.want {
					t.Fatalf("body:\n got: %s\nwant: %s", w.Body.String(), tc.want)
				}
			})
		}
	}
}

// EP3 dry run round R, R10: the cluster object from the detail and the
// comparison calls carries adopted and taken_over, read off ArgoCD.
func TestClusterDetailAndComparison_CarryTheOwnershipMarks(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "adopted-1", "server": "https://a.example.com", "annotations": map[string]string{"sharko.dev/adopted": "true"}},
		{"name": "taken-1", "server": "https://t.example.com", "annotations": map[string]string{"sharko.dev/taken-over-at": "2026-10-23T10:00:00Z"}},
		{"name": "plain-1", "server": "https://p.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: adopted-1\n  labels: {}\n- name: taken-1\n  labels: {}\n- name: plain-1\n  labels: {}\n")}
	_, router := reconcileTestServer(t, gp, argo.URL)

	want := map[string][2]bool{"adopted-1": {true, false}, "taken-1": {false, true}, "plain-1": {false, false}}
	for name, marks := range want {
		for _, suffix := range []string{"", "/comparison"} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+name+suffix, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("%s%s: status = %d (body %s)", name, suffix, w.Code, w.Body.String())
			}
			var body struct {
				Cluster map[string]interface{} `json:"cluster"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Cluster["adopted"] != marks[0] || body.Cluster["taken_over"] != marks[1] {
				t.Fatalf("%s%s: adopted=%v taken_over=%v, want %v %v", name, suffix,
					body.Cluster["adopted"], body.Cluster["taken_over"], marks[0], marks[1])
			}
		}
	}
}
