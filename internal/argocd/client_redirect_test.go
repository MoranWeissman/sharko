package argocd

// client_redirect_test.go — VF-1, video finding V2.
//
// A default ArgoCD install (argo-helm chart 10.9.6) keeps TLS on with a
// certificate it signed itself, and answers plain http on the same port with
// a 307 to https. Before this fix:
//
//   - discovery followed that 307, failed the certificate check under the
//     http label, and fell back to an http:// address that only redirects;
//   - the real client followed the 307 too — after it had already sent the
//     ArgoCD token over plain http on the first hop.
//
// These tests use a pair of httptest servers to stand in for that: a plain
// http server whose only job is to send a 307 to a TLS server (self-signed,
// like ArgoCD's own certificate).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// authRecorder records every request a test server saw, and whether it
// carried an Authorization header.
type authRecorder struct {
	mu       sync.Mutex
	requests int
	withAuth int
}

func (a *authRecorder) see(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests++
	if r.Header.Get("Authorization") != "" {
		a.withAuth++
	}
}

func (a *authRecorder) counts() (requests, withAuth int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requests, a.withAuth
}

// redirectPair starts a TLS "ArgoCD" and a plain http server in front of it
// that answers every request with a 307 to the same path on the TLS server —
// what ArgoCD itself does on its http port when TLS is on.
func redirectPair(t *testing.T) (plain, tlsSrv *httptest.Server, plainSeen, tlsSeen *authRecorder) {
	t.Helper()
	plainSeen, tlsSeen = &authRecorder{}, &authRecorder{}
	tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tlsSeen.see(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(tlsSrv.Close)
	plain = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainSeen.see(r)
		http.Redirect(w, r, tlsSrv.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	t.Cleanup(plain.Close)
	return plain, tlsSrv, plainSeen, tlsSeen
}

// Discovery: an http address that answers with a 307 to https on the same
// host is reported as the https address — with the certificate check ON, and
// even though the certificate does not verify. That is the real address; the
// client explains the certificate problem next.
func TestProbeArgoCD_HTTPRedirectToHTTPSReportsHTTPS(t *testing.T) {
	plain, tlsSrv, plainSeen, tlsSeen := redirectPair(t)

	got := probeArgoCD(plain.URL, false)
	if got != tlsSrv.URL {
		t.Fatalf("discovery of an http address that redirects to https:\n got: %q\nwant: %q", got, tlsSrv.URL)
	}
	if !strings.HasPrefix(got, "https://") {
		t.Fatalf("discovery must report an https address, got %q", got)
	}
	// The probe does not follow the redirect, and never sends a credential.
	if n, _ := tlsSeen.counts(); n != 0 {
		t.Errorf("the discovery probe followed the redirect to the TLS server (%d requests)", n)
	}
	if _, withAuth := plainSeen.counts(); withAuth != 0 {
		t.Errorf("the discovery probe sent an Authorization header")
	}
}

// Discovery: a redirect to https on a DIFFERENT host is not taken as "the
// address is https" — that is somebody else's server.
func TestProbeArgoCD_RedirectToOtherHostIsNotDiscovered(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example"+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer plain.Close()
	if got := probeArgoCD(plain.URL, false); got != "" {
		t.Fatalf("a redirect to another host must not be discovered, got %q", got)
	}
}

// The real client given the http address: it must never send the token over
// plain http, must not quietly carry on over https, and must say which https
// address to use instead.
func TestClient_HTTPRedirectToHTTPS_NoTokenOverHTTP(t *testing.T) {
	for _, insecure := range []bool{false, true} {
		name := "verify on"
		if insecure {
			name = "skip TLS ticked"
		}
		t.Run(name, func(t *testing.T) {
			plain, tlsSrv, plainSeen, tlsSeen := redirectPair(t)

			c := NewClient(plain.URL, "test-token", insecure)
			err := c.TestConnection(context.Background())
			if err == nil {
				t.Fatal("expected an error for an http address that redirects to https, got nil")
			}
			if !errors.Is(err, ErrArgocdNeedsHTTPS) {
				t.Fatalf("expected errors.Is(err, ErrArgocdNeedsHTTPS), got: %v", err)
			}
			if !strings.Contains(err.Error(), tlsSrv.URL) {
				t.Errorf("the error must name the https address %q; got: %v", tlsSrv.URL, err)
			}
			if _, withAuth := plainSeen.counts(); withAuth != 0 {
				t.Errorf("the ArgoCD token was sent over plain http %d time(s)", withAuth)
			}
			if n, _ := tlsSeen.counts(); n != 0 {
				t.Errorf("the client quietly followed the redirect to https (%d requests)", n)
			}
		})
	}
}

// The real client given the https address that discovery now reports: with
// Skip TLS ticked it connects, and nothing ever touches plain http.
func TestClient_DiscoveredHTTPSWithSkipTLSConnects(t *testing.T) {
	plain, tlsSrv, plainSeen, tlsSeen := redirectPair(t)

	found := probeArgoCD(plain.URL, false)
	c := NewClient(found, "test-token", true)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("the discovered https address with Skip TLS ticked must connect, got: %v", err)
	}
	if _, withAuth := tlsSeen.counts(); withAuth == 0 {
		t.Errorf("the TLS server never saw the authenticated request")
	}
	if _, withAuth := plainSeen.counts(); withAuth != 0 {
		t.Errorf("the ArgoCD token was sent over plain http")
	}
	_ = tlsSrv
}

// A redirect from https down to plain http must be refused — following it
// would send the token unencrypted.
func TestClient_RefusesRedirectFromHTTPSToHTTP(t *testing.T) {
	plainSeen := &authRecorder{}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainSeen.see(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer tlsSrv.Close()

	c := NewClient(tlsSrv.URL, "test-token", true)
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected the https → http redirect to be refused, got nil")
	}
	if !errors.Is(err, ErrArgocdRedirectToHTTP) {
		t.Fatalf("expected errors.Is(err, ErrArgocdRedirectToHTTP), got: %v", err)
	}
	if n, _ := plainSeen.counts(); n != 0 {
		t.Errorf("the client followed the redirect down to plain http (%d requests)", n)
	}
}

// A plain http ArgoCD that does NOT redirect (server.insecure=true) keeps
// working, and the credential-free check is asked once per path per client,
// not once per call.
func TestClient_PlainHTTPArgoCDCheckedOnce(t *testing.T) {
	seen := &authRecorder{}
	var versionChecks int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.see(r)
		if r.Header.Get(plainHTTPCheckHeader) != "" {
			mu.Lock()
			versionChecks++
			mu.Unlock()
			if r.Header.Get("Authorization") != "" {
				t.Errorf("the check request carried an Authorization header")
			}
			if r.Method != http.MethodGet {
				t.Errorf("the check request was a %s, want GET", r.Method)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token", false)
	for i := 0; i < 3; i++ {
		if err := c.TestConnection(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if versionChecks != 1 {
		t.Errorf("the credential-free check ran %d times for one path and one client, want 1", versionChecks)
	}
}

// newPlainArgoTestServer starts a plain http test server that answers the
// client's credential-free check requests itself (an ArgoCD with
// server.insecure=true answers it with 200), and hands every other request to
// h. Tests that assert the method or path of EVERY request they see use this,
// so the check does not trip their assertions.
func newPlainArgoTestServer(h http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(plainHTTPCheckHeader) != "" && r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Version":"test"}`))
			return
		}
		h.ServeHTTP(w, r)
	}))
}
