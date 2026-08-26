package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Trusted-proxy handling — the ONE place Sharko decides which address a
// request came from.
//
// Why this exists: rate limiting and the security audit fact are only worth
// anything if the address they key on is one the caller cannot choose. Before
// this, `X-Forwarded-For` was believed from anybody who could reach the port,
// so a caller could hand out a fresh address on every request and never hit a
// limit. The header is now read ONLY when the real TCP peer is an address the
// operator listed in SHARKO_TRUSTED_PROXIES.
//
// The rules, exactly:
//
//  1. Start from the real TCP peer (r.RemoteAddr). Never from a header.
//  2. Peer not on the list → every forwarding header (X-Forwarded-For,
//     X-Real-IP and anything else of that shape) is ignored outright. The
//     answer is the peer.
//  3. Peer on the list → walk the forwarded chain from the trusted edge
//     inward (rightmost entry first — that is the hop nearest to us) and take
//     the FIRST address that is not itself on the list.
//  4. The leftmost value is never trusted just because it is present. That
//     applies to X-Real-IP too: it is read ONLY when there is no
//     X-Forwarded-For header at all, and then the LAST instance wins, walking
//     right to left exactly like the chain. Reading it left-to-right, or
//     reading it as a fallback when the chain resolved to nothing, were both
//     ways a caller could get a fresh identity per request — see
//     firstUntrustedForwarded.
//  5. An empty setting trusts NO proxy.
//  6. Loopback, private ranges and Kubernetes ranges get no implicit trust.
//     There are no hidden defaults; the list is exactly what the operator wrote.
//  7. Anything that is not a plain IP address or CIDR range fails startup.
//  8. Both rate limiters call this and nothing else, so they can never
//     disagree about who a caller is. The one security log line that names a
//     caller (the dead /api/v1/login route in router.go) calls it too. The
//     audit LOG records no client address at all, anywhere — a 429 from
//     either limiter produces no audit entry naming the caller.
//  9. The forwarding header itself is never logged. At debug level Sharko
//     logs how many entries it looked at, after the bounds below are applied
//     — never their contents.
//
// A proxy you list is a proxy you trust to overwrite what its own client
// sent. If a listed proxy passes a client's X-Forwarded-For through
// untouched, the client is choosing its own address again — that is inherent
// in listing it, not something this code can undo.

// TrustedProxiesEnv is the environment variable that names the proxy
// addresses whose forwarding headers Sharko believes.
const TrustedProxiesEnv = "SHARKO_TRUSTED_PROXIES"

const (
	headerXForwardedFor = "X-Forwarded-For"
	headerXRealIP       = "X-Real-IP"
)

const (
	// maxForwardedChainBytes bounds how much of the forwarding header is
	// examined at all. When the header is longer than this, the TAIL is
	// kept: the entries nearest our proxy are the meaningful ones, and the
	// far left of a very long chain is exactly the part a caller can invent.
	//
	// Resolution itself only parses the chain for a caller that really did
	// arrive from a listed proxy. There is one other reader:
	// logIgnoredForwarding counts the entries an UNTRUSTED caller sent, and
	// only when the log level is debug. That work is bounded by this
	// constant — a chain of any length flattens to at most
	// maxForwardedEntries — so it is not an amplifier, but it is not free
	// either, which is why it is behind the level check.
	maxForwardedChainBytes = 8192

	// maxForwardedEntries bounds how many chain entries are examined,
	// counting from the trusted edge inward.
	maxForwardedEntries = 50

	// maxForwardedEntryLen is longer than any legal textual IP address
	// (an IPv6 address maxes out at 45 characters, plus room for a port
	// and brackets). Anything longer is not an address and is skipped.
	maxForwardedEntryLen = 64

	// maxPeerFallbackLen bounds the raw RemoteAddr string used as a
	// last-resort key when it does not parse as an address at all, so a
	// junk value can never grow a log line or a map key without limit.
	maxPeerFallbackLen = 64
)

// TrustedProxies is the parsed SHARKO_TRUSTED_PROXIES list. A nil or empty
// value trusts no proxy at all, which is the safe default: forwarding headers
// are then ignored on every request.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies turns the raw SHARKO_TRUSTED_PROXIES value into the
// trusted set. Entries are separated by commas and may be single IP addresses
// (v4 or v6) or CIDR ranges.
//
// It returns an error — which the caller turns into a failed startup — for
// anything that is not an address or range, and for any entry that would
// trust every address ("*", 0.0.0.0/0, ::/0): trusting everyone is the exact
// hole this setting closes. The error names the setting and the position of
// the bad entry, and deliberately does NOT repeat the configured value.
func ParseTrustedProxies(raw string) (*TrustedProxies, error) {
	t := &TrustedProxies{}
	for i, field := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(field)
		if entry == "" {
			// A trailing or doubled comma adds no trust. Ignore it
			// rather than refuse to start over a stray character.
			continue
		}
		if entry == "*" {
			return nil, fmt.Errorf("%s: entry %d trusts every address; list each proxy address or CIDR range explicitly", TrustedProxiesEnv, i+1)
		}

		prefix, err := parseTrustedEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: entry %d is not an IP address or CIDR range", TrustedProxiesEnv, i+1)
		}
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("%s: entry %d trusts every address; list each proxy address or CIDR range explicitly", TrustedProxiesEnv, i+1)
		}
		t.prefixes = append(t.prefixes, prefix)
	}
	return t, nil
}

// parseTrustedEntry accepts one address or one CIDR range and returns it as a
// normalized prefix (IPv4-mapped IPv6 unwrapped, zone dropped, host bits
// cleared) so that comparison later is a plain prefix test.
func parseTrustedEntry(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		p, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		addr := p.Addr()
		bits := p.Bits()
		if addr.Is4In6() {
			// ::ffff:10.0.0.0/104 means the same thing as 10.0.0.0/8.
			// Anything wider than the mapped block is not a range of
			// real addresses at all.
			if bits < 96 {
				return netip.Prefix{}, fmt.Errorf("mapped IPv4 prefix is too wide")
			}
			addr = addr.Unmap()
			bits -= 96
		}
		return netip.PrefixFrom(addr.WithZone(""), bits).Masked(), nil
	}

	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = normalizeAddr(addr)
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// Count reports how many entries are trusted. Startup logs the count so an
// operator can see the setting took effect without the values reaching a log.
func (t *TrustedProxies) Count() int {
	if t == nil {
		return 0
	}
	return len(t.prefixes)
}

// trusts reports whether addr is one of the listed proxies.
func (t *TrustedProxies) trusts(addr netip.Addr) bool {
	if t == nil || len(t.prefixes) == 0 || !addr.IsValid() {
		return false
	}
	for _, p := range t.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP resolves the address Sharko attributes this request to. It is the
// only resolver: both rate limiters and the one security log line that names a
// caller all call it, so they can never disagree about who a caller is. (The
// audit log itself records no client address anywhere — see rule 8 above.)
func (t *TrustedProxies) ClientIP(r *http.Request) string {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok {
		// The peer address did not parse. We still refuse to read a
		// header — an unreadable peer is not a trusted one.
		return truncate(strings.TrimSpace(r.RemoteAddr), maxPeerFallbackLen)
	}

	if !t.trusts(peer) {
		logIgnoredForwarding(r, peer)
		return peer.String()
	}

	if addr, found := t.firstUntrustedForwarded(r); found {
		return addr.String()
	}
	return peer.String()
}

// firstUntrustedForwarded walks the forwarded chain from the trusted edge
// inward and returns the first address that is not itself a listed proxy.
// Only called once the peer is known to be trusted.
//
// X-Forwarded-For is the only header consulted when it is present at all.
// X-Real-IP is a SECOND way to resolve the same question, and both of the
// obvious ways to wire it up were holes:
//
//   - Reading it when the chain merely produced nothing usable is
//     client-controllable. A caller behind our own proxies can send a chain
//     made only of listed addresses — which resolves to nothing — and then
//     name itself in X-Real-IP, fresh on every request. Proven: 80 login
//     attempts under a fully trusted chain, none refused. So the presence of
//     an X-Forwarded-For header ends the question, whatever it resolved to.
//   - Taking the FIRST instance contradicts rule 4. A proxy that appends its
//     own X-Real-IP rather than replacing the client's leaves the client's
//     value on the left, and taking the first one hands the client's spoof
//     straight through. The last instance is the one nearest us, exactly as
//     with the chain.
func (t *TrustedProxies) firstUntrustedForwarded(r *http.Request) (netip.Addr, bool) {
	if xff := r.Header.Values(headerXForwardedFor); len(xff) > 0 {
		entries := forwardedChain(xff)
		for i := len(entries) - 1; i >= 0; i-- {
			addr, ok := parseForwardedEntry(entries[i])
			if !ok {
				// An entry that is not an address tells us
				// nothing. Keep walking inward; a caller cannot
				// use junk to hide what the proxy appended to
				// its right.
				continue
			}
			if t.trusts(addr) {
				continue
			}
			return addr, true
		}
		// The chain was there and named no outside caller. The answer
		// is the peer. X-Real-IP is deliberately NOT consulted here.
		return netip.Addr{}, false
	}

	// No X-Forwarded-For at all. Some proxies set only X-Real-IP; read it
	// right to left, same rule as the chain.
	values := r.Header.Values(headerXRealIP)
	for i := len(values) - 1; i >= 0; i-- {
		if addr, ok := parseForwardedEntry(values[i]); ok && !t.trusts(addr) {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

// forwardedChain flattens every X-Forwarded-For header instance into one
// left-to-right list of entries, bounded in both bytes and count. When the
// bound bites, the entries nearest the trusted edge (the right) are kept.
func forwardedChain(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	joined := strings.Join(values, ",")
	if len(joined) > maxForwardedChainBytes {
		joined = joined[len(joined)-maxForwardedChainBytes:]
		// Drop whatever partial entry the cut landed in the middle of.
		if idx := strings.IndexByte(joined, ','); idx >= 0 {
			joined = joined[idx+1:]
		} else {
			return nil
		}
	}
	parts := strings.Split(joined, ",")
	if len(parts) > maxForwardedEntries {
		parts = parts[len(parts)-maxForwardedEntries:]
	}
	return parts
}

// parseForwardedEntry reads one chain entry. Entries are normally a bare
// address, but some proxies add a port, and IPv6 may arrive bracketed.
func parseForwardedEntry(entry string) (netip.Addr, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" || len(entry) > maxForwardedEntryLen {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(entry); err == nil {
		return normalizeAddr(addr), true
	}
	if host, _, err := net.SplitHostPort(entry); err == nil {
		if addr, err := netip.ParseAddr(strings.TrimSpace(host)); err == nil {
			return normalizeAddr(addr), true
		}
	}
	if stripped := strings.TrimSuffix(strings.TrimPrefix(entry, "["), "]"); stripped != entry {
		if addr, err := netip.ParseAddr(stripped); err == nil {
			return normalizeAddr(addr), true
		}
	}
	return netip.Addr{}, false
}

// peerAddr extracts the address of the real TCP peer from RemoteAddr, which
// Go writes as host:port ("192.0.2.1:1234", "[2001:db8::1]:1234").
func peerAddr(remote string) (netip.Addr, bool) {
	host := strings.TrimSpace(remote)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if stripped := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"); stripped != host {
		host = stripped
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return normalizeAddr(addr), true
}

// normalizeAddr makes two spellings of the same address compare equal:
// an IPv4-mapped IPv6 address becomes its plain IPv4 form, and the interface
// zone (which says nothing about who the caller is) is dropped.
func normalizeAddr(a netip.Addr) netip.Addr {
	return a.Unmap().WithZone("")
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// logIgnoredForwarding records that a caller who is not a listed proxy sent
// forwarding headers anyway — the shape of an attempt to choose its own
// address. Only the real peer and a COUNT are logged; the header contents are
// attacker-written text and never reach a log line.
func logIgnoredForwarding(r *http.Request, peer netip.Addr) {
	if !slog.Default().Enabled(r.Context(), slog.LevelDebug) {
		return
	}
	n := len(forwardedChain(r.Header.Values(headerXForwardedFor)))
	n += len(r.Header.Values(headerXRealIP))
	if n == 0 {
		return
	}
	slog.Debug("ignoring forwarding headers from a peer that is not a trusted proxy",
		"peer", peer.String(),
		"forwarded_entry_count", n,
		"setting", TrustedProxiesEnv,
	)
}

// SetTrustedProxies publishes the parsed SHARKO_TRUSTED_PROXIES list. Call it
// once at startup, before the server takes traffic. Never calling it leaves
// the safe default in place: no proxy is trusted and forwarding headers are
// ignored on every request.
func (s *Server) SetTrustedProxies(t *TrustedProxies) {
	s.trustedProxies = t
}

// clientIP is the single call path every consumer uses. There is deliberately
// no second way to derive a caller's address anywhere in this package.
func (s *Server) clientIP(r *http.Request) string {
	return s.trustedProxies.ClientIP(r)
}
