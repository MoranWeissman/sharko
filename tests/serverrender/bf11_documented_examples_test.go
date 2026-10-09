package serverrender

// bf11_documented_examples_test.go — the written examples have to obey the
// rule the chart now enforces.
//
// A rendered chart that refuses a credential-bearing address is only half the
// job. The other half is that nothing in values.yaml's comments or under docs/
// shows an operator a shape the chart will refuse, and that no example fills
// in a bootstrap password with something that looks like a real one. An
// example is the thing people copy.
//
// This lives beside the render guards rather than in a docs package because it
// asks the same question of the same three fields, and because credsafe is the
// one thing allowed to answer it.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// addressFieldPattern is the alternation of field names the scanner looks for.
//
// It is DERIVED from validatedAddresses rather than typed out. The old version
// carried three names — repoURL, serverURL, baseURL — and missed ai.ollama.url
// entirely, which is one of the four addresses the chart validates. Building
// the pattern from the rule's own list means a fifth address with a new field
// name is covered the day it is added, and
// TestTheScannerLooksForEveryFieldTheRuleChecks fails if it somehow is not.
func addressFieldPattern() string {
	seen := map[string]bool{}
	var leaves []string
	for _, path := range validatedAddresses {
		leaf := path
		if i := strings.LastIndex(path, "."); i >= 0 {
			leaf = path[i+1:]
		}
		if leaf == "" || seen[leaf] {
			continue
		}
		seen[leaf] = true
		leaves = append(leaves, leaf)
	}
	// Longest first, so "repoURL" is preferred over a bare "url" that would
	// otherwise match nothing useful inside it.
	sort.Slice(leaves, func(i, j int) bool { return len(leaves[i]) > len(leaves[j]) })
	return strings.Join(leaves, "|")
}

// addressAssignment matches a written example that sets one of the address
// fields, in either YAML (`repoURL: "https://..."`) or flag (`--set
// ai.baseURL=https://...`) form.
//
// # What BF12-4 changed here
//
// The character class used to exclude "#", so the capture stopped dead at a
// fragment marker — and a fragment is one of the three carriers a credential
// travels in, and one of the three this sweep certifies. An example written
// `repoURL: https://github.com/org/repo#<token>` was read as the perfectly
// ordinary `https://github.com/org/repo` and reported clean. The positive
// control had no fragment case either, so nothing said so.
//
// "#" is in the capture now. Trailing YAML comments are stripped separately,
// by stripTrailingComment below, which is the job the exclusion was really
// doing.
var addressAssignment = regexp.MustCompile(
	`(?i)\b(` + addressFieldPattern() + `)\s*[:=]\s*["']?([^\s"'` + "`" + `,)\]}]+)`)

// stripTrailingComment removes a trailing YAML or shell comment from a line,
// and nothing else.
//
// It cannot just cut at the first "#": values.yaml documents its examples
// INSIDE comments, so a line that is entirely a comment must be kept and read.
// The rule is the ordinary one — a "#" that follows whitespace, outside
// quotes, after some real content on the line — which leaves a whole-line
// comment alone and leaves a "#" inside an address alone too.
func stripTrailingComment(line string) string {
	var quote rune
	seenContent := false
	prevIsSpace := true
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			seenContent = true
		case r == '#' && prevIsSpace && seenContent:
			return line[:i]
		case r != ' ' && r != '\t':
			seenContent = true
		}
		prevIsSpace = r == ' ' || r == '\t'
	}
	return line
}

// literalPasswordEnv matches the shape BF11 removed from the chart: the
// bootstrap password written into a Pod as a plain environment value.
//
// Documentation is checked for this rather than for "an example password",
// because showing an operator how to SET bootstrapAdmin.password is the whole
// point of that section — the made-up value in it is not a credential. What
// must not survive anywhere is the claim, or the copyable snippet, that says
// the value ends up in the Deployment in the clear.
var literalPasswordEnv = regexp.MustCompile(`SHARKO_BOOTSTRAP_ADMIN_PASSWORD[\s\S]{0,80}?\n\s*value:\s*\S`)

// addressShape is the "is this even an address?" filter.
//
// Broadening the field names from three to every leaf the rule checks brought
// in a bare `url`, and `url` is an ordinary word: docs/iam-setup.md has a
// Terraform block with `url = data.aws_eks_cluster...` in it, which is not an
// address and never reaches the chart.
//
// The filter below cannot hide anything this sweep exists to find, and that is
// the only reason it is allowed to exist. A capture holding "@", "?" or "#" is
// kept unconditionally — those three characters are the carriers, so the
// shapes being hunted are never filtered. Only a capture with none of them and
// no address shape at all is dropped, and a string with none of the three has
// nowhere for a credential to sit.
var addressShape = regexp.MustCompile(
	`^(?:[A-Za-z][A-Za-z0-9+.\-]*://|//)|^[A-Za-z0-9._~\-]+(?::[^/]*)?(?:/|$)`)

// looksLikeAnAddress says whether a captured value is worth classifying.
func looksLikeAnAddress(value string) bool {
	if credsafe.ContainsCredentialCarrierRune(value) {
		return true
	}
	return addressShape.MatchString(value)
}

// documentedAddress is one example found in one file.
type documentedAddress struct {
	file    string
	line    int
	field   string
	address string
}

// scanForAddresses walks text and returns every address example in it.
func scanForAddresses(file, text string) []documentedAddress {
	var out []documentedAddress
	for i, line := range strings.Split(text, "\n") {
		for _, m := range addressAssignment.FindAllStringSubmatch(stripTrailingComment(line), -1) {
			if !looksLikeAnAddress(m[2]) {
				continue
			}
			out = append(out, documentedAddress{file: file, line: i + 1, field: m[1], address: m[2]})
		}
	}
	return out
}

// TestTheExampleScannerFindsAPlantedExample is the positive control. Without
// it, a regular expression that matched nothing would report every document
// clean.
//
// The FRAGMENT case is the one BF12-4 added. The old pattern excluded "#" from
// the capture, so an example carrying a token in the fragment was read as the
// ordinary address in front of it and reported clean — and a fragment is one
// of the three carriers this sweep certifies. The old control had no fragment
// case either, so it agreed.
//
// The scheme-less case and the ai.ollama.url case are the other two the old
// scanner could not see.
func TestTheExampleScannerFindsAPlantedExample(t *testing.T) {
	planted := strings.Join([]string{
		"Set the repository:",
		`    repoURL: "https://ghp_plantedtoken@github.com/org/repo"`,
		"or on the command line:",
		"    --set ai.baseURL=https://gateway.example.com/api?key=planted",
		"    --set bootstrapAdmin.password=hunter2",
		"a token hung off the end, which the old pattern could not see:",
		"    repoURL: https://github.com/org/repo#PLANTEDFRAGMENTTOKEN",
		"the fourth address, which the old pattern did not look for:",
		"    --set ai.ollama.url=https://svc:PLANTEDPASSWORD@ollama.internal:11434",
		"and written with no scheme at all:",
		"    repoURL: user:PLANTEDPASSWORD@git.example/o/r.git",
		"and perfectly ordinary ones:",
		`    serverURL: "https://argocd.example.com"`,
		"    repoURL: https://github.com/org/repo   # a trailing note",
		"  # baseURL: https://gateway.example.com/v1",
	}, "\n")

	var got []string
	for _, a := range scanForAddresses("planted.md", planted) {
		verdict := "accepted"
		if credsafe.ClassifyAddress(a.address) != credsafe.AddressCredentialFree {
			verdict = "refused"
		}
		got = append(got, a.field+" "+verdict+" "+a.address)
	}
	want := []string{
		"repoURL refused https://ghp_plantedtoken@github.com/org/repo",
		"baseURL refused https://gateway.example.com/api?key=planted",
		"repoURL refused https://github.com/org/repo#PLANTEDFRAGMENTTOKEN",
		"url refused https://svc:PLANTEDPASSWORD@ollama.internal:11434",
		"repoURL refused user:PLANTEDPASSWORD@git.example/o/r.git",
		"serverURL accepted https://argocd.example.com",
		"repoURL accepted https://github.com/org/repo",
		"baseURL accepted https://gateway.example.com/v1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the scanner does not read the written forms it is trusted to sweep for.\nwanted:\n  %s\ngot:\n  %s",
			strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}

	plantedEnv := "        - name: SHARKO_BOOTSTRAP_ADMIN_PASSWORD\n          value: \"hunter2\"\n"
	if !literalPasswordEnv.MatchString(plantedEnv) {
		t.Fatal("the scanner does not find a planted literal password environment entry, so the sweep " +
			"below would report every page clean whether it was or not")
	}
	safeEnv := "        - name: SHARKO_BOOTSTRAP_ADMIN_PASSWORD\n          valueFrom:\n            secretKeyRef:\n              key: admin.bootstrapPassword\n"
	if literalPasswordEnv.MatchString(safeEnv) {
		t.Fatal("the scanner reports a Secret reference as a literal value, so it fires on the shape " +
			"the chart is supposed to use")
	}
}

// TestTheScannerLooksForEveryFieldTheRuleChecks is the growth guard.
//
// The old pattern named three fields by hand and missed ai.ollama.url, which
// the chart validates. The pattern is built from validatedAddresses now, so a
// fifth address is covered the day it is added — and if its field name is one
// the pattern somehow cannot match, this fails rather than going quiet.
func TestTheScannerLooksForEveryFieldTheRuleChecks(t *testing.T) {
	if len(validatedAddresses) == 0 {
		t.Fatal("the rule checks no address at all, so this guard read nothing")
	}
	for _, path := range validatedAddresses {
		t.Run(path, func(t *testing.T) {
			// Both written forms, with a credential planted in each.
			for _, line := range []string{
				"    " + path + ": https://PLANTEDTOKEN@host.example/x",
				"    --set " + path + "=https://PLANTEDTOKEN@host.example/x",
			} {
				found := scanForAddresses("planted.md", line)
				if len(found) != 1 {
					t.Fatalf("the scanner found %d examples in %q, wanted 1 — an address the chart "+
						"validates is written in documentation in a form this sweep cannot see",
						len(found), line)
				}
				if credsafe.ClassifyAddress(found[0].address) == credsafe.AddressCredentialFree {
					t.Errorf("the scanner read %q out of %q and called it credential-free",
						found[0].address, line)
				}
			}
		})
	}
}

// TestTrailingCommentsAreStrippedButCommentedExamplesAreNot pins the reader
// that replaced the old "# is not part of an address" shortcut.
//
// values.yaml writes its examples INSIDE comments, so a whole-line comment has
// to be read, not thrown away. Only a comment that follows real content on the
// line is a trailing comment. And a "#" with no space in front of it is part
// of the value — that is a fragment, and it is exactly what the old pattern
// threw away.
func TestTrailingCommentsAreStrippedButCommentedExamplesAreNot(t *testing.T) {
	cases := []struct{ in, want string }{
		{"repoURL: https://h/r  # a note", "repoURL: https://h/r  "},
		{"  # repoURL: https://h/r", "  # repoURL: https://h/r"},
		{"repoURL: https://h/r#frag", "repoURL: https://h/r#frag"},
		{`repoURL: "https://h/r # inside the quotes"`, `repoURL: "https://h/r # inside the quotes"`},
		{"# a markdown heading", "# a markdown heading"},
	}
	for _, c := range cases {
		if got := stripTrailingComment(c.in); got != c.want {
			t.Errorf("stripTrailingComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if len(cases) == 0 {
		t.Fatal("no line was read, so this guard proves nothing")
	}
}

// TestTheAddressFilterNeverDropsACredentialCarrier proves the one property
// that makes looksLikeAnAddress safe to have at all.
//
// It exists to keep an HCL expression out of the sweep. If it could also drop
// a credential-bearing example, it would be hiding the very thing the sweep is
// for. Every string holding one of the three carriers is kept, whatever else
// it looks like.
func TestTheAddressFilterNeverDropsACredentialCarrier(t *testing.T) {
	carriers := []string{
		"https://tok@github.com/org/repo",
		"user:pw@git.example/o/r.git",
		"//u:pw@git.example/r",
		"git.example/r?access_token=T",
		"git.example/r#T",
		"data.aws_eks_cluster.sharko@identity[0",
		"?",
		"#",
		"@",
	}
	for _, c := range carriers {
		if !looksLikeAnAddress(c) {
			t.Errorf("the filter drops %q, which holds a carrier — the sweep would then never "+
				"classify it and would report the page clean", c)
		}
	}
	// And it does drop the thing it was added for.
	if looksLikeAnAddress("data.aws_eks_cluster.sharko.identity[0") {
		t.Error("the filter keeps a Terraform expression, so it is doing nothing and the sweep " +
			"fails on a line that is not an address")
	}
	if len(carriers) == 0 {
		t.Fatal("no value was filtered, so this guard proves nothing")
	}
}

// TestNoWrittenExampleShowsACredentialBearingAddress sweeps the real files.
func TestNoWrittenExampleShowsACredentialBearingAddress(t *testing.T) {
	root := repoRoot(t)
	files := documentedFiles(t, root)

	var bad []string
	filesWithAddressExamples := map[string]bool{}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, a := range scanForAddresses(rel, string(body)) {
			filesWithAddressExamples[rel] = true
			if credsafe.ClassifyAddress(a.address) != credsafe.AddressCredentialFree {
				bad = append(bad, a.file+":"+itoa(a.line)+" sets "+a.field+" to an address the chart refuses")
			}
		}
		if literalPasswordEnv.Match(body) {
			bad = append(bad, rel+" shows the bootstrap password as a literal Deployment environment "+
				"value, which is the shape BF11 removed from the chart")
		}
	}

	// Not vacuous: these two files carry address examples today. If either
	// stops doing so, the sweep is reading less than it thinks and somebody
	// has to look at this list rather than let it shrink in silence.
	for _, mustHaveExamples := range []string{
		"docs/site/operator/git-native-config.md",
		"docs/site/user-guide/addons.md",
	} {
		if !filesWithAddressExamples[mustHaveExamples] {
			t.Errorf("no address example was found in %s. Either the file moved, or the scanner has "+
				"stopped matching the form these examples are written in.", mustHaveExamples)
		}
	}

	if len(bad) != 0 {
		sort.Strings(bad)
		t.Errorf("written examples show shapes the chart refuses, so an operator who copies one gets a "+
			"failed install:\n  %s", strings.Join(bad, "\n  "))
	}
}

// documentedFiles is charts/sharko/values.yaml plus every markdown file under
// docs/, walked rather than listed — a hand-written list goes stale the day
// somebody adds a page.
func documentedFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{"charts/sharko/values.yaml"}
	err := filepath.Walk(filepath.Join(root, "docs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs/: %v", err)
	}
	if len(files) < 2 {
		t.Fatal("the walk found no documentation at all, so this sweep read nothing")
	}
	sort.Strings(files)
	return files
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
