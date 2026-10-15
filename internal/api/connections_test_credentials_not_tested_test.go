package api

// VF-1 side finding: when the Git repository address in a test-credentials
// request could not be read, the test stopped before it reached ArgoCD — and
// the ArgoCD result came back "ok". These tests pin that it now says the
// ArgoCD test did not run, and that ArgoCD is really not contacted.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/models"
)

func TestTestCredentials_BadGitAddressLeavesArgoCDNotTested(t *testing.T) {
	argo := newRecordingHost(t)
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderAzureDevOps,
			RepoURL:  "https://dev.azure.com/org/project/repo", // missing /_git/
			Token:    "git-token",
		},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "argo-token", Namespace: "argocd"},
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v; body = %s", err, rw.Body.String())
	}
	if got := body["argocd"]["status"]; got != "not_tested" {
		t.Errorf(`argocd.status = %v, want "not_tested" (it used to be "ok" though ArgoCD was never asked)`, got)
	}
	const wantMsg = "ArgoCD was not tested, because Sharko could not read the Git repository address. Fix the Git address and test again."
	if got := body["argocd"]["message"]; got != wantMsg {
		t.Errorf("argocd.message:\n got: %v\nwant: %q", got, wantMsg)
	}
	if got := body["git"]["status"]; got != "error" {
		t.Errorf(`git.status = %v, want "error"`, got)
	}
	if argo.hitCount() != 0 {
		t.Errorf("ArgoCD was contacted %d time(s) although the test stopped at the Git address", argo.hitCount())
	}
}

// A good Git address still lets the ArgoCD test run and report "ok" — the
// not-tested state is only for a test that really did not run.
func TestTestCredentials_GoodGitAddressStillTestsArgoCD(t *testing.T) {
	argo := newRecordingHost(t)
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  argo.URL + "/acme/platform",
			Token:    "git-token",
		},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "argo-token", Namespace: "argocd"},
	})
	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v; body = %s", err, rw.Body.String())
	}
	if got := body["argocd"]["status"]; got != "ok" {
		t.Errorf(`argocd.status = %v, want "ok"; body = %s`, got, rw.Body.String())
	}
	if argo.hitCount() == 0 {
		t.Errorf("ArgoCD was never contacted")
	}
}

// VF-1, video finding V2: an http ArgoCD address that redirects to https.
// The connection-test body must not claim a certificate problem, and its
// cause must name the https address to use instead.
func TestConnectionErrorFields_ArgoCDNeedsHTTPSNamesTheAddress(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsSrv.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the ArgoCD token was sent over plain http")
		}
		http.Redirect(w, r, tlsSrv.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer plain.Close()

	err := argocd.NewClient(plain.URL, "argo-token", false).TestConnection(context.Background())
	if !errors.Is(err, argocd.ErrArgocdNeedsHTTPS) {
		t.Fatalf("want ErrArgocdNeedsHTTPS, got %v", err)
	}
	body := connectionErrorFields(connectionKindArgoCD, err)
	cause, _ := body["cause"].(string)
	if !strings.Contains(cause, tlsSrv.URL) {
		t.Errorf("cause must name the https address %q; got %q", tlsSrv.URL, cause)
	}
	if msg, _ := body["message"].(string); strings.Contains(msg, "certificate") {
		t.Errorf("an http→https redirect is not a certificate problem; message = %q", msg)
	}
}
