package logging

// credential_rule_guard_test.go — two things that must stay true about this
// package, checked against the tree rather than remembered (BF1).
//
// # 1. The rule about which characters can carry a credential is written once
//
// A repository address can hold its token in three places: after a "@" (the
// userinfo section), after a "?" (a query parameter) and after a "#" (a
// fragment). internal/credsafe has always said so — credentialBearingRunes is
// the three characters together. The log sink held its own copy of that rule
// and the copy was one character wide: it asked whether the value contained a
// "@" and nothing else. So an address written
// https://host/repo?access_token=... never reached the stripper that exists to
// remove query parameters, and every test in this package passed anyway,
// because none of them was pointed at the half of the rule that was missing.
//
// The fix was not to widen the second copy. It was to delete it. The sink now
// asks credsafe.ClassifyAddress and holds no character rule of its own.
// This guard is what keeps a new copy from appearing: it walks the shipping
// tree, finds every place that tests a string against a literal containing one
// of those three characters, and compares that against a written list.
//
// It fails on growth (a new place decides for itself) and on staleness (a
// listed place is gone, so the list has stopped describing the tree).
//
// # 2. Nothing is formatted into a log MESSAGE
//
// RedactHandler rebuilds the record with the original message and walks only
// the attributes. Whatever is in the message text goes out exactly as written.
// That is safe for as long as every message is a fixed string a person typed,
// and stops being safe the first time somebody writes
//
//	slog.Error(fmt.Sprintf("could not read %s", repoURL))
//
// which no attribute detector can see. There is no such call in the tree
// today. This guard is what makes that a fact about the tree instead of a fact
// about when somebody last looked.

import (
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// credentialBearingChars are the three characters the rule is about. Written
// out here rather than imported so the guard and the thing it guards cannot
// move together — a fixture that reads the same constant as the code proves
// only that one equals itself.
const credentialBearingChars = "@?#"

// searchFuncs are the strings-package helpers that ask "where in this value is
// that character?". A credential-position rule, written by hand, is one of
// these with one of the three characters as its needle.
//
// The shaping helpers are deliberately NOT here — HasPrefix, TrimPrefix, Split
// and their neighbours. A "#" at the front of a line is a comment marker and a
// "?" at the front of a query string is punctuation; neither is anybody asking
// whether a value has somewhere for a credential to be, and sweeping them in
// would bury the sites that matter under YAML and markdown parsing.
var searchFuncs = map[string]bool{
	"Contains": true, "ContainsAny": true, "ContainsRune": true,
	"Index": true, "IndexAny": true, "IndexByte": true, "IndexRune": true,
	"LastIndex": true, "LastIndexAny": true, "LastIndexByte": true,
	"Cut": true,
}

// characterRuleSite is one entry in the list: a file, and the exact spellings
// of the searches that file makes.
type characterRuleSite struct {
	file  string
	sites string // sorted "strings.Fn(\"x\")" spellings, exactly as the tree shows
	why   string // required: what this place is looking for and why it may
}

// characterRuleAllowed is THE LIST: every place in the shipping tree that
// searches a value for "@", "?" or "#".
//
// Every entry needs a written reason, because the reason is the whole cost of
// a second place looking at these characters at all. An entry whose honest
// reason would be "decides whether this value is carrying a credential" is the
// bug this guard exists to catch — that decision has exactly one home, and a
// new entry claiming it should be refused in review rather than written here.
var characterRuleAllowed = []characterRuleSite{
	{
		file:  "internal/credsafe/repourl.go",
		sites: `strings.ContainsAny("@?#")`,
		why: "This IS the rule. credentialBearingRunes is declared here and its " +
			"only reader is ContainsCredentialCarrierRune, in this file, which " +
			"ClassifyAddress and SafeRepoURL both call — one spelling, so the " +
			"list shows one search. Every other package asks ClassifyAddress " +
			"rather than writing the characters out again.",
	},
	{
		file:  "internal/api/catalog_project_readme.go",
		sites: `strings.IndexAny("?#")`,
		why: "Trims a query and fragment off a URL before deriving a display " +
			"name from its last path segment. It is shaping a label, not " +
			"judging whether the value is safe to show — the value it works " +
			"on has already been through credsafe wherever it is displayed.",
	},
	{
		file:  "internal/catalog/scorecard.go",
		sites: `strings.IndexAny("?#")`,
		why: "Same shaping as catalog_project_readme.go: cuts a URL back to its " +
			"path so a repository can be recognised across two spellings of " +
			"the same address. No safety decision hangs on it.",
	},
	{
		file:  "internal/helm/fetcher.go",
		sites: `strings.IndexAny("?#")`,
		why: "Builds a cache key from a chart repository address, so two " +
			"requests for the same index do not fetch twice. A cache key, not " +
			"a value anybody is shown.",
	},
	{
		file:  "internal/api/tiered_git.go",
		sites: `strings.Contains("@")`,
		why: "Checks whether a Git commit author string is an email address " +
			"before using it for attribution. An email, never a URL.",
	},
	{
		file:  "internal/orchestrator/catalog_yaml_edit.go",
		sites: `strings.Index("#")`,
		why: "Finds a trailing YAML comment on a catalog line so an edit keeps " +
			"the comment. A YAML comment marker, never a URL fragment.",
	},
	{
		file:  "internal/orchestrator/cluster_template_seed.go",
		sites: `strings.Index("# --- per-cluster overrides template ---")`,
		why: "Finds Sharko's own marker line inside a generated values file. " +
			"The \"#\" is the first character of a fixed sentence Sharko wrote, " +
			"not a character being looked for in somebody's value.",
	},
	{
		file:  "internal/orchestrator/smart_values_header.go",
		sites: `strings.LastIndex("@")`,
		why: "Splits a chart reference of the form <chart>@<version> so the " +
			"generated values header can name the version. A version " +
			"separator, never a userinfo section.",
	},
}

// discoverCharacterRuleSites type-checks the tree and reports every file that
// searches a value for one of the three characters.
//
// It reads the type checker's CONSTANT value for each needle, not the source
// text. That matters: credsafe writes its rule as
// strings.ContainsAny(raw, credentialBearingRunes), so a guard that only
// recognised quoted literals would miss the one definition it exists to pin —
// and would then report the whole list as fine while the real rule sat
// unlisted. A guard that gets happier as the thing it guards disappears is
// worse than none.
func discoverCharacterRuleSites(t *testing.T) (map[string]string, int) {
	t.Helper()
	root := logGuardRepoRoot(t)
	for _, dir := range logGuardSweepDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("swept directory %q does not exist — the guard covers less than it claims: %v", dir, err)
		}
	}
	pkgs := loadTypedPackages(t, root, "./...")

	found := map[string]map[string]bool{}
	filesRead := 0

	for _, pkg := range pkgs {
		for i, syntax := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			if !inSweptDir(rel) {
				continue
			}
			filesRead++

			ast.Inspect(syntax, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || !searchFuncs[sel.Sel.Name] {
					return true
				}
				fn, isFn := pkg.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
				if !isFn || fn.Pkg() == nil || fn.Pkg().Path() != "strings" {
					return true
				}
				for argIdx, arg := range call.Args {
					if argIdx == 0 {
						// The haystack. Only the needle names characters.
						continue
					}
					tv, known := pkg.TypesInfo.Types[arg]
					if !known || tv.Value == nil || tv.Value.Kind() != constant.String {
						continue
					}
					text := constant.StringVal(tv.Value)
					if text == "" || !strings.ContainsAny(text, credentialBearingChars) {
						continue
					}
					if found[rel] == nil {
						found[rel] = map[string]bool{}
					}
					found[rel]["strings."+sel.Sel.Name+"("+strconv.Quote(text)+")"] = true
				}
				return true
			})
		}
	}

	flat := map[string]string{}
	for file, sites := range found {
		keys := make([]string, 0, len(sites))
		for k := range sites {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		flat[file] = strings.Join(keys, " ")
	}
	return flat, filesRead
}

func TestCredentialCharacterRule_IsWrittenInExactlyOnePlace(t *testing.T) {
	found, filesRead := discoverCharacterRuleSites(t)

	if filesRead != wantSweptGoFiles {
		t.Fatalf(`the guard read %d shipping Go files, want exactly %d.

Fewer means it is no longer reaching the tree and nothing it reports can be
trusted. More means the tree grew — update wantSweptGoFiles once you have
looked at whether the new files search for these characters.`, filesRead, wantSweptGoFiles)
	}
	if len(found) == 0 {
		t.Fatal(`the guard found no place at all that searches a value for "@", "?" or "#".

credsafe defines the rule with exactly such a search, so finding none means the
walk read nothing useful and every result below is worthless.`)
	}
	if len(characterRuleAllowed) == 0 {
		t.Fatal("characterRuleAllowed is empty — an empty list agrees with everything and proves nothing")
	}

	// The one entry the whole guard is about must be present in the tree,
	// spelled with all three characters. Without this, deleting the rule and
	// deleting its list entry together would leave the guard green.
	const ruleFile = "internal/credsafe/repourl.go"
	if sites, ok := found[ruleFile]; !ok {
		t.Fatalf("%s no longer searches for the credential-bearing characters at all — the rule this guard exists to pin has gone", ruleFile)
	} else if !strings.Contains(sites, strconv.Quote(credentialBearingChars)) {
		t.Fatalf("%s searches for %s, not for %q — the rule has been narrowed, which is the exact defect this guard was written after", ruleFile, sites, credentialBearingChars)
	}

	allowed := map[string]characterRuleSite{}
	for _, entry := range characterRuleAllowed {
		if strings.TrimSpace(entry.why) == "" {
			t.Errorf("%s is listed with no reason. The reason in words is the whole cost of a second place looking at these characters.", entry.file)
		}
		if entry.sites == "" {
			t.Errorf("%s lists no searches, so it should not be in this list at all", entry.file)
		}
		if _, dup := allowed[entry.file]; dup {
			t.Errorf("%s is listed twice — a duplicate hides whichever entry is wrong", entry.file)
		}
		allowed[entry.file] = entry
	}

	var unlisted, stale, changed []string
	for file, sites := range found {
		entry, ok := allowed[file]
		if !ok {
			unlisted = append(unlisted, file+"  ["+sites+"]")
			continue
		}
		if entry.sites != sites {
			changed = append(changed, file+"\n      listed: "+entry.sites+"\n      tree:   "+sites)
		}
	}
	for file := range allowed {
		if _, ok := found[file]; !ok {
			stale = append(stale, file)
		}
	}

	report := func(label string, items []string, why string) {
		if len(items) == 0 {
			return
		}
		sort.Strings(items)
		t.Errorf("%s (%d):\n  %s\n\n%s", label, len(items), strings.Join(items, "\n  "), why)
	}

	report(`files that search a value for "@", "?" or "#" and are NOT listed`, unlisted,
		"If it is deciding whether a value could be carrying a credential, it must not: ask "+
			"credsafe.ClassifyAddress, which is the one place that rule is written. If it is "+
			"doing something else — a YAML comment, an email address, a cache key — add it here "+
			"with a reason saying what.")

	report("listed files that no longer make such a search", stale,
		"A stale entry is a hole: the list has stopped describing the tree, and the next real "+
			"copy of the rule can hide behind the mismatch. If the code moved, move the entry.")

	report("listed files whose searches have changed", changed,
		"The spellings are part of the entry on purpose. A file that starts searching for a NEW "+
			"character shows up here loudly instead of passing quietly under an entry written for "+
			"the old one.")
}

// TestLogMessagesAreFixedText walks every slog call in the shipping tree and
// requires the MESSAGE argument to be a string literal.
//
// RedactHandler rebuilds the record with the message untouched, so a message
// built with fmt.Sprintf, or read out of a variable, goes to the collector
// exactly as assembled — no detector runs on it. Keeping every message a fixed
// piece of text a person typed is what makes "the sink protects the log" true
// of the whole line rather than of the attributes only.
func TestLogMessagesAreFixedText(t *testing.T) {
	root := logGuardRepoRoot(t)
	pkgs := loadTypedPackages(t, root, "./...")

	// msgArgIndex says which argument carries the message, per method.
	msgArgIndex := map[string]int{
		"Debug": 0, "Info": 0, "Warn": 0, "Error": 0,
		"DebugContext": 1, "InfoContext": 1, "WarnContext": 1, "ErrorContext": 1,
		"Log": 2, "LogAttrs": 2,
	}

	filesRead := 0
	callsChecked := 0
	var bad []string

	for _, pkg := range pkgs {
		for i, syntax := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			if !inSweptDir(rel) {
				continue
			}
			filesRead++

			pos := pkg.Fset
			ast.Inspect(syntax, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || !isSlogCall(pkg.TypesInfo, sel) {
					return true
				}
				idx, known := msgArgIndex[sel.Sel.Name]
				if !known || idx >= len(call.Args) {
					return true
				}
				callsChecked++
				arg := call.Args[idx]
				// A CONSTANT expression is fixed text a person typed: a
				// literal, a named constant, or literals and constants
				// joined with "+". The type checker folds all three and
				// hands back a value. Anything it cannot fold was
				// assembled at run time out of something, and that
				// something is what the sink never sees.
				if tv, known := pkg.TypesInfo.Types[arg]; known && tv.Value != nil {
					return true
				}
				bad = append(bad, rel+":"+strconv.Itoa(pos.Position(arg.Pos()).Line)+"  "+sel.Sel.Name)
				return true
			})
		}
	}

	if filesRead != wantSweptGoFiles {
		t.Fatalf(`the walk read %d shipping Go files, want exactly %d — nothing below can be trusted.`, filesRead, wantSweptGoFiles)
	}
	if callsChecked == 0 {
		t.Fatal("the walk checked no slog call at all, so its silence means nothing")
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf(`%d log calls build their MESSAGE instead of typing it out:
  %s

The redaction sink rewrites attributes and leaves the message exactly as it
was assembled, so anything formatted into one goes to the collector verbatim.
Move the value into an attribute — slog.String("repo", repoURL) — where the
sink can see it, and leave the message as fixed text.`, len(bad), strings.Join(bad, "\n  "))
	}
}
