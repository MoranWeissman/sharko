package credsafe

// repourl_carrier_test.go — ClassifyAddress, the decision that used to be made
// twice (BF1) and then used to fail OPEN (BF12).
//
// The bool it replaced said "no credential" for three different situations:
// the string held none of "@", "?" or "#"; net/url refused to parse it; or the
// parse produced no scheme and no host. Only the first is an answer. The other
// two are "I could not tell", and calling that false is how a password reached
// a Kubernetes Deployment in the clear.
//
// Every expected answer below is typed out by hand. Nothing here derives an
// answer from credentialBearingRunes or from the code under test — a fixture
// that reads the same constant as the code moves whenever the code moves, and
// then it proves only that one thing equals itself.

import (
	"net/url"
	"strings"
	"testing"
)

// TestAddressVerdict_ZeroValueIsTheUnsafeOne is the first thing to check and
// the easiest to lose. A struct field nobody filled in, a variable declared and
// not assigned, a map miss — all of them hand back the zero value, and the zero
// value has to be a refusal or every one of those becomes a printed credential.
func TestAddressVerdict_ZeroValueIsTheUnsafeOne(t *testing.T) {
	var unset AddressVerdict
	if unset == AddressCredentialFree {
		t.Fatal("the zero value of AddressVerdict is the SAFE state. A verdict nobody assigned now reads as " +
			"'this address is fine', which is the exact shape of the defect BF12 was opened for.")
	}
	if unset != AddressUnclassifiable {
		t.Fatalf("the zero value is %v, want the unclassifiable state", unset)
	}
	// And the one safe state must not be the zero of anything.
	if AddressCredentialFree == 0 {
		t.Fatal("AddressCredentialFree is 0, so it is the zero value")
	}
}

// TestClassifyAddress_TheContract is the ten-point address contract, one row
// per shape, each expected verdict written out here.
func TestClassifyAddress_TheContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want AddressVerdict
		why  string
	}{
		// Point 1 — an explicit scheme is parsed normally.
		{"a plain https repository", "https://charts.example/org/repo", AddressCredentialFree,
			"an ordinary address with a scheme"},
		{"a plain https repository on a port", "https://charts.example:8443/org/repo", AddressCredentialFree, "same, with a port"},
		{"a plain oci registry", "oci://registry.example/charts", AddressCredentialFree, "any scheme, not just http"},

		// Points 2, 3 and 5 — scheme-less endpoints keep working.
		{"a scheme-less host and port", "localhost:8080", AddressCredentialFree,
			"read as a network-path reference, so this is a host and a port"},
		{"a scheme-less IPv6 host and port", "[::1]:8080", AddressCredentialFree, "the IPv6 form of the same"},
		{"a scheme-less host and path", "charts.example/org/repo", AddressCredentialFree, "a credential-free path"},
		{"a bare word", "embedded", AddressCredentialFree, "read as a network reference, this is the host \"embedded\""},
		// BF13 tightened this one. A bare path has no authority at all, and
		// the grammar requires a valid non-empty host for BOTH accepted
		// shapes, so there is no reading of "/some/path" that is an address.
		// It used to be waved through by a branch that said "no host, and no
		// @ ? or # in the text, so nothing can be hiding" — a character
		// test standing in for a structural one, which is the same kind of
		// reasoning the mistyped-scheme leak walked through.
		{"a bare path", "/some/path", AddressUnclassifiable, "no host, so it is not an address in either accepted shape"},

		// Point 4 — an "@" inside a proven path is not user information.
		{"a version suffix in the path", "github.com/org/repo@v1", AddressCredentialFree,
			"host github.com, path /org/repo@v1 — the @ is in the path"},
		{"a version suffix in the path, with a scheme", "https://github.com/org/repo@v1", AddressCredentialFree, "same, with a scheme"},

		// Point 5 — user information in the authority is refused.
		{"a token in the username position", "https://ghp_example_token@charts.example/org/repo", AddressCarriesCredential, "userinfo"},
		{"a token in the password position", "https://x-access-token:ghp_example_token@charts.example/org/repo", AddressCarriesCredential, "userinfo"},
		{"a token in the username position of an oci registry", "oci://ghp_example_token@registry.example/charts", AddressCarriesCredential, "userinfo"},
		{"a scheme-less address with a password", "user:PASSWORD@git.example/o/r.git", AddressCarriesCredential,
			"the shape that rendered into a Deployment in the clear"},
		{"a network-path address with a password", "//u:PASSWORD@git.example/r", AddressCarriesCredential, "same, written with //"},
		{"an empty userinfo is still userinfo", "https://@charts.example/org/repo", AddressCarriesCredential, "the @ is there"},

		// Point 5 — a query is refused, harmless or not.
		{"a token as a query parameter", "https://charts.example/org/repo?access_token=ghp_example_token", AddressCarriesCredential, "query"},
		{"a token as one query parameter among several", "https://charts.example/index.yaml?ref=main&private_token=x", AddressCarriesCredential, "query"},
		{"an ordinary query", "https://charts.example/index.yaml?ref=main", AddressCarriesCredential,
			"structural, so an innocent query goes the same way as a token"},
		{"an empty query, which is still a query", "https://charts.example/org/repo?", AddressCarriesCredential, "ForceQuery"},
		{"a scheme-less address with a query", "git.example/r?access_token=T", AddressCarriesCredential, "query, no scheme"},

		// Point 5 — a fragment is refused.
		{"a token in the fragment", "https://charts.example/org/repo#ghp_example_token", AddressCarriesCredential, "fragment"},
		{"a scheme-less address with a fragment", "git.example/r#F", AddressCarriesCredential, "fragment, no scheme"},

		// Point 6 — malformed addresses are refused, not waved through.
		{"a non-numeric port", "https://host:notaport/", AddressUnclassifiable,
			"net/url cannot read it, so nothing about it is known"},
		{"a scheme-less non-numeric port", "sharko.example:notaport", AddressUnclassifiable, "same without a scheme"},
		{"a non-numeric port with a credential in it", "https://SENTINELUSER:SENTINELPASS@sharko.example:notaport/api", AddressUnclassifiable,
			"the address that reached http.NewRequest and came back out on a terminal"},

		// Point 7 — an unreadable string holding one of the three characters.
		{"an scp-style git remote", "git@charts.example:org/repo.git", AddressUnclassifiable,
			"Sharko does not support this shape and net/url cannot read it"},
		{"an english sentence with an at sign in it", "not a url at all, just words with an @ in them", AddressUnclassifiable, "unreadable"},
		// Everything before the "#" is a fragment marker, so what is left to
		// be an address is the empty string and there is no host in it. BF13
		// moved this from "carries a credential" to "unreadable" by checking
		// the host before asking about credential positions. Both are
		// refusals, which is all that matters — what must never happen is a
		// third answer.
		{"a yaml comment", "# this is a comment", AddressUnclassifiable, "nothing in front of the # to be a host"},
		{"four slashes, where the userinfo disappears", "////user:pw@host", AddressUnclassifiable,
			"no authority after four slashes, and the string holds an @"},

		// Empty is not an address and is certainly not a safe one.
		{"empty", "", AddressUnclassifiable, "not configured is the caller's business, not a safe address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyAddress(tc.raw)
			if got != tc.want {
				t.Errorf("ClassifyAddress(%q) = %v, want %v (%s)", tc.raw, got, tc.want, tc.why)
			}
		})
	}
}

// TestClassifyAddress_RefusesAPercentEscapeThatSmugglesUserInformation pins
// the percent half of the rule where it is actually doing work.
//
// # Why this test had to be written on purpose
//
// Switching the percent refusal off left the whole suite green, which looked
// like proof the refusal was pointless. It is not. The addresses that were
// exercising it are written with a single layer of encoding —
// "%3A" and "%40" straight in the authority — and net/url refuses those
// itself, so the refusal was standing behind a wall somebody else was holding
// up.
//
// Encode the percent sign as well and net/url stops helping. "%25" is the one
// escape it lets through a host, because a bracketed IPv6 zone identifier
// needs it, so "%2540" arrives as the literal text "%40" sitting inside the
// hostname. net/url reports no user information at all: as far as it is
// concerned the whole thing is one long host name. Decode it once more, which
// is what anything downstream that unescapes a host will do, and the user name
// and password are back.
//
// So this is the shape the refusal exists for, and until now nothing was
// checking it.
func TestClassifyAddress_RefusesAPercentEscapeThatSmugglesUserInformation(t *testing.T) {
	const password = "synthetic-pw-not-real"

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a doubly-encoded name and password in front of the host",
			"https://synthetic-user%253A" + password + "%2540git.example/o/r"},
		{"a doubly-encoded single token in front of the host",
			"https://" + password + "%2540git.example/o/r"},
		{"the same with no scheme at all",
			password + "%2540git.example/o/r"},
		{"the same written as a network reference",
			"//" + password + "%2540git.example/o/r"},
		{"a doubly-encoded at sign pointing the address somewhere else",
			"https://git.example%2540evil.example/o/r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Positive control, in two parts. net/url has to ACCEPT the
			// string, and it has to report no user information — otherwise
			// something else is doing the refusing and this row proves
			// nothing about credsafe's own percent check.
			probe := tc.raw
			if !strings.HasPrefix(probe, "//") && !schemeRe.MatchString(probe) {
				probe = "//" + probe
			}
			u, err := url.Parse(probe)
			if err != nil {
				t.Fatalf("net/url refuses %q on its own (%v), so this row cannot show that credsafe's "+
					"percent check does any work. Use an escape net/url lets through.", tc.raw, err)
			}
			if u.User != nil {
				t.Fatalf("net/url already reads user information out of %q, so the userinfo refusal would "+
					"catch this and the percent check would not be what is being tested", tc.raw)
			}

			if got := ClassifyAddress(tc.raw); got != AddressUnclassifiable {
				t.Errorf("ClassifyAddress(%q) = %v — a percent escape carried user information through an "+
					"unbracketed host and the address was not refused", tc.raw, got)
			}
			if got := SafeRepoURL(tc.raw); got != "" {
				t.Errorf("SafeRepoURL(%q) = %q — the address was handed back instead of withheld", tc.raw, got)
			}
			if got := SafeRepoURL(tc.raw); strings.Contains(got, password) {
				t.Errorf("SafeRepoURL(%q) returned the password: %q", tc.raw, got)
			}
		})
	}
}

// TestClassifyAddress_RefusesASchemeWrittenWithoutItsColon pins the last
// member of the mistyped-scheme family, and pins it where it is doing work.
//
// # Why this one was still open after the other three were closed
//
// A scheme written with one slash, with a digit in front, or with an
// underscore in it all leave a fragment that still carries its colon, so read
// as a scheme-less address the authority is "https:" or "1https:" — a host
// with a port written and left empty, which is refused further down. Nothing
// in that reasoning is about schemes at all, which is why it never covered the
// shape where the COLON is the part that was left out. "https" on its own is a
// perfectly good host name, so the address was read as host "https" with the
// whole of the user information sitting in the path, where an "@" is ordinary
// and deliberately allowed for github.com/org/repo@v1.
//
// # The positive controls
//
// Each row checks, before asking credsafe anything, that net/url reads the
// string without complaint, reports NO user information, and finds a non-empty
// host. That is what makes the row evidence: every other check in the grammar
// is satisfied, so a refusal can only be coming from the doubled-slash rule.
func TestClassifyAddress_RefusesASchemeWrittenWithoutItsColon(t *testing.T) {
	const password = "synthetic-pw-not-real"

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a scheme with its colon left out",
			"https//synthetic-user:" + password + "@git.example/o/r"},
		{"the same with a query written on the end",
			"https//synthetic-user:" + password + "@git.example/o/r?ref=main"},
		{"the same shape with no credential in it at all",
			"https//git.example/o/r"},
		{"a first segment that reads as an ordinary host",
			"localhost//synthetic-user:" + password + "@evil.example/x"},
		{"a first segment that reads as a repository host",
			"git.example//synthetic-user:" + password + "@evil.example/x"},
		{"the doubled slash on its own, with nothing hidden after it",
			"git.example//o/r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Positive control, in three parts. If net/url refuses the
			// string, or reads user information out of it, or finds no
			// host, then something else in the grammar would turn it away
			// and this row would go green without the doubled-slash rule
			// existing at all.
			probe := "//" + tc.raw
			u, err := url.Parse(probe)
			if err != nil {
				t.Fatalf("net/url refuses %q on its own (%v), so this row cannot show that the "+
					"doubled-slash refusal does any work", tc.raw, err)
			}
			if u.User != nil {
				t.Fatalf("net/url already reads user information out of %q, so the userinfo refusal "+
					"would catch this and the doubled-slash rule would not be what is being tested", tc.raw)
			}
			if u.Hostname() == "" {
				t.Fatalf("net/url finds no host in %q, so the host refusal would catch this and the "+
					"doubled-slash rule would not be what is being tested", tc.raw)
			}

			if got := ClassifyAddress(tc.raw); got != AddressUnclassifiable {
				t.Errorf("ClassifyAddress(%q) = %v — an address written with no scheme is a host, an "+
					"optional port and an optional path, and a path there never opens a second authority",
					tc.raw, got)
			}
			if got := SafeRepoURL(tc.raw); got != "" {
				t.Errorf("SafeRepoURL(%q) = %q — the address was handed back instead of withheld", tc.raw, got)
			}
			if got := SafeRepoURL(tc.raw); strings.Contains(got, password) {
				t.Errorf("SafeRepoURL(%q) returned the password: %q", tc.raw, got)
			}
		})
	}
}

// TestClassifyAddress_StillReadsADoubledSlashPathTheOperatorOpenedThemselves is
// the other side of that rule. It only applies where the leading "//" was
// credsafe's own doing. When the operator wrote the "//" — or wrote a scheme
// and its "://" — there is no doubt about where the authority ends, and a
// doubled slash after it is ordinary path text that has to keep working.
func TestClassifyAddress_StillReadsADoubledSlashPathTheOperatorOpenedThemselves(t *testing.T) {
	for _, raw := range []string{
		"https://git.example//o/r",
		"//git.example//o/r",
	} {
		if got := ClassifyAddress(raw); got != AddressCredentialFree {
			t.Errorf("ClassifyAddress(%q) = %v — the authority is delimited by something the operator "+
				"wrote, so what follows it is a path and a doubled slash in a path is not refused", raw, got)
		}
		if got := SafeRepoURL(raw); got != raw {
			t.Errorf("SafeRepoURL(%q) = %q — an operator must see the address they wrote", raw, got)
		}
	}
}

// TestSafeRepoURL_ShowsNothingItsOwnGrammarWillNotVouchFor is the guarantee the
// strip branch used to state in a comment and not check.
//
// The string that branch returns is not the address the grammar read: it is a
// new one, re-rendered by net/url with the credential positions cleared and
// then trimmed. The promise this pins is about that new string — whatever
// SafeRepoURL hands back is either nothing at all, or an address that comes
// back credential-free when it is put through the classifier itself.
func TestSafeRepoURL_ShowsNothingItsOwnGrammarWillNotVouchFor(t *testing.T) {
	const password = "synthetic-pw-not-real"

	stripped, withheld := 0, 0
	for _, raw := range []string{
		"https://synthetic-user:" + password + "@git.example/o/r",
		"https://" + password + "@git.example/o/r",
		"https://@git.example/o/r",
		"synthetic-user:" + password + "@git.example/o/r",
		"//synthetic-user:" + password + "@git.example/o/r",
		"https://git.example:8443@evil.example/o/r",
		"https://git.example/o/r?ref=main",
		"https://git.example/o/r#main",
		"https://git.example/o/r?",
		"git.example/r?access_token=" + password,
		"git.example/r#" + password,
		"https://synthetic-user:" + password + "@git.example//o/r",
		"//synthetic-user:" + password + "@git.example//o/r",
		"https://synthetic-user:" + password + "@[fe80::1%25eth0]:8443/o/r",
		"https//synthetic-user:" + password + "@git.example/o/r",
		"https//synthetic-user:" + password + "@git.example/o/r?ref=main",
		"localhost//synthetic-user:" + password + "@evil.example/x",
		"git.example//synthetic-user:" + password + "@evil.example/x",
		"https://synthetic-user:" + password + "@git.example:notaport/o/r",
		"git@github.com:org/repo.git",
	} {
		got := SafeRepoURL(raw)
		if got == "" {
			withheld++
			continue
		}
		stripped++
		if v := ClassifyAddress(got); v != AddressCredentialFree {
			t.Errorf("SafeRepoURL(%q) = %q, and the classifier says %v about that result. Anything "+
				"shown to a person has to be an address this same grammar vouches for.", raw, got, v)
		}
		if strings.Contains(got, password) {
			t.Errorf("SafeRepoURL(%q) returned the password: %q", raw, got)
		}
	}
	// Both outcomes have to happen, or one half of the promise was never
	// exercised: nothing stripped means the credential-free check below never
	// ran, and nothing withheld means no unreadable address was tried.
	if stripped == 0 {
		t.Fatal("not one address came back with its credential stripped, so the check on the stripped " +
			"result never ran and this test proved nothing")
	}
	if withheld == 0 {
		t.Fatal("not one address was withheld, so no unreadable shape was exercised here")
	}
	t.Logf("%d addresses came back stripped and vouched for, %d were withheld entirely", stripped, withheld)
}

// TestClassifyAddress_StillAcceptsAnIPv6ZoneIdentifier is the other side of
// the percent rule, and it is not decoration. A refusal that also turned away
// the one place a "%" legitimately belongs in an authority would be
// over-strict, and an over-strict rule is what gets loosened later — which is
// how the hole this file exists for was made in the first place.
func TestClassifyAddress_StillAcceptsAnIPv6ZoneIdentifier(t *testing.T) {
	for _, raw := range []string{
		"[fe80::1%25eth0]:8080",
		"https://[fe80::1%25eth0]:8080/o/r",
		"[fe80::1%25eth0]",
	} {
		if got := ClassifyAddress(raw); got != AddressCredentialFree {
			t.Errorf("ClassifyAddress(%q) = %v — inside brackets, %%25 is the required spelling of the "+
				"%% in an IPv6 zone identifier and has to keep working", raw, got)
		}
		if got := SafeRepoURL(raw); got != raw {
			t.Errorf("SafeRepoURL(%q) = %q — an operator must see the address they wrote", raw, got)
		}
	}
}

// TestClassifyAddress_RefusesAControlCharacterNetURLWouldLetThrough pins the
// raw-control-character half of the rule, and pins it where it is actually
// doing work.
//
// # Why this test had to be written on purpose
//
// Switching the control-character refusal off and running everything left the
// whole suite green, which looked like proof that the refusal was pointless.
// It is not. The addresses that were exercising it all carry a C0 control — a
// newline, a carriage return, a tab, a NUL — and net/url refuses every one of
// those by itself, so the refusal was standing behind a wall somebody else was
// already holding up.
//
// The C1 controls, U+0080 to U+009F, are a different matter: they are
// multi-byte in UTF-8, net/url's own control-character scan never looks at
// them, and it will read one as part of a host name without complaint. Those
// are the ones this refusal is the only thing standing in front of.
//
// The control character sits in the MIDDLE of every address on purpose. At
// either end some of them count as whitespace to Go and would be caught by the
// whitespace refusal instead, and then this test would be proving the wrong
// guard.
func TestClassifyAddress_RefusesAControlCharacterNetURLWouldLetThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a next-line control inside the host", "git.exa\u0085mple/o/r"},
		{"a next-line control inside the path", "https://git.example/o/\u0085r"},
		{"the bottom of the C1 range inside the host", "git.exa\u0080mple:8080"},
		{"the top of the C1 range inside the host", "git.exa\u009fmple/o/r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Positive control. If net/url refuses this string by itself,
			// then a refusal below proves nothing about credsafe's own
			// check and this row would go green for the wrong reason —
			// which is exactly what was happening before this test existed.
			probe := tc.raw
			if !strings.HasPrefix(probe, "//") && !schemeRe.MatchString(probe) {
				probe = "//" + probe
			}
			if _, err := url.Parse(probe); err != nil {
				t.Fatalf("net/url refuses %q on its own (%v), so this row cannot show that credsafe's "+
					"control-character refusal does any work. Use a control character net/url lets through.",
					tc.raw, err)
			}

			if got := ClassifyAddress(tc.raw); got != AddressUnclassifiable {
				t.Errorf("ClassifyAddress(%q) = %v — a raw control character anywhere in an address is "+
					"refused, and net/url will not catch this one for us", tc.raw, got)
			}
			if got := SafeRepoURL(tc.raw); got != "" {
				t.Errorf("SafeRepoURL(%q) = %q — an address holding a raw control character was handed "+
					"back instead of withheld", tc.raw, got)
			}
		})
	}
}

// TestClassifyAddress_NoScreenReadingIsTakenAsCredentialFree pins point 8 on
// its own, because it is the half that used to be silently wrong.
//
// Parsed bare, "localhost:8080" reads as scheme "localhost" with the opaque
// part "8080": no host, no user information, nothing that looks like a
// credential — and nothing proven either. Whatever else happens, no reading
// like that may come back as credential-free by accident.
func TestClassifyAddress_NoScreenReadingIsTakenAsCredentialFree(t *testing.T) {
	// The one address where the opaque reading is a real hazard must be read
	// as a host and a port instead.
	if got := ClassifyAddress("localhost:8080"); got != AddressCredentialFree {
		t.Errorf("ClassifyAddress(%q) = %v — a valid scheme-less endpoint must keep working", "localhost:8080", got)
	}
	if got := SafeRepoURL("localhost:8080"); got != "localhost:8080" {
		t.Errorf("SafeRepoURL(%q) = %q — the operator must see the address they wrote", "localhost:8080", got)
	}
	// And every unreadable shape stays a refusal, never a false all-clear.
	for _, raw := range []string{
		"https://host:notaport/",
		"git@charts.example:org/repo.git",
		"http://%zz",
		"://nonsense",
		"# this is a comment",
	} {
		if got := ClassifyAddress(raw); got == AddressCredentialFree {
			t.Errorf("ClassifyAddress(%q) = credential-free, but nothing about this string was ever read", raw)
		}
	}
}

// TestClassifyAddress_AgreesWithSafeRepoURL is the point of one reading: when
// the verdict is credential-free the display must have something to show, and
// when the verdict is unclassifiable the display must show nothing.
func TestClassifyAddress_AgreesWithSafeRepoURL(t *testing.T) {
	checked := 0
	for _, raw := range []string{
		"https://tok@charts.example/org/repo",
		"https://charts.example/org/repo?access_token=tok",
		"https://charts.example/org/repo#tok",
		"https://user:pass@charts.example:8443/org/repo?a=b#c",
		"https://charts.example/org/repo",
		"charts.example/org/repo",
		"localhost:8080",
		"[::1]:8080",
		"github.com/org/repo@v1",
		"embedded",
		"git@charts.example:org/repo.git",
		"https://host:notaport/",
		"# this is a comment",
		"////user:pw@host",
	} {
		checked++
		safe := SafeRepoURL(raw)
		switch ClassifyAddress(raw) {
		case AddressCredentialFree:
			if safe == "" {
				t.Errorf("ClassifyAddress(%q) said credential-free and SafeRepoURL came back empty — the "+
					"operator sees a blank where a perfectly showable address should be", raw)
			}
		case AddressCarriesCredential:
			if safe == raw {
				t.Errorf("SafeRepoURL(%q) handed the address back unchanged while the verdict said it "+
					"carries a credential", raw)
			}
		case AddressUnclassifiable:
			if safe != "" {
				t.Errorf("ClassifyAddress(%q) could not read the address, but SafeRepoURL showed %q anyway — "+
					"display and classification have come apart", raw, safe)
			}
		}
	}
	if checked == 0 {
		t.Fatal("this test examined no address at all, so its silence means nothing")
	}
}

// TestSafeRepoURL_StripsWithoutRewritingTheRest keeps the display half honest:
// the credential goes and the recognisable part stays, in the shape the
// operator wrote it.
func TestSafeRepoURL_StripsWithoutRewritingTheRest(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://tok@charts.example/org/repo", "https://charts.example/org/repo"},
		{"https://x-access-token:tok@charts.example/org/repo", "https://charts.example/org/repo"},
		{"https://charts.example/org/repo?access_token=tok", "https://charts.example/org/repo"},
		{"https://charts.example/org/repo#tok", "https://charts.example/org/repo"},
		{"user:PASSWORD@git.example/o/r.git", "git.example/o/r.git"},
		{"//u:PASSWORD@git.example/r", "//git.example/r"},
		{"git.example/r?access_token=T", "git.example/r"},
		{"charts.example/org/repo", "charts.example/org/repo"},
		{"localhost:8080", "localhost:8080"},
		{"[::1]:8080", "[::1]:8080"},
		{"github.com/org/repo@v1", "github.com/org/repo@v1"},
		{"embedded", "embedded"},
		{"git@charts.example:org/repo.git", ""},
		{"https://host:notaport/", ""},
	} {
		if got := SafeRepoURL(tc.in); got != tc.want {
			t.Errorf("SafeRepoURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRefusalTextNeverEchoesTheValue pins point 10 of the contract. A refusal
// travels — into a shell history, a CI log, a bug report — so the value must
// not travel with it, and the sentence has to say what the allowed structure
// is instead.
func TestRefusalTextNeverEchoesTheValue(t *testing.T) {
	const sentinel = "K7QD-refusal-echo-sentinel-2b8m4p-never-leaves-the-server-a1c5"
	addresses := []string{
		"https://" + sentinel + "@charts.example/org/repo",
		"https://charts.example/org/repo?token=" + sentinel,
		"https://charts.example/org/repo#" + sentinel,
		"https://" + sentinel + ":notaport/org/repo",
		sentinel + ":pw@git.example/r",
	}
	checked := 0
	for _, raw := range addresses {
		for name, err := range map[string]error{
			"the catalog repository rule": ValidateSupportedRepoURLAt("catalog.yaml", "addons.keda.repoURL", raw),
			"the server address rule":     ValidateServerAddressAt("--server", raw),
		} {
			checked++
			if err == nil {
				t.Fatalf("%s accepted %q, so this test proves nothing about its refusal text", name, raw)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Errorf("%s echoed the value back: %q", name, err.Error())
			}
			if strings.Contains(err.Error(), "charts.example") || strings.Contains(err.Error(), "git.example") {
				t.Errorf("%s echoed part of the value back: %q", name, err.Error())
			}
		}
	}
	if checked != len(addresses)*2 {
		t.Fatalf("checked %d refusals, want %d", checked, len(addresses)*2)
	}
	// The sentence has to describe the allowed structure, not only forbid
	// something. Both halves of the rule are named.
	for name, msg := range map[string]string{
		"UnsupportedRepoURLMessage":       UnsupportedRepoURLMessage,
		"UnsupportedServerAddressMessage": UnsupportedServerAddressMessage,
	} {
		for _, phrase := range []string{"a host", "an optional port", "an optional path", "User information", "query string", "fragment", "cannot read"} {
			if !strings.Contains(msg, phrase) {
				t.Errorf("%s does not say %q, so it does not describe the rule Sharko actually applies:\n%s", name, phrase, msg)
			}
		}
	}
}

// TestValidatorsRefuseEveryVerdictThatIsNotCredentialFree is the acceptance
// rule written as a test: a door opens on an explicit credential-free verdict
// and on nothing else.
//
// # Why every answer below is typed out
//
// This test used to work out what it expected by calling the function it was
// checking:
//
//	wantOpen := ClassifyAddress(raw) == AddressCredentialFree
//
// which passes for any classifier at all. Loosen the classifier and the
// expectation loosens with it, in the same direction, in the same run — so the
// guard reports green while the thing it guards has stopped working. That is
// how the mistyped-scheme leak sat here green.
//
// The answers are now constants. The broad, independently written list of
// addresses lives in testdata/address-rule-corpus.yaml and is put through
// these same two doors by TestTheDoorsAgreeWithTheWrittenDownRule; this table
// is the short hand-written one that stays next to the doors themselves.
func TestValidatorsRefuseEveryVerdictThatIsNotCredentialFree(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		open bool
	}{
		{"https://charts.example/org/repo", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"github.com/org/repo@v1", true},
		{"charts.example/org/repo", true},
		{"https://tok@charts.example/org/repo", false},
		{"user:PASSWORD@git.example/o/r.git", false},
		{"//u:PASSWORD@git.example/r", false},
		{"git.example/r?access_token=T", false},
		{"git.example/r#F", false},
		{"https://host:notaport/", false},
		{"git@github.com:org/repo.git", false},
		{"////user:pw@host", false},
		// BF13: a mistyped scheme, with and without a credential in it.
		// Both used to be let through here.
		{"https:/synthetic-user:synthetic-pw-not-real@git.example/o/r", false},
		{"1https://git.example/o/r", false},
		{"ht_tps://git.example/o/r", false},
		{"https://git.example:70000/o/r", false},
		{"https://git.example:/o/r", false},
	} {
		for name, err := range map[string]error{
			"ValidateSupportedRepoURL": ValidateSupportedRepoURL(tc.raw),
			"ValidateServerAddress":    ValidateServerAddress(tc.raw),
		} {
			if tc.open && err != nil {
				t.Errorf("%s refused %q, which the rule allows", name, tc.raw)
			}
			if !tc.open && err == nil {
				t.Errorf("%s accepted %q, which the rule refuses", name, tc.raw)
			}
		}
	}
}
