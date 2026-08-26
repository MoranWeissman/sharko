package envreg

// registry_test.go — the guard rules on the configuration registry.
//
//	1  every SHARKO_* read from non-test Go is a registered name
//	2  the registry file is NOT a read site, and is nobody's ReaderFile
//	3  the declared ReaderFile really READS it — the name has to reach
//	   os.Getenv, os.LookupEnv or envreg.Lookup, not merely appear in the
//	   file; and a production reader may not be a _test.go file or live
//	   under tests/
//	4  scripts/ is not a production reader
//	5  charts/ is a setter, not documentation
//	6  a deprecated alias whose canonical name is still read with a bare
//	   os.Getenv is refused — it would resolve for nobody
//
// Rules 4 and 5 are enforced in documented_env_vars_test.go, where they
// bite; the shape of them is asserted here so the scan helpers cannot be
// loosened without a test noticing.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envTokenRe matches a SHARKO_ name that is not the tail of a longer
// underscore word. Without the leading guard, E2E_SHARKO_SERVER reads as
// a mention of SHARKO_SERVER and the guard invents a defect.
var envTokenRe = regexp.MustCompile(`(^|[^A-Z0-9_])(SHARKO_[A-Z0-9_]*[A-Z0-9])`)

func repoRootForEnvSweep(t *testing.T) string {
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

// isProductionReaderPath says whether a repo-relative path may count as
// a file that really reads a production setting.
//
// The three exclusions are rules 2, 3 and 4, in one place so they can be
// tested directly rather than inferred from behaviour:
//
//	_test.go   a test file can name any string it likes
//	tests/     tests/e2e/harness/sharko_helm.go is NOT named _test.go, so
//	           before this it counted as a production reader
//	scripts/   a shell-local variable is not a server setting; this is
//	           the rule that kills the SHARKO_GITOPS_REPO_URL hole
//	registry   the registry naming itself is the registry being its own
//	           evidence
func isProductionReaderPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch {
	case strings.HasSuffix(rel, "_test.go"):
		return false
	case rel == "tests" || strings.HasPrefix(rel, "tests/"):
		return false
	case rel == "scripts" || strings.HasPrefix(rel, "scripts/"):
		return false
	case rel == filepath.ToSlash(RegistryFile):
		return false
	}
	return true
}

// isTestReaderPath is the inverse for the TestHarness kind: a setting
// that claims to be read only by the harness must name a reader that
// really is test code. Without this, TestHarness would be the free pass
// — park any name there with a plausible sentence and no reader at all.
func isTestReaderPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	return strings.HasSuffix(rel, "_test.go") || rel == "tests" || strings.HasPrefix(rel, "tests/")
}

// collectGoReadSites returns every SHARKO_* name read from non-test Go,
// with the files it is read in.
//
// It reads the parsed index, not the bytes. A string that merely APPEARS
// in a file is not a read: see readsites_test.go for why that mattered and
// what the rule is now.
func collectGoReadSites(t *testing.T, root string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, files := range repoEnvReadIndex(t, root) {
		for file := range files {
			// A _test.go file can name any string it likes — ban lists,
			// fixtures, break-test scaffolding. Not a read site.
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			// RULE 2. The registry names every registered setting; it
			// cannot be its own evidence. The analysis already refuses it
			// structurally (a name in a struct field is not a call
			// argument), and the exclusion stays as the stated rule.
			if file == filepath.ToSlash(RegistryFile) {
				continue
			}
			out[name] = append(out[name], file)
		}
	}
	for name := range out {
		sort.Strings(out[name])
	}
	return out
}

// TestRule1_EveryReadSiteIsRegistered is the code direction: the server
// may not read a setting the registry has never heard of.
func TestRule1_EveryReadSiteIsRegistered(t *testing.T) {
	root := repoRootForEnvSweep(t)
	read := collectGoReadSites(t, root)

	if len(read) < 60 {
		t.Fatalf("only %d SHARKO_* names found in non-test Go — the read scan is broken, not the code", len(read))
	}

	var unregistered []string
	for name, files := range read {
		if _, ok := Get(name); ok {
			continue
		}
		sort.Strings(files)
		unregistered = append(unregistered, name+" — read at "+strings.Join(dedupe(files), ", "))
	}
	if len(unregistered) > 0 {
		sort.Strings(unregistered)
		t.Errorf("%d setting(s) are read by the code and are not in the registry:\n\n  %s\n\n"+
			"Register the name in internal/envreg/registry.go with its kind, a one-line summary "+
			"and the file that reads it. An unregistered read is a setting nobody documented, "+
			"nobody can find, and nothing checks.",
			len(unregistered), strings.Join(unregistered, "\n  "))
	}
}

// TestRule2_TheRegistryIsNotItsOwnEvidence proves the exclusion is real
// in both directions: the read scan skips the registry file, and no
// entry may name the registry file as its reader.
func TestRule2_TheRegistryIsNotItsOwnEvidence(t *testing.T) {
	root := repoRootForEnvSweep(t)

	body, err := os.ReadFile(filepath.Join(root, RegistryFile))
	if err != nil {
		t.Fatalf("reading %s: %v", RegistryFile, err)
	}
	if !strings.Contains(string(body), `"SHARKO_PORT"`) {
		t.Fatalf("%s does not contain the literals this rule is about — the check would prove nothing", RegistryFile)
	}

	// The registry file is full of SHARKO_* literals; the read scan must
	// not attribute any of them to it.
	for name, files := range collectGoReadSites(t, root) {
		for _, f := range files {
			if f == filepath.ToSlash(RegistryFile) {
				t.Errorf("%s is being counted as a read site for %s — the registry would be its own evidence", RegistryFile, name)
			}
		}
	}

	if isProductionReaderPath(RegistryFile) {
		t.Errorf("%s is accepted as a production reader path — an entry could name itself as its own reader", RegistryFile)
	}
	for _, s := range settings {
		if filepath.ToSlash(s.ReaderFile) == filepath.ToSlash(RegistryFile) {
			t.Errorf("%s names the registry file as its reader", s.Name)
		}
	}
}

// TestRule3_DeclaredReadersReallyReadThem checks that every declared
// reader really reads its setting, and enforces what a reader is allowed
// to be.
//
// This is the rule that closes two live holes. tests/e2e/harness/
// sharko_helm.go and gitfake_helm.go are not named *_test.go, so under an
// earlier guard they counted as production readers: a name used only by
// the kind harness could prove a documented server setting existed.
//
// The second hole was in the word "reads". This used to be a byte scan for
// the quoted name over the whole file, so a COMMENT naming the setting
// satisfied it, and so did `var _ = "SHARKO_X"`. A setting documented for
// operators, classified production, and read by nothing, passed. The
// question is now asked of the parsed tree: the name has to reach
// os.Getenv, os.LookupEnv or envreg.Lookup, directly or through a helper.
// See readsites_test.go.
func TestRule3_DeclaredReadersReallyReadThem(t *testing.T) {
	root := repoRootForEnvSweep(t)
	index := repoEnvReadIndex(t, root)

	for _, s := range settings {
		rel := filepath.ToSlash(s.ReaderFile)

		if _, err := os.Stat(filepath.Join(root, s.ReaderFile)); err != nil {
			t.Errorf("%s names reader %s, which cannot be read: %v", s.Name, rel, err)
			continue
		}
		if !index.readsIn(rel, s.Name) {
			t.Errorf("%s names reader %s, but nothing in that file reads it. The name has to reach "+
				"os.Getenv, os.LookupEnv or envreg.Lookup — as a literal, or as a constant handed to "+
				"something that does. A comment mentioning the name, or an unused constant holding "+
				"it, is the name written down and nothing more.", s.Name, rel)
		}

		switch s.Kind {
		case Production, DeprecatedAlias, Internal:
			if !isProductionReaderPath(rel) {
				t.Errorf("%s is %s but names reader %s. A production reader may not be a _test.go "+
					"file, may not live under tests/ (tests/e2e/harness/*.go are not named _test.go "+
					"and used to slip through), may not live under scripts/ (a shell variable is not "+
					"a server setting), and may not be the registry file itself.", s.Name, s.Kind, rel)
			}
		case TestHarness:
			if !isTestReaderPath(rel) {
				t.Errorf("%s is test-harness-only but names reader %s, which is production code. "+
					"If production code reads it, it is not test-harness-only — give it the kind it "+
					"really has.", s.Name, rel)
			}
		}
	}
}

// TestRule4_ScriptsAreNotProductionReaders and
// TestRule5_ChartsAreASetterNotDocumentation assert the shape of the two
// rules whose effect lands in documented_env_vars_test.go, so neither can
// be loosened by editing one helper.
func TestRule4_ScriptsAreNotProductionReaders(t *testing.T) {
	for _, p := range []string{
		"scripts/sharko-dev.sh",
		"scripts/smoke/third-party-catalog.sh",
		"scripts/helm-deploy.sh",
	} {
		if isProductionReaderPath(p) {
			t.Errorf("%s is accepted as a production reader — a shell-local variable would prove a server setting exists", p)
		}
	}
	for _, s := range settings {
		if strings.HasPrefix(filepath.ToSlash(s.ReaderFile), "scripts/") {
			t.Errorf("%s names a reader under scripts/", s.Name)
		}
	}
	root := repoRootForEnvSweep(t)
	for name, files := range collectGoReadSites(t, root) {
		for _, f := range files {
			if strings.HasPrefix(f, "scripts/") {
				t.Errorf("the read scan attributed %s to %s — scripts/ is not a read site", name, f)
			}
		}
	}
}

// envregDir is this package, the one place a bare os.Getenv of a canonical
// name is fine: it is the resolver itself.
const envregDir = "internal/envreg"

// aliasesThatCannotWork returns the deprecated aliases that would do
// nothing at all if somebody added them today.
//
// A deprecated alias only works for callers that resolve through
// envreg.Lookup. A caller reading the canonical name with a bare
// os.Getenv never sees the alias — no value, no warning, nothing. So an
// alias whose canonical name is still read that way anywhere outside this
// package is a published promise the resolver cannot keep.
//
// It takes the direct-reader lookup as an argument so the rule can be
// driven over inputs that are not the shipped registry. There are no
// aliases today; a test that only ran over `settings` would report ok
// forever without ever executing its own comparison, which is exactly the
// kind of check this round is about.
func aliasesThatCannotWork(in []Setting, directReaders func(canonical string) []string) []string {
	var out []string
	for _, s := range in {
		if s.Kind != DeprecatedAlias {
			continue
		}
		readers := directReaders(s.AliasOf)
		if len(readers) == 0 {
			continue
		}
		out = append(out, s.Name+" aliases "+s.AliasOf+", which is still read straight from the "+
			"environment at "+strings.Join(readers, ", "))
	}
	sort.Strings(out)
	return out
}

// TestRule6_AnAliasMustReallyResolve is the rule that stops a deprecated
// alias being added as decoration.
//
// The alias machinery already refuses an alias whose canonical name is not
// a registered Production setting. That is a check on the REGISTRY, and it
// says nothing about the code: a canonical name read with a bare
// os.Getenv never goes past the resolver, so an alias for it would build
// green, test green, and do nothing — not even warn.
//
// Most canonical names are still read that way. The exception is the
// listen port, which is why the alias for it works: cmd/sharko/serve.go
// calls envreg.ResolveHTTPPort instead of reading SHARKO_HTTP_PORT
// itself, and the only reads of the pair are inside this package.
func TestRule6_AnAliasMustReallyResolve(t *testing.T) {
	root := repoRootForEnvSweep(t)
	index := repoEnvReadIndex(t, root)
	live := func(canonical string) []string {
		return index.directReadersOutside(canonical, envregDir)
	}

	if broken := aliasesThatCannotWork(settings, live); len(broken) > 0 {
		t.Errorf("%d deprecated alias(es) cannot work:\n\n  %s\n\n"+
			"Route the canonical setting through envreg.Lookup at its read site first. Until then "+
			"the alias resolves for nobody: the code reads the canonical name directly, so an "+
			"operator who sets the old name gets no value and no warning.",
			len(broken), strings.Join(broken, "\n  "))
	}

	// The rule fires. Driven over a synthetic registry, because the one
	// alias in the shipped registry is a working alias, and a rule that
	// has never once returned a finding is a rule nobody has checked.
	//
	// SHARKO_CONFIG is the canonical name here because cmd/sharko/serve.go
	// really does read it with a bare os.Getenv. It used to be
	// SHARKO_PORT, until the port moved behind envreg.ResolveHTTPPort and
	// the fixture stopped describing the code.
	synthetic := []Setting{
		{Name: "SHARKO_OLD_CONFIG", Kind: DeprecatedAlias, AliasOf: "SHARKO_CONFIG",
			Summary: "Former name for the config file path.", ReaderFile: readerServe},
	}
	if broken := aliasesThatCannotWork(synthetic, live); len(broken) != 1 {
		t.Errorf("an alias for SHARKO_CONFIG — which cmd/sharko/serve.go reads with a bare os.Getenv "+
			"— produced %d finding(s), want 1. The rule cannot fire, so it protects nothing.",
			len(broken))
	}

	// And it does not fire on an alias whose canonical name nobody reads
	// bare, which is the whole point of the exercise.
	none := func(string) []string { return nil }
	if broken := aliasesThatCannotWork(synthetic, none); len(broken) != 0 {
		t.Errorf("the same alias produced %d finding(s) when nothing reads the canonical name "+
			"directly, want 0 — the rule refuses aliases on principle rather than on evidence",
			len(broken))
	}
}

func TestRule5_ChartsAreASetterNotDocumentation(t *testing.T) {
	for _, root := range docRoots {
		if root == "charts" || strings.HasPrefix(root, "charts"+string(filepath.Separator)) {
			t.Errorf("charts/ is in the documentation roots. A chart template SETS a value; that is "+
				"not the same as telling an operator the setting exists, and treating it as "+
				"documentation is how a name with no reader stayed green. docRoots = %v", docRoots)
		}
	}
}

// TestRegistryEntriesAreWellFormed is the entry-shape guard, run through
// the same Validate the server boots with so the two can never disagree.
func TestRegistryEntriesAreWellFormed(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("the shipped registry does not validate: %v", err)
	}
	for _, s := range settings {
		if strings.TrimSpace(s.Summary) == "" {
			t.Errorf("%s has no summary", s.Name)
		}
		if !strings.HasSuffix(strings.TrimSpace(s.Summary), ".") {
			t.Errorf("%s: the summary should be a sentence, ending in a full stop: %q", s.Name, s.Summary)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
