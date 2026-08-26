package serverrender

// bf11_addresses_test.go — the three addresses the chart writes into the Pod
// as ordinary environment values, and the rule that makes calling them
// non-secret true.
//
// # What was wrong
//
// values.yaml declares connection.git.repoURL, connection.argocd.serverURL
// and ai.baseURL as NON-SECRET, and the Deployment writes each one into the
// Pod as a plain `value:`. But the chart accepted every credential-bearing
// form of them — https://<token>@host/org/repo, ?access_token=..., #<token> —
// so the word non-secret was a hope about how operators would fill the field
// in, not something the chart made true. Anyone who could read the Deployment
// could read whatever the operator had put there.
//
// # Which of the two ways out this took
//
// Credentials in these addresses were never a supported way to authenticate.
// The Git token and the ArgoCD token live in the encrypted connection Secret;
// the AI credential is ai.apiKey and goes into the chart's Secret;
// models.GitRepoConfig.ParseRepoURL never reads the userinfo section of the
// address at all. So no operator loses a working setup, and the chart now
// REFUSES the credential-bearing forms at render time. The values are then
// genuinely non-secret, which is what the Deployment writing them in the clear
// has always assumed.
//
// # There is one rule, written twice, and pinned
//
// internal/credsafe owns this classification for Go code. A chart cannot call
// Go, so _helpers.tpl states the rule again in template syntax — and a rule
// written twice is a rule that drifts. TestTheChartAndCredsafeAgree runs both
// over one shared table and fails when they disagree about an address in that
// table. That is a check over a list; it says nothing about an address the
// list does not hold.
//
// # What BF12 changed here
//
// The first version of both rules stopped looking at an address that had no
// "<scheme>://" in front of it, and called it credential-free. So
// user:PASSWORD@git.example/o/r.git was written into the Deployment in the
// clear. The shared table never caught it, because the table had no
// scheme-less entry in it at all and the two rules were wrong the same way.
//
// Both rules now read a scheme-less address as a network-path reference — the
// same thing net/url does with "//" in front — and both refuse anything they
// cannot read. The table below has scheme-less and scheme-relative entries in
// it now, in both directions, which is the part that was missing.
//
// # What BF13 changed here
//
// The table below is still hand-written, and a hand-written table is still how
// a whole class of address stayed invisible twice. It is no longer the main
// source: testdata/address-rule-corpus.yaml holds the addresses written down
// from the rule itself, and bf13_chart_corpus_test.go runs both copies of the
// rule over that list AND over everything in this table. Nothing here was
// dropped — every address it was watching is still watched, and now by the
// stronger check, which compares the two copies' actual verdicts rather than
// just whether each refused.
//
// chartIsKnownToLagOn is empty. The chart used to be the looser of the two on
// twenty-one of the addresses in that list, and it is not looser on any of
// them any more.

import (
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// addressField is one of the three values the chart writes into the Pod, with
// the environment variable it lands in.
type addressField struct {
	valuesPath string
	envName    string
	// extraSet is anything else that has to be set for the environment entry
	// to be rendered at all.
	extraSet []string
}

// bf11AddressRenderRecipes says, for each address the rule checks, which
// environment entry it lands in and what else has to be set for the chart to
// render that entry at all.
//
// The KEYS are not a hand-written list of what the rule covers —
// TestEveryAddressTheRuleChecksIsExercisedHere compares them against
// validatedAddresses, which TestTheRuleChecksExactlyTheAddressesClaimedHere
// in turn compares against the actual includes in _helpers.tpl, which
// TestEveryOperatorSetEnvironmentValueIsClassified in turn compares against
// every value the chart writes. A fifth address cannot be added anywhere in
// that chain without a test failing.
var bf11AddressRenderRecipes = map[string]addressField{
	"connection.git.repoURL":      {envName: "SHARKO_CONN_GIT_REPO_URL"},
	"connection.argocd.serverURL": {envName: "SHARKO_CONN_ARGOCD_SERVER_URL"},
	"ai.baseURL": {envName: "AI_BASE_URL", extraSet: []string{
		"--set", "ai.enabled=true", "--set-string", "ai.provider=custom-openai",
	}},
	"ai.ollama.url": {envName: "AI_OLLAMA_URL", extraSet: []string{
		"--set", "ai.enabled=true", "--set-string", "ai.provider=ollama",
		"--set", "ai.ollama.deploy=false",
	}},
}

// bf11AddressFields is the same thing as a stable, sorted slice, so a failure
// names the same field every run.
var bf11AddressFields = addressFieldsInOrder()

func addressFieldsInOrder() []addressField {
	paths := make([]string, 0, len(bf11AddressRenderRecipes))
	for p := range bf11AddressRenderRecipes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]addressField, 0, len(paths))
	for _, p := range paths {
		f := bf11AddressRenderRecipes[p]
		f.valuesPath = p
		out = append(out, f)
	}
	return out
}

// TestEveryAddressTheRuleChecksIsExercisedHere closes the loop: an address
// added to sharko.validateAddresses but not given a recipe here would be
// enforced and never tested, and one given a recipe but dropped from the rule
// would be tested and never enforced.
func TestEveryAddressTheRuleChecksIsExercisedHere(t *testing.T) {
	var exercised []string
	for p := range bf11AddressRenderRecipes {
		exercised = append(exercised, p)
	}
	sort.Strings(exercised)
	want := append([]string{}, validatedAddresses...)
	sort.Strings(want)
	if strings.Join(exercised, ",") != strings.Join(want, ",") {
		t.Fatalf("the rule checks %v; the tests below exercise %v", want, exercised)
	}
	if len(exercised) == 0 {
		t.Fatal("no address is exercised, so every test below runs over nothing")
	}
	// Each recipe must actually make its environment entry appear, or the
	// round-trip check is silently skipping that field.
	for _, f := range bf11AddressFields {
		stdout, stderr, err := renderWithAddress(t, f, "https://example.internal/path")
		if err != nil {
			t.Fatalf("the recipe for %s does not render: %s", f.valuesPath, stderr)
		}
		if got := envValueIn(t, stdout, f.envName); got != "https://example.internal/path" {
			t.Errorf("the recipe for %s does not make %s appear with the value set", f.valuesPath, f.envName)
		}
	}
}

// renderWithAddress renders the chart with one address set.
func renderWithAddress(t *testing.T, f addressField, address string) (stdout, stderr string, err error) {
	t.Helper()
	args := append([]string{}, f.extraSet...)
	args = append(args, "--set-string", f.valuesPath+"="+address)
	return runHelmTemplate(t, args...)
}

// --------------------------------------------------------------------------
// case 1 — ordinary credential-free addresses keep working
// --------------------------------------------------------------------------

// supportedAddresses are shapes an operator legitimately writes today. Every
// one of them must still render, and must still reach the Pod unchanged.
var supportedAddresses = []string{
	"https://github.com/org/repo",
	"https://github.com/org/repo.git",
	"https://github.example.com:8443/org/repo",
	"https://dev.azure.com/org/project/_git/repo",
	"http://gitea.internal/org/repo",
	// written with no scheme at all. These are the shapes the BF12 fix had
	// to keep working while it closed the scheme-less hole, so they are the
	// half of that change that is easiest to break by accident.
	"github.com/org/repo",
	"github.com/org/repo@v1", // an "@" in the PATH is not user information
	"localhost:8080",
	"[::1]:8080",
	"sharko.example",
	"//github.com/org/repo", // written as a network-path reference already
}

// TestOrdinaryAddressesStillWork is the "nothing was broken" half.
//
// It does not merely check that helm did not fail: it reads the value back out
// of the rendered Pod and compares it to what went in. A rule that quietly
// rewrote the address would pass a render check and break the install.
func TestOrdinaryAddressesStillWork(t *testing.T) {
	for _, f := range bf11AddressFields {
		for _, address := range supportedAddresses {
			t.Run(f.valuesPath+"="+address, func(t *testing.T) {
				stdout, stderr, err := renderWithAddress(t, f, address)
				if err != nil {
					t.Fatalf("the chart refused an ordinary credential-free address.\n%s", stderr)
				}
				got := envValueIn(t, stdout, f.envName)
				if got != address {
					t.Errorf("%s reached the Pod as %q, but the operator wrote %q. The chart must not "+
						"rewrite an address it accepts.", f.envName, got, address)
				}
			})
		}
	}
}

// --------------------------------------------------------------------------
// case 2 — credential-bearing addresses are refused, quietly
// --------------------------------------------------------------------------

// bf11AddressSentinel is planted inside each refused address so the refusal
// can be checked for echoing it.
const bf11AddressSentinel = "BF11-PLANTED-URL-CREDENTIAL-4d81be"

// refusedAddresses carry the sentinel in each of the three carriers a
// credential can travel in, with and without a scheme in front, plus the
// shapes that carry no sentinel but must still be refused.
//
// The four scheme-less entries are the ones measured leaking into a rendered
// Deployment before BF12. They are here by their actual shape rather than as
// a note in a comment, so the day the scheme-less reading is loosened again
// this file goes red.
var refusedAddresses = map[string]string{
	"a token as the whole userinfo":     "https://" + bf11AddressSentinel + "@github.com/org/repo",
	"a token as the userinfo password":  "https://x-access-token:" + bf11AddressSentinel + "@github.com/org/repo",
	"a token in the query string":       "https://github.com/org/repo?access_token=" + bf11AddressSentinel,
	"a token in the fragment":           "https://github.com/org/repo#" + bf11AddressSentinel,
	"an ordinary query with no token":   "https://github.com/org/repo?ref=main",
	"userinfo with an ssh-style scheme": "ssh://" + bf11AddressSentinel + "@github.com/org/repo",

	// no scheme in front — every one of these rendered into the pod before BF12
	"no scheme, a password in the userinfo":    "user:" + bf11AddressSentinel + "@git.example/o/r.git",
	"no scheme, a token in the query":          "git.example/r?access_token=" + bf11AddressSentinel,
	"no scheme, a token in the fragment":       "git.example/r#" + bf11AddressSentinel,
	"a network-path reference with a password": "//u:" + bf11AddressSentinel + "@git.example/r",

	// read whole, but nothing in them to hide a credential behind a name
	"an scp-style Git remote, which Sharko does not support": "git@github.com:org/repo.git",
	"empty userinfo, which is still userinfo":                "https://user@/org/repo",
	"a port that is not a number, so it cannot be read":      "https://github.com:notaport/org/repo",
}

// TestCredentialBearingAddressesAreRefused is the enforcement half.
func TestCredentialBearingAddressesAreRefused(t *testing.T) {
	for _, f := range bf11AddressFields {
		for what, address := range refusedAddresses {
			t.Run(f.valuesPath+" with "+what, func(t *testing.T) {
				_, stderr, err := renderWithAddress(t, f, address)
				if err == nil {
					t.Fatalf("the chart rendered with %s in %s. values.yaml calls that field non-secret "+
						"and the Deployment writes it into the Pod in the clear, so accepting this "+
						"shape puts the value in front of anyone who can read a Deployment.",
						what, f.valuesPath)
				}
				if !strings.Contains(stderr, f.valuesPath) {
					t.Errorf("the refusal does not say which setting is at fault; an operator cannot act "+
						"on it. It said:\n%s", stderr)
				}
			})
		}
	}
}

// TestARefusalNeverEchoesTheAddress checks the refusal text itself.
//
// A refusal travels further than the terminal it was written for — into a
// shell history, a CI log, a bug report. Naming the setting is help; quoting
// the value is a second copy of the credential.
func TestARefusalNeverEchoesTheAddress(t *testing.T) {
	var checked int
	for _, f := range bf11AddressFields {
		for what, address := range refusedAddresses {
			if !strings.Contains(address, bf11AddressSentinel) {
				continue // the ordinary "?ref=main" case carries nothing to look for
			}
			checked++
			stdout, stderr, err := renderWithAddress(t, f, address)
			if err == nil {
				t.Fatalf("%s in %s was accepted, so there is no refusal to inspect", what, f.valuesPath)
			}
			for label, text := range map[string]string{"the refusal": stderr, "what was rendered": stdout} {
				if strings.Contains(text, bf11AddressSentinel) {
					t.Errorf("%s for %s in %s carries the value the operator supplied", label, what, f.valuesPath)
				}
			}
			// Nor any run of it. A refusal that quoted "the last eight
			// characters" would still be handing part of a credential on.
			for size := 8; size < len(bf11AddressSentinel); size += 4 {
				if strings.Contains(stderr, bf11AddressSentinel[:size]) {
					t.Errorf("the refusal for %s in %s carries the first %d characters of the value",
						what, f.valuesPath, size)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no refusal was inspected — this guard checked nothing")
	}
}

// --------------------------------------------------------------------------
// the two copies of the rule must agree
// --------------------------------------------------------------------------

// agreementTable is the one list both classifiers are run over. Every entry
// is an address; what it should be classified as is not written down here on
// purpose — credsafe decides, and the chart has to match it. Writing the
// expected answer down twice would just be a third copy of the rule.
var agreementTable = []string{
	// ordinary, credential-free
	"https://github.com/org/repo",
	"https://github.com/org/repo.git",
	"https://github.example.com:8443/org/repo",
	"http://gitea.internal/org/repo",
	"https://argocd.example.com",
	"https://github.com/org/repo@v2", // an "@" in the PATH is not userinfo
	// written with no scheme, and credential-free. Both rules read these as
	// a network-path reference, which is the whole of the BF12 fix.
	"github.com/org/repo",
	"github.com/org/repo@v1",
	"localhost:8080",
	"[::1]:8080",
	"[fe80::1%25eth0]:80",
	"sharko.example",
	"1.2.3.4:8080",
	"host:", // an empty port — see chartIsKnownToLagOn below
	// written as a network-path reference, and credential-free
	"//github.com/org/repo",
	// credential-bearing, all three carriers
	"https://tokenvalue@github.com/org/repo",
	"https://x-access-token:tokenvalue@github.com/org/repo",
	"https://github.com/org/repo?access_token=tokenvalue",
	"https://github.com/org/repo#tokenvalue",
	"ssh://git@github.com/org/repo.git",
	// credential-bearing with NO scheme in front. These four are the exact
	// shapes measured rendering into a Deployment in the clear before BF12,
	// and their absence from this table is why it stayed quiet.
	"user:pw@git.example/o/r.git",
	"git.example/r?access_token=tokenvalue",
	"git.example/r#tokenvalue",
	"//u:pw@git.example/r",
	// the shapes where the two rules could most easily part company
	"https://github.com/org/repo?ref=main", // an ordinary query is still refused
	"https://github.com/org/repo?",         // a bare "?" is a query as far as net/url is concerned
	"https://github.com/org/repo#",         // a bare "#" is an EMPTY fragment, so it is not
	"https://@github.com/org/repo",         // empty userinfo is still userinfo
	"https://user@/org/repo",               // userinfo and no host at all
	"////user:pw@host",                     // a second "//" would make the userinfo vanish into a path
	"git@github.com:org/repo.git",          // scp-style, which cannot be read and is not supported
	// unreadable, which is a refusal in its own right and never "safe"
	"https://github.com:notaport/org/repo",
	"https://github.com:notaport/org/repo?token=x",
	"https://host/%zz", // a percent escape that is not one
	":8080",            // a port and no host
	"[a b]:80",         // brackets with something that is not an address in them
}

// chartIsKnownToLagOn named the addresses where the Go rule had already been
// tightened and the chart had not caught up yet. It is EMPTY, and it is empty
// because there is nothing left to put in it: the two copies give the same
// answer about every address in the table above and in
// testdata/address-rule-corpus.yaml.
//
// That is agreement about the addresses that have been written down, which is
// not the same as the two copies being one rule. They read what is inside
// square brackets differently — see the note in _helpers.tpl — and neither
// list holds a shape that tells them apart. Nothing in that difference can
// carry a credential.
//
// # Why it is still here with nothing in it
//
// The list is checked in BOTH directions. An address in it that still diverges
// is reported and allowed; an address in it that has STOPPED diverging fails
// the test and says to delete the entry. Emptying it was the acceptance signal
// for that catching-up work, and leaving the mechanism in place is what stops
// the next divergence being written down and then quietly forgotten.
//
// Nothing may be added here to make a NEW divergence quiet. An entry is only
// legitimate when the Go side is the stricter of the two and a named story
// owns closing the gap on the chart side.
var chartIsKnownToLagOn = map[string]string{}

// TestTheChartAndCredsafeAgree runs both copies of the rule over one table.
//
// internal/credsafe.ClassifyAddress is the rule of record. The chart
// cannot call it, so _helpers.tpl says it again in template syntax. This test,
// and the wider sweep in bf13_chart_corpus_test.go, are what stand between
// that and two rules that slowly stop meaning the same — over the addresses
// those two lists hold, and only over those.
func TestTheChartAndCredsafeAgree(t *testing.T) {
	var refusedByBoth, acceptedByBoth int
	for _, address := range agreementTable {
		address := address
		t.Run(address, func(t *testing.T) {
			credsafeRefuses := credsafe.ClassifyAddress(address) != credsafe.AddressCredentialFree
			_, stderr, err := renderWithAddress(t, bf11AddressFields[0], address)
			chartRefuses := err != nil

			if why, lagging := chartIsKnownToLagOn[address]; lagging {
				// A recorded gap. It has to still BE a gap: once the chart
				// catches up, the entry is stale and has to go, and the only
				// way to make sure that happens is to fail here.
				if credsafeRefuses && !chartRefuses {
					t.Logf("known gap, owned by the chart story: %s", why)
					return
				}
				t.Fatalf("this address is listed in chartIsKnownToLagOn, but the two rules now agree "+
					"about it. The gap is closed, so delete the entry — a stale exception is an "+
					"exception nobody is looking at.\n  recorded gap: %s", why)
			}

			switch {
			case credsafeRefuses && !chartRefuses:
				t.Errorf("internal/credsafe treats this address as credential-bearing and the chart "+
					"installs with it. The chart is the LOOSER of the two, which is the direction that "+
					"leaks.\nhelm said: %s", stderr)
			case !credsafeRefuses && chartRefuses:
				t.Errorf("the chart refuses an address internal/credsafe accepts. The two copies of the "+
					"rule have drifted, and an operator is being told no by one half of Sharko and yes "+
					"by the other.\nhelm said: %s", stderr)
			}
			if credsafeRefuses {
				refusedByBoth++
				return
			}
			acceptedByBoth++
		})
	}
	// A table that is all one answer proves only half the rule.
	if refusedByBoth == 0 {
		t.Error("no address in the table was classified as credential-bearing, so the agreement was " +
			"only ever tested in one direction")
	}
	if acceptedByBoth == 0 {
		t.Error("every address in the table was classified as credential-bearing, so nothing proved " +
			"the rule can say yes")
	}
}

// --------------------------------------------------------------------------
// small reader
// --------------------------------------------------------------------------

// envValueIn returns the literal value of one environment variable in the
// Sharko container of a rendered chart.
func envValueIn(t *testing.T, rendered, envName string) string {
	t.Helper()
	res := parseRendered(t, rendered)
	deployment, ok := res.docsByName()[theDeployment]
	if !ok {
		t.Fatalf("no %s in the render", theDeployment)
	}
	spec, _ := deployment["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, raw := range containers {
		c, _ := raw.(map[string]any)
		env, _ := c["env"].([]any)
		for _, rawEnv := range env {
			e, _ := rawEnv.(map[string]any)
			if n, _ := e["name"].(string); n == envName {
				v, _ := e["value"].(string)
				return v
			}
		}
	}
	t.Fatalf("no container in the render sets %s, so this reader is not seeing what it thinks it is", envName)
	return ""
}
