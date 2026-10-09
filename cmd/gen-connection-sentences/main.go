// Command gen-connection-sentences reads api.ConnectionSentences and
// api.ConnectionFailureMessages — the Go catalogs of every sentence the server
// authors for the connection surface — and emits TypeScript `as const`
// literals at ui/src/generated/connection-sentences.ts, so the browser renders
// server-owned wording it never has to type.
//
// # Two catalogs, one file
//
// api.ConnectionSentences is fixed sentences keyed by identifier.
// api.ConnectionFailureMessages is the PARAMETERIZED family (story P2c): the
// connection-test failure sentence, which the server assembles from a
// per-kind fragment and a runtime hint. The wire carries the finished sentence
// and its identifier; this file carries the same finished sentences so the
// browser has a generated fallback for the offline case instead of the three
// hand-typed fragments it used to hold — fragments that had already drifted,
// carrying a full stop the server never emits.
//
// What is deliberately NOT emitted is the fragment, the hint, the join or any
// other half of the sentence. The product owner's ruling: the browser "must
// not reproduce the concatenation logic". Emitting the pieces is what would
// let it.
//
// Why: fifty-two server-authored sentences were hand-typed into twenty-one
// browser files, fourteen of them in shipped code. That produced test fixtures
// describing responses the server cannot send, and assertions that can never
// fail again. The product owner's ruling: "Give server-owned messages stable
// symbolic identifiers; do not make browser code locate them by sentence
// text." This generator plus the matching CI check
// ("Connection Sentences Up To Date") is what turns that ruling into something
// the repository enforces rather than something people remember.
//
// # It reads the catalog at RUNTIME, and parses nothing
//
// The sibling generator cmd/gen-provider-types walks Go source with go/ast,
// because what it needs (the arms of a switch statement) has no runtime
// representation. This one needs the opposite: a map of strings that IS a
// runtime value. So it imports internal/api and reads the variable.
//
// That is not a style preference, it is the correctness argument. Several
// catalog entries are ALIASED across a package boundary — the api package
// declares constants whose value is a connectioncompare constant — and a
// source parser handed one of those sees an identifier where a sentence
// should be. The compiler resolves it for free. Reading the value also means
// this file cannot hold a stale second copy of the words: there is no sentence
// string anywhere in it, and cmd/gen-connection-sentences/main_test.go fails
// the build if one ever appears.
//
// # No commit SHA in the header
//
// Deliberately. A generated file stamped with the revision it was built from
// churns on every unrelated commit and produces CI failures that say
// "out of date" about a file whose content is correct. The regenerate-and-diff
// check already proves the file matches the revision it was built from — a
// file born from a different revision cannot survive regeneration from this
// one.
//
// Usage:
//
//	go run ./cmd/gen-connection-sentences
//
// or via the Makefile:
//
//	make generate-connection-sentences
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/api"
)

const defaultOutputPath = "ui/src/generated/connection-sentences.ts"

func main() {
	var outputPath string
	flag.StringVar(&outputPath, "output", defaultOutputPath,
		"path to the TypeScript file to (over)write")
	flag.Parse()

	if err := run(api.ConnectionSentences, api.ConnectionFailureMessages, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "gen-connection-sentences: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable entry point: it renders both catalogs and writes the
// result to `outputPath`. The output directory is created if it doesn't exist.
//
// The catalogs are PARAMETERS rather than package references so the renderer
// can be driven over synthetic values in tests. main passes the real
// api.ConnectionSentences / api.ConnectionFailureMessages and nothing else
// does.
func run(sentences map[string]string, failures []api.ConnectionFailureMessage, outputPath string) error {
	if len(sentences) == 0 {
		return fmt.Errorf("the sentence catalog is empty — refusing to write an empty contract to %s", outputPath)
	}
	if len(failures) == 0 {
		return fmt.Errorf("the failure-message catalog is empty — refusing to write an empty contract to %s", outputPath)
	}

	rendered, err := renderTypeScript(sentences, failures)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outputPath), err)
	}
	if err := os.WriteFile(outputPath, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	fmt.Printf("gen-connection-sentences: wrote %d sentences and %d failure messages to %s\n",
		len(sentences), len(failures), outputPath)
	return nil
}

// renderTypeScript renders the deterministic TS output. The format is pinned
// by tests and by the CI "Connection Sentences Up To Date" check — keep it
// byte-stable across refactors.
//
// Keys are sorted so Go's randomised map iteration cannot reorder the file
// between two runs over the same catalog.
func renderTypeScript(sentences map[string]string, failures []api.ConnectionFailureMessage) (string, error) {
	ids := make([]string, 0, len(sentences))
	for id := range sentences {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	sortedFailures := make([]api.ConnectionFailureMessage, len(failures))
	copy(sortedFailures, failures)
	sort.Slice(sortedFailures, func(i, j int) bool { return sortedFailures[i].ID < sortedFailures[j].ID })

	var b strings.Builder
	b.WriteString("// Code generated by cmd/gen-connection-sentences. DO NOT EDIT.\n")
	b.WriteString("// Source: internal/api/connection_sentences.go\n")
	b.WriteString("// Run `make generate-connection-sentences` to refresh.\n")
	b.WriteString("//\n")
	b.WriteString("// Every sentence the server authors for the connection surface, keyed by the\n")
	b.WriteString("// identifier the server owns. Render these by identifier. Never type one of\n")
	b.WriteString("// these sentences into browser code, and never match on the text of one:\n")
	b.WriteString("// a copy drifts the moment the server edits the wording, and a text match\n")
	b.WriteString("// silently stops matching at the same moment.\n")
	b.WriteString("\n")
	b.WriteString("export const CONNECTION_SENTENCES = {\n")
	for _, id := range ids {
		quoted, err := quoteForTypeScript(sentences[id])
		if err != nil {
			return "", fmt.Errorf("sentence %q: %w", id, err)
		}
		b.WriteString("  ")
		b.WriteString(id)
		b.WriteString(": ")
		b.WriteString(quoted)
		b.WriteString(",\n")
	}
	b.WriteString("} as const\n")
	b.WriteString("\n")
	b.WriteString("export type ConnectionSentenceId = keyof typeof CONNECTION_SENTENCES\n")

	b.WriteString("\n")
	b.WriteString("// The connection-test failure family, already assembled by the server. Each\n")
	b.WriteString("// entry is the FINISHED sentence for one connection kind and one failure\n")
	b.WriteString("// code. The response carries the finished sentence and its identifier, so\n")
	b.WriteString("// render what the server sent; these are here for the offline case only.\n")
	b.WriteString("// Never join a fragment to a hint in browser code — that is the second\n")
	b.WriteString("// formatter this contract exists to remove.\n")
	b.WriteString("export const CONNECTION_FAILURE_MESSAGES = {\n")
	for _, f := range sortedFailures {
		quoted, err := quoteForTypeScript(f.Sentence)
		if err != nil {
			return "", fmt.Errorf("failure message %q: %w", f.ID, err)
		}
		b.WriteString("  ")
		b.WriteString(f.ID)
		b.WriteString(": ")
		b.WriteString(quoted)
		b.WriteString(",\n")
	}
	b.WriteString("} as const\n")
	b.WriteString("\n")
	b.WriteString("export type ConnectionFailureMessageId = keyof typeof CONNECTION_FAILURE_MESSAGES\n")
	return b.String(), nil
}

// quoteForTypeScript renders one sentence as a TypeScript double-quoted string
// literal.
//
// It goes through encoding/json rather than strconv.Quote. JSON's string
// grammar is a strict subset of JavaScript's, so anything json produces is
// valid TypeScript — whereas Go's own quoting can emit `\U0001F600` for an
// astral-plane rune, an escape JavaScript does not have. Several of these
// sentences already carry an em dash and a backtick, and apostrophes are
// everywhere; json handles all of them, and escapes an astral rune as the
// surrogate pair JavaScript expects.
//
// HTML escaping is turned off so `<`, `>` and `&` stay readable — they are
// inside a TypeScript source file, not an HTML document.
func quoteForTypeScript(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	// Encode appends a newline of its own.
	return strings.TrimRight(buf.String(), "\n"), nil
}
