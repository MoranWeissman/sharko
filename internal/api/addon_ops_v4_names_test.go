package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The v4 enable/disable routes take BOTH a cluster name and an addon name
// straight out of the URL and paste them into git commit paths
// (clusters/<cluster>.yaml, values/clusters/<cluster>/<addon>.yaml). Go
// 1.22's ServeMux unescapes each path segment before PathValue returns it,
// so a request written as "..%2F..%2Fengine" arrives at the handler as the
// plain "../../engine" — path.Join would then clean the write straight out
// of the v4 data folders and onto the engine pin.
//
// These tests go through the REAL router (not the handler function) so the
// unescaping actually happens, which is the whole point of the finding.

func v4EnableURL(cluster, addon string) string {
	return "/api/v1/v4/clusters/" + cluster + "/addons/" + addon
}

func assertPlainWords400(t *testing.T, w *httptest.ResponseRecorder, wantSubstr string) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, wantSubstr) {
		t.Errorf("error = %q, want it to mention %q", msg, wantSubstr)
	}
}

func TestEnableAddonV4Route_URLEncodedTraversalClusterName_Rejected(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	// "..%2F..%2Fengine" — the encoded form a client would actually send.
	req := httptest.NewRequest(http.MethodPost,
		v4EnableURL("..%2F..%2Fengine", "metrics-server"),
		bytes.NewReader([]byte(`{"yes":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertPlainWords400(t, w, "invalid cluster name")
}

func TestEnableAddonV4Route_URLEncodedTraversalAddonName_Rejected(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost,
		v4EnableURL("prod-eu", "..%2F..%2Fengine"),
		bytes.NewReader([]byte(`{"yes":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertPlainWords400(t, w, "invalid addon name")
}

func TestDisableAddonV4Route_URLEncodedTraversalClusterName_Rejected(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodDelete,
		v4EnableURL("..%2F..%2Fengine", "metrics-server"), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertPlainWords400(t, w, "invalid cluster name")
}

// TestAddToCatalogRoute_TraversalName_Rejected covers the third write
// surface: the addon name in the POST body becomes a
// values/global/<addon>.yaml path segment (and a Kubernetes label key) the
// moment somebody enables the addon.
func TestAddToCatalogRoute_TraversalName_Rejected(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body := `{"addons":[{"name":"../../engine/application","repo_url":"https://charts.example.com","chart":"evil","version":"1.0.0"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/addons", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertPlainWords400(t, w, "invalid addon name")
}

// Ordinary names must still reach the connection check — proof the guard
// rejects the traversal specifically, not every request.
func TestEnableAddonV4Route_OrdinaryNames_PassTheNameGate(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost,
		v4EnableURL("prod-eu", "metrics-server"),
		bytes.NewReader([]byte(`{"yes":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("ordinary names were rejected by the name gate: %s", w.Body.String())
	}
}
