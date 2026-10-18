package api

// connection_redirect_to_http_sentence_test.go — LO-7 R2 (epic
// video-leftovers, 2026-10-18).
//
// An https ArgoCD address that tries to send Sharko on to plain http used to
// give the connection test the general "Sharko can't reach ArgoCD" sentence
// as its main message (the right words were only in the cause). It now gets
// one whole sentence of its own, opening with the same words as the list
// sentence for this case. Every expected sentence here is a literal.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

const argocdRedirectToHTTPWant = "ArgoCD tried to send Sharko from https to plain http. Sharko does not follow that, so the ArgoCD token is never sent unencrypted. Check the ArgoCD address and any proxy in front of ArgoCD, then test again."

func TestConnectionFailure_ArgoCDRedirectToHTTPSentence(t *testing.T) {
	body := connectionErrorFields(connectionKindArgoCD, redirectToHTTPListErr(t))

	if body["message"] != argocdRedirectToHTTPWant {
		t.Errorf("message:\n got: %q\nwant: %q", body["message"], argocdRedirectToHTTPWant)
	}
	if body["message"] == oldArgoCDGeneric {
		t.Error("the general can't-reach sentence came back")
	}
	if body["message_id"] != "connectionFailureArgoCDRedirectToHTTP" {
		t.Errorf("message_id = %q, want connectionFailureArgoCDRedirectToHTTP", body["message_id"])
	}
	if entry, ok := connectionFailureEntry("connectionFailureArgoCDRedirectToHTTP"); !ok {
		t.Error("the message_id is in no catalog, so the browser has never been given it")
	} else if entry.Sentence != argocdRedirectToHTTPWant {
		t.Errorf("catalog sentence:\n got: %q\nwant: %q", entry.Sentence, argocdRedirectToHTTPWant)
	}
	// It opens with the same words as the list sentence.
	const opening = "ArgoCD tried to send Sharko from https to plain http"
	if got := argoListFailureSentence(argoReadClusters, redirectToHTTPListErr(t)); !strings.HasPrefix(got, opening) {
		t.Errorf("the list sentence does not open like the connection test's: %q", got)
	}
	msg, _ := body["message"].(string)
	for _, leak := range []string{"plain-http-target", "127.0.0.1", "argo-token"} {
		if strings.Contains(msg, leak) {
			t.Errorf("%q is in the message: %q", leak, msg)
		}
	}
}

// TestTestCredentials_ArgoCDRedirectToHTTPThroughTheHandler drives the
// wizard's own endpoint, POST /connections/test-credentials, end to end.
func TestTestCredentials_ArgoCDRedirectToHTTPThroughTheHandler(t *testing.T) {
	httpsURL := newHTTPSToHTTPServer(t)
	gitHost := newRecordingHost(t)
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  gitHost.URL + "/acme/platform",
			Token:    "git-token",
		},
		// Insecure only so the test's self-signed certificate is accepted;
		// the redirect is what is under test.
		Argocd: models.ArgocdConfig{ServerURL: httpsURL, Token: "argo-token", Namespace: "argocd", Insecure: true},
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rw.Code, rw.Body.String())
	}
	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v; body = %s", err, rw.Body.String())
	}
	if got := body["argocd"]["message"]; got != argocdRedirectToHTTPWant {
		t.Errorf("argocd.message:\n got: %v\nwant: %q", got, argocdRedirectToHTTPWant)
	}
	if got := body["argocd"]["message_id"]; got != "connectionFailureArgoCDRedirectToHTTP" {
		t.Errorf("argocd.message_id = %v", got)
	}
	if strings.Contains(rw.Body.String(), "argo-token") {
		t.Error("the ArgoCD token is in the response body")
	}
}
