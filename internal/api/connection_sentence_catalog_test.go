package api

// connection_sentence_catalog_test.go — GUARD A, the one that makes the whole
// message-catalog scheme real rather than decorative.
//
// # The failure it exists to stop
//
// Somebody adds a new sentence constant to one of the contract files and
// never adds it to ConnectionSentences. The browser then hand-types that new
// sentence — the exact thing the catalog exists to prevent — and every test in
// the tree stays green forever. That is WORSE than having no catalog, because
// the catalog now looks authoritative while quietly covering less than it
// claims.
//
// So this guard parses the five contract files and fails BY NAME:
//
//   - every sentence constant whose identifier is missing from the catalog;
//   - every catalog key whose constant no longer exists;
//   - every user-facing sentence typed INLINE instead of as a constant, which
//     would be outside the catalog and outside the check above as well;
//   - every new alias-shaped constant, which has to be a decision rather than
//     a gap.
//
// # It is a LIST, never a count
//
// Every failure names the constant. A floor ("at least N sentences were
// found") passes while stale entries rot and tells whoever hits it nothing
// about what to do — this repo already has one of those, a hidden
// `families < 40` in internal/metrics/contract_registry_test.go that kills the
// whole metrics suite with an error that does not mention the real problem.
// There is no count anywhere in this file.
//
// # How it decides "this is a sentence"
//
// A value whose FIRST RUNE IS AN UPPERCASE LETTER is prose a person reads.
// Everything else in these five files is a wire value, and every wire value in
// them is lowercase: "out_of_sync", "eks_token", "git_definition", "ok",
// "not_checked", "full_connection". The split is exact across all five files
// today — it needs no exclusion list, and there is none.
//
// A LENGTH OR SPACE RULE WOULD BE WRONG HERE, and not marginally. The shortest
// real sentence in the set is headlineBlocked = "Blocked": seven characters,
// no space at all. A "sentences have spaces in them" filter — which is what
// the neighbouring Git-capitalisation guard uses, correctly, for its own
// narrower job — would silently skip it, and skip any future one-word
// headline with it. TestConnectionSentences_FilterAcceptsTheShortestSentence
// pins both directions of the rule so it cannot rot into a heuristic.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// connectionContractFiles are the files whose sentences ConnectionSentences
// must cover, relative to the repository root. Adding a sentence to a file
// that is not on this list puts it outside the contract — which is a real
// decision, so the list is here in the open rather than inferred.
var connectionContractFiles = []string{
	"internal/api/connection_canonical.go",
	"internal/api/connection_reconciliation.go",
	"internal/api/connection_comparison.go",
	"internal/api/connection_credential_check.go",
	"internal/api/connection_repair.go",
	"internal/connectioncompare/mode.go",
	"internal/connectioncompare/compare.go",
}

// catalogAliases are the constants in the contract files whose value is
// another constant rather than a literal of their own.
//
// They are NOT catalog entries. An alias carries no words — it points at a
// sentence that is already catalogued under the identifier of the constant it
// aliases, and giving the same sentence a second identifier would put back
// the ambiguity the identifier scheme removes.
//
// This map is the reviewable record of that decision, one line per alias. A
// NEW alias is not silently tolerated: the guard fails naming it, so somebody
// has to decide again whether it is a pointer at an existing sentence or a new
// sentence wearing an alias's clothes. Stale entries fail too — an alias
// listed here that no longer exists is named and must be removed.
var catalogAliases = map[string]string{
	"reasonSecretMissingDurable":       "connectioncompare.LimitReasonSecretMissingDurable",
	"reasonSecretMissingLegacyInline":  "connectioncompare.LimitReasonSecretMissingLegacyInline",
	"reasonSecretMissingSelfManaged":   "connectioncompare.LimitReasonSecretMissingSelfManaged",
	"reasonSecretMissingUnknownSource": "connectioncompare.LimitReasonSecretMissingUnknownSource",

	// The repair endpoint's "no Git connection" refusal held a byte-identical
	// copy of the comparison endpoint's. Two identifiers for one set of words
	// is the ambiguity the identifier scheme removes, so it points at the one
	// constant now.
	"repairFailNoGit": "failNoGitConnection",
}

// looksLikeASentence reports whether a string constant's value is prose a
// person reads, as opposed to a wire value.
//
// THE RULE: the first rune is an uppercase letter. See this file's header for
// why it is not a length or space rule.
func looksLikeASentence(value string) bool {
	for _, r := range value {
		return unicode.IsUpper(r)
	}
	return false
}

// catalogIDFor applies the naming rule from connection_sentences.go: the Go
// constant's name with its first letter lowercased.
func catalogIDFor(constName string) string {
	if constName == "" {
		return ""
	}
	r := []rune(constName)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// sentenceConstant is one collected declaration.
type sentenceConstant struct {
	name  string
	value string
	file  string
	line  int
}

// collectContractSentences parses the contract files and returns every string
// CONSTANT whose value is prose, plus every alias-shaped constant it saw, plus
// every prose string literal that is NOT part of a constant declaration.
func collectContractSentences(t *testing.T) (sentences []sentenceConstant, aliases []sentenceConstant, inlineProse []sentenceConstant) {
	t.Helper()
	root := repoRootForSweep(t)

	for _, rel := range connectionContractFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}

		// Every string literal that belongs to a const declaration, so the
		// inline sweep below can tell "declared as a constant" from "typed
		// into the middle of a function".
		inConstDecl := map[ast.Node]bool{}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, val := range vs.Values {
					name := ""
					if i < len(vs.Names) {
						name = vs.Names[i].Name
					}
					pos := fset.Position(val.Pos())
					switch v := val.(type) {
					case *ast.BasicLit:
						if v.Kind != token.STRING {
							continue
						}
						inConstDecl[v] = true
						text, unquoteErr := strconv.Unquote(v.Value)
						if unquoteErr != nil {
							continue
						}
						if looksLikeASentence(text) {
							sentences = append(sentences, sentenceConstant{name, text, rel, pos.Line})
						}
					case *ast.Ident, *ast.SelectorExpr:
						aliases = append(aliases, sentenceConstant{name, exprName(val), rel, pos.Line})
					}
				}
			}
		}

		// The inline sweep. A sentence typed straight into a function body is
		// invisible to the catalog AND to the missing-entry check above, so
		// without this the guard would report full coverage of a file that
		// ships sentences it never saw. Six sentences were in exactly that
		// state before this guard existed.
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || inConstDecl[lit] {
				return true
			}
			text, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				return true
			}
			// Prose shape: an uppercase opening AND a space. The space is
			// required HERE (unlike for constants) because a function body is
			// full of legitimate capitalised single words that are wire values
			// — ArgoCD's own "Successful" and "Failed", for two.
			prose := looksLikeASentence(text) && strings.Contains(text, " ")
			// …and a re-typed catalogued sentence is caught whatever its
			// shape, which closes the one-word gap the space rule leaves.
			if !prose && !isCatalogued(text) {
				return true
			}
			inlineProse = append(inlineProse, sentenceConstant{"", text, rel, fset.Position(lit.Pos()).Line})
			return true
		})
	}
	return sentences, aliases, inlineProse
}

// isCatalogued reports whether text is exactly one of the catalogued
// sentences.
func isCatalogued(text string) bool {
	for _, v := range ConnectionSentences {
		if v == text {
			return true
		}
	}
	return false
}

// exprName renders an Ident or SelectorExpr as source text.
func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprName(v.X) + "." + v.Sel.Name
	}
	return fmt.Sprintf("%T", e)
}

// TestConnectionSentences_CatalogCoversEverySentence is GUARD A.
func TestConnectionSentences_CatalogCoversEverySentence(t *testing.T) {
	sentences, aliases, inlineProse := collectContractSentences(t)

	// 1. Every sentence constant is in the catalog, under the ruled identifier.
	//    Named one by one — a reader of the failure must not have to go
	//    looking for which ones.
	var missing []string
	declared := map[string]sentenceConstant{}
	for _, s := range sentences {
		id := catalogIDFor(s.name)
		if prev, dup := declared[id]; dup {
			t.Errorf("two constants map to the identifier %q — %s (%s:%d) and %s (%s:%d). "+
				"One would silently swallow the other in the catalog; rename one of the constants.",
				id, prev.name, prev.file, prev.line, s.name, s.file, s.line)
		}
		declared[id] = s
		if _, ok := ConnectionSentences[id]; !ok {
			missing = append(missing, fmt.Sprintf("  %s (%s:%d) → add %q: %s,", s.name, s.file, s.line, id, s.name))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d sentence constant(s) are NOT in ConnectionSentences.\n"+
			"Every one of them ships to a person's screen from outside the catalog, so the browser\n"+
			"would have to hand-type it. Add each line to internal/api/connection_sentences.go:\n%s",
			len(missing), strings.Join(missing, "\n"))
	}

	// 2. Every catalog key still has a constant behind it. A key whose
	//    constant was deleted or renamed is a stale entry that a browser is
	//    still importing.
	var stale []string
	for id := range ConnectionSentences {
		if _, ok := declared[id]; !ok {
			stale = append(stale, "  "+id)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d catalog key(s) no longer have a sentence constant in the contract files.\n"+
			"The constant was renamed or deleted and the catalog was not updated — the browser is\n"+
			"importing an identifier the server can no longer produce:\n%s",
			len(stale), strings.Join(stale, "\n"))
	}

	// 3. No sentence is typed inline. An inline sentence is outside the
	//    catalog and outside check 1, so it would ship uncovered.
	if len(inlineProse) > 0 {
		var lines []string
		for _, p := range inlineProse {
			lines = append(lines, fmt.Sprintf("  %s:%d  %q", p.file, p.line, p.value))
		}
		sort.Strings(lines)
		t.Errorf("%d user-facing sentence(s) are typed inline instead of declared as constants.\n"+
			"A sentence that is not a constant cannot be catalogued and is not seen by the coverage\n"+
			"check above, so it would ship to a person's screen from outside this contract entirely.\n"+
			"Promote each one to a named constant and add it to ConnectionSentences:\n%s",
			len(inlineProse), strings.Join(lines, "\n"))
	}

	// 4. Every alias is an acknowledged decision, and every acknowledgement is
	//    still true.
	seenAliases := map[string]bool{}
	for _, a := range aliases {
		seenAliases[a.name] = true
		want, known := catalogAliases[a.name]
		if !known {
			t.Errorf("%s:%d: %s aliases %s and is not in catalogAliases.\n"+
				"An alias is deliberately not a catalog entry — but that has to be a decision, not a gap.\n"+
				"If it points at an already-catalogued sentence, add it to catalogAliases. If it carries\n"+
				"words of its own, make it a literal constant and catalogue it.",
				a.file, a.line, a.name, a.value)
			continue
		}
		if want != a.value {
			t.Errorf("%s:%d: %s now aliases %s, but catalogAliases says %s — the record is stale.",
				a.file, a.line, a.name, a.value, want)
		}
	}
	for name := range catalogAliases {
		if !seenAliases[name] {
			t.Errorf("catalogAliases lists %q, but no such alias constant exists in the contract files any more — remove the stale entry.", name)
		}
	}
}

// TestConnectionSentences_FilterAcceptsTheShortestSentence pins the "is this a
// sentence" rule in BOTH directions, because getting it wrong is the single
// most likely way Guard A covers less than it appears to.
//
// The accept side uses the genuinely shortest constant in the set —
// headlineBlocked, "Blocked", seven characters and no space. A length filter
// or a "must contain a space" filter would drop it, and would drop any future
// one-word headline with it, while the guard went on reporting success.
func TestConnectionSentences_FilterAcceptsTheShortestSentence(t *testing.T) {
	// The shortest sentence in the contract files, found rather than assumed —
	// so this test keeps testing the real extreme as the set changes.
	sentences, _, _ := collectContractSentences(t)
	if len(sentences) == 0 {
		t.Fatal("no sentence constants were collected at all — the parser found nothing and Guard A is blind")
	}
	shortest := sentences[0]
	for _, s := range sentences[1:] {
		if len(s.value) < len(shortest.value) {
			shortest = s
		}
	}
	if !looksLikeASentence(shortest.value) {
		t.Errorf("the filter rejects the shortest real sentence in the set — %s = %q (%s:%d)",
			shortest.name, shortest.value, shortest.file, shortest.line)
	}
	t.Logf("shortest sentence the filter accepts: %s = %q (%d bytes, %d spaces)",
		shortest.name, shortest.value, len(shortest.value), strings.Count(shortest.value, " "))

	// The accept side, stated explicitly as well, so a change to the corpus
	// cannot quietly retire the one-word case.
	for _, accept := range []string{"Blocked", "Out of sync", "Not checked yet", "Unknown — check failed"} {
		if !looksLikeASentence(accept) {
			t.Errorf("the filter rejects %q, which is a real headline a person reads", accept)
		}
	}

	// The reject side: genuine non-sentences. These are the wire values that
	// live beside the sentences in the same files, including ones that are
	// longer than "Blocked" — proving the rule is not length in disguise.
	for _, reject := range []string{
		"blocked",                    // the sync-state wire value for that same headline
		"out_of_sync",                // longer than "Blocked", still not prose
		"backend_stored_credentials", // the longest wire value in the set
		"git_definition",
		"not_checked",
		"full_connection",
		"ok",
	} {
		if looksLikeASentence(reject) {
			t.Errorf("the filter accepts %q as a sentence — it is a wire value and must stay out of the catalog", reject)
		}
	}
}
