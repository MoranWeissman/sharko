package config

// catalog_sources_redaction_test.go — BF16: a rejected catalog source names
// the setting, the constant redacted identity, its position in the list, and
// a safe reason — never the configured address.
//
// Why this matters: the documented private-catalog form writes an auth token
// into the URL path (see LoadMarketplaceSourcesFromFile's TOKEN-LEAK GUARD
// comment). A startup rejection is printed to stderr twice (the command
// framework and then main), and a crash-looping pod reprints it on every
// restart. So the rejection message must never carry the address — not raw,
// not percent-encoded, not any partial form such as the host.
//
// Probe discipline: every absence claim here is preceded by a positive
// control proving the same probe finds a planted copy of the same sentinel
// in the same kind of text. A probe that cannot find a planted value has
// proved nothing.

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// redactionSentinel is the synthetic secret planted inside every rejected
// address. It contains ":" so its percent-encoded form (%3A) differs from
// the raw form, letting the probe tell the two apart. Synthetic only —
// this never was and never will be a real credential.
const redactionSentinel = "SYNTHtok:aa11bb22cc33dd44"

// sentinelForms returns every shape of the sentinel the probe must be able
// to find: raw, percent-encoded (query and path flavors), and partial forms
// (every 12-byte window of the raw sentinel, which catches a message that
// echoes only a piece of the address).
func sentinelForms(sentinel string) map[string]string {
	forms := map[string]string{
		"raw":           sentinel,
		"query_escaped": url.QueryEscape(sentinel),
		"path_escaped":  url.PathEscape(sentinel),
		"lowercase":     strings.ToLower(sentinel),
	}
	const window = 12
	for i := 0; i+window <= len(sentinel); i++ {
		forms[fmt.Sprintf("partial_%d", i)] = sentinel[i : i+window]
	}
	return forms
}

// probeForSentinel returns the names of every sentinel form found in text.
func probeForSentinel(text, sentinel string) []string {
	var found []string
	for name, form := range sentinelForms(sentinel) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// assertProbePositiveControl proves the probe can find a planted sentinel in
// the given text before any absence is claimed about that text. It plants
// the raw form and one escaped form into a copy and requires both hits.
func assertProbePositiveControl(t *testing.T, text, sentinel string) {
	t.Helper()
	planted := text + "\nplanted-raw: " + sentinel +
		"\nplanted-escaped: " + url.QueryEscape(sentinel)
	found := probeForSentinel(planted, sentinel)
	hasRaw, hasEscaped := false, false
	for _, name := range found {
		if name == "raw" {
			hasRaw = true
		}
		if name == "query_escaped" {
			hasEscaped = true
		}
	}
	if !hasRaw || !hasEscaped {
		t.Fatalf("POSITIVE CONTROL FAILED: probe did not find the planted sentinel (raw=%v escaped=%v, found=%v) — the probe is vacuous and every absence claim below it is void",
			hasRaw, hasEscaped, found)
	}
}

// rejectionCase is one of the seven rejection messages: six branches inside
// validateAndCanonicalize plus the GitOps wrap. Each carries a bad address
// with the sentinel embedded where the documented private-catalog token
// lives (the path), and the words the message must still say.
type rejectionCase struct {
	name string
	// url is the configured address (sentinel embedded).
	url string
	// stubDNS, when non-nil, replaces DNS resolution for this case.
	stubDNS func(string) ([]string, error)
	// wantWords are message fragments an operator needs: the reason.
	wantWords []string
	// hostWord is a host fragment that must NOT appear (partial address).
	hostWord string
}

func redactionCases() []rejectionCase {
	return []rejectionCase{
		{
			name:      "malformed_url",
			url:       "https://catalogs.example.com/private/" + redactionSentinel + "/%zz-bad",
			wantWords: []string{"not a well-formed web address", "https://"},
			hostWord:  "catalogs.example.com",
		},
		{
			name:      "missing_scheme",
			url:       "//catalogs.example.com/private/" + redactionSentinel + "/catalog.yaml",
			wantWords: []string{"missing the https:// prefix"},
			hostWord:  "catalogs.example.com",
		},
		{
			name:      "scheme_not_allowed",
			url:       "http://catalogs.example.com/private/" + redactionSentinel + "/catalog.yaml",
			wantWords: []string{`scheme "http" is not allowed`, "HTTPS-only"},
			hostWord:  "catalogs.example.com",
		},
		{
			name:      "missing_host",
			url:       "https:///private/" + redactionSentinel + "/catalog.yaml",
			wantWords: []string{"missing a host name"},
			hostWord:  "", // no host to leak
		},
		{
			name:      "loopback_hostname",
			url:       "https://localhost/private/" + redactionSentinel + "/catalog.yaml",
			wantWords: []string{"loopback", "SSRF guard", EnvCatalogAllowPrivate + "=true"},
			hostWord:  "localhost",
		},
		{
			name: "ssrf_private_address",
			url:  "https://internal.example.com/private/" + redactionSentinel + "/catalog.yaml",
			stubDNS: func(string) ([]string, error) {
				return []string{"10.1.2.3"}, nil
			},
			wantWords: []string{"private", "SSRF guard", EnvCatalogAllowPrivate + "=true"},
			hostWord:  "internal.example.com",
		},
	}
}

// assertRejectionMessage runs the shared checks on one rejection error:
// positive control first, then absence of every sentinel form and of the
// host, then presence of the setting, the constant identity, the position,
// and the actionable reason.
func assertRejectionMessage(t *testing.T, msg, setting, position string, tc rejectionCase) {
	t.Helper()

	// 1. Positive control BEFORE any absence claim.
	assertProbePositiveControl(t, msg, redactionSentinel)

	// 2. No form of the sentinel appears.
	if found := probeForSentinel(msg, redactionSentinel); len(found) > 0 {
		t.Errorf("rejection message carries the address token (forms %v): %q", found, msg)
	}
	// 3. The host is a partial form of the address — it must not appear.
	if tc.hostWord != "" && strings.Contains(msg, tc.hostWord) {
		t.Errorf("rejection message carries the host %q (a partial form of the address): %q", tc.hostWord, msg)
	}
	// 4. The setting is named, so the operator knows where to look.
	if !strings.Contains(msg, setting) {
		t.Errorf("rejection message does not name the setting %q: %q", setting, msg)
	}
	// 5. The constant identity stands in for the address.
	if !strings.Contains(msg, "("+redactedWord+")") {
		t.Errorf("rejection message does not carry the constant identity %q: %q", redactedWord, msg)
	}
	// 6. The position in the list, so the operator can find the entry.
	if !strings.Contains(msg, position) {
		t.Errorf("rejection message does not say which entry it was (want %q): %q", position, msg)
	}
	// 7. The reason describes the allowed structure.
	for _, w := range tc.wantWords {
		if !strings.Contains(msg, w) {
			t.Errorf("rejection message lost the reason fragment %q: %q", w, msg)
		}
	}
}

// redactedWord pins the exact identity word by way of the one constant that
// owns its spelling. (Import cycle note: this test lives in package config,
// which already imports credsafe.)
const redactedWord = "redacted"

// TestCatalogSourceRejection_EnvPath_NeverEchoesAddress drives all six
// validateAndCanonicalize branches through LoadCatalogSourcesFromEnv — the
// exact function the server's startup path calls — and checks every
// rejection message with a positive control first.
//
// Each case configures the bad address as the 2ND source (after a valid
// one), so the position assertion proves the message points the operator at
// the right entry, not just at "a" entry.
func TestCatalogSourceRejection_EnvPath_NeverEchoesAddress(t *testing.T) {
	for _, tc := range redactionCases() {
		t.Run(tc.name, func(t *testing.T) {
			// A leading valid source, so the bad one is the 2nd. The
			// .invalid TLD never resolves; checkSSRF fails open on DNS
			// error, so this source passes validation offline.
			t.Setenv(EnvCatalogURLs, "https://first-source.invalid/catalog.yaml,"+tc.url)
			t.Setenv(EnvCatalogAllowPrivate, "")
			t.Setenv(EnvCatalogRefreshInterval, "")

			if tc.stubDNS != nil {
				orig := lookupHostFn
				lookupHostFn = func(host string) ([]string, error) {
					if host == "internal.example.com" {
						return tc.stubDNS(host)
					}
					return nil, fmt.Errorf("no DNS in tests for %s", host)
				}
				defer func() { lookupHostFn = orig }()
			}

			cfg, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatalf("expected a rejection for %s, got config %+v — Sharko must still refuse to start", tc.name, cfg)
			}
			assertRejectionMessage(t, err.Error(), EnvCatalogURLs, "the 2nd source", tc)
		})
	}
}

// TestCatalogSourceRejection_GitOpsPath_NeverEchoesAddress covers the
// seventh message: the marketplace-sources.yaml wrap. Same checks, and the
// wrap must name the file as the setting instead of the env var.
func TestCatalogSourceRejection_GitOpsPath_NeverEchoesAddress(t *testing.T) {
	for _, tc := range redactionCases() {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`apiVersion: sharko.dev/v1
kind: MarketplaceSources
metadata:
  name: marketplace-sources
spec:
  sources:
    - url: "https://first-source.invalid/catalog.yaml"
    - url: ` + fmt.Sprintf("%q", tc.url) + `
`)
			lookup := func(host string) ([]string, error) {
				if tc.stubDNS != nil && host == "internal.example.com" {
					return tc.stubDNS(host)
				}
				return nil, fmt.Errorf("no DNS in tests for %s", host)
			}
			cfg, err := loadMarketplaceSourcesFromFileImpl(body, lookup)
			if err == nil {
				t.Fatalf("expected a rejection for %s, got config %+v — Sharko must still refuse to start", tc.name, cfg)
			}
			msg := err.Error()
			assertRejectionMessage(t, msg, "marketplace-sources.yaml", "the 2nd source", tc)
			// The GitOps wrap must not smuggle the env var name in as the
			// setting — the operator's fix is in the FILE here.
			if !strings.HasPrefix(msg, "marketplace-sources.yaml: ") {
				t.Errorf("GitOps rejection should lead with the file it came from: %q", msg)
			}
		})
	}
}

// TestCatalogSourceRejection_PositionCountsLikeAnOperator pins the counting
// rule: blank pieces (stray commas) take no number, so the position in the
// message matches what an operator sees reading their own value left to
// right past the gaps.
func TestCatalogSourceRejection_PositionCountsLikeAnOperator(t *testing.T) {
	// ",," before the first real source, and a blank between the 1st and
	// the bad 2nd — the bad one is still "the 2nd source".
	t.Setenv(EnvCatalogURLs, ",,https://first-source.invalid/catalog.yaml,  ,http://catalogs.example.com/private/"+redactionSentinel+"/catalog.yaml")
	t.Setenv(EnvCatalogAllowPrivate, "")
	t.Setenv(EnvCatalogRefreshInterval, "")

	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), "the 2nd source") {
		t.Errorf("blanks must not take a position number; want %q in %q", "the 2nd source", err.Error())
	}
}

// TestCatalogSourceRejection_SSRFClassifiedByType proves the SSRF rejection
// is classifiable by errors.Is on the sentinel — by type and by branch,
// never by matching message text.
func TestCatalogSourceRejection_SSRFClassifiedByType(t *testing.T) {
	orig := lookupHostFn
	lookupHostFn = func(string) ([]string, error) { return []string{"192.168.1.10"}, nil }
	defer func() { lookupHostFn = orig }()

	t.Setenv(EnvCatalogURLs, "https://internal.example.com/catalog.yaml")
	t.Setenv(EnvCatalogAllowPrivate, "")
	t.Setenv(EnvCatalogRefreshInterval, "")

	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected an SSRF rejection")
	}
	if !errors.Is(err, errPrivateAddress) {
		t.Errorf("SSRF rejection must be classifiable with errors.Is(err, errPrivateAddress); got %v", err)
	}
}

// TestCatalogSourceRejection_BehaviourUnchanged pins that the fix changed
// only the words: a bad source still fails the load (Sharko still refuses
// to start), and a fully valid list still loads with dedup intact.
func TestCatalogSourceRejection_BehaviourUnchanged(t *testing.T) {
	t.Setenv(EnvCatalogAllowPrivate, "")
	t.Setenv(EnvCatalogRefreshInterval, "")

	t.Setenv(EnvCatalogURLs, "http://bad.invalid/catalog.yaml")
	if _, err := LoadCatalogSourcesFromEnv(); err == nil {
		t.Fatal("a rejected source must still fail the load")
	}

	t.Setenv(EnvCatalogURLs, "https://a.invalid/cat.yaml,https://A.invalid/cat.yaml,https://b.invalid/cat.yaml")
	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("valid list must still load: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Errorf("dedup changed: got %d sources, want 2", len(cfg.Sources))
	}
}

// TestRedactedWordMatchesCredsafe ties this file's literal pin to the one
// constant that owns the spelling. If credsafe ever changes the word, this
// fails and forces the messages and the pins to move together.
func TestRedactedWordMatchesCredsafe(t *testing.T) {
	if redactedWord != credsafe.RedactedSourceLabel {
		t.Fatalf("this file pins %q but credsafe.RedactedSourceLabel is %q — one spelling only", redactedWord, credsafe.RedactedSourceLabel)
	}
	if got := credsafe.PublicSourceLabel(); got != redactedWord {
		t.Fatalf("PublicSourceLabel() = %q, want %q", got, redactedWord)
	}
}

// ---------------------------------------------------------------------------
// Credential-shaped scheme (BF16 follow-up).
//
// url.Parse treats everything before the first colon as the scheme whenever
// it fits the scheme character set. So a credential pasted in the
// historically common shape TOKEN:x-oauth-basic@host/path parses with the
// TOKEN as the scheme — and the old "scheme %q is not allowed" message
// printed it in full. The fix names a scheme only when it exactly matches
// the fixed knownHarmlessSchemes list (and then prints the LIST's word);
// everything else gets a message that echoes nothing.
// ---------------------------------------------------------------------------

// credSchemeSentinel is the synthetic token planted in the scheme slot.
// It is purely alphanumeric on purpose — that is the character class that
// parses as a scheme and was echoed before the fix. Synthetic only; this
// never was and never will be a real credential. Its first and last
// characters are chosen so that even a 3-byte window of it is unlikely to
// collide with honest message text.
const credSchemeSentinel = "zq4vSYNTHtok7aa22bb33cc44dd55xj8w"

// credSchemeURL is the exact historically common credential shape, with the
// synthetic token where the credential sits.
const credSchemeURL = credSchemeSentinel + ":x-oauth-basic@github.com/acme/catalog.yaml"

// credSchemeDerivedForms is every derived form of the sentinel the probe
// must check: raw, lowercase, percent-encoded and double-encoded, base64
// standard and URL-safe with and without padding, hex, and every first-N /
// last-N partial for N from 3 to 12.
func credSchemeDerivedForms(sentinel string) map[string]string {
	b := []byte(sentinel)
	forms := map[string]string{
		"raw":            sentinel,
		"lowercase":      strings.ToLower(sentinel),
		"percent":        url.QueryEscape(sentinel),
		"double_percent": url.QueryEscape(url.QueryEscape(sentinel)),
		"b64_std":        base64.StdEncoding.EncodeToString(b),
		"b64_std_nopad":  base64.RawStdEncoding.EncodeToString(b),
		"b64_url":        base64.URLEncoding.EncodeToString(b),
		"b64_url_nopad":  base64.RawURLEncoding.EncodeToString(b),
		"hex":            hex.EncodeToString(b),
	}
	for n := 3; n <= 12 && n <= len(sentinel); n++ {
		forms[fmt.Sprintf("first_%d", n)] = sentinel[:n]
		forms[fmt.Sprintf("last_%d", n)] = sentinel[len(sentinel)-n:]
	}
	return forms
}

// probeCredSchemeForms returns the names of every derived form found in text.
func probeCredSchemeForms(text, sentinel string) []string {
	var found []string
	for name, form := range credSchemeDerivedForms(sentinel) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// assertCredSchemeControl proves the derived-forms probe finds planted
// copies (raw AND hex) in the given text before any absence is claimed.
func assertCredSchemeControl(t *testing.T, text, sentinel string) {
	t.Helper()
	planted := text + "\nplanted-raw: " + sentinel +
		"\nplanted-hex: " + hex.EncodeToString([]byte(sentinel))
	found := probeCredSchemeForms(planted, sentinel)
	hasRaw, hasHex := false, false
	for _, name := range found {
		if name == "raw" {
			hasRaw = true
		}
		if name == "hex" {
			hasHex = true
		}
	}
	if !hasRaw || !hasHex {
		t.Fatalf("POSITIVE CONTROL FAILED: probe did not find the planted sentinel (raw=%v hex=%v, found=%v) — the probe is vacuous and every absence claim below it is void",
			hasRaw, hasHex, found)
	}
}

// assertCredSchemeRejection runs the shared checks on the rejection error
// produced by the credential-shaped scheme: control first, then absence of
// every derived form and of the rest of the supplied address, then the
// operator-facing fragments.
func assertCredSchemeRejection(t *testing.T, msg, setting string) {
	t.Helper()

	// 1. Positive control BEFORE any absence claim.
	assertCredSchemeControl(t, msg, credSchemeSentinel)

	// 2. No derived form of the token appears.
	if found := probeCredSchemeForms(msg, credSchemeSentinel); len(found) > 0 {
		t.Errorf("rejection message carries the credential-shaped scheme (forms %v): %q", found, msg)
	}
	// 3. The rest of the supplied address stays out too.
	for _, part := range []string{"x-oauth-basic", "github.com", "acme"} {
		if strings.Contains(msg, part) {
			t.Errorf("rejection message carries the supplied address fragment %q: %q", part, msg)
		}
	}
	// 4. No scheme is NAMED at all for an off-allowlist scheme.
	if strings.Contains(msg, `scheme "`) {
		t.Errorf("an off-allowlist scheme must not be named: %q", msg)
	}
	// 5. The operator still gets everything they need.
	for _, want := range []string{
		setting,
		"(" + redactedWord + ")",
		"the 2nd source",
		"uses a scheme other than https",
		"https://",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("rejection message lost the operator-facing fragment %q: %q", want, msg)
		}
	}
}

// TestCatalogSourceRejection_CredentialShapedScheme_EnvPath drives the
// reproduced credential shape through LoadCatalogSourcesFromEnv — the exact
// function the server's startup path calls.
func TestCatalogSourceRejection_CredentialShapedScheme_EnvPath(t *testing.T) {
	t.Setenv(EnvCatalogURLs, "https://first-source.invalid/catalog.yaml,"+credSchemeURL)
	t.Setenv(EnvCatalogAllowPrivate, "")
	t.Setenv(EnvCatalogRefreshInterval, "")

	cfg, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatalf("expected a rejection, got config %+v — Sharko must still refuse to start", cfg)
	}
	assertCredSchemeRejection(t, err.Error(), EnvCatalogURLs)
}

// TestCatalogSourceRejection_CredentialShapedScheme_GitOpsPath drives the
// same shape through the marketplace-sources.yaml loader, which shares the
// validation branch.
func TestCatalogSourceRejection_CredentialShapedScheme_GitOpsPath(t *testing.T) {
	body := []byte(`apiVersion: sharko.dev/v1
kind: MarketplaceSources
metadata:
  name: marketplace-sources
spec:
  sources:
    - url: "https://first-source.invalid/catalog.yaml"
    - url: ` + fmt.Sprintf("%q", credSchemeURL) + `
`)
	lookup := func(host string) ([]string, error) {
		return nil, fmt.Errorf("no DNS in tests for %s", host)
	}
	cfg, err := loadMarketplaceSourcesFromFileImpl(body, lookup)
	if err == nil {
		t.Fatalf("expected a rejection, got config %+v — Sharko must still refuse to start", cfg)
	}
	assertCredSchemeRejection(t, err.Error(), "marketplace-sources.yaml")
}

// TestCatalogSourceScheme_AllowlistNamesTheListWord pins that an
// allowlisted scheme is still named — the existing operator-help behaviour
// — and that the word printed is the LIST's spelling, not the supplied
// bytes' casing.
func TestCatalogSourceScheme_AllowlistNamesTheListWord(t *testing.T) {
	for _, supplied := range []string{"http", "HTTP", "Http"} {
		t.Run(supplied, func(t *testing.T) {
			t.Setenv(EnvCatalogURLs, supplied+"://catalogs.example.com/catalog.yaml")
			t.Setenv(EnvCatalogAllowPrivate, "")
			t.Setenv(EnvCatalogRefreshInterval, "")

			_, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatal("expected a rejection")
			}
			if !strings.Contains(err.Error(), `scheme "http" is not allowed`) {
				t.Errorf("an allowlisted scheme must be named with the list's word: %q", err.Error())
			}
			// ("HTTPS-only" legitimately contains the letters HTTP, so
			// the no-echo check is on the quoted scheme form, which is
			// the only place a scheme is ever printed.)
			if supplied != "http" && strings.Contains(err.Error(), `scheme "`+supplied+`"`) {
				t.Errorf("the supplied casing %q must not be echoed — only the list's word: %q", supplied, err.Error())
			}
		})
	}
}

// TestCatalogSourceScheme_AllowlistComparisonIsExact proves the allowlist
// match is an exact word match, never a prefix or substring match. A scheme
// that merely starts with, contains, or is a prefix of an allowlisted word
// must fall to the no-echo message — a widened comparison is one step from
// naming supplied bytes again.
func TestCatalogSourceScheme_AllowlistComparisonIsExact(t *testing.T) {
	for _, scheme := range []string{
		"httpx",  // extension of "http" — a prefix match would name it
		"https2", // extension of "https"
		"sshx",   // extension of "ssh"
		"htt",    // prefix of "http" — a reversed prefix match would name it
		"xgitx",  // contains "git" — a substring match would name it
	} {
		t.Run(scheme, func(t *testing.T) {
			t.Setenv(EnvCatalogURLs, scheme+"://catalogs.example.com/catalog.yaml")
			t.Setenv(EnvCatalogAllowPrivate, "")
			t.Setenv(EnvCatalogRefreshInterval, "")

			_, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatal("expected a rejection")
			}
			msg := err.Error()
			if strings.Contains(msg, `scheme "`) {
				t.Errorf("scheme %q is not on the allowlist and must not be named (exact match only): %q", scheme, msg)
			}
			if !strings.Contains(msg, "uses a scheme other than https") {
				t.Errorf("off-allowlist scheme must get the structure-only reason: %q", msg)
			}
		})
	}
}

// TestCatalogSourceScheme_AllowlistIsFixedAndHarmless is the guard on the
// allowlist itself: it is a LIST, walked entry by entry, compared with an
// EXACT expected list — it fails on growth AND on a stale entry, and it
// refuses to pass vacuously (an empty list is fatal). Every entry printed
// into a startup error must be one of these known scheme words; adding
// anything else means printing it verbatim to stderr.
func TestCatalogSourceScheme_AllowlistIsFixedAndHarmless(t *testing.T) {
	want := []string{"http", "ftp", "file", "oci", "git", "ssh"}
	if len(knownHarmlessSchemes) == 0 {
		t.Fatal("knownHarmlessSchemes is empty — the guard would pass vacuously")
	}
	if len(knownHarmlessSchemes) != len(want) {
		t.Fatalf("knownHarmlessSchemes changed size: got %d entries %v, want exactly %d %v — a new entry is printed verbatim into a startup error and needs this review",
			len(knownHarmlessSchemes), knownHarmlessSchemes, len(want), want)
	}
	for i, w := range want {
		if knownHarmlessSchemes[i] != w {
			t.Errorf("knownHarmlessSchemes[%d] = %q, want %q — the list is fixed; changing it is a reviewed decision", i, knownHarmlessSchemes[i], w)
		}
	}
}
