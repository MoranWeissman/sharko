package api

// restart_sync_permission_test.go — restarting an addon's sync is the one
// place in Sharko that needs ArgoCD's `applications, sync` permission
// directly. Installing Sharko does not grant that permission and Sharko never
// grants it to itself, so an operator can perfectly reasonably be running
// without it — and what happens then has to be an honest refusal, named, with
// nothing terminated on the way.
//
// Two failures are pinned here, in both directions:
//
//   - A refusal must not be reported as an ArgoCD outage. It used to be: any
//     error from the sync call became 502 with ArgoCD's own words appended,
//     so "you are not allowed" read as "ArgoCD is broken" and sent the
//     operator looking at the wrong thing.
//   - A refusal must not arrive AFTER the running operation was terminated.
//     That is the shape that leaves an application worse off than not
//     pressing the button at all, and it is why the capability is asked
//     about before anything is touched.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// argocdPermissionServer builds an ArgoCD stand-in whose can-i endpoint and
// sync endpoint can each be set independently, and which records whether
// terminate and sync were reached.
//
// canIStatus/canIBody describe what /api/v1/account/can-i answers. An ArgoCD
// too old to have the endpoint answers 404, and that is a real case: it must
// not read as a refusal.
func argocdPermissionServer(
	t *testing.T,
	appName string,
	canIStatus int,
	canIBody string,
	syncStatus int,
) (ts *httptest.Server, terminated *bool, syncAttempted *bool) {
	t.Helper()
	term, syn := false, false
	terminated, syncAttempted = &term, &syn

	canIPath := "/api/v1/account/can-i/applications/sync/"

	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, canIPath):
			w.WriteHeader(canIStatus)
			_, _ = w.Write([]byte(canIBody))
		case r.Method == http.MethodGet && path == "/api/v1/applications/"+appName:
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{
				"metadata":{"name":%q,"namespace":"argocd"},
				"spec":{"project":"sharko-addons","source":{"repoURL":"https://github.com/example/repo"}},
				"status":{
					"sync":{"status":"OutOfSync"},
					"health":{"status":"Healthy"},
					"operationState":{"phase":"Running","startedAt":"2026-06-10T11:50:00Z"}
				}
			}`, appName)
		case r.Method == http.MethodDelete && path == "/api/v1/applications/"+appName+"/operation":
			term = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && path == "/api/v1/applications/"+appName+"/sync":
			syn = true
			w.WriteHeader(syncStatus)
			// ArgoCD's refusal body names the account and the object. It is
			// deliberately written here so a test can prove it does not come
			// back out in the HTTP response.
			_, _ = w.Write([]byte(`{"error":"permission denied: applications, sync, sharko-addons/` + appName + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	return ts, terminated, syncAttempted
}

func restartSyncResponse(t *testing.T, argoURL string) *httptest.ResponseRecorder {
	t.Helper()
	srv := newTestServerWithArgocd(t, argoURL, "test-token")
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// errorBody pulls the message out of the standard error response.
func errorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response body is not JSON: %v — body: %s", err, w.Body.String())
	}
	for _, key := range []string{"error", "message", "detail"} {
		if v, ok := payload[key].(string); ok && v != "" {
			return v
		}
	}
	t.Fatalf("no message field in the error response: %s", w.Body.String())
	return ""
}

// TestRestartAddonSync_WhenArgoCDSaysNo_RefusesBeforeTerminating is the
// capability check doing its job.
func TestRestartAddonSync_WhenArgoCDSaysNo_RefusesBeforeTerminating(t *testing.T) {
	appName := "keda-moran-test"
	ts, terminated, syncAttempted := argocdPermissionServer(
		t, appName, http.StatusOK, `{"value":"no"}`, http.StatusOK)
	defer ts.Close()

	w := restartSyncResponse(t, ts.URL)

	if w.Code != http.StatusForbidden {
		t.Fatalf("ArgoCD said this token may not sync, and the answer was %d, not 403. A refusal reported "+
			"as anything else sends the operator looking for a fault that is not there. body: %s",
			w.Code, w.Body.String())
	}
	if *terminated {
		t.Error("the running ArgoCD operation was terminated even though the re-sync was never going to be " +
			"allowed. That leaves the application worse off than not pressing the button at all.")
	}
	if *syncAttempted {
		t.Error("the sync was attempted after ArgoCD had already said no")
	}

	msg := errorBody(t, w)
	if msg != syncNotPermitted {
		t.Errorf("the refusal does not carry the one fixed explanation.\ngot:  %s\nwant: %s", msg, syncNotPermitted)
	}
}

// TestRestartAddonSync_WhenTheCheckCannotAnswer_TheSyncItselfDecides pins the
// third state. An ArgoCD without the can-i endpoint must not have its 404 read
// as "denied" — that would take the feature away from installs that have the
// permission and always did.
func TestRestartAddonSync_WhenTheCheckCannotAnswer_TheSyncItselfDecides(t *testing.T) {
	appName := "keda-moran-test"
	ts, _, syncAttempted := argocdPermissionServer(
		t, appName, http.StatusNotFound, `{"message":"unknown endpoint"}`, http.StatusOK)
	defer ts.Close()

	w := restartSyncResponse(t, ts.URL)

	if w.Code != http.StatusOK {
		t.Fatalf("ArgoCD could not answer the capability question, so the sync itself is the authority and "+
			"it succeeded. Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !*syncAttempted {
		t.Error("the sync was never attempted. An unanswerable capability check must not silently do nothing — " +
			"that is the false no-op this check exists to prevent.")
	}
}

// TestRestartAddonSync_A403FromTheSyncIsARefusalNotAnOutage covers the case
// the capability check cannot catch: the check could not answer, the sync went
// ahead, and ArgoCD refused it.
func TestRestartAddonSync_A403FromTheSyncIsARefusalNotAnOutage(t *testing.T) {
	appName := "keda-moran-test"
	ts, _, syncAttempted := argocdPermissionServer(
		t, appName, http.StatusNotFound, `{"message":"unknown endpoint"}`, http.StatusForbidden)
	defer ts.Close()

	w := restartSyncResponse(t, ts.URL)

	if !*syncAttempted {
		t.Fatal("the sync was never attempted, so this test is not exercising what it claims to")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("ArgoCD refused the sync and Sharko answered %d. 502 says ArgoCD is broken; it is not, it "+
			"answered clearly. body: %s", w.Code, w.Body.String())
	}
	msg := errorBody(t, w)
	if msg != syncNotPermitted {
		t.Errorf("a refused sync does not carry the one fixed explanation.\ngot:  %s\nwant: %s", msg, syncNotPermitted)
	}
	if strings.Contains(w.Body.String(), "permission denied: applications, sync") {
		t.Error("ArgoCD's own refusal body came back out in the HTTP response. What ArgoCD quotes inside an " +
			"error is its business, not the caller's.")
	}
}

// TestSyncNotPermitted_SaysWhatIsMissingWhoGrantsItAndWhatStillWorks pins the
// sentence itself. A message that only says "forbidden" tells an operator
// nothing about whether their installation is broken.
func TestSyncNotPermitted_SaysWhatIsMissingWhoGrantsItAndWhatStillWorks(t *testing.T) {
	mustContain := map[string]string{
		"names the permission":              "sync an application",
		"says installing does not grant it": "Installing Sharko does not grant this permission",
		"says who grants it":                "ArgoCD administrator",
		"says it is scoped":                 "scoped to Sharko's own project",
		"says what still works":             "Git still holds the desired state and ArgoCD still applies it",
		"points at the page":                "operator security page",
	}
	for what, phrase := range mustContain {
		if !strings.Contains(syncNotPermitted, phrase) {
			t.Errorf("the refusal no longer %s — %q is gone from it.\nfull text: %s", what, phrase, syncNotPermitted)
		}
	}
	// The wording this replaced. An operator told "gateway error" goes and
	// restarts ArgoCD, which is the wrong thing and the reason this is banned
	// by exact text rather than by intent.
	for _, banned := range []string{"gateway", "Bad Gateway", "failed to sync application"} {
		if strings.Contains(strings.ToLower(syncNotPermitted), strings.ToLower(banned)) {
			t.Errorf("the refusal says %q. That describes a broken ArgoCD, and ArgoCD is not broken when it "+
				"refuses something it was never asked to allow.", banned)
		}
	}
}
