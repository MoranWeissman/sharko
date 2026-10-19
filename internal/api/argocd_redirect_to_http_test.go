package api

// argocd_redirect_to_http_test.go — L15 (epic video-leftovers, 2026-10-18).
//
// When an https ArgoCD address sends Sharko on to plain http, Sharko refuses
// to follow, so the token never travels unencrypted. The list paths (GET
// /clusters/available, the cluster write paths and the doctor) used to show
// the general "could not reach the ArgoCD API" sentence for that, which is
// wrong: Sharko did reach ArgoCD. They now say what happened, with the same
// opening words as the connection test's sentence for this case. Every
// expected sentence here is a literal.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
)

const oldListClustersGeneric = "Sharko could not reach the ArgoCD API to list clusters. Check that the ArgoCD server address on the connection is correct and that ArgoCD is reachable from Sharko, then try again."

// newHTTPSToHTTPServer starts an https ArgoCD stand-in that sends every
// request on to plain http on a made-up host. It returns the https address.
func newHTTPSToHTTPServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://plain-http-target.example"+r.URL.RequestURI(), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func redirectToHTTPListErr(t *testing.T) error {
	t.Helper()
	// insecure=true only so the test's own self-signed certificate is
	// accepted; the redirect is what is under test.
	_, err := argocd.NewClient(newHTTPSToHTTPServer(t), "argo-token", true).ListClusters(context.Background())
	if !errors.Is(err, argocd.ErrArgocdRedirectToHTTP) {
		t.Fatalf("setup: want ErrArgocdRedirectToHTTP, got %v", err)
	}
	return err
}

func TestDiscoverClusters_ArgoCDRedirectToHTTPSentence(t *testing.T) {
	listErr := redirectToHTTPListErr(t)

	srv := newDoctorTestServer(t)
	installCredProvider(srv, stubClusterCredProvider{}, nil, nil)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{listClustersErr: listErr})

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	const want = "ArgoCD tried to send Sharko from https to plain http, so Sharko did not list clusters. Sharko does not follow that, so the ArgoCD token is never sent unencrypted. Check the ArgoCD address on the connection in Settings and any proxy in front of ArgoCD, then try again."
	if body["error"] != want {
		t.Errorf("error:\n got: %q\nwant: %q", body["error"], want)
	}
	if body["error"] == oldListClustersGeneric {
		t.Error("the general could-not-reach sentence came back")
	}
	for _, leak := range []string{"plain-http-target", "127.0.0.1", "argo-token"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("%q leaked into the answer: %s", leak, w.Body.String())
		}
	}
}

// The applications reads use the same classifier, with the same words.
func TestArgoListFailureSentence_RedirectToHTTPApplications(t *testing.T) {
	got := argoListFailureSentence(argoReadApplications, redirectToHTTPListErr(t))
	const want = "ArgoCD tried to send Sharko from https to plain http, so Sharko did not list applications. Sharko does not follow that, so the ArgoCD token is never sent unencrypted. Check the ArgoCD address on the connection in Settings and any proxy in front of ArgoCD, then try again."
	if got != want {
		t.Errorf("sentence:\n got: %q\nwant: %q", got, want)
	}
}

// The list sentence opens with the same words as the sentence the connection
// test gives for this case (the cause it shows), so the two places agree.
func TestArgoListFailureSentence_RedirectToHTTPMatchesTheConnectionTest(t *testing.T) {
	err := redirectToHTTPListErr(t)
	body := connectionErrorFields(connectionKindArgoCD, err)
	cause, _ := body["cause"].(string)
	const opening = "ArgoCD tried to send Sharko from https to plain http"
	if !strings.HasPrefix(cause, opening) {
		t.Fatalf("the connection test's sentence for this case changed: %q", cause)
	}
	if got := argoListFailureSentence(argoReadClusters, err); !strings.HasPrefix(got, opening) {
		t.Errorf("the list sentence does not open like the connection test's: %q", got)
	}
}
