package api

import (
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
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+tc.name, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
			}
			if w.Body.String() != tc.want {
				t.Fatalf("body:\n got: %s\nwant: %s", w.Body.String(), tc.want)
			}
		})
	}
}
