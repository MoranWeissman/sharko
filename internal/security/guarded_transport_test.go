package security

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// The hole: the typed address passes the check, then answers with a redirect
// to an inside address, and a plain client follows it.
func TestGuardedClient_RedirectToAnInsideAddressIsNotFollowed(t *testing.T) {
	var innerCalls atomic.Int32
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalls.Add(1)
		_, _ = w.Write([]byte("entries:\n  inner-name: []\n"))
	}))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL+"/any/inner/path", http.StatusFound)
	}))
	defer outer.Close()

	// Proof the two servers do what the hole needs: a plain client ends up
	// reading the inner answer.
	resp, err := http.Get(outer.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "inner-name") || innerCalls.Load() != 1 {
		t.Fatalf("test setup: a plain client should follow the redirect, got %q", body)
	}
	innerCalls.Store(0)

	// Part 1, the connect-time check on its own. The outer server stands in
	// for a public address (it is let through), everything else is refused
	// when the connection is opened. No redirect check is set, so only the
	// connect-time check can stop the second hop.
	outerAddr := strings.TrimPrefix(outer.URL, "http://")
	onlyOuter := &http.Client{Transport: guardedTransport(func(address string) error {
		if address == outerAddr {
			return nil
		}
		return blockedDialAddress(address)
	})}
	if resp, err = onlyOuter.Get(outer.URL); err == nil {
		resp.Body.Close()
		t.Error("the redirect to an inside address was followed")
	} else if !errors.Is(err, ErrBlockedAtConnect) {
		t.Errorf("error = %v, want ErrBlockedAtConnect", err)
	}
	if innerCalls.Load() != 0 {
		t.Error("the inside address was connected to")
	}

	// Part 2, the check on each redirect on its own: a transport with no
	// connect-time check, and the redirect is still refused.
	onlyRedirectCheck := &http.Client{CheckRedirect: checkGuardedRedirect}
	if resp, err = onlyRedirectCheck.Get(outer.URL); err == nil {
		resp.Body.Close()
		t.Error("the redirect to an inside address was followed")
	} else if !errors.Is(err, ErrBlockedAtConnect) {
		t.Errorf("error = %v, want ErrBlockedAtConnect", err)
	}
	if innerCalls.Load() != 0 {
		t.Error("the inside address was connected to")
	}

	// Part 3, the client the routes get has both.
	c := GuardedClient()
	if c.CheckRedirect == nil {
		t.Error("GuardedClient has no redirect check")
	}
	if resp, err = c.Get(inner.URL); err == nil {
		resp.Body.Close()
		t.Error("GuardedClient connected to a loopback address")
	} else if !errors.Is(err, ErrBlockedAtConnect) {
		t.Errorf("error = %v, want ErrBlockedAtConnect", err)
	}
	if innerCalls.Load() != 0 {
		t.Error("the inside address was connected to")
	}
}

func TestBlockedDialAddress(t *testing.T) {
	blocked := []string{"127.0.0.1:80", "10.0.0.5:443", "192.168.1.1:80", "172.16.0.1:80", "169.254.169.254:80", "[::1]:80", "[fd00:ec2::254]:80", "0.0.0.0:80", "not-an-address", "example.com:80"}
	for _, a := range blocked {
		if blockedDialAddress(a) == nil {
			t.Errorf("%s was let through", a)
		}
	}
	for _, a := range []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443"} {
		if err := blockedDialAddress(a); err != nil {
			t.Errorf("%s was refused: %v", a, err)
		}
	}
}

// The operator's own outbound proxy sits on a private address in most
// companies. The connection to it is let through; nothing else is.
func TestGuardedTransport_OwnProxyIsNotRefused(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = w.Write([]byte("through the proxy: " + r.URL.String()))
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)

	c := &http.Client{Transport: guardedTransportWithProxy(blockedDialAddress, http.ProxyURL(proxyURL))}
	resp, err := c.Get("http://charts.example.com/index.yaml")
	if err != nil {
		t.Fatalf("a call through the operator's own proxy was refused: %v", err)
	}
	resp.Body.Close()
	if proxied.Load() != 1 {
		t.Error("the call did not go through the proxy")
	}

	// With no proxy the same private address is refused.
	direct := &http.Client{Transport: guardedTransportWithProxy(blockedDialAddress, nil)}
	if resp, err = direct.Get(proxy.URL); err == nil {
		resp.Body.Close()
		t.Error("a private address was connected to directly")
	}
}
