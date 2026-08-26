package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Story B11 — SHARKO_TRUSTED_PROXIES, proven through the real handler chain.
//
// Every test here drives the router built by NewRouter, so what passes is the
// behavior production has: the same middleware, the same rate limiters, the
// same resolver. A test that called the resolver function on its own would
// prove nothing about the server.
//
// The thing being proven is an identity: which caller a request is charged
// to. That identity is not exposed in any response, so the tests read it the
// way an attacker would — through the rate limiter. Two requests that land in
// the same bucket were charged to the same caller; two that do not, were not.
//
// The headline case is "the bypass": one caller sending a different
// X-Forwarded-For on every single request must still run into the limit.

const (
	// A caller talking straight to the port, on no proxy list anywhere.
	directCallerPeer = "198.51.100.77:41000"

	// The proxy used by the trusted-list tests, inside 10.0.0.0/24.
	proxyPeer = "10.0.0.1:41000"

	// How many attempts a test is willing to make before it decides the
	// limiter is never going to fire. The live login limit is 5/minute and
	// the write limit is 30/minute, so this is comfortably above both.
	maxAttempts = 80
)

// routerTrusting builds the production router for a server whose trusted
// proxy list is exactly raw. A parse failure fails the test — the invalid
// input cases are asserted separately, on ParseTrustedProxies itself.
func routerTrusting(t *testing.T, raw string) http.Handler {
	t.Helper()
	tp, err := ParseTrustedProxies(raw)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q) failed: %v", raw, err)
	}
	srv := newIsolatedTestServer(t)
	srv.SetTrustedProxies(tp)
	return NewRouter(srv, nil)
}

// login sends one real login attempt through the whole chain and returns the
// status code. Every value in forwarded becomes its own X-Forwarded-For
// header instance, which is how a chain arrives when several hops each add
// their own.
func login(t *testing.T, h http.Handler, remoteAddr string, forwarded ...string) int {
	t.Helper()
	return loginWith(t, h, remoteAddr, forwarded, nil)
}

// loginWith is login plus X-Real-IP. Each string in either slice becomes its
// own header instance, which is how repeated headers arrive when several hops
// each add their own instead of replacing what was there.
func loginWith(t *testing.T, h http.Handler, remoteAddr string, forwarded, realIPs []string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"nobody","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	for _, v := range forwarded {
		req.Header.Add("X-Forwarded-For", v)
	}
	for _, v := range realIPs {
		req.Header.Add("X-Real-IP", v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// firstThrottled repeats send until it answers 429 and reports which attempt
// that was (1-based), or 0 if the limiter never fired.
func firstThrottled(t *testing.T, send func(attempt int) int) int {
	t.Helper()
	for i := 1; i <= maxAttempts; i++ {
		if send(i) == http.StatusTooManyRequests {
			return i
		}
	}
	return 0
}

// mustThrottle drains a caller's allowance and fails loudly if it never runs out.
func mustThrottle(t *testing.T, what string, send func(attempt int) int) {
	t.Helper()
	if n := firstThrottled(t, send); n == 0 {
		t.Fatalf("RATE LIMIT BYPASS: %s — %d requests and not one was refused with 429", what, maxAttempts)
	}
}

// TestDirectCaller_ForwardedHeadersAreIgnored — a caller reaching the port
// itself does not get to say where it came from. Its spoofed address is
// ignored, so it shares one bucket with itself no matter what it sends.
func TestDirectCaller_ForwardedHeadersAreIgnored(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a direct caller sending a fixed spoofed X-Forwarded-For", func(int) int {
		return login(t, h, directCallerPeer, "203.0.113.9")
	})

	// The spoofed address must not have been charged: a caller who really
	// is 203.0.113.9, arriving from somewhere else, still has its own
	// allowance.
	if code := login(t, h, "203.0.113.9:5000"); code == http.StatusTooManyRequests {
		t.Fatal("the spoofed address was charged for the spoofer's requests — the header was believed")
	}
}

// TestDirectCaller_RotatingSpoofDoesNotEscapeTheLimit is the bypass proof and
// the reason this story exists. The caller hands out a brand-new address on
// every single request. Before the fix that was a free pass; now the limiter
// keys on the real TCP peer and stops them anyway.
func TestDirectCaller_RotatingSpoofDoesNotEscapeTheLimit(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a direct caller changing X-Forwarded-For on every request", func(i int) int {
		return login(t, h, directCallerPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})

	// Control: the limiter did not simply start refusing everyone. A
	// genuinely different caller is still served.
	if code := login(t, h, "198.51.100.78:41000"); code == http.StatusTooManyRequests {
		t.Fatal("an unrelated caller inherited the throttle — the limiter is not keying per caller at all")
	}
}

// TestDirectCaller_RotatingSpoofOnWriteEndpoints covers the second call site:
// the write limiter, which is a separate middleware from the login limiter.
func TestDirectCaller_RotatingSpoofOnWriteEndpoints(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	post := func(remoteAddr, forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/no/such/path", strings.NewReader(`{}`))
		req.RemoteAddr = remoteAddr
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	mustThrottle(t, "a direct caller changing X-Forwarded-For on every write", func(i int) int {
		return post(directCallerPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})

	if code := post("198.51.100.79:41000", ""); code == http.StatusTooManyRequests {
		t.Fatal("an unrelated caller inherited the write throttle")
	}
}

// TestEmptyTrustedProxies_TrustsNobody — the default configuration trusts no
// proxy at all, so the same bypass attempt fails the same way.
func TestEmptyTrustedProxies_TrustsNobody(t *testing.T) {
	h := routerTrusting(t, "")

	mustThrottle(t, "a rotating spoof with no trusted proxies configured", func(i int) int {
		return login(t, h, directCallerPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})
}

// TestNoImplicitTrust_LoopbackAndPrivateRanges — nothing is trusted by
// accident. A request from loopback, or from a private range that was not
// listed, is still just a caller.
func TestNoImplicitTrust_LoopbackAndPrivateRanges(t *testing.T) {
	for _, peer := range []string{"127.0.0.1:41000", "[::1]:41000", "192.168.1.10:41000", "172.16.4.4:41000"} {
		t.Run(peer, func(t *testing.T) {
			h := routerTrusting(t, "10.0.0.0/24")
			mustThrottle(t, "a rotating spoof from "+peer, func(i int) int {
				return login(t, h, peer, fmt.Sprintf("203.0.113.%d", i%250+1))
			})
		})
	}
}

// TestOneTrustedProxy_ClientAddressIsUsed — with the proxy listed, the
// address it forwards is honored: two clients behind it get their own
// allowances instead of sharing the proxy's.
func TestOneTrustedProxy_ClientAddressIsUsed(t *testing.T) {
	h := routerTrusting(t, "10.0.0.1")

	mustThrottle(t, "one client behind a trusted proxy", func(int) int {
		return login(t, h, proxyPeer, "203.0.113.10")
	})

	if code := login(t, h, proxyPeer, "203.0.113.11"); code == http.StatusTooManyRequests {
		t.Fatal("a second client behind the same trusted proxy was refused — the forwarded address is not being used")
	}
}

// TestTrustedProxyChain_WalksInFromTheTrustedEdge — several proxies in a row,
// all listed. The answer is the client at the far end, and the identity does
// not change when the trusted hops are spelled differently.
func TestTrustedProxyChain_WalksInFromTheTrustedEdge(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24, 172.20.5.0/24")

	mustThrottle(t, "a client behind three trusted hops", func(int) int {
		return login(t, h, proxyPeer, "203.0.113.20, 172.20.5.9, 10.0.0.2")
	})

	// Same client, different trusted hops in between: still the same caller.
	if code := login(t, h, proxyPeer, "203.0.113.20, 10.0.0.7"); code != http.StatusTooManyRequests {
		t.Fatalf("the same client through different trusted hops was treated as a new caller (status %d)", code)
	}

	// A different client through the same chain gets its own allowance.
	if code := login(t, h, proxyPeer, "203.0.113.21, 172.20.5.9, 10.0.0.2"); code == http.StatusTooManyRequests {
		t.Fatal("a different client behind the same chain was refused")
	}
}

// TestUntrustedHopInsideChain_StopsAtTheFirstUntrustedAddress — the walk stops
// at the first hop that is not on the list. Everything to the left of it was
// written by someone we have no reason to believe, and taking the leftmost
// value would hand that caller a new identity per request.
func TestUntrustedHopInsideChain_StopsAtTheFirstUntrustedAddress(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a chain whose middle hop is not trusted", func(int) int {
		return login(t, h, proxyPeer, "203.0.113.30, 198.51.100.4, 10.0.0.2")
	})

	// The leftmost value changes; the first untrusted hop does not. Same
	// caller — this is the assertion that the leftmost value is not used.
	if code := login(t, h, proxyPeer, "203.0.113.99, 198.51.100.4, 10.0.0.2"); code != http.StatusTooManyRequests {
		t.Fatalf("changing the leftmost value produced a new caller (status %d) — the leftmost address is being trusted", code)
	}

	// A genuinely different untrusted hop is a different caller.
	if code := login(t, h, proxyPeer, "203.0.113.30, 198.51.100.5, 10.0.0.2"); code == http.StatusTooManyRequests {
		t.Fatal("a different untrusted hop was treated as the same caller")
	}
}

// TestSpoofThroughTrustedProxy_IsStillBounded — a caller behind a trusted
// proxy prepends fake hops. The proxy appends the truth to the right, so the
// walk meets the real address before any of the invented ones.
func TestSpoofThroughTrustedProxy_IsStillBounded(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a client behind a trusted proxy prepending a new fake hop each time", func(i int) int {
		spoof := fmt.Sprintf("203.0.113.%d, 198.51.100.4", i%250+1)
		return login(t, h, proxyPeer, spoof+", 10.0.0.2")
	})
}

// TestAllTrustedChain_FallsBackToThePeer — every entry is a listed proxy, so
// the chain never names an outside caller. The answer is the peer, and a
// caller cannot mint identities by listing our own proxies.
//
// The X-Real-IP header is set here on purpose. Without it this test asserted a
// property the code only had while that header was absent: the chain resolved
// to nothing, and the resolver then fell through to X-Real-IP, which the
// caller also writes. So "falls back to the peer" was true only for a request
// that did not try the other door. It tries it now.
func TestAllTrustedChain_FallsBackToThePeer(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a chain made only of trusted proxies", func(i int) int {
		return loginWith(t, h, proxyPeer,
			[]string{fmt.Sprintf("10.0.0.%d, 10.0.0.2", i%250+1)},
			[]string{fmt.Sprintf("203.0.113.%d", i%250+1)})
	})
}

// TestAllTrustedChain_RotatingXRealIPDoesNotEscapeTheLimit is the second
// bypass proof, and it is a real one: it was demonstrated through the router
// before this fix, 80 login attempts and not one refused.
//
// The shape is a caller behind our own proxies. It sends a chain made only of
// addresses that are on the trusted list — which resolves to no outside caller
// at all — and then names itself in X-Real-IP, differently every time. The
// resolver used to consult X-Real-IP whenever the chain produced nothing, so
// the caller got a fresh identity per request and the limiter never fired.
//
// X-Forwarded-For being present at all now ends the question.
func TestAllTrustedChain_RotatingXRealIPDoesNotEscapeTheLimit(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a caller under a fully trusted chain rotating X-Real-IP", func(i int) int {
		return loginWith(t, h, proxyPeer,
			[]string{"10.0.0.5, 10.0.0.2"},
			[]string{fmt.Sprintf("198.51.100.%d", i%250+1)})
	})

	// Control: the limiter is not simply refusing everyone. A different
	// real client, arriving the normal way, still gets its own allowance.
	if code := login(t, h, proxyPeer, "203.0.113.240, 10.0.0.2"); code == http.StatusTooManyRequests {
		t.Fatal("a genuine forwarded client was refused, so the throttling above proves nothing")
	}
}

// TestXRealIP_LastInstanceWins — the chain is walked right to left because the
// leftmost value is never trusted just because it is there. X-Real-IP gets the
// same rule.
//
// A proxy that APPENDS its own X-Real-IP rather than replacing the client's
// leaves two instances on the request: the client's on the left, the proxy's
// on the right. Taking the first one handed the client's spoof straight
// through, so a caller could rotate the left value and mint an identity per
// request. The last instance is the one nearest us.
func TestXRealIP_LastInstanceWins(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a client rotating the first of two X-Real-IP headers", func(i int) int {
		return loginWith(t, h, proxyPeer, nil, []string{
			fmt.Sprintf("198.51.100.%d", i%250+1), // the caller's own, appended first
			"203.0.113.55",                        // what our proxy added, nearest us
		})
	})

	// Control: the value that decided it really is the last one. A request
	// naming a DIFFERENT address last is a different caller and is not
	// refused by the bucket the loop above just drained.
	if code := loginWith(t, h, proxyPeer, nil, []string{"198.51.100.1", "203.0.113.56"}); code == http.StatusTooManyRequests {
		t.Fatal("a second client behind the same proxy shared the first client's allowance")
	}
}

// TestForwardedEntriesWithPorts — some proxies append host:port. The port is
// not part of who the caller is.
func TestForwardedEntriesWithPorts(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a client whose forwarded entry carries a port", func(i int) int {
		return login(t, h, proxyPeer, fmt.Sprintf("203.0.113.40:%d, 10.0.0.2:9999", 30000+i))
	})

	// The same address without a port is the same caller.
	if code := login(t, h, proxyPeer, "203.0.113.40, 10.0.0.2"); code != http.StatusTooManyRequests {
		t.Fatalf("the same address with and without a port was treated as two callers (status %d)", code)
	}
}

// TestIPv6_BracketedPeerAndForwardedEntries — IPv6 arrives bracketed with a
// port on RemoteAddr, and may arrive either way in the header.
func TestIPv6_BracketedPeerAndForwardedEntries(t *testing.T) {
	h := routerTrusting(t, "2001:db8:aaaa::1/128")

	mustThrottle(t, "an IPv6 client behind a bracketed IPv6 proxy", func(int) int {
		return login(t, h, "[2001:db8:aaaa::1]:41000", "2001:db8:bbbb::9")
	})

	// Bracketed, with a port: same caller.
	if code := login(t, h, "[2001:db8:aaaa::1]:41000", "[2001:db8:bbbb::9]:5000"); code != http.StatusTooManyRequests {
		t.Fatalf("the same IPv6 client bracketed with a port was treated as a new caller (status %d)", code)
	}

	// A different IPv6 client gets its own allowance.
	if code := login(t, h, "[2001:db8:aaaa::1]:41000", "2001:db8:bbbb::10"); code == http.StatusTooManyRequests {
		t.Fatal("a different IPv6 client behind the same proxy was refused")
	}
}

// TestIPv6_UnlistedProxyIsNotTrusted — the IPv6 side gets no implicit trust
// either, and a rotating IPv6 spoof is stopped like any other.
func TestIPv6_UnlistedProxyIsNotTrusted(t *testing.T) {
	h := routerTrusting(t, "2001:db8:aaaa::1/128")

	mustThrottle(t, "a rotating IPv6 spoof from an unlisted IPv6 caller", func(i int) int {
		return login(t, h, "[2001:db8:cccc::5]:41000", fmt.Sprintf("2001:db8:bbbb::%d", i%250+1))
	})
}

// TestIPv4MappedIPv6_IsTheSameAddress — ::ffff:10.0.0.1 and 10.0.0.1 are one
// address written two ways, both when matching the list and when reading a
// forwarded entry. Treating them as different would let a caller dodge the
// limiter by switching spelling.
func TestIPv4MappedIPv6_IsTheSameAddress(t *testing.T) {
	h := routerTrusting(t, "10.0.0.1/32")

	mustThrottle(t, "a client behind a proxy whose peer arrives IPv4-mapped", func(i int) int {
		if i%2 == 0 {
			return login(t, h, "[::ffff:10.0.0.1]:41000", "::ffff:203.0.113.50")
		}
		return login(t, h, "10.0.0.1:41000", "203.0.113.50")
	})
}

// TestMalformedForwardedEntries_AreNotAnEscapeHatch — junk in the header,
// from a trusted proxy, must not mint a new caller on every request.
func TestMalformedForwardedEntries_AreNotAnEscapeHatch(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a trusted proxy forwarding junk that changes every request", func(i int) int {
		return login(t, h, proxyPeer, fmt.Sprintf("not-an-ip-%d, 1.2.3.4.5, ,  , 10.0.0.2", i))
	})
}

// TestMalformedForwardedEntries_FromDirectCaller — the same junk from a
// caller that is on no list is ignored outright.
func TestMalformedForwardedEntries_FromDirectCaller(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a direct caller sending junk that changes every request", func(i int) int {
		return login(t, h, directCallerPeer, fmt.Sprintf("%d-junk", i))
	})
}

// TestRepeatedForwardedHeaders_AreOneChain — several X-Forwarded-For header
// instances are the same chain as one comma-joined header, read in order.
func TestRepeatedForwardedHeaders_AreOneChain(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a chain split across repeated header instances", func(int) int {
		return login(t, h, proxyPeer, "203.0.113.60", "10.0.0.2")
	})

	// The same chain in one header is the same caller.
	if code := login(t, h, proxyPeer, "203.0.113.60, 10.0.0.2"); code != http.StatusTooManyRequests {
		t.Fatalf("the same chain split across headers was treated as a different caller (status %d)", code)
	}
}

// TestOversizedForwardedHeader_IsBoundedAndSafe — a very long chain is read
// from the trusted edge inward and cut off. What is beyond the cut is exactly
// the part a caller can invent, so cutting it must not hand out new identities.
func TestOversizedForwardedHeader_IsBoundedAndSafe(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	// 900 trusted hops (well past both the byte and the entry bound), with
	// a rotating invented address at the far left.
	tail := strings.Repeat(", 10.0.0.2", 900)

	mustThrottle(t, "an oversized chain with a rotating invented address at the far left", func(i int) int {
		return login(t, h, proxyPeer, fmt.Sprintf("203.0.113.%d%s", i%250+1, tail))
	})

	// A chain that is long but still within the bound keeps working
	// normally — the bound must not break real multi-hop deployments.
	h2 := routerTrusting(t, "10.0.0.0/24")
	mustThrottle(t, "a ten-hop chain within the bound", func(int) int {
		return login(t, h2, proxyPeer, "203.0.113.70"+strings.Repeat(", 10.0.0.2", 10))
	})
	if code := login(t, h2, proxyPeer, "203.0.113.71"+strings.Repeat(", 10.0.0.2", 10)); code == http.StatusTooManyRequests {
		t.Fatal("a different client on a ten-hop chain was refused — the bound is cutting real chains short")
	}
}

// TestXRealIP_OnlyFromATrustedProxy — X-Real-IP is a forwarding header like
// any other: ignored from a direct caller, used when the peer is a listed
// proxy and the chain said nothing.
func TestXRealIP_OnlyFromATrustedProxy(t *testing.T) {
	sendRealIP := func(h http.Handler, peer, value string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"nobody","password":"wrong"}`))
		req.RemoteAddr = peer
		req.Header.Set("X-Real-IP", value)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	direct := routerTrusting(t, "10.0.0.0/24")
	mustThrottle(t, "a direct caller rotating X-Real-IP", func(i int) int {
		return sendRealIP(direct, directCallerPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})

	viaProxy := routerTrusting(t, "10.0.0.0/24")
	mustThrottle(t, "one client behind a trusted proxy that sets X-Real-IP", func(int) int {
		return sendRealIP(viaProxy, proxyPeer, "203.0.113.80")
	})
	if code := sendRealIP(viaProxy, proxyPeer, "203.0.113.81"); code == http.StatusTooManyRequests {
		t.Fatal("a second client behind a trusted proxy that sets X-Real-IP was refused")
	}
}

// TestXForwardedFor_WinsOverXRealIP — when both are present the chain
// decides, so a caller cannot use the simpler header to talk past the walk.
func TestXForwardedFor_WinsOverXRealIP(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	send := func(realIP string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"nobody","password":"wrong"}`))
		req.RemoteAddr = proxyPeer
		req.Header.Set("X-Forwarded-For", "203.0.113.90, 10.0.0.2")
		req.Header.Set("X-Real-IP", realIP)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	mustThrottle(t, "a client behind a trusted proxy rotating X-Real-IP under a fixed chain", func(i int) int {
		return send(fmt.Sprintf("198.51.100.%d", i%250+1))
	})
}

// TestSecurityLogLine_UsesTheSameResolvedAddress — the one security log line
// that names a caller and the rate limiters must never disagree about who
// called. The dead-route stub logs client_ip; from a caller that is on no
// list, that must be the real peer.
//
// It is a LOG LINE, not an audit entry. The audit log records no client
// address anywhere in Sharko, so a 429 from either limiter leaves no audit
// entry naming the caller. This test used to be called TestAuditFact_…, which
// read as a wider guarantee than the code gives.
func TestSecurityLogLine_UsesTheSameResolvedAddress(t *testing.T) {
	restore := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	h := routerTrusting(t, "10.0.0.0/24")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{}`))
	req.RemoteAddr = directCallerPeer
	req.Header.Set("X-Forwarded-For", "203.0.113.200")
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := loggedClientIP(t, logs.Bytes())
	if got != "198.51.100.77" {
		t.Fatalf("security log line client_ip = %q, want the real peer 198.51.100.77", got)
	}

	// And through a trusted proxy it is the forwarded client, matching what
	// the limiter charged.
	logs.Reset()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{}`))
	req.RemoteAddr = proxyPeer
	req.Header.Set("X-Forwarded-For", "203.0.113.201, 10.0.0.2")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := loggedClientIP(t, logs.Bytes()); got != "203.0.113.201" {
		t.Fatalf("security log line client_ip = %q, want the forwarded client 203.0.113.201", got)
	}
}

// loggedClientIP pulls client_ip out of the dead-route warning line.
func loggedClientIP(t *testing.T, out []byte) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.Contains(line, "dead /api/v1/login") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		ip, _ := rec["client_ip"].(string)
		return ip
	}
	t.Fatalf("the dead-route warning was not logged; output was:\n%s", out)
	return ""
}

// TestForwardingHeaderIsNeverLogged — the header is written by whoever is
// calling. Its contents must not reach a log line, at any level. Only the
// real peer and a count may.
func TestForwardingHeaderIsNeverLogged(t *testing.T) {
	restore := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	h := routerTrusting(t, "10.0.0.0/24")

	const marker = "203.0.113.111"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"nobody","password":"wrong"}`))
	req.RemoteAddr = directCallerPeer
	req.Header.Set("X-Forwarded-For", marker+", "+marker+", "+marker)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(logs.String(), marker) {
		t.Fatalf("the caller-supplied forwarding header reached the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "forwarded_entry_count") {
		t.Fatalf("expected the bounded count fact at debug level; output was:\n%s", logs.String())
	}
}

// TestParseTrustedProxies_Valid covers the shapes an operator may write.
func TestParseTrustedProxies_Valid(t *testing.T) {
	cases := []struct {
		raw   string
		count int
	}{
		{"", 0},
		{"   ", 0},
		{"10.0.0.1", 1},
		{"10.0.0.0/8", 1},
		{"10.0.0.0/8,192.168.0.0/16", 2},
		{"10.0.0.0/8, 192.168.0.0/16 , 172.16.0.1", 3},
		{"10.0.0.0/8,", 1},
		{"2001:db8::1", 1},
		{"2001:db8::/32", 1},
		{"::ffff:10.0.0.1", 1},
		{"10.0.0.5/24", 1}, // host bits set — normalized, not refused
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			tp, err := ParseTrustedProxies(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tp.Count() != tc.count {
				t.Fatalf("Count() = %d, want %d", tp.Count(), tc.count)
			}
		})
	}
}

// TestParseTrustedProxies_InvalidFailsStartup — the parse refuses anything
// that is not an address or a range, and anything that would trust every
// address. The message names the setting and never repeats what was
// configured, because that value ends up in logs an operator shares.
func TestParseTrustedProxies_InvalidFailsStartup(t *testing.T) {
	cases := []string{
		"*",
		"0.0.0.0/0",
		"::/0",
		"10.0.0.0/8, *",
		"garbage",
		"999.1.1.1",
		"10.0.0.0/33",
		"2001:db8::/129",
		"10.0.0.0-10.0.0.255",
		"proxy.internal.example",
		"10.0.0.1/",
		"/24",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			tp, err := ParseTrustedProxies(raw)
			if err == nil {
				t.Fatalf("ParseTrustedProxies(%q) accepted an invalid list (count=%d) — startup would not fail", raw, tp.Count())
			}
			if !strings.Contains(err.Error(), TrustedProxiesEnv) {
				t.Errorf("error does not name the setting: %v", err)
			}
			for _, entry := range strings.Split(raw, ",") {
				entry = strings.TrimSpace(entry)
				if entry == "" {
					continue
				}
				if strings.Contains(err.Error(), entry) {
					t.Errorf("error repeats a configured value (%q): %v", entry, err)
				}
			}
		})
	}
}

// TestUnparseablePeer_StillIgnoresTheHeader — if RemoteAddr is not an address
// at all (an odd listener, a test harness), the peer is not trusted, so the
// header is still ignored and the caller is still bounded.
func TestUnparseablePeer_StillIgnoresTheHeader(t *testing.T) {
	h := routerTrusting(t, "10.0.0.0/24")

	mustThrottle(t, "a caller whose peer address does not parse", func(i int) int {
		return login(t, h, "not-an-address", fmt.Sprintf("203.0.113.%d", i%250+1))
	})
}

// TestNilTrustedProxies_TrustsNobody — a server that was never given a list
// (a wiring mistake, or a test) falls back to the safe answer rather than to
// the old header-believing one.
func TestNilTrustedProxies_TrustsNobody(t *testing.T) {
	srv := newIsolatedTestServer(t)
	// deliberately no SetTrustedProxies call
	h := NewRouter(srv, nil)

	mustThrottle(t, "a rotating spoof against a server with no trusted proxy list wired", func(i int) int {
		return login(t, h, directCallerPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})
}
