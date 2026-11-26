package ai

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// address.go: which addresses Sharko will call for an AI provider.
//
// THE RULE (2026-11-26, pre-release fix round, story C):
//
//  1. Only an admin can make Sharko call an AI address at all. Saving the AI
//     settings and both test calls (POST /ai/test-config, POST /ai/test) are
//     admin only.
//  2. An address in a private network or on the local machine IS allowed.
//     That is where a self-hosted provider lives: the Ollama the chart can
//     deploy sits on a cluster-internal address, and a laptop setup uses
//     localhost:11434. Blocking those would break the feature.
//  3. Link-local addresses (169.254.0.0/16 and fe80::/10) are never called,
//     for anybody. That range is where cloud machines serve their own
//     settings and short-lived cloud keys, and no AI provider lives there.
//     The same goes for the "no address" address and multicast. This is
//     checked on the typed address AND again at the moment of connecting, so
//     a name that resolves there, or a redirect that points there, is
//     stopped too.
//  4. An address may not carry a user name or password. The key goes in the
//     key field. (The Helm chart already refused this; now the API agrees.)
//  5. Whatever an address answers is never passed on word for word. See
//     provider_errors.go.

// Sentences for a refused address. They are shown to the admin as they are.
const (
	AddressNeedsHTTPSentence      = "The address must start with http:// or https://."
	AddressHasLoginSentence       = "Take the user name and password out of the address. If the provider needs a key, put it in the API key field."
	AddressNotAllowedSentence     = "Sharko does not call this address. Addresses that start with 169.254 (and fe80:) belong to the machine's own network setup, and no AI provider lives there."
	AddressCouldNotBeReadSentence = "Sharko could not read this address. Write it like http://ollama.example.com:11434."
)

// errBlockedAddress is what the connection attempt fails with when the
// address it is about to connect to is one Sharko never calls.
var errBlockedAddress = errors.New("address not allowed")

// neverCalled reports whether ip is in a range Sharko never calls for AI.
func neverCalled(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// ValidateProviderAddress checks an address a person typed for a self-hosted
// provider (the Ollama URL or the custom base URL). The error text is one of
// the sentences above and is safe to show.
func ValidateProviderAddress(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New(AddressCouldNotBeReadSentence)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New(AddressNeedsHTTPSentence)
	}
	if u.User != nil {
		return errors.New(AddressHasLoginSentence)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New(AddressCouldNotBeReadSentence)
	}
	if ip := net.ParseIP(host); ip != nil && neverCalled(ip) {
		return errors.New(AddressNotAllowedSentence)
	}
	return nil
}

// guardedTransport is the transport every AI call goes through. It is the
// default transport with one addition: just before a connection is opened, the
// real IP address is checked, and a never-called address is refused. Checking
// here, and not only on the typed text, covers a host name that resolves to
// such an address and a redirect that points at one.
func guardedTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address)
		},
	}
	t.DialContext = dialer.DialContext
	return t
}

// checkDialAddress is the check run just before a connection is opened.
// address is the real "ip:port" about to be connected to, after any name was
// resolved. An address that cannot be read is refused.
func checkDialAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddress
	}
	if neverCalled(net.ParseIP(host)) {
		return errBlockedAddress
	}
	return nil
}
