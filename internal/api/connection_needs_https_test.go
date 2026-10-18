package api

// connection_needs_https_test.go — L7 (epic video-leftovers, 2026-10-18).
//
// A typed http ArgoCD address that only sends Sharko on to https used to get
// the general "Sharko can't reach ArgoCD" sentence. It now gets one whole
// sentence that names the https address to use. Every expected sentence here
// is a literal.

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

const oldArgoCDGeneric = "Sharko can't reach ArgoCD. Check the connection settings and try again."

// newHTTPToHTTPSPair starts an https ArgoCD stand-in and a plain http server
// that sends every request on to it. The http server fails the test if it is
// ever sent a token.
func newHTTPToHTTPSPair(t *testing.T) (plainURL, httpsURL string) {
	t.Helper()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(tlsSrv.Close)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the ArgoCD token was sent over plain http to %s", r.URL.Path)
		}
		http.Redirect(w, r, tlsSrv.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(plain.Close)
	return plain.URL, tlsSrv.URL
}

// TestConnectionFailure_ArgoCDNeedsHTTPSSentence: the body builder every
// connection test uses names the https address in one whole sentence.
func TestConnectionFailure_ArgoCDNeedsHTTPSSentence(t *testing.T) {
	plainURL, httpsURL := newHTTPToHTTPSPair(t)

	err := argocd.NewClient(plainURL, "argo-token", false).TestConnection(context.Background())
	if !errors.Is(err, argocd.ErrArgocdNeedsHTTPS) {
		t.Fatalf("want ErrArgocdNeedsHTTPS, got %v", err)
	}
	body := connectionErrorFields(connectionKindArgoCD, err)

	want := "ArgoCD answers only over https at this address. Change the ArgoCD address to " + httpsURL + " and test again."
	if body["message"] != want {
		t.Errorf("message:\n got: %q\nwant: %q", body["message"], want)
	}
	if body["message_id"] != "connectionFailureArgoCDNeedsHTTPS" {
		t.Errorf("message_id = %q, want connectionFailureArgoCDNeedsHTTPS", body["message_id"])
	}
	if _, ok := connectionFailureEntry("connectionFailureArgoCDNeedsHTTPS"); !ok {
		t.Error("the message_id is in no catalog, so the browser has never been given it")
	}
	if body["message"] == oldArgoCDGeneric {
		t.Error("the general can't-reach sentence came back")
	}
	if msg, _ := body["message"].(string); strings.Contains(msg, "argo-token") {
		t.Errorf("the token is in the message: %q", msg)
	}
}

// TestConnectionFailure_ArgoCDNeedsHTTPSOtherHost: when the redirect goes to
// another host, Sharko does not name that host. The sentence without the
// address comes back — the same one the catalog holds.
func TestConnectionFailure_ArgoCDNeedsHTTPSOtherHost(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the ArgoCD token was sent over plain http")
		}
		http.Redirect(w, r, "https://somewhere-else.example/api/v1/clusters", http.StatusFound)
	}))
	defer plain.Close()

	err := argocd.NewClient(plain.URL, "argo-token", false).TestConnection(context.Background())
	body := connectionErrorFields(connectionKindArgoCD, err)

	const want = "ArgoCD answers only over https at this address. Change the ArgoCD address so it starts with https:// and test again."
	if body["message"] != want {
		t.Errorf("message:\n got: %q\nwant: %q", body["message"], want)
	}
	entry, _ := connectionFailureEntry("connectionFailureArgoCDNeedsHTTPS")
	if entry.Sentence != want {
		t.Errorf("the catalog holds %q, but the server sent %q", entry.Sentence, want)
	}
	if msg, _ := body["message"].(string); strings.Contains(msg, "somewhere-else") {
		t.Errorf("the other host was named: %q", msg)
	}
}

// TestTestCredentials_ArgoCDNeedsHTTPSThroughTheHandler drives the wizard's
// own endpoint, POST /connections/test-credentials, end to end.
func TestTestCredentials_ArgoCDNeedsHTTPSThroughTheHandler(t *testing.T) {
	plainURL, httpsURL := newHTTPToHTTPSPair(t)
	gitHost := newRecordingHost(t)
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  gitHost.URL + "/acme/platform",
			Token:    "git-token",
		},
		Argocd: models.ArgocdConfig{ServerURL: plainURL, Token: "argo-token", Namespace: "argocd"},
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rw.Code, rw.Body.String())
	}
	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v; body = %s", err, rw.Body.String())
	}
	want := "ArgoCD answers only over https at this address. Change the ArgoCD address to " + httpsURL + " and test again."
	if got := body["argocd"]["message"]; got != want {
		t.Errorf("argocd.message:\n got: %v\nwant: %q", got, want)
	}
	if got := body["argocd"]["message_id"]; got != "connectionFailureArgoCDNeedsHTTPS" {
		t.Errorf("argocd.message_id = %v", got)
	}
	if strings.Contains(rw.Body.String(), "argo-token") {
		t.Error("the ArgoCD token is in the response body")
	}
}

// TestDiscoverClusters_ArgoCDSentences pins, by exact text, every sentence
// GET /clusters/available (the cluster discovery path) can send back when
// listing clusters in ArgoCD fails.
func TestDiscoverClusters_ArgoCDSentences(t *testing.T) {
	plainURL, _ := newHTTPToHTTPSPair(t)
	_, needsHTTPSErr := argocd.NewClient(plainURL, "argo-token", false).ListClusters(context.Background())
	if !errors.Is(needsHTTPSErr, argocd.ErrArgocdNeedsHTTPS) {
		t.Fatalf("setup: want ErrArgocdNeedsHTTPS, got %v", needsHTTPSErr)
	}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"permission", argocd.ErrPermissionDenied,
			"Sharko's ArgoCD account is not allowed to list clusters. Grant that account permission to list clusters in ArgoCD and try again."},
		{"token", argocd.ErrTokenInvalid,
			"ArgoCD refused Sharko's token while listing clusters. The token may be expired or wrong — replace it on the ArgoCD connection in Settings and try again."},
		{"certificate", &argocd.UnreachableError{Verb: "GET", Endpoint: "/api/v1/clusters", Code: argocd.UnreachableTLSUntrusted},
			"Sharko reached ArgoCD but could not verify its certificate, so it did not list clusters. Add the certificate authority to the ArgoCD connection in Settings and try again."},
		{"no answer", errors.New("dial tcp 10.0.0.9:443: connect: connection refused"),
			"Sharko could not reach the ArgoCD API to list clusters. Check that the ArgoCD server address on the connection is correct and that ArgoCD is reachable from Sharko, then try again."},
		{"http address that only sends on to https", needsHTTPSErr,
			"Sharko could not reach the ArgoCD API to list clusters. Check that the ArgoCD server address on the connection is correct and that ArgoCD is reachable from Sharko, then try again."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newDoctorTestServer(t)
			installCredProvider(srv, stubClusterCredProvider{}, nil, nil)
			srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{listClustersErr: tc.err})

			req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
			w := httptest.NewRecorder()
			srv.handleDiscoverClusters(w, req)

			if w.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", w.Code)
			}
			var body map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body["error"] != tc.want {
				t.Errorf("error:\n got: %q\nwant: %q", body["error"], tc.want)
			}
			if strings.Contains(w.Body.String(), "10.0.0.9") || strings.Contains(w.Body.String(), "argo-token") {
				t.Errorf("an address or token leaked: %s", w.Body.String())
			}
		})
	}
}
