package argocd

// needs_https_path_test.go — LO-7 R3 (epic video-leftovers, 2026-10-18).
//
// ArgoCD can run under a path (server.rootpath). Before, the "needs https"
// address Sharko named was "https://" plus the host only, so an operator who
// typed http://argocd.example.com/argocd was told to change it to
// https://argocd.example.com — and following that dropped /argocd. The
// address now keeps the path of the address Sharko was given.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSameHostHTTPSRedirect starts a plain http server that sends every
// request on to https on the same host and port. It fails the test if it is
// ever sent the token.
func newSameHostHTTPSRedirect(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the ArgoCD token was sent over plain http: %s %s", r.Method, r.URL.Path)
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPSAddress_KeepsThePathOfTheAddress(t *testing.T) {
	srv := newSameHostHTTPSRedirect(t)
	host := strings.TrimPrefix(srv.URL, "http://")

	cases := []struct {
		name    string
		address string
		want    string
	}{
		{"no path", srv.URL, "https://" + host},
		{"under a path", srv.URL + "/argocd", "https://" + host + "/argocd"},
		{"under a path, trailing slash", srv.URL + "/argocd/", "https://" + host + "/argocd"},
		{"under two parts", srv.URL + "/tools/argo-cd", "https://" + host + "/tools/argo-cd"},
		{"user part, query and fragment are dropped", "http://someone:hunter2@" + host + "/argocd?token=abc#frag",
			"https://" + host + "/argocd"},
		// A path that is not a plain path: no address is named at all.
		{"path with an encoded character", srv.URL + "/argo%20cd", ""},
		{"path with a dot-dot part", srv.URL + "/a/../argocd", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(tc.address, "argo-token", false).ListClusters(context.Background())
			if !errors.Is(err, ErrArgocdNeedsHTTPS) {
				t.Fatalf("want ErrArgocdNeedsHTTPS, got %v", err)
			}
			if got := HTTPSAddress(err); got != tc.want {
				t.Errorf("https address = %q, want %q", got, tc.want)
			}
			for _, leak := range []string{"hunter2", "someone", "token=abc", "frag"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%q is in the error: %q", leak, err.Error())
				}
			}
		})
	}
}

// The write paths name the same address as the read path.
func TestHTTPSAddress_KeepsThePathOnWrites(t *testing.T) {
	srv := newSameHostHTTPSRedirect(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	c := NewClient(srv.URL+"/argocd", "argo-token", false)
	want := "https://" + host + "/argocd"

	_, postErr := c.doPost(context.Background(), "/api/v1/clusters", []byte(`{}`))
	_, putErr := c.doPut(context.Background(), "/api/v1/clusters/x", []byte(`{}`))
	_, delErr := c.doDelete(context.Background(), "/api/v1/clusters/x")
	for name, err := range map[string]error{"POST": postErr, "PUT": putErr, "DELETE": delErr} {
		if got := HTTPSAddress(err); got != want {
			t.Errorf("%s: https address = %q, want %q (error %v)", name, got, want, err)
		}
	}
}
