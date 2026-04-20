// Package security holds cross-cutting hardening primitives shared across
// the Sharko codebase.
//
// url_guard.go — Story V121-8.2 SSRF guard.
//
// Several Sharko endpoints take a user-supplied URL and fetch it server-side
// (e.g. /catalog/validate fetches `<repo>/index.yaml`). Without a guard, an
// authenticated operator could coax the server into hitting cluster-internal
// addresses (the K8s API, ArgoCD, the cloud-provider metadata service, other
// pods on the same network) and exfiltrate state.
//
// ValidateExternalURL is a defense-in-depth check: the network policy that
// fronts a production Sharko pod should already block egress to RFC1918, but
// for self-hosted or dev installs that aren't behind such policy this guard
// is the only line of defense. It runs cheap (one DNS lookup) and is wired
// in front of every URL-fetching handler.
//
// Locked decisions (Moran, 2026-04-17):
//   - Blocked nets are baked in (RFC1918, loopback, link-local, IPv6 ULA/LL,
//     IPv4-mapped variants of the above). Operators don't override the deny
//     list; they widen it via the allowlist below.
//   - Optional `SHARKO_URL_ALLOWLIST` env var lets operators pin the guard
//     to specific external hosts (e.g. only api.scorecard.dev + their own
//     Helm repo). When set, hostnames not in the list are rejected. Empty
//     means "default deny private nets, allow everything else".
//   - DNS resolution failure is treated as a block — better to surface a
//     clear "ssrf_blocked" than a confusing fetch error mid-flight.
//   - All blocked categories return the same error class so the API layer
//     can return a single 422 / `ssrf_blocked` response without leaking
//     which net the user hit.
package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
)

// SSRFError is the typed error returned when a URL fails the SSRF guard.
// Callers type-assert against this to surface a structured 422 response
// without ambiguity (e.g. distinguishing it from a generic 400 parse error).
type SSRFError struct {
	// URL is the original input as supplied by the user. Safe to echo back.
	URL string
	// Reason is the short machine-readable reason (`scheme`, `host`,
	// `dns`, `private_net`, `loopback`, `link_local`, `unique_local`,
	// `not_in_allowlist`). For a single user-facing message use Error().
	Reason string
}

// Error implements error. The message is short, no stack trace, safe for
// the API response body.
func (e *SSRFError) Error() string {
	return fmt.Sprintf("URL %q blocked by SSRF guard: %s", e.URL, e.Reason)
}

// IsSSRFError reports whether err (or anything it wraps) is an *SSRFError.
func IsSSRFError(err error) bool {
	var s *SSRFError
	return errors.As(err, &s)
}

// allowlist caches the parsed SHARKO_URL_ALLOWLIST env var contents. Read
// once on first use; refreshing requires a pod restart (matching Sharko's
// other env-driven knobs).
var (
	allowlistOnce sync.Once
	allowlistSet  map[string]struct{}
)

func loadAllowlist() {
	raw := strings.TrimSpace(os.Getenv("SHARKO_URL_ALLOWLIST"))
	if raw == "" {
		allowlistSet = nil
		return
	}
	allowlistSet = make(map[string]struct{})
	for _, host := range strings.Split(raw, ",") {
		host = strings.TrimSpace(strings.ToLower(host))
		if host == "" {
			continue
		}
		allowlistSet[host] = struct{}{}
	}
}

// resetAllowlistForTest is a test hook so the test suite can poke the env
// var per case. Not exported beyond the package.
func resetAllowlistForTest() {
	allowlistOnce = sync.Once{}
	allowlistSet = nil
}

// ValidateExternalURL runs the SSRF guard on a user-supplied URL. Returns
// nil on success; an *SSRFError on rejection.
//
// The check stages are:
//  1. URL parses, scheme is http/https, host is non-empty.
//  2. If SHARKO_URL_ALLOWLIST is set, the hostname must be in it.
//  3. The hostname's resolved IPs must not be loopback / private / link-local.
//
// A hostname that resolves to multiple IPs is rejected if ANY one of them is
// a private net. This blocks DNS-rebinding tricks where the attacker returns
// both a public and a private answer.
func ValidateExternalURL(rawURL string) error {
	allowlistOnce.Do(loadAllowlist)

	u, err := url.Parse(rawURL)
	if err != nil {
		return &SSRFError{URL: rawURL, Reason: "malformed: " + err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &SSRFError{URL: rawURL, Reason: "scheme must be http or https (got " + u.Scheme + ")"}
	}
	host := u.Hostname()
	if host == "" {
		return &SSRFError{URL: rawURL, Reason: "host"}
	}

	if allowlistSet != nil {
		if _, ok := allowlistSet[strings.ToLower(host)]; !ok {
			return &SSRFError{URL: rawURL, Reason: "not_in_allowlist"}
		}
	}

	// Resolve. If the host is already an IP literal, LookupIP returns it
	// directly. Otherwise we hit the resolver.
	ips, err := net.LookupIP(host)
	if err != nil {
		return &SSRFError{URL: rawURL, Reason: "dns: " + err.Error()}
	}
	if len(ips) == 0 {
		return &SSRFError{URL: rawURL, Reason: "dns: no addresses"}
	}
	for _, ip := range ips {
		if reason := classifyBlocked(ip); reason != "" {
			return &SSRFError{URL: rawURL, Reason: reason}
		}
	}
	return nil
}

// classifyBlocked returns a non-empty reason if the IP belongs to a blocked
// range, or "" if the address is acceptable for outbound fetches. The set of
// blocked nets matches the RFC1918 + loopback + link-local + IPv6 ULA/LL
// list from the Story V121-8.2 acceptance criteria.
func classifyBlocked(ip net.IP) string {
	if ip == nil {
		return "invalid_ip"
	}
	if ip.IsLoopback() {
		return "loopback"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link_local"
	}
	if ip.IsPrivate() {
		// IsPrivate covers RFC1918 (10/8, 172.16/12, 192.168/16) + RFC4193
		// (fc00::/7 IPv6 ULA). Cleaner than re-implementing the CIDR list.
		return "private_net"
	}
	if ip.IsUnspecified() {
		// 0.0.0.0 / ::
		return "unspecified"
	}
	if ip.IsMulticast() {
		// Outbound HTTP to multicast makes no sense for Sharko's catalog
		// fetches. Block as a defense-in-depth guard.
		return "multicast"
	}
	return ""
}
