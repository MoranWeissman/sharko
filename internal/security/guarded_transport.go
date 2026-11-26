package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"syscall"
	"time"
)

// ErrBlockedAtConnect is what a guarded call fails with when the address it
// was about to connect to is one Sharko does not call for this kind of
// request (loopback, private network, link-local and the rest of the
// classifyBlocked list).
var ErrBlockedAtConnect = errors.New("this address is not allowed")

// maxGuardedRedirects is how many redirects a guarded call follows.
const maxGuardedRedirects = 5

// GuardedClient is the HTTP client for a call to an address that a caller
// with a low role typed in (the chart repository checks a viewer can run).
//
// ValidateExternalURL on the typed address is not enough on its own, for two
// reasons this client closes:
//
//   - A redirect. The typed address passes the check, answers "go to
//     http://10.0.0.5/...", and a plain client follows it. Here every hop is
//     put through ValidateExternalURL again.
//   - A name that changes its answer. The name is looked up once for the
//     check and again for the call. Here the real IP address is checked at
//     the moment the connection is opened, after the last lookup.
func GuardedClient() *http.Client {
	return &http.Client{
		Transport:     guardedTransport(blockedDialAddress),
		CheckRedirect: checkGuardedRedirect,
	}
}

// checkGuardedRedirect runs the same check on every address a redirect names
// that the typed address went through.
func checkGuardedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxGuardedRedirects {
		return errors.New("too many redirects")
	}
	if err := ValidateExternalURL(req.URL.String()); err != nil {
		return ErrBlockedAtConnect
	}
	return nil
}

// blockedDialAddress is the check run just before a connection is opened.
// address is the real "ip:port", after any name was resolved. An address that
// cannot be read is refused.
func blockedDialAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrBlockedAtConnect
	}
	if classifyBlocked(net.ParseIP(host)) != "" {
		return ErrBlockedAtConnect
	}
	return nil
}

// guardedTransport is the default transport with one addition: check runs on
// the real address just before each connection is opened.
//
// One connection is let through unchecked: the one to the outbound proxy
// Sharko itself was configured with (HTTP_PROXY / HTTPS_PROXY). A company
// proxy normally sits on a private address, and it is the operator's own
// setting, never something a caller typed. Behind such a proxy the far end is
// connected to by the proxy, so the connect-time check cannot see it; the
// per-hop ValidateExternalURL check still runs.
func guardedTransport(check func(address string) error) http.RoundTripper {
	return guardedTransportWithProxy(check, http.ProxyFromEnvironment)
}

// guardedTransportWithProxy is guardedTransport with the "which proxy" lookup
// passed in, so a test can name a proxy without changing the environment.
func guardedTransportWithProxy(check func(address string) error, proxyFor func(*http.Request) (*url.URL, error)) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()

	var proxies sync.Map // "host:port" of each proxy the environment named
	t.Proxy = func(req *http.Request) (*url.URL, error) {
		if proxyFor == nil {
			return nil, nil
		}
		u, err := proxyFor(req)
		if err == nil && u != nil {
			proxies.Store(proxyDialAddress(u), struct{}{})
		}
		return u, err
	}

	guarded := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return check(address)
		},
	}
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if _, isProxy := proxies.Load(addr); isProxy {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
	return t
}

// proxyDialAddress is the "host:port" the transport dials for a proxy URL.
func proxyDialAddress(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
