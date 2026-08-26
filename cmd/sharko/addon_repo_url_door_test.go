// addon_repo_url_door_test.go — the CLI turns away a chart repository address
// that has somewhere in it for a credential to sit, before it sends anything.
//
// The server refuses too, by asking the same function, so this is not a second
// rule and not the thing standing between a credential and Git. It is the
// early word: the operator is typing right now, and hearing the rule now is
// better than hearing it after a round trip.
//
// The test proves it never sends: the fake server fails the test if it is
// contacted at all.
package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// cliDoorSecret is the stand-in for an operator's token.
const cliDoorSecret = "P4ss-w0rd-the-operator-actually-stored-8v3n"

// cliDoorSweptSecret is the same text written independently.
const cliDoorSweptSecret = "P4ss-w0rd-the-operator-actually-stored-8v3n"

// cliDoorRuleSentence is typed out in full rather than read from the code.
const cliDoorRuleSentence = "Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL."

func TestCLIDoorSentinelsAgree(t *testing.T) {
	if cliDoorSecret != cliDoorSweptSecret || cliDoorSecret == "" {
		t.Fatalf("planted %q and swept %q disagree, or are empty", cliDoorSecret, cliDoorSweptSecret)
	}
	if credsafe.UnsupportedRepoURLMessage != cliDoorRuleSentence {
		t.Errorf("the sentence the CLI says has changed.\n  now:  %s\n  want: %s", credsafe.UnsupportedRepoURLMessage, cliDoorRuleSentence)
	}
}

func TestAddAddonCLI_RefusesAnUnsupportedRepoURLWithoutSendingAnything(t *testing.T) {
	addresses := map[string]string{
		"userinfo with a password":                     "https://git-user:" + cliDoorSecret + "@charts.example/org/charts",
		"userinfo without a password":                  "https://" + cliDoorSecret + "@charts.example/org/charts",
		"a query string":                               "https://charts.example/org/charts?access_token=" + cliDoorSecret,
		"a fragment":                                   "https://charts.example/org/charts#" + cliDoorSecret,
		"an ordinary query string, refused on purpose": "https://charts.example/org/charts?ref=main",
	}
	if len(addresses) != 5 {
		t.Fatalf("the address table has %d entries, want exactly 5", len(addresses))
	}

	for name, addr := range addresses {
		t.Run(name, func(t *testing.T) {
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("the CLI sent %s %s — it should have refused the address before contacting anything", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusOK)
			})

			resetFlags(addAddonCmd)
			setFlags(t, addAddonCmd, map[string]string{
				"chart":   "leaky",
				"repo":    addr,
				"version": "1.0.0",
			})

			err := addAddonCmd.RunE(addAddonCmd, []string{"leaky"})
			if err == nil {
				t.Fatal("the CLI accepted the address")
			}
			var typed *credsafe.UnsupportedRepoURLError
			if !strings.Contains(err.Error(), cliDoorRuleSentence) {
				t.Errorf("the refusal does not state the rule.\ngot: %q", err.Error())
			}
			if !asUnsupported(err, &typed) {
				t.Errorf("the refusal is not a *credsafe.UnsupportedRepoURLError: %v", err)
			}

			// Positive control before the absence check: the sweep really can
			// see this text when it is there.
			if !strings.Contains(addr, cliDoorSweptSecret) && !strings.Contains(addr, "ref=main") {
				t.Fatal("the address under test carries neither the planted secret nor the ordinary query string — the fixture has drifted")
			}
			if strings.Contains(err.Error(), cliDoorSweptSecret) {
				t.Errorf("the refusal repeats the planted secret: %q", err.Error())
			}
			if strings.Contains(err.Error(), "git-user") {
				t.Errorf("the refusal repeats the user half of the address: %q", err.Error())
			}
		})
	}
}

// TestAddAddonCLI_LetsACleanAddressThrough is the positive control: a CLI that
// refused everything would pass every case above. Here the fake server MUST be
// contacted.
func TestAddAddonCLI_LetsACleanAddressThrough(t *testing.T) {
	var contacted bool
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pr_url":"https://git.example/pr/1"}`))
	})

	resetFlags(addAddonCmd)
	setFlags(t, addAddonCmd, map[string]string{
		"chart":   "leaky",
		"repo":    "https://charts.example/org/charts",
		"version": "1.0.0",
	})

	// The fake server's reply shape is not what this test is about — what
	// matters is that the CLI got past the door and sent the request at all.
	err := addAddonCmd.RunE(addAddonCmd, []string{"leaky"})
	var typed *credsafe.UnsupportedRepoURLError
	if asUnsupported(err, &typed) {
		t.Fatalf("a perfectly ordinary address was turned away at the door: %v", err)
	}
	if !contacted {
		t.Error("the CLI never contacted the server for a clean address, so the 'never sends' checks above prove nothing")
	}
}

// asUnsupported keeps errors.As out of the assertion line so the intent reads
// plainly.
func asUnsupported(err error, target **credsafe.UnsupportedRepoURLError) bool {
	for e := err; e != nil; {
		if t, ok := e.(*credsafe.UnsupportedRepoURLError); ok {
			*target = t
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
