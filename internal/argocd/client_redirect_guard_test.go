package argocd

// client_redirect_guard_test.go — L8 (epic video-leftovers, 2026-10-18).
//
// Before, the plain-http guard asked only GET /api/version, once per host.
// A proxy that sends only SOME paths on to https passed that check, and the
// first real call to one of those paths carried the ArgoCD token over plain
// http. These tests pin the rule that replaced it:
//
//  1. once any http request to a host was sent on to https, the token is
//     never sent to that host over http again;
//  2. every other request with a token is first checked without it, on its
//     own path, unless that host and path already answered plain http.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeHop is a RoundTripper that answers from a function and records every
// request it was handed.
type fakeHop struct {
	mu     sync.Mutex
	seen   []*http.Request
	answer func(r *http.Request) (*http.Response, error)
}

func (f *fakeHop) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.seen = append(f.seen, r)
	f.mu.Unlock()
	return f.answer(r)
}

func (f *fakeHop) withToken() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.seen {
		if r.Header.Get("Authorization") != "" {
			out = append(out, r.Method+" "+r.URL.String())
		}
	}
	return out
}

func (f *fakeHop) all() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.seen...)
}

func answerWith(status int, location string) *http.Response {
	h := http.Header{}
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("{}"))}
}

// redirectPaths answers a redirect to https for the listed paths and 200 for
// every other path.
func redirectPaths(to string, paths ...string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		for _, p := range paths {
			if r.URL.Path == p {
				return answerWith(http.StatusTemporaryRedirect, to+r.URL.RequestURI()), nil
			}
		}
		return answerWith(http.StatusOK, ""), nil
	}
}

func tokenRequest(t *testing.T, method, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, strings.NewReader(`{"secret":"body"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer argo-token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func roundTrip(g *plainHTTPGuardTransport, req *http.Request) error {
	resp, err := g.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

// TestGuard_ProxyThatRedirectsOnlySomePaths is the hole itself, end to end
// through the real client: /api/version answers plain http, but the clusters
// path is sent on to https. The token must not go over http.
func TestGuard_ProxyThatRedirectsOnlySomePaths(t *testing.T) {
	plainSeen := &authRecorder{}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainSeen.see(r)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the ArgoCD token was sent over plain http: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/api/version" {
			_, _ = w.Write([]byte(`{"Version":"test"}`))
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer plain.Close()

	c := NewClient(plain.URL, "argo-token", false)
	_, err := c.ListClusters(context.Background())
	if !errors.Is(err, ErrArgocdNeedsHTTPS) {
		t.Fatalf("want ErrArgocdNeedsHTTPS, got %v", err)
	}
	if got, want := HTTPSAddress(err), "https://"+strings.TrimPrefix(plain.URL, "http://"); got != want {
		t.Errorf("https address = %q, want %q", got, want)
	}
	if _, withAuth := plainSeen.counts(); withAuth != 0 {
		t.Errorf("%d requests carried the token over plain http", withAuth)
	}
}

// TestGuard_OnceRedirectedNeverAgain: a path that answered plain http before
// is still never sent the token once another path on the same host was sent
// on to https — and the second request does not even leave Sharko.
func TestGuard_OnceRedirectedNeverAgain(t *testing.T) {
	hop := &fakeHop{answer: redirectPaths("https://argocd.example", "/api/v1/clusters")}
	g := &plainHTTPGuardTransport{base: hop}

	if err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/applications")); err != nil {
		t.Fatalf("a path that answers plain http failed: %v", err)
	}
	err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/clusters"))
	if !errors.Is(err, ErrArgocdNeedsHTTPS) {
		t.Fatalf("want ErrArgocdNeedsHTTPS for the redirected path, got %v", err)
	}
	before := len(hop.all())

	// Same host, other port, other case, and the path that was fine before.
	for _, u := range []string{
		"http://argocd.example/api/v1/applications",
		"http://ARGOCD.example:8080/api/v1/applications",
	} {
		err := roundTrip(g, tokenRequest(t, http.MethodGet, u))
		if !errors.Is(err, ErrArgocdNeedsHTTPS) {
			t.Errorf("%s: want ErrArgocdNeedsHTTPS, got %v", u, err)
		}
		if got := HTTPSAddress(err); got != "https://argocd.example" {
			t.Errorf("%s: https address = %q", u, got)
		}
	}
	if after := len(hop.all()); after != before {
		t.Errorf("%d more requests went out to a host known to be https only", after-before)
	}
	want := []string{"GET http://argocd.example/api/v1/applications"}
	if got := hop.withToken(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("requests that carried the token:\n got: %q\nwant: %q", got, want)
	}

	// Another host is not touched by this.
	if err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://other.example/api/v1/applications")); err != nil {
		t.Errorf("another host was refused too: %v", err)
	}
}

// TestGuard_RealAnswerRedirectIsRemembered: the check said plain http was
// fine, then the real answer was a redirect to https (a proxy that changed
// its mind). That one request already carried the token; it must be the last.
func TestGuard_RealAnswerRedirectIsRemembered(t *testing.T) {
	hop := &fakeHop{answer: func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			return answerWith(http.StatusFound, "https://argocd.example/api/v1/clusters"), nil
		}
		return answerWith(http.StatusOK, ""), nil
	}}
	g := &plainHTTPGuardTransport{base: hop}

	_ = roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/clusters"))
	err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/clusters"))
	if !errors.Is(err, ErrArgocdNeedsHTTPS) {
		t.Fatalf("want ErrArgocdNeedsHTTPS on the second call, got %v", err)
	}
	if n := len(hop.withToken()); n != 1 {
		t.Errorf("the token went over plain http %d times, want only the first", n)
	}
}

// TestGuard_CheckIsPerPathAndSafe: every new path gets its own check; the
// check is a GET with no token, no body, no query and no user part, even when
// the real request is a POST; and a path checked once is not checked again.
func TestGuard_CheckIsPerPathAndSafe(t *testing.T) {
	hop := &fakeHop{answer: redirectPaths("https://argocd.example")}
	g := &plainHTTPGuardTransport{base: hop}

	for _, r := range []*http.Request{
		tokenRequest(t, http.MethodPost, "http://user:pw@argocd.example/api/v1/applications/a/sync?x=1"),
		tokenRequest(t, http.MethodPost, "http://argocd.example/api/v1/applications/a/sync?x=2"),
		tokenRequest(t, http.MethodDelete, "http://argocd.example/api/v1/clusters/c"),
	} {
		if err := roundTrip(g, r); err != nil {
			t.Fatalf("%s %s: %v", r.Method, r.URL.Path, err)
		}
	}

	var checks []string
	for _, r := range hop.all() {
		if r.Header.Get(plainHTTPCheckHeader) == "" {
			continue
		}
		checks = append(checks, r.Method+" "+r.URL.String())
		if r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "" {
			t.Errorf("the check carried a header of the real request: %v", r.Header)
		}
		if r.Body != nil && r.Body != http.NoBody {
			if b, _ := io.ReadAll(r.Body); len(b) != 0 {
				t.Errorf("the check carried a body: %q", b)
			}
		}
	}
	want := []string{
		"GET http://argocd.example/api/v1/applications/a/sync",
		"GET http://argocd.example/api/v1/clusters/c",
	}
	if strings.Join(checks, ",") != strings.Join(want, ",") {
		t.Errorf("checks:\n got: %q\nwant: %q", checks, want)
	}
}

// TestGuard_NoAnswerToTheCheckSendsNoToken: before, a check that got no
// answer let the real request go ahead with the token.
func TestGuard_NoAnswerToTheCheckSendsNoToken(t *testing.T) {
	refused := errors.New("connection refused")
	hop := &fakeHop{answer: func(r *http.Request) (*http.Response, error) {
		if r.Header.Get(plainHTTPCheckHeader) != "" {
			return nil, refused
		}
		return answerWith(http.StatusOK, ""), nil
	}}
	g := &plainHTTPGuardTransport{base: hop}

	err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/clusters"))
	if !errors.Is(err, refused) {
		t.Fatalf("want the check's own error, got %v", err)
	}
	if got := hop.withToken(); len(got) != 0 {
		t.Errorf("the token was sent although the check got no answer: %q", got)
	}
}

// TestGuard_HTTPSAndNoTokenAreUntouched: no check for https, and none for a
// request with no token.
func TestGuard_HTTPSAndNoTokenAreUntouched(t *testing.T) {
	hop := &fakeHop{answer: redirectPaths("https://argocd.example")}
	g := &plainHTTPGuardTransport{base: hop}
	_ = roundTrip(g, tokenRequest(t, http.MethodGet, "https://argocd.example/api/v1/clusters"))
	plainReq, _ := http.NewRequest(http.MethodGet, "http://argocd.example/api/version", nil)
	_ = roundTrip(g, plainReq)
	if n := len(hop.all()); n != 2 {
		t.Errorf("%d requests went out, want 2 (no checks)", n)
	}
}

// TestSameHostHTTPSAddress_RefusesBadPortsAndUserParts: Sharko never names an
// address it would not use.
func TestSameHostHTTPSAddress_RefusesBadPortsAndUserParts(t *testing.T) {
	from, _ := url.Parse("http://argocd.example:8080/api/version")
	cases := map[string]string{
		"https://argocd.example/api/version":       "https://argocd.example",
		"https://argocd.example:8443/api/version":  "https://argocd.example:8443",
		"https://argocd.example:65535/x":           "https://argocd.example:65535",
		"https://argocd.example:1/x":               "https://argocd.example:1",
		"https://argocd.example:0/api/version":     "",
		"https://argocd.example:99999/api/version": "",
		"https://argocd.example:65536/api/version": "",
		"https://argocd.example:08443/api/version": "",
		"https://user@argocd.example/api/version":  "",
		"https://u:p@argocd.example/api/version":   "",
		"https://other.example/api/version":        "",
		"http://argocd.example/api/version":        "",
	}
	for loc, want := range cases {
		to, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("parse %q: %v", loc, err)
		}
		if got := sameHostHTTPSAddress(from, to); got != want {
			t.Errorf("%s: got %q, want %q", loc, got, want)
		}
	}
}

// TestGuard_BadPortIsNotNamed: the redirect still stops the token, but the
// sentence does not name an address with port 0 or 99999.
func TestGuard_BadPortIsNotNamed(t *testing.T) {
	for _, loc := range []string{"https://argocd.example:0", "https://argocd.example:99999"} {
		hop := &fakeHop{answer: redirectPaths(loc, "/api/v1/clusters")}
		g := &plainHTTPGuardTransport{base: hop}
		err := roundTrip(g, tokenRequest(t, http.MethodGet, "http://argocd.example/api/v1/clusters"))
		if !errors.Is(err, ErrArgocdNeedsHTTPS) {
			t.Errorf("%s: want ErrArgocdNeedsHTTPS, got %v", loc, err)
		}
		if got := HTTPSAddress(err); got != "" {
			t.Errorf("%s: an address with a bad port was named: %q", loc, got)
		}
		if got := hop.withToken(); len(got) != 0 {
			t.Errorf("%s: token sent: %q", loc, got)
		}
	}
}

// probeWithLocation runs discovery against a plain http server that answers
// GET /api/version with a redirect to loc (on its own host).
func probeWithLocation(t *testing.T, loc func(host string) string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", loc(r.Host))
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	return probeArgoCD(srv.URL, false)
}

// TestProbeArgoCD_CleansThePathItCopies: discovery copies the path prefix
// from the redirect (ArgoCD under server.rootpath). A prefix with "..", a
// doubled slash, a user part, or odd characters is not copied — nothing is
// discovered from that redirect at all.
func TestProbeArgoCD_CleansThePathItCopies(t *testing.T) {
	good := probeWithLocation(t, func(h string) string { return "https://" + h + "/argo/cd/api/v1/version" })
	if !strings.HasSuffix(good, "/argo/cd") || !strings.HasPrefix(good, "https://") {
		t.Errorf("a plain path prefix was not kept: %q", good)
	}
	none := probeWithLocation(t, func(h string) string { return "https://" + h + "/api/v1/version" })
	if !strings.HasPrefix(none, "https://") || strings.Count(none, "/") != 2 {
		t.Errorf("no prefix: got %q", none)
	}

	for name, loc := range map[string]func(string) string{
		"dot dot":       func(h string) string { return "https://" + h + "/a/../b/api/v1/version" },
		"leading dots":  func(h string) string { return "https://" + h + "/../api/v1/version" },
		"single dot":    func(h string) string { return "https://" + h + "/a/./api/v1/version" },
		"double slash":  func(h string) string { return "https://" + h + "/a//b/api/v1/version" },
		"user part":     func(h string) string { return "https://user:pw@" + h + "/argo/api/v1/version" },
		"at in path":    func(h string) string { return "https://" + h + "/a@evil.example/api/v1/version" },
		"encoded slash": func(h string) string { return "https://" + h + "/a%2F..%2Fb/api/v1/version" },
		"bad port":      func(h string) string { return "https://" + strings.Split(h, ":")[0] + ":99999/api/v1/version" },
		"port zero":     func(h string) string { return "https://" + strings.Split(h, ":")[0] + ":0/api/v1/version" },
	} {
		if got := probeWithLocation(t, loc); got != "" {
			t.Errorf("%s: discovery trusted the redirect and returned %q", name, got)
		}
	}
}
