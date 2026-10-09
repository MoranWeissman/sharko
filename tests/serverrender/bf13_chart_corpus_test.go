package serverrender

// bf13_chart_corpus_test.go — the chart's copy of the address rule, checked
// against the list that was written down separately from both copies.
//
// # Why this file exists on top of the tests already here
//
// bf11_addresses_test.go asks whether the chart and internal/credsafe agree
// with EACH OTHER, over a table typed out by hand next to the fix. Twice that
// was green while a password went into a Deployment in the clear, because the
// table only held the shapes whoever wrote the fix had already thought of.
//
// So the expectations here come from testdata/address-rule-corpus.yaml, which
// was written from the rule, at the repository root, before either copy was
// touched. internal/credsafe is checked against the same file by
// internal/credsafe/repourl_corpus_test.go. Neither side can shape its own
// test data, and neither side is the other's expectation.
//
// # How the chart's verdict is read
//
// A chart cannot be called like a function, so the questions the chart-side
// tests ask have always been "did helm refuse to render". That is a two-state
// answer, and the rule has three states — "could not read it" and "carries a
// credential" are both refusals but they are not the same answer, and folding
// them together is what let "I could not tell" pass for "it is safe" in the
// first place.
//
// So this file asks the chart for the verdict itself. probeChart builds a
// throwaway chart in a temporary directory OUTSIDE the repository whose only
// template prints what sharko.classifyAddress says, using the shipped partial
// templates copied byte for byte out of charts/sharko/templates. The rule
// under test is the one an operator installs; only the caller is different.
//
// # Why the value arrives in a values file and not on the command line
//
// helm's --set-string has an escaping language of its own: commas, backslashes
// and dots mean something to it before the chart ever sees them, and a raw
// newline or NUL does not survive it at all. An address turned away by that
// layer looks exactly like an address the rule refused, and a guard that
// cannot tell those apart reports green for a rule that never ran.
//
// Every address here is handed over in a values file written as JSON, which is
// YAML, with every character outside plain ASCII spelled out as an escape. The
// escaping is the part BF13-4 had to add: helm reads a values file through a
// YAML reader, and a YAML reader will not accept a raw character out of the
// printable range in the stream at all. An address carrying one made helm stop
// before the chart was even loaded, so the rule was never asked — which is a
// different answer from a refusal, and telling those two apart is the whole
// job of this file. Written as escapes, the address arrives at the rule as
// exactly the characters the operator wrote.
//
// The difference is not cosmetic: with the value delivered this way, the
// chart's rule is measurably reached for every row in the list, and
// TestTheProbeReallyRunsTheShippedRule shows it is the rule answering rather
// than a constant.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MoranWeissman/sharko/internal/addresscorpus"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// the three words the rule can answer with. They are spelled out here rather
// than imported from either copy so a rename on one side is a failure rather
// than a silent agreement.
const (
	verdictUnclassifiable = "unclassifiable"
	verdictCarries        = "carries-credential"
	verdictFree           = "credential-free"
)

// loadAddressCorpus reads the written-down list, or stops the test. A list
// that fails to load, or loads empty, must never read as "nothing to check".
func loadAddressCorpus(t *testing.T) []addresscorpus.Row {
	t.Helper()
	rows, err := addresscorpus.Load()
	if err != nil {
		t.Fatalf("the address rule list did not load, so nothing below checked anything: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the address rule list holds no rows, so every check below would pass without looking at an address")
	}
	refused, accepted := addresscorpus.Counts(rows)
	if refused == 0 || accepted == 0 {
		t.Fatalf("the list has %d refused and %d accepted rows — it needs both sides to prove anything", refused, accepted)
	}
	return rows
}

// --------------------------------------------------------------------------
// the probe chart
// --------------------------------------------------------------------------

// probeChartDir builds the throwaway chart once per test and returns its path.
//
// It copies every partial-template file out of charts/sharko/templates by
// WALKING the directory. Naming the files here would mean a rule moved into a
// second partial silently stopped being tested.
//
// # Which files count as a partial, and why the test is not the extension
//
// It used to copy every file whose name ended ".tpl". That is a convention,
// not helm's rule. helm's rule is the leading underscore: a file whose base
// name starts with "_" renders no object of its own, and every other file
// under the templates directory does, whatever it is called. So a rule moved
// into a partial named "_address.yaml" would have been left behind by the old
// copy and this whole file would have gone on measuring a chart that no longer
// held the rule. The leading underscore is what is tested now, and the copy is
// then checked for the rule itself rather than trusted.
func probeChartDir(t *testing.T) string {
	t.Helper()

	root := repoRoot(t)
	shipped := filepath.Join(root, "charts", "sharko", "templates")
	entries, err := os.ReadDir(shipped)
	if err != nil {
		t.Fatalf("cannot read the shipped templates directory: %v", err)
	}

	dir := t.TempDir()
	if strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Fatalf("the probe chart would be built inside the repository at %s. helm renders every "+
			"file under a templates directory, so a stray chart there changes what the real chart "+
			"renders.", dir)
	}
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatalf("cannot make the probe chart's templates directory: %v", err)
	}

	copied := 0
	sawTheRule := false
	for _, e := range entries {
		// A partial holds only {{ define }} blocks and renders nothing of
		// its own, which is exactly what the probe needs: the rule, with
		// none of the chart's objects. helm decides that by the leading
		// underscore, so that is what is tested here.
		if e.IsDir() || !strings.HasPrefix(e.Name(), "_") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(shipped, e.Name()))
		if readErr != nil {
			t.Fatalf("cannot read the shipped partial %s: %v", e.Name(), readErr)
		}
		if strings.Contains(string(body), `define "sharko.classifyAddress"`) {
			sawTheRule = true
		}
		if writeErr := os.WriteFile(filepath.Join(templates, e.Name()), body, 0o644); writeErr != nil {
			t.Fatalf("cannot write the probe copy of %s: %v", e.Name(), writeErr)
		}
		copied++
	}
	if copied == 0 {
		t.Fatal("no partial template was copied out of charts/sharko/templates, so the probe below " +
			"would be asking a chart that does not hold the rule")
	}
	// Copying files is not the same as copying the rule. If the rule has
	// moved into a file this copy does not take, everything below would be
	// asking a chart that cannot answer, and the failure would look like a
	// broken probe rather than an untested rule.
	if !sawTheRule {
		t.Fatalf("none of the %d partial templates copied out of charts/sharko/templates defines "+
			"sharko.classifyAddress, so the probe chart does not hold the rule this file measures", copied)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("cannot write %s for the probe chart: %v", name, err)
		}
	}
	write("Chart.yaml", "apiVersion: v2\nname: sharko-address-probe\nversion: 0.0.0\n")
	write("values.yaml", "probe: \"\"\n")
	if err := os.WriteFile(filepath.Join(templates, "probe.yaml"),
		[]byte("kind: AddressProbe\nverdict: {{ include \"sharko.classifyAddress\" .Values.probe | quote }}\n"), 0o644); err != nil {
		t.Fatalf("cannot write the probe template: %v", err)
	}
	return dir
}

var probeVerdictRe = regexp.MustCompile(`(?m)^verdict: "([a-z-]*)"\s*$`)

// valuesFileFor writes one values document, with every character outside plain
// ASCII spelled as a \uXXXX escape.
//
// # Why the escaping is not optional
//
// encoding/json escapes the characters below U+0020 and leaves everything else
// as the bytes it was, so an address holding U+009F went into the values file
// as two raw bytes. helm reads a values file through a YAML reader, YAML does
// not allow a raw character out of that range in the stream, and helm stopped
// with "control characters are not allowed" — BEFORE the chart was loaded, let
// alone the rule run.
//
// That is the difference between "the rule refused this address" and "the rule
// was never asked", and this whole file exists because those two must never be
// confused. The guard did say so rather than passing quietly, which is what a
// guard is for; this is the repair it asked for. Written as escapes, the same
// address arrives at the rule as exactly the characters the operator wrote,
// and every row in the written-down list can be put to the chart.
func valuesFileFor(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("cannot write the address into a values file: %v", err)
	}
	var out strings.Builder
	for _, r := range string(body) {
		if r >= 0x20 && r <= 0x7e {
			out.WriteRune(r)
			continue
		}
		// Anything else is spelled out. A rune above the basic plane needs
		// the pair of escapes, which is what utf16.Encode gives.
		for _, unit := range utf16.Encode([]rune{r}) {
			out.WriteString(fmt.Sprintf(`\u%04x`, unit))
		}
	}
	return []byte(out.String())
}

// chartVerdict asks the shipped rule about one address.
//
// reached is false when helm never got as far as the rule — a values file it
// would not read, a chart it would not load. That is a different answer from
// a refusal by the rule, and every caller below treats it as one.
func chartVerdict(t *testing.T, chartDir, address string) (verdict string, reached bool, helmSaid string) {
	t.Helper()

	helm, lookErr := exec.LookPath("helm")
	if lookErr != nil {
		t.Fatalf("helm is not on PATH, so the shipped rule cannot be asked anything: %v", lookErr)
	}
	valuesPath := filepath.Join(t.TempDir(), "probe-values.json")
	body := valuesFileFor(t, map[string]string{"probe": address})
	if err := os.WriteFile(valuesPath, body, 0o644); err != nil {
		t.Fatalf("cannot write the probe values file: %v", err)
	}

	cmd := exec.Command(helm, "template", "probe", chartDir, "-f", valuesPath)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", false, string(out)
	}
	m := probeVerdictRe.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("the probe rendered but printed no verdict line, so this reader is not seeing what it "+
			"thinks it is:\n%s", out)
	}
	return m[1], true, string(out)
}

// TestTheProbeReallyRunsTheShippedRule is the positive control for everything
// below it.
//
// A probe that returned one constant, or that quietly failed to reach the rule
// at all, would make every check in this file agree with whatever it was
// asked. So: three addresses that the rule must answer three different ways,
// and a fourth that carries a raw NUL, to show a control character reaches the
// rule rather than being turned away by helm before it gets there.
func TestTheProbeReallyRunsTheShippedRule(t *testing.T) {
	dir := probeChartDir(t)

	for _, tc := range []struct {
		what    string
		address string
		want    string
	}{
		{"a plain address", "https://git.example/o/r", verdictFree},
		{"user information", "https://synthetic-user:synthetic-pw-not-real@git.example/o/r", verdictCarries},
		{"nothing at all", "", verdictUnclassifiable},
	} {
		got, reached, said := chartVerdict(t, dir, tc.address)
		if !reached {
			t.Fatalf("the probe did not reach the rule for %s, so nothing below is measuring the rule:\n%s", tc.what, said)
		}
		if got != tc.want {
			t.Errorf("the shipped rule says %q for %s; expected %q. The probe is either not running "+
				"the rule or the rule has changed meaning.", got, tc.what, tc.want)
		}
	}

	// The control-character control. This one is not about the verdict: it is
	// about WHICH LAYER answers. helm's --set-string will not carry a raw NUL
	// at all, so a test written that way cannot tell "the rule refused it"
	// from "helm never handed it over".
	withNUL := "https://git.example/o/r\x00https://synthetic-user:synthetic-pw-not-real@evil.example/o/r"
	got, reached, said := chartVerdict(t, dir, withNUL)
	if !reached {
		t.Fatalf("an address carrying a raw NUL never reached the rule, so every control-character "+
			"row below would be measuring helm rather than the chart:\n%s", said)
	}
	if got == "" {
		t.Fatal("the rule answered with an empty verdict for an address carrying a raw NUL")
	}
	t.Logf("an address carrying a raw NUL reaches the rule, which answers %q", got)

	// And the other kind of control character, which is the one the values
	// file could not carry at all until BF13-4. A NUL survives because
	// encoding/json spells it out; U+0080 to U+009F are left as the bytes
	// they are unless something escapes them, and helm's YAML reader stops
	// on a raw one before the chart is loaded. Both ends of that range are
	// asked here so a values writer that stops escaping is a failure rather
	// than a whole class of address quietly going unasked.
	for _, tc := range []struct {
		what    string
		address string
	}{
		{"the bottom of the upper control range", "https://git.exa\u0080mple/o/r"},
		{"the one control in that range a YAML reader does allow raw", "https://git.exa\u0085mple/o/r"},
		{"the top of the upper control range", "https://git.exa\u009fmple/o/r"},
	} {
		got, reached, said := chartVerdict(t, dir, tc.address)
		if !reached {
			t.Fatalf("an address carrying %s never reached the rule, so every row in the "+
				"written-down list that holds one is measuring helm rather than the chart:\n%s",
				tc.what, said)
		}
		if got != verdictUnclassifiable {
			t.Errorf("the shipped rule says %q for an address carrying %s; a raw control character "+
				"anywhere in an address is refused", got, tc.what)
		}
	}
}

// --------------------------------------------------------------------------
// the chart against the written-down rule
// --------------------------------------------------------------------------

// TestTheChartAgreesWithTheWrittenDownRule is the test of record for the
// chart's copy.
//
// "accepted" in the list must be exactly credential-free. "refused" must be
// anything else — which of the two refusals a row lands on is not pinned,
// because the rule does not distinguish them and the list never claimed to.
func TestTheChartAgreesWithTheWrittenDownRule(t *testing.T) {
	rows := loadAddressCorpus(t)
	dir := probeChartDir(t)

	checked := 0
	for _, row := range rows {
		got, reached, said := chartVerdict(t, dir, row.Address)
		checked++
		if !reached {
			t.Errorf("the chart's rule was never asked about %q — helm turned the value away before "+
				"the rule ran, so this row proves nothing either way.\n  helm said: %s", row.Address, said)
			continue
		}
		switch row.Verdict {
		case addresscorpus.Accepted:
			if got != verdictFree {
				t.Errorf("the chart says %q for %q, but the rule accepts it, so an operator is turned "+
					"away by the chart and let through by the server.\n  the rule says: %s",
					got, row.Address, strings.TrimSpace(row.Reason))
			}
		case addresscorpus.Refused:
			if got == verdictFree {
				t.Errorf("the chart says credential-free for %q, but the rule refuses it. The chart "+
					"writes this value into the Deployment in the clear, so anyone who can read a "+
					"Deployment can read it.\n  the rule says: %s",
					row.Address, strings.TrimSpace(row.Reason))
			}
		}
	}
	if checked != len(rows) {
		t.Fatalf("asked the chart about %d addresses out of the %d rows in the list", checked, len(rows))
	}
	t.Logf("asked the chart's own rule about %d addresses from the written-down list", checked)
}

// TestTheChartAndCredsafeReachTheSameVerdict is the stronger form of the
// agreement check, and the one that stops the two copies drifting into
// "refuses for a different reason".
//
// bf11_addresses_test.go compares refuse-or-not, which is two states. The rule
// has three, and the difference between them is the difference between "this
// has a credential in it" and "I could not read this at all". Two copies that
// answer those differently have already stopped being one rule.
func TestTheChartAndCredsafeReachTheSameVerdict(t *testing.T) {
	dir := probeChartDir(t)

	seen := map[string]bool{}
	var addresses []string
	add := func(a string) {
		if seen[a] {
			return
		}
		seen[a] = true
		addresses = append(addresses, a)
	}
	rows := loadAddressCorpus(t)
	for _, row := range rows {
		add(row.Address)
	}
	// Everything the older hand-written table holds as well, so nothing that
	// list was watching stops being watched.
	for _, a := range agreementTable {
		add(a)
	}
	if len(addresses) < len(rows) {
		t.Fatalf("the sweep holds %d addresses, fewer than the %d rows in the list", len(addresses), len(rows))
	}

	counts := map[string]int{}
	for _, address := range addresses {
		got, reached, said := chartVerdict(t, dir, address)
		if !reached {
			t.Errorf("the chart's rule was never asked about %q:\n  helm said: %s", address, said)
			continue
		}
		want := credsafe.ClassifyAddress(address).String()
		// credsafe's String() spells one verdict as a sentence fragment; the
		// chart spells it as a word. The two are the same answer.
		if want == "carries a credential" {
			want = verdictCarries
		}
		counts[got]++
		if got != want {
			t.Errorf("the two copies of the rule answer differently about %q.\n"+
				"  internal/credsafe: %s\n  the chart:         %s", address, want, got)
		}
	}
	// All three answers have to appear, or the sweep proved the copies agree
	// only about whichever one it happened to see.
	for _, want := range []string{verdictFree, verdictCarries, verdictUnclassifiable} {
		if counts[want] == 0 {
			t.Errorf("not one address in the sweep came back %q, so agreement about that answer was "+
				"never tested", want)
		}
	}
	t.Logf("both copies of the rule were asked about %d addresses: %d credential-free, %d carries-credential, %d unclassifiable",
		len(addresses), counts[verdictFree], counts[verdictCarries], counts[verdictUnclassifiable])
}

// TestTheWrittenDownListCarriesMostOfTheCoverage records which of the two
// lists the agreement above mostly rests on, and keeps it the right one.
//
// The hand-written table in bf11_addresses_test.go and the written-down list
// use different example hostnames on purpose, so most rows appear in only one
// of them and counting the overlap says nothing. What matters is which list is
// doing the work. A second hand-written list growing past the written-down one
// is how a whole class of address stayed invisible twice: the list next to the
// fix only ever holds the shapes whoever wrote the fix had already thought of.
//
// Nothing here edits either list. Rows go into the written-down list from the
// rule, in its own story, never because a chart test wanted one.
func TestTheWrittenDownListCarriesMostOfTheCoverage(t *testing.T) {
	rows := loadAddressCorpus(t)
	if len(agreementTable) == 0 {
		t.Fatal("the hand-written table is empty, so nothing it used to watch is being watched")
	}
	if len(rows) <= len(agreementTable) {
		t.Errorf("the written-down list holds %d addresses and the hand-written table holds %d. "+
			"The list written from the rule has to be the bigger of the two, or the rule is mostly "+
			"being checked against a table written next to the code it checks.", len(rows), len(agreementTable))
	}

	inList := map[string]bool{}
	for _, row := range rows {
		inList[row.Address] = true
	}
	var onlyInTable []string
	for _, a := range agreementTable {
		if !inList[a] {
			onlyInTable = append(onlyInTable, a)
		}
	}
	sort.Strings(onlyInTable)
	t.Logf("the written-down list holds %d addresses; %d of the %d in the hand-written table are not "+
		"among them and are checked only through the sweep above: %s",
		len(rows), len(onlyInTable), len(agreementTable), strings.Join(onlyInTable, ", "))
}

// --------------------------------------------------------------------------
// behaviour: what actually reaches the Pod
// --------------------------------------------------------------------------

// bf13Sentinel is planted inside each address below so the rendered Deployment
// can be searched for it byte for byte rather than looked at.
const bf13Sentinel = "synthetic-pw-not-real-BF13-6a02f1"

// bf13AdversarialAddresses are the shapes measured going into a rendered
// Deployment in the clear, plus the three that carry no credential but that
// the rule refuses all the same.
//
// Each one is here by its actual text rather than as a note in a comment, so
// the day one of them is let through again this file goes red.
var bf13AdversarialAddresses = map[string]string{
	// A mistyped scheme. One slash instead of two, a digit in front, an
	// underscore in the middle: each of these used to leave the whole of the
	// user information sitting in what the chart called the path.
	"a scheme written with one slash":     "https:/synthetic-user:" + bf13Sentinel + "@git.example/o/r",
	"a scheme with a digit in front":      "1https://synthetic-user:" + bf13Sentinel + "@git.example/o/r",
	"a scheme with an underscore in it":   "ht_tps://synthetic-user:" + bf13Sentinel + "@git.example/o/r",
	"user information written as escapes": "https://synthetic-user%3A" + bf13Sentinel + "%40git.example/o/r",
	"user information escaped twice":      "https://synthetic-user%253A" + bf13Sentinel + "%2540git.example/o/r",

	// The member of that family where the COLON is what was left out. The
	// other four leave a fragment that still carries its colon, so they are
	// turned away as a host with an empty port; "https" on its own is a
	// perfectly good host name, so this one was read as a host followed by a
	// path and went into the Deployment exactly as written until BF13-6.
	"a scheme with its colon left out":              "https//synthetic-user:" + bf13Sentinel + "@git.example/o/r",
	"a scheme with its colon left out and a query":  "https//synthetic-user:" + bf13Sentinel + "@git.example/o/r?ref=main",
	"a doubled slash after an ordinary host name":   "localhost//synthetic-user:" + bf13Sentinel + "@evil.example/x",
	"a doubled slash after a repository host name":  "git.example//synthetic-user:" + bf13Sentinel + "@evil.example/x",
	"a doubled slash with nothing written after it": "git.example//o/r",

	// A raw control character, with a second whole address after it. A reader
	// that stops at the first line treats what follows as a separate, safe
	// value.
	"a newline and a second address": "https://git.example/o/r\nhttps://synthetic-user:" + bf13Sentinel + "@evil.example/o/r",
	"a tab and a second address":     "https://git.example/o/r\thttps://synthetic-user:" + bf13Sentinel + "@evil.example/o/r",
	"a NUL and a second address":     "https://git.example/o/r\x00https://synthetic-user:" + bf13Sentinel + "@evil.example/o/r",

	// No credential in these three at all. The rule refuses them because it
	// cannot read them whole, and "I could not tell" is not permission.
	"a port written but left empty": "git.example:",
	"a port above the highest one":  "https://git.example:99999999999999/o/r",
	"no host at all":                "https://",
}

// bf13SafeAddresses must keep working. Every one of them was measured
// rendering before this change and must still render after it.
var bf13SafeAddresses = []string{
	"localhost:8080",
	"[::1]:8080",
	"[fe80::1%25eth0]:80",
	"github.com/org/repo@v1",
	"oci://ghcr.io/org/chart",
	"git+ssh://git.example/o/r",
	// The boundary of the doubled-slash refusal. Here the "//" that opens the
	// authority is the operator's own, so where the authority ends is not in
	// doubt and what follows it is an ordinary path. These must keep
	// rendering, or the refusal has reached past the shape it is about.
	"https://git.example//o/r",
	"//git.example//o/r",
}

// renderWithAddressFile renders the real chart with one address set, handing
// the value over in a values file so helm's --set-string escaping is not
// sitting between the test and the rule.
func renderWithAddressFile(t *testing.T, f addressField, address string) (stdout, stderr string, err error) {
	t.Helper()

	// "connection.git.repoURL" -> {"connection":{"git":{"repoURL": address}}}
	var node any = address
	parts := strings.Split(f.valuesPath, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		node = map[string]any{parts[i]: node}
	}
	body := valuesFileFor(t, node)
	path := filepath.Join(t.TempDir(), "address-values.json")
	if writeErr := os.WriteFile(path, body, 0o644); writeErr != nil {
		t.Fatalf("cannot write the values file for %s: %v", f.valuesPath, writeErr)
	}
	args := append([]string{}, f.extraSet...)
	args = append(args, "-f", path)
	return runHelmTemplate(t, args...)
}

// TestNoAdversarialAddressReachesThePod is the behavioural half, one field at
// a time.
//
// Every one of the four values the chart writes into the Pod is set on its
// own, with nothing else touched, and the rendered output is searched for the
// planted text byte for byte. Each field carries its own positive control —
// the plainly-written credential form, which must be refused — so that "the
// planted text is not in the render" means the rule ran rather than the render
// being empty.
func TestNoAdversarialAddressReachesThePod(t *testing.T) {
	if len(bf11AddressFields) == 0 {
		t.Fatal("no address field is exercised, so this test rendered nothing")
	}

	for _, f := range bf11AddressFields {
		f := f

		// The control first. If this shape is NOT refused, every reading
		// below is worthless, because the field is not being checked at all.
		control := "https://synthetic-user:" + bf13Sentinel + "@git.example/o/r"
		if _, _, err := renderWithAddressFile(t, f, control); err == nil {
			t.Fatalf("%s rendered with a plainly-written credential in it, so this field is not "+
				"checked at all and nothing below it means anything", f.valuesPath)
		}
		// And the other way round: an ordinary address must render, or a
		// refusal proves only that the values file was broken.
		if _, stderr, err := renderWithAddressFile(t, f, "https://git.example/o/r"); err != nil {
			t.Fatalf("%s refused an ordinary address, so the values file this test writes is not "+
				"reaching the field:\n%s", f.valuesPath, stderr)
		}

		for what, address := range bf13AdversarialAddresses {
			what, address := what, address
			t.Run(f.valuesPath+" with "+what, func(t *testing.T) {
				stdout, stderr, err := renderWithAddressFile(t, f, address)
				if err == nil {
					t.Fatalf("the chart rendered with %s in %s. values.yaml calls that field "+
						"non-secret and the Deployment writes it into the Pod in the clear.", what, f.valuesPath)
				}
				for label, text := range map[string]string{"what was rendered": stdout, "the refusal": stderr} {
					if strings.Contains(text, bf13Sentinel) {
						t.Errorf("%s for %s in %s carries the value the operator supplied",
							label, what, f.valuesPath)
					}
				}
				if !strings.Contains(stderr, f.valuesPath) {
					t.Errorf("the refusal for %s does not say which setting is at fault:\n%s", what, stderr)
				}
			})
		}

		for _, address := range bf13SafeAddresses {
			address := address
			t.Run(f.valuesPath+" keeps "+address, func(t *testing.T) {
				stdout, stderr, err := renderWithAddressFile(t, f, address)
				if err != nil {
					t.Fatalf("the chart refused %q, which the rule allows and which rendered before "+
						"this change:\n%s", address, stderr)
				}
				if got := envValueIn(t, stdout, f.envName); got != address {
					t.Errorf("%s reached the Pod as %q, but the operator wrote %q", f.envName, got, address)
				}
			})
		}
	}
}

// TestEveryAdversarialShapeIsActuallyPlanted stops the table above from
// quietly losing its teeth. A row whose text no longer carries the planted
// string would be checked for a credential that was never in it.
func TestEveryAdversarialShapeIsActuallyPlanted(t *testing.T) {
	carriers, plain := 0, 0
	for what, address := range bf13AdversarialAddresses {
		if strings.Contains(address, bf13Sentinel) {
			carriers++
			continue
		}
		plain++
		if strings.Contains(address, "@") || strings.Contains(address, "?") || strings.Contains(address, "#") {
			t.Errorf("%q has somewhere for a credential to sit but carries no planted text, so the "+
				"searches above look for nothing in it", what)
		}
	}
	if carriers == 0 {
		t.Fatal("no adversarial address carries the planted text, so every search above finds nothing " +
			"whatever the chart does")
	}
	if plain == 0 {
		t.Fatal("every adversarial address carries a credential, so nothing proves the chart also " +
			"refuses a shape it merely cannot read")
	}
	if got := fmt.Sprintf("%d/%d", carriers, len(bf13AdversarialAddresses)); carriers+plain != len(bf13AdversarialAddresses) {
		t.Fatalf("the shapes do not add up: %s", got)
	}
}
