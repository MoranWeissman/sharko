package api

// trusted_proxy_docs_test.go — the security page's own example must not
// reopen the hole the page is about.
//
// docs/site/operator/security.md used to show:
//
//	extraEnv:
//	  - name: SHARKO_TRUSTED_PROXIES
//	    value: "10.0.0.0/8"
//
// The line was older than the enforcement, and harmless while nothing read
// the setting. Making the setting real made it dangerous: in a normal
// Kubernetes cluster the pod network is inside 10.0.0.0/8, so an operator
// copying that example trusts every pod. A compromised addon or a tenant
// workload can then reach Sharko's port directly, set its own
// X-Forwarded-For, and get a fresh identity per request. Demonstrated against
// the real router: a pod at 10.244.3.17 made 80 login attempts rotating the
// header and not one was refused, against a limit of 5 a minute.
//
// The page's own warning further down already said "List only proxies you
// control". The example contradicted it, and an operator copies the example.
//
// This guard reads the shipped page. TestPodNetworkRange_IsARealBypass below
// is the other half: it proves, through the production router, that the value
// the page used to suggest really does hand out that bypass.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// securityPagePath is the operator page that documents SHARKO_TRUSTED_PROXIES.
const securityPagePath = "docs/site/operator/security.md"

// podNetworkRanges are the values that do not name a proxy — they name
// everything that can open a socket. The first three are the private ranges a
// Kubernetes pod network is normally carved out of; the last three are the
// explicit spellings of "everyone".
//
// The list still holds all six, and deliberately. Which of them is a finding
// is not decided here — see mentioningIsHarmless below.
var podNetworkRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"0.0.0.0/0",
	"::/0",
	"*",
}

// mentioningIsHarmless reports whether the page may safely NAME this value.
//
// # Why this exists, and why it is not an exemption list
//
// The page has to be able to warn people who are upgrading. Older Sharko docs
// published SHARKO_TRUSTED_PROXIES="*" as advice, and an operator who copied
// it now gets a server that refuses to start with no idea why. The warning is
// findable only if it prints the value they have — so a guard that bans the
// page from containing the characters cannot tell "do this" from "if you have
// this, delete it", and blocks the fix for the very hole it is about.
//
// The honest split is not about wording, which anyone can fake. It is about
// what the SERVER does with the value:
//
//   - A value ParseTrustedProxies REFUSES cannot hurt whoever copies it. They
//     get a startup failure naming the setting, not a silent over-trust. The
//     page may name it.
//   - A value ParseTrustedProxies ACCEPTS leaves the copier with a running
//     server and a hole they cannot see. That stays banned outright, wherever
//     on the page it appears and whatever prose surrounds it.
//
// So this asks the production parser rather than carrying a second list that
// could drift away from it. That makes the guard STRONGER than the version
// that hardcoded six strings: if a change ever made "*" parse successfully
// again, this returns false the same day and the page that names it starts
// failing — no one has to remember to update a list.
func mentioningIsHarmless(entry string) bool {
	_, err := ParseTrustedProxies(entry)
	return err != nil
}

// trustedProxiesValueRe finds every value assigned to SHARKO_TRUSTED_PROXIES
// on the page, in either of the two shapes the docs use: the Helm extraEnv
// block (name: … / value: "…") and an inline SHARKO_TRUSTED_PROXIES="…".
var trustedProxiesValueRe = regexp.MustCompile(
	`(?s)SHARKO_TRUSTED_PROXIES(?:"?\s*\n\s*value:\s*"([^"]*)"|\s*=\s*"?([^"\s` + "`" + `]+)"?)`)

func repoRootForDocsGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find go.mod walking up from the working directory")
	return ""
}

// TestSecurityPage_TrustedProxyExampleIsNotAWholeNetwork reads the shipped
// page and fails if any value it suggests for SHARKO_TRUSTED_PROXIES would
// trust a whole private network.
func TestSecurityPage_TrustedProxyExampleIsNotAWholeNetwork(t *testing.T) {
	root := repoRootForDocsGuard(t)
	body, err := os.ReadFile(filepath.Join(root, securityPagePath))
	if err != nil {
		t.Fatalf("reading %s: %v", securityPagePath, err)
	}

	matches := trustedProxiesValueRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatalf("%s no longer shows any SHARKO_TRUSTED_PROXIES value — either the page "+
			"stopped documenting the setting, or this guard's pattern has gone stale and "+
			"is now watching nothing", securityPagePath)
	}

	for _, m := range matches {
		value := m[1]
		if value == "" {
			value = m[2]
		}
		for _, entry := range strings.Split(value, ",") {
			entry = strings.TrimSpace(entry)
			if mentioningIsHarmless(entry) {
				// The server refuses this value at startup, so naming it on
				// the page cannot leave anybody quietly over-trusting. The
				// page needs to name it to warn upgraders.
				continue
			}
			for _, bad := range podNetworkRanges {
				if entry == bad {
					t.Errorf("%s suggests SHARKO_TRUSTED_PROXIES=%q, and the server ACCEPTS that "+
						"value. In a normal Kubernetes cluster the pod network sits inside %s, so "+
						"this trusts every pod: any workload on the cluster can reach the port "+
						"directly, set its own X-Forwarded-For, and never hit the login rate "+
						"limit. List the proxy addresses themselves.", securityPagePath, value, bad)
				}
			}
		}
	}
}

// TestMentioningIsHarmless_MatchesWhatTheServerReallyDoes pins the reasoning
// the guard above leans on, so it is checked rather than assumed.
//
// Every value in podNetworkRanges must fall into exactly one bucket, and which
// bucket it is in must come from the parser:
//
//   - "*", "0.0.0.0/0" and "::/0" are REFUSED, so the page may name them and
//     the upgrade warning is possible.
//   - the three private ranges are ACCEPTED, so the page may not, and the ban
//     on them is doing real work rather than restating a startup failure.
//
// If someone widens the parser to accept "*" again, this test fails here
// first, with a message saying the docs guard has just gone quiet.
func TestMentioningIsHarmless_MatchesWhatTheServerReallyDoes(t *testing.T) {
	refused := map[string]bool{"*": true, "0.0.0.0/0": true, "::/0": true}

	for _, entry := range podNetworkRanges {
		harmless := mentioningIsHarmless(entry)
		if refused[entry] && !harmless {
			t.Errorf("%q is supposed to be refused at startup, but ParseTrustedProxies accepted "+
				"it. An operator who copies it now gets a running server that trusts everyone, "+
				"and the docs guard has stopped objecting to the page naming it.", entry)
		}
		if !refused[entry] && harmless {
			t.Errorf("%q is supposed to be ACCEPTED by the parser — that is why naming it on the "+
				"page is banned. ParseTrustedProxies rejected it instead, which means the ban on "+
				"it now costs nothing and is only restating a startup failure.", entry)
		}
	}
}

// TestSecurityPage_WarnsUpgradersAboutTheStarValue — the ruling that made "*"
// fail at startup also required the page to tell the people who copied the old
// advice what happened to them. A page that merely stopped recommending "*"
// leaves them with a CrashLoopBackOff and nothing to search for.
func TestSecurityPage_WarnsUpgradersAboutTheStarValue(t *testing.T) {
	root := repoRootForDocsGuard(t)
	body, err := os.ReadFile(filepath.Join(root, securityPagePath))
	if err != nil {
		t.Fatalf("reading %s: %v", securityPagePath, err)
	}
	page := string(body)

	// The exact startup error, so a log search lands on this page.
	const startupError = "trusts every address; list each proxy address or CIDR range explicitly"
	if !strings.Contains(page, startupError) {
		t.Errorf("%s does not quote the startup error an operator with \"*\" will actually see "+
			"(%q). That string is what they will paste into a search box.", securityPagePath, startupError)
	}
	if !strings.Contains(page, "will not start") {
		t.Errorf("%s does not say plainly that the server will not start, so somebody scanning "+
			"the page for their symptom will walk past the section that explains it", securityPagePath)
	}
}

// TestSecurityPage_ExplainsWhyABroadRangeIsWrong — an operator who is only
// told "do not do that" invents it again the next time the setting looks
// inconvenient. The page has to say what goes wrong.
func TestSecurityPage_ExplainsWhyABroadRangeIsWrong(t *testing.T) {
	root := repoRootForDocsGuard(t)
	body, err := os.ReadFile(filepath.Join(root, securityPagePath))
	if err != nil {
		t.Fatalf("reading %s: %v", securityPagePath, err)
	}
	page := strings.ToLower(string(body))

	for _, phrase := range []string{"pod network", "every pod"} {
		if !strings.Contains(page, phrase) {
			t.Errorf("%s does not explain why a broad private range is the wrong value "+
				"(missing %q). The reason is the part that stops an operator reinventing it.",
				securityPagePath, phrase)
		}
	}
}

// TestSecurityPage_DocumentsTheXRealIPRule — the page lists the rules Sharko
// follows for a trusted peer, and X-Real-IP is one of them. It was resolved by
// a second, differently-written code path and appeared nowhere on the page.
func TestSecurityPage_DocumentsTheXRealIPRule(t *testing.T) {
	root := repoRootForDocsGuard(t)
	body, err := os.ReadFile(filepath.Join(root, securityPagePath))
	if err != nil {
		t.Fatalf("reading %s: %v", securityPagePath, err)
	}
	page := strings.ToLower(string(body))

	if !strings.Contains(page, "x-real-ip") {
		t.Fatalf("%s never mentions X-Real-IP, which Sharko does resolve from", securityPagePath)
	}
	// The two halves of the rule: when it is read at all, and which one wins.
	if !strings.Contains(page, "only when there is no `x-forwarded-for`") {
		t.Errorf("%s does not say that X-Real-IP is read only when X-Forwarded-For is absent",
			securityPagePath)
	}
	if !strings.Contains(page, "the **last** one wins") {
		t.Errorf("%s does not say which X-Real-IP instance wins; taking the first one is how a "+
			"client's spoof passed through a proxy that appends rather than replaces",
			securityPagePath)
	}
}

// TestPodNetworkRange_IsARealBypass is the executable half of the docs guard:
// it shows, through the production router, that the value the page used to
// suggest really does destroy the login rate limit.
//
// The caller here is a pod. It is not a proxy, it was never meant to be
// trusted, and it is inside 10.0.0.0/8 — which is the entire point.
func TestPodNetworkRange_IsARealBypass(t *testing.T) {
	const podPeer = "10.244.3.17:52000"

	// What the page used to suggest.
	asDocumented := routerTrusting(t, "10.0.0.0/8")
	if n := firstThrottled(t, func(i int) int {
		return login(t, asDocumented, podPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	}); n != 0 {
		t.Fatalf("a pod inside the trusted range was refused on attempt %d — this test no "+
			"longer demonstrates the bypass the docs guard exists to prevent, so the guard "+
			"is asserting something that costs nothing", n)
	}

	// What the page suggests now: the proxy's own address. Same pod, same
	// rotating header, and the limiter stops it.
	asFixed := routerTrusting(t, "10.42.0.11,10.42.0.12")
	mustThrottle(t, "a pod rotating X-Forwarded-For when only the proxies are listed", func(i int) int {
		return login(t, asFixed, podPeer, fmt.Sprintf("203.0.113.%d", i%250+1))
	})

	// Control: the real proxy still works. Two clients behind it keep their
	// own allowances, so the fix did not simply switch trust off.
	if code := login(t, asFixed, "10.42.0.11:40000", "203.0.113.240"); code == http.StatusTooManyRequests {
		t.Fatal("a client behind a listed proxy was refused, so listing proxies by address is not usable")
	}
}
