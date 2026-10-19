package api

// clusters_register_kubeconfig_refused_test.go — LO-13 K1 and K2. A
// kubeconfig that points at a file or runs a program is a caller error, so
// register answers 400 with the refusal sentence: for a pasted kubeconfig
// (it used to answer 502) and for one stored in a secrets backend (it used to
// register quietly with no credentials).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// The pod's own ServiceAccount token: the path the tokenFile bug was about.
const podTokenFileKubeconfig = "apiVersion: v1\nkind: Config\ncurrent-context: c\n" +
	"clusters:\n- name: k\n  cluster:\n    server: https://10.0.0.9:6443\n" +
	"contexts:\n- name: c\n  context:\n    cluster: k\n    user: u\n" +
	"users:\n- name: u\n  user:\n    tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token\n"

// Written out in full on purpose, so a change to the words a user sees fails
// here.
const wantTokenFileRefusalSentence = "unsupported kubeconfig auth method: kubeconfig reads its bearer token from a file path (tokenFile); Sharko does not read a kubeconfig's files from its own disk, so put the token itself in the kubeconfig (token) or generate one with `kubectl create token <serviceaccount> --duration=24h`"

func postRegister(t *testing.T, srv *Server, fields map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(fields)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewRouter(srv, nil).ServeHTTP(w, req)
	return w
}

func TestRegisterCluster_PastedKubeconfigRefused_Answers400(t *testing.T) {
	srv := newRegisterBackendTestServer(t, nil)
	enableLegacyInlineForTest(t, srv)

	w := postRegister(t, srv, map[string]interface{}{
		"name":         "prod",
		"creds_source": "inline-kubeconfig",
		"kubeconfig":   podTokenFileKubeconfig,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	want := `parsing inline kubeconfig for cluster "prod": ` + wantTokenFileRefusalSentence
	if got := decodeError(t, w); got != want {
		t.Errorf("error:\n got: %q\nwant: %q", got, want)
	}
}

func TestRegisterCluster_StoredKubeconfigRefused_Answers400(t *testing.T) {
	backend := providers.NewAWSSecretsManagerProviderForTest(podTokenFileKubeconfig,
		func(context.Context, string, string, string) (string, error) {
			return "", errors.New("a raw kubeconfig must never mint a token")
		})
	srv := newRegisterBackendTestServer(t, backend)

	w := postRegister(t, srv, map[string]interface{}{
		"name":         "prod",
		"creds_source": "secret-kubeconfig",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	want := `the kubeconfig stored for cluster "prod" cannot be used: ` + wantTokenFileRefusalSentence
	if got := decodeError(t, w); got != want {
		t.Errorf("error:\n got: %q\nwant: %q", got, want)
	}
}
